package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/owner/back-ez/internal/hub/server"
	"github.com/owner/back-ez/internal/hub/store"
	"github.com/owner/back-ez/internal/protocol"
)

// UIClient represents an active WebSocket connection from the Web UI.
type UIClient struct {
	conn   *websocket.Conn
	seqMu  sync.Mutex
	seq    int64
	sendCh chan []byte
}

func (c *UIClient) NextSeq() int64 {
	c.seqMu.Lock()
	defer c.seqMu.Unlock()
	c.seq++
	return c.seq
}

// AgentConn represents an active WebSocket connection with an Agent.
type AgentConn struct {
	clientID string
	conn     *websocket.Conn
	seqMu    sync.Mutex
	seq      int64
	sendCh   chan []byte
}

func (a *AgentConn) NextSeq() int64 {
	a.seqMu.Lock()
	defer a.seqMu.Unlock()
	a.seq++
	return a.seq
}

// PendingCommand tracks in-flight commands sent to an Agent.
type PendingCommand struct {
	CmdID     string
	ClientID  string
	Event     protocol.EventType
	ExpiresAt time.Time
	ResultCh  chan protocol.CmdResultPayload
}

// HubWS manages both /ws (Web UI) and /ws/agent (Agent) channels.
type HubWS struct {
	service *server.Service
	store   *store.Store

	mu         sync.RWMutex
	uiClients  map[*UIClient]struct{}
	agentConns map[string]*AgentConn // clientID -> AgentConn

	cmdMu       sync.Mutex
	pendingCmds map[string]*PendingCommand // cmd_id -> PendingCommand
}

func NewHubWS(svc *server.Service, st *store.Store) *HubWS {
	h := &HubWS{
		service:     svc,
		store:       st,
		uiClients:   make(map[*UIClient]struct{}),
		agentConns:  make(map[string]*AgentConn),
		pendingCmds: make(map[string]*PendingCommand),
	}
	return h
}

// --- /ws (Web UI Channel) ---

func (h *HubWS) HandleUIWS(w http.ResponseWriter, r *http.Request) {
	// Authenticate via Session Cookie
	cookie, err := r.Cookie("backez_session")
	if err != nil || cookie.Value == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if _, err := h.service.ValidateSession(cookie.Value); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Internal dev / test proxy support
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	client := &UIClient{
		conn:   conn,
		seq:    0,
		sendCh: make(chan []byte, 64),
	}

	h.mu.Lock()
	h.uiClients[client] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.uiClients, client)
		h.mu.Unlock()
		close(client.sendCh)
	}()

	// 1. Send initial HELLO event (seq = 1)
	helloPayload := protocol.HelloPayload{
		ServerTime:      time.Now().UTC().Format(time.RFC3339),
		Seq:             client.NextSeq(),
		ContractVersion: 1,
	}
	h.sendToUIClient(client, protocol.EventHello, helloPayload)

	// Start writer loop
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		for msg := range client.sendCh {
			if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
				return
			}
		}
	}()

	// Reader loop (UI is mostly read-only, but checks for valid messages)
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}

		var msg protocol.WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		// Reject undefined incoming events from browser
		if !h.isKnownUIEvent(msg.Event) {
			errMsg := protocol.WSMessage{
				V:         1,
				Seq:       client.NextSeq(),
				Event:     protocol.EventAgentError,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Payload:   []byte(`{"error_code":"INVALID_ARGUMENT","message":"unknown event"}`),
			}
			raw, _ := json.Marshal(errMsg)
			select {
			case client.sendCh <- raw:
			default:
			}
		}
	}
}

func (h *HubWS) isKnownUIEvent(evt protocol.EventType) bool {
	switch evt {
	case protocol.EventHello,
		protocol.EventClientStatusChanged,
		protocol.EventClientUpserted,
		protocol.EventGroupUpdated,
		protocol.EventRestorePointsUpdated,
		protocol.EventWOLSent,
		protocol.EventTaskCreated,
		protocol.EventDeployProgress,
		protocol.EventDeployFinished,
		protocol.EventDeployFailed,
		protocol.EventAgentError:
		return true
	default:
		return false
	}
}

func (h *HubWS) sendToUIClient(client *UIClient, event protocol.EventType, payload any) {
	rawPayload, _ := json.Marshal(payload)
	msg := protocol.WSMessage{
		V:         1,
		Seq:       client.seq, // already incremented by NextSeq()
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload:   rawPayload,
	}
	data, _ := json.Marshal(msg)
	select {
	case client.sendCh <- data:
	default:
	}
}

