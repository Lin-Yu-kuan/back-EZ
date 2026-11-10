package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/owner/back-ez/internal/agent/secure"
	"github.com/owner/back-ez/internal/protocol"
)

type MockAgent struct {
	ID        string
	IP        string
	MAC       string
	Status    protocol.ClientStatus
	Mode      protocol.AgentMode
	HubURL    string
	TLSConfig *tls.Config

	conn *websocket.Conn
	seq  int64
	mu   sync.Mutex
}

func (a *MockAgent) nextSeq() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seq++
	return a.seq
}

func (a *MockAgent) ConnectAndRun(ctx context.Context, triggerIllegal bool) error {
	opts := &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: a.TLSConfig,
			},
		},
	}

	conn, _, err := websocket.Dial(ctx, a.HubURL, opts)
	if err != nil {
		return fmt.Errorf("agent %s dial failed: %w", a.ID, err)
	}
	defer conn.CloseNow()
	a.conn = conn

	// 1. Send AGENT_HELLO
	helloPayload := protocol.AgentHelloPayload{
		ClientID:     a.ID,
		Hostname:     fmt.Sprintf("MOCK-PC-%s", a.IP),
		OSEdition:    "Windows 11 Education",
		AgentVersion: "0.1.0",
		MACAddress:   &a.MAC,
		CurrentIP:    a.IP,
		PEReady:      true,
		Mode:         a.Mode,
	}
	if err := a.sendMessage(ctx, protocol.ReportAgentHello, helloPayload); err != nil {
		return err
	}

	if triggerIllegal {
		// Attempt forbidden transition directly from ONLINE_WINDOWS to DEPLOYING
		_ = a.UpdateStatus(ctx, protocol.ClientStatusDeploying)
	}

	// Message handling loop
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}

		var msg protocol.WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		a.handleMessage(ctx, msg)
	}
}

func (a *MockAgent) sendMessage(ctx context.Context, event protocol.EventType, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	msg := protocol.WSMessage{
		V:         1,
		Seq:       a.nextSeq(),
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload:   raw,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return a.conn.Write(ctx, websocket.MessageText, data)
}

func (a *MockAgent) UpdateStatus(ctx context.Context, next protocol.ClientStatus) error {
	a.Status = next
	return a.sendMessage(ctx, protocol.ReportAgentStatus, protocol.AgentStatusPayload{
		ClientID: a.ID,
		Status:   next,
	})
}

func (a *MockAgent) handleMessage(ctx context.Context, msg protocol.WSMessage) {
	switch msg.Event {
	case protocol.CmdPing:
		var ping protocol.CmdPingPayload
		if err := json.Unmarshal(msg.Payload, &ping); err == nil {
			_ = a.sendMessage(ctx, protocol.ReportCmdResult, protocol.CmdResultPayload{
				CmdID: ping.CmdID,
				OK:    true,
			})
		}

	case protocol.CmdPrepareDeploy:
		var prep protocol.CmdPrepareDeployPayload
		if err := json.Unmarshal(msg.Payload, &prep); err == nil {
			// Report network identity
			_ = a.sendMessage(ctx, protocol.ReportNetworkIdentity, protocol.NetworkIdentityPayload{
				TaskID: prep.TaskID,
				Identity: protocol.NetworkIdentity{
					MACAddress:       a.MAC,
					IPMode:           protocol.IPModeStatic,
					IPAddress:        a.IP,
					SubnetMask:       "255.255.255.0",
					DefaultGateway:   "192.168.1.1",
					DNSServers:       []string{"8.8.8.8"},
					OriginalHostname: fmt.Sprintf("MOCK-PC-%s", a.IP),
					CapturedAt:       time.Now().UTC().Format(time.RFC3339),
				},
			})
			_ = a.sendMessage(ctx, protocol.ReportCmdResult, protocol.CmdResultPayload{
				CmdID: prep.CmdID,
				OK:    true,
			})
		}

	case protocol.CmdEnterPE:
		var enter protocol.CmdEnterPEPayload
		if err := json.Unmarshal(msg.Payload, &enter); err == nil {
			_ = a.sendMessage(ctx, protocol.ReportCmdResult, protocol.CmdResultPayload{
				CmdID: enter.CmdID,
				OK:    true,
			})
		}

	case protocol.CmdStartReceiver:
		var recv protocol.CmdStartReceiverPayload
		if err := json.Unmarshal(msg.Payload, &recv); err == nil {
			_ = a.sendMessage(ctx, protocol.ReportCmdResult, protocol.CmdResultPayload{
				CmdID: recv.CmdID,
				OK:    true,
			})
		}
	}
}

func main() {
	hubURL := flag.String("hub", "ws://127.0.0.1:8080/ws/agent", "Hub WebSocket Agent endpoint")
	count := flag.Int("count", 20, "Number of mock agents to run")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("Starting %d Mock Agents connecting to %s...", *count, *hubURL)

	var wg sync.WaitGroup
	for i := 1; i <= *count; i++ {
		agent := &MockAgent{
			ID:     fmt.Sprintf("cli_%016x", i),
			IP:     fmt.Sprintf("192.168.1.%d", 100+i),
			MAC:    fmt.Sprintf("aa:bb:cc:11:22:%02x", i),
			Status: protocol.ClientStatusOnlineWindows,
			Mode:   protocol.AgentModeWindows,
			HubURL: *hubURL,
		}

		wg.Add(1)
		go func(a *MockAgent) {
			defer wg.Done()
			if err := a.ConnectAndRun(ctx, false); err != nil {
				log.Printf("[%s] agent finished: %v", a.ID, err)
			}
		}(agent)
	}

	<-ctx.Done()
	wg.Wait()
	log.Println("All mock agents terminated.")
}

// Helper for testing export
func NewMockAgent(id, ip, mac string, mode protocol.AgentMode, hubURL string, tlsConf *tls.Config) *MockAgent {
	return &MockAgent{
		ID:        id,
		IP:        ip,
		MAC:       mac,
		Status:    protocol.ClientStatusOnlineWindows,
		Mode:      mode,
		HubURL:    hubURL,
		TLSConfig: tlsConf,
	}
}

// Silence unused secure import if not in main
var _ = secure.NeedsRotation