// BroadcastToUI sends an event to all connected dashboard clients with per-connection monotonic seq.
func (h *HubWS) BroadcastToUI(event protocol.EventType, payload any) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for client := range h.uiClients {
		msg := protocol.WSMessage{
			V:         1,
			Seq:       client.NextSeq(),
			Event:     event,
			Timestamp: now,
			Payload:   rawPayload,
		}
		data, _ := json.Marshal(msg)
		select {
		case client.sendCh <- data:
		default:
		}
	}
}

// --- /ws/agent (Agent Channel) ---

func (h *HubWS) HandleAgentWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	agentConn := &AgentConn{
		conn:   conn,
		seq:    0,
		sendCh: make(chan []byte, 64),
	}

	go func() {
		for msg := range agentConn.sendCh {
			if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
				return
			}
		}
	}()

	defer func() {
		if agentConn.clientID != "" {
			h.mu.Lock()
			delete(h.agentConns, agentConn.clientID)
			h.mu.Unlock()

			// Mark client as OFFLINE on disconnect if not already in terminal error
			c, err := h.store.GetClient(agentConn.clientID)
			if err == nil && c != nil {
				if protocol.IsValidClientTransition(c.Status, protocol.ClientStatusOffline) {
					from := c.Status
					c.Status = protocol.ClientStatusOffline
					_ = h.store.UpsertClient(*c)
					h.BroadcastToUI(protocol.EventClientStatusChanged, protocol.ClientStatusChangedPayload{
						ClientID: c.ClientID,
						IP:       c.CurrentIP,
						From:     from,
						To:       c.Status,
					})
				}
			}
		}
		close(agentConn.sendCh)
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}

		var msg protocol.WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		if err := h.processAgentReport(agentConn, msg); err != nil {
			// Reply with error result if relevant
			h.sendAgentMessage(agentConn, protocol.EventAgentError, protocol.AgentErrorPayload{
				ErrorCode: err.Error(),
				Message:   err.Error(),
			})
		}
	}
}

func (h *HubWS) processAgentReport(a *AgentConn, msg protocol.WSMessage) error {
	switch msg.Event {
	case protocol.ReportAgentHello:
		var hello protocol.AgentHelloPayload
		if err := json.Unmarshal(msg.Payload, &hello); err != nil {
			return errors.New(protocol.ErrInvalidArgument)
		}
		a.clientID = hello.ClientID

		h.mu.Lock()
		h.agentConns[hello.ClientID] = a
		h.mu.Unlock()

		status := protocol.ClientStatusOnlineWindows
		if hello.Mode == protocol.AgentModePE {
			status = protocol.ClientStatusReadyForCast
		}

		clientNode := protocol.ClientNode{
			ClientID:     hello.ClientID,
			CurrentIP:    hello.CurrentIP,
			MACAddress:   hello.MACAddress,
			Hostname:     hello.Hostname,
			OSEdition:    hello.OSEdition,
			AgentVersion: hello.AgentVersion,
			PEReady:      hello.PEReady,
			Status:       status,
			LastSeen:     time.Now().UTC().Format(time.RFC3339),
		}
		if err := h.store.UpsertClient(clientNode); err != nil {
			return err
		}

		// Broadcast to UI
		saved, err := h.store.GetClient(hello.ClientID)
		if err == nil {
			h.BroadcastToUI(protocol.EventClientUpserted, protocol.ClientUpsertedPayload{Client: *saved})
		}
		return nil

	case protocol.ReportAgentStatus:
		if a.clientID == "" {
			return errors.New(protocol.ErrUnauthorized)
		}
		var statusPayload protocol.AgentStatusPayload
		if err := json.Unmarshal(msg.Payload, &statusPayload); err != nil {
			return errors.New(protocol.ErrInvalidArgument)
		}

		currentClient, err := h.store.GetClient(a.clientID)
		if err != nil {
			return errors.New(protocol.ErrNotFound)
		}

		// Enforce State Machine transition check
		if !protocol.IsValidClientTransition(currentClient.Status, statusPayload.Status) {
			// Red line: Illegal transition MUST be rejected, return INVALID_STATE, DO NOT write to DB
			return errors.New(protocol.ErrInvalidState)
		}

		from := currentClient.Status
		currentClient.Status = statusPayload.Status
		currentClient.LastSeen = time.Now().UTC().Format(time.RFC3339)
		if statusPayload.Reason != nil {
			currentClient.ErrorMessage = statusPayload.Reason
		}
		if err := h.store.UpsertClient(*currentClient); err != nil {
			return err
		}

		h.BroadcastToUI(protocol.EventClientStatusChanged, protocol.ClientStatusChangedPayload{
			ClientID: currentClient.ClientID,
			IP:       currentClient.CurrentIP,
			From:     from,
			To:       currentClient.Status,
		})
		return nil

	case protocol.ReportAgentProgress:
		var prog protocol.AgentProgressPayload
		if err := json.Unmarshal(msg.Payload, &prog); err != nil {
			return errors.New(protocol.ErrInvalidArgument)
		}

		if a.clientID != "" {
			c, err := h.store.GetClient(a.clientID)
			if err == nil && c != nil {
				c.ProgressPercent = &prog.Percent
				_ = h.store.UpsertClient(*c)

				h.BroadcastToUI(protocol.EventDeployProgress, protocol.DeployProgressPayload{
					TaskID:    prog.TaskID,
					SpeedMbps: prog.SpeedMbps,
					Clients: []protocol.DeployClientProgress{
						{
							IP:       c.CurrentIP,
							Progress: prog.Percent,
							Status:   c.Status,
						},
					},
				})
			}
		}
		return nil

	case protocol.ReportNetworkIdentity:
		var netPayload protocol.NetworkIdentityPayload
		if err := json.Unmarshal(msg.Payload, &netPayload); err != nil {
			return errors.New(protocol.ErrInvalidArgument)
		}
		if a.clientID != "" {
			_ = h.store.SaveNetworkIdentity(a.clientID, netPayload.Identity)
		}
		return nil

	case protocol.ReportImageVerifyResult:
		var verify protocol.ImageVerifyResultPayload
		if err := json.Unmarshal(msg.Payload, &verify); err != nil {
			return errors.New(protocol.ErrInvalidArgument)
		}
		if !verify.OK {
			if a.clientID != "" {
				c, err := h.store.GetClient(a.clientID)
				if err == nil {
					errMsg := "image verification failed"
					c.Status = protocol.ClientStatusError
					c.ErrorMessage = &errMsg
					_ = h.store.UpsertClient(*c)
				}
			}
			return errors.New(protocol.ErrImageVerifyFailed)
		}
		return nil

	case protocol.ReportCmdResult:
		var res protocol.CmdResultPayload
		if err := json.Unmarshal(msg.Payload, &res); err != nil {
			return errors.New(protocol.ErrInvalidArgument)
		}

		h.cmdMu.Lock()
		pending, exists := h.pendingCmds[res.CmdID]
		if exists {
			delete(h.pendingCmds, res.CmdID)
		}
		h.cmdMu.Unlock()

		if exists && pending != nil {
			select {
			case pending.ResultCh <- res:
			default:
			}
		}
		return nil

	default:
		// Undefined agent event rejected
		return errors.New(protocol.ErrInvalidArgument)
	}
}

func (h *HubWS) sendAgentMessage(a *AgentConn, event protocol.EventType, payload any) {
	rawPayload, _ := json.Marshal(payload)
	msg := protocol.WSMessage{
		V:         1,
		Seq:       a.NextSeq(),
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload:   rawPayload,
	}
	data, _ := json.Marshal(msg)
	select {
	case a.sendCh <- data:
	default:
	}
}

// SendCommandToAgent dispatches a command to a specific connected Agent.
func (h *HubWS) SendCommandToAgent(ctx context.Context, clientID string, event protocol.EventType, payload any, cmdID string, expiresAt time.Time) (*protocol.CmdResultPayload, error) {
	h.mu.RLock()
	agent, exists := h.agentConns[clientID]
	h.mu.RUnlock()

	if !exists {
		return nil, errors.New(protocol.ErrNotFound)
	}

	resultCh := make(chan protocol.CmdResultPayload, 1)
	pending := &PendingCommand{
		CmdID:     cmdID,
		ClientID:  clientID,
		Event:     event,
		ExpiresAt: expiresAt,
		ResultCh:  resultCh,
	}

	h.cmdMu.Lock()
	h.pendingCmds[cmdID] = pending
	h.cmdMu.Unlock()

	defer func() {
		h.cmdMu.Lock()
		delete(h.pendingCmds, cmdID)
		h.cmdMu.Unlock()
	}()

	h.sendAgentMessage(agent, event, payload)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(time.Until(expiresAt)):
		return nil, errors.New("command expired")
	case res := <-resultCh:
		return &res, nil
	}
}
