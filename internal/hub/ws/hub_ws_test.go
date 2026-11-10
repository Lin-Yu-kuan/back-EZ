package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/owner/back-ez/internal/hub/server"
	"github.com/owner/back-ez/internal/hub/store"
	"github.com/owner/back-ez/internal/protocol"
	"golang.org/x/crypto/bcrypt"
)

func setupTestHubWS(t *testing.T) (*HubWS, *server.Server, *store.Store, *server.Service, string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "backez-ws-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(dir, "hub.db")
	st, err := store.Open(dbPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("failed to open store: %v", err)
	}

	// Create admin
	hash, _ := bcrypt.GenerateFromPassword([]byte("adminpass"), 12)
	admin := store.AdminUser{
		ID:           "adm_ws",
		Username:     "admin",
		PasswordHash: string(hash),
		CreatedAt:    "2026-11-10T00:00:00Z",
		UpdatedAt:    "2026-11-10T00:00:00Z",
	}
	_ = st.CreateAdmin(admin)

	svc := server.NewService(st)
	srv := server.NewServer(svc)
	hws := NewHubWS(svc, st)
	srv.RegisterWS(hws.HandleUIWS, hws.HandleAgentWS)

	// Login to get session
	loginRes, err := svc.Login("admin", "adminpass", "127.0.0.1")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	cleanup := func() {
		_ = st.Close()
		_ = os.RemoveAll(dir)
	}
	return hws, srv, st, svc, loginRes.SessionToken, cleanup
}

func TestHubWS_UIChannel_SeqAndHello(t *testing.T) {
	hws, srv, _, _, sessionToken, cleanup := setupTestHubWS(t)
	defer cleanup()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Unauthenticated connection -> 401
	_, _, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		t.Fatalf("expected dial to fail without session cookie")
	}

	// 2. Connect with Session Cookie
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{"backez_session=" + sessionToken},
		},
	}
	conn, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("dial failed with session: %v", err)
	}
	defer conn.CloseNow()

	// First message MUST be HELLO with seq == 1
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read HELLO: %v", err)
	}

	var helloMsg protocol.WSMessage
	if err := json.Unmarshal(data, &helloMsg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if helloMsg.Event != protocol.EventHello {
		t.Errorf("expected HELLO event, got %s", helloMsg.Event)
	}
	if helloMsg.Seq != 1 {
		t.Errorf("expected seq == 1 for first message, got %d", helloMsg.Seq)
	}

	// 3. Broadcast events and verify monotonic seq without gaps (seq: 2, 3, 4)
	hws.BroadcastToUI(protocol.EventWOLSent, protocol.WOLSentPayload{
		IPs:  []string{"192.168.1.101"},
		MACs: []string{"aa:bb:cc:11:22:33"},
	})
	hws.BroadcastToUI(protocol.EventTaskCreated, protocol.TaskCreatedPayload{
		Task: protocol.DeployTask{TaskID: "task_01"},
	})

	// Read message 2
	_, data2, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read event 2: %v", err)
	}
	var msg2 protocol.WSMessage
	_ = json.Unmarshal(data2, &msg2)
	if msg2.Event != protocol.EventWOLSent || msg2.Seq != 2 {
		t.Errorf("expected WOL_SENT seq 2, got %s seq %d", msg2.Event, msg2.Seq)
	}

	// Read message 3
	_, data3, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read event 3: %v", err)
	}
	var msg3 protocol.WSMessage
	_ = json.Unmarshal(data3, &msg3)
	if msg3.Event != protocol.EventTaskCreated || msg3.Seq != 3 {
		t.Errorf("expected TASK_CREATED seq 3, got %s seq %d", msg3.Event, msg3.Seq)
	}
}

func TestHubWS_AgentChannel_StateMachineAndIllegalTransition(t *testing.T) {
	_, srv, st, _, _, cleanup := setupTestHubWS(t)
	defer cleanup()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	agentWSURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/agent"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, agentWSURL, nil)
	if err != nil {
		t.Fatalf("dial agent WS failed: %v", err)
	}
	defer conn.CloseNow()

	clientID := "cli_0000000000000001"

	// 1. Send AGENT_HELLO
	helloMsg := protocol.WSMessage{
		V:         1,
		Seq:       1,
		Event:     protocol.ReportAgentHello,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload: []byte(`{
			"client_id": "` + clientID + `",
			"hostname": "TEST-PC",
			"os_edition": "Windows 11 Education",
			"agent_version": "0.1.0",
			"current_ip": "192.168.1.101",
			"pe_ready": true,
			"mode": "WINDOWS"
		}`),
	}
	helloBytes, _ := json.Marshal(helloMsg)
	if err := conn.Write(ctx, websocket.MessageText, helloBytes); err != nil {
		t.Fatalf("write AGENT_HELLO failed: %v", err)
	}

	// Wait for DB insertion
	time.Sleep(50 * time.Millisecond)
	c, err := st.GetClient(clientID)
	if err != nil {
		t.Fatalf("client was not saved to DB: %v", err)
	}
	if c.Status != protocol.ClientStatusOnlineWindows {
		t.Errorf("expected ONLINE_WINDOWS status, got %s", c.Status)
	}

	// 2. Trigger ILLEGAL status transition: ONLINE_WINDOWS -> DEPLOYING (SPEC §2.6 forbidden)
	illegalMsg := protocol.WSMessage{
		V:         1,
		Seq:       2,
		Event:     protocol.ReportAgentStatus,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload: []byte(`{
			"client_id": "` + clientID + `",
			"status": "DEPLOYING"
		}`),
	}
	illegalBytes, _ := json.Marshal(illegalMsg)
	if err := conn.Write(ctx, websocket.MessageText, illegalBytes); err != nil {
		t.Fatalf("write illegal status failed: %v", err)
	}

	// Read error response
	_, errResp, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read error response: %v", err)
	}
	var respMsg protocol.WSMessage
	_ = json.Unmarshal(errResp, &respMsg)
	if respMsg.Event != protocol.EventAgentError {
		t.Errorf("expected AGENT_ERROR event, got %s", respMsg.Event)
	}

	// RED LINE CHECK: Verify DB status was NOT changed!
	cAfter, _ := st.GetClient(clientID)
	if cAfter.Status != protocol.ClientStatusOnlineWindows {
		t.Errorf("RED LINE VIOLATION: status in DB was altered to %s after illegal transition!", cAfter.Status)
	}

	// 3. Trigger LEGAL status transition: ONLINE_WINDOWS -> ENTERING_PE
	legalMsg := protocol.WSMessage{
		V:         1,
		Seq:       3,
		Event:     protocol.ReportAgentStatus,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload: []byte(`{
			"client_id": "` + clientID + `",
			"status": "ENTERING_PE"
		}`),
	}
	legalBytes, _ := json.Marshal(legalMsg)
	if err := conn.Write(ctx, websocket.MessageText, legalBytes); err != nil {
		t.Fatalf("write legal status failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	cLegal, _ := st.GetClient(clientID)
	if cLegal.Status != protocol.ClientStatusEnteringPE {
		t.Errorf("expected status ENTERING_PE in DB, got %s", cLegal.Status)
	}
}

func TestHubWS_AgentCommandDispatch(t *testing.T) {
	hws, srv, _, _, _, cleanup := setupTestHubWS(t)
	defer cleanup()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	agentWSURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/agent"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, agentWSURL, nil)
	if err != nil {
		t.Fatalf("dial agent WS failed: %v", err)
	}
	defer conn.CloseNow()

	clientID := "cli_agent_cmd_test"

	// Register agent via AGENT_HELLO
	helloMsg := protocol.WSMessage{
		V:         1,
		Seq:       1,
		Event:     protocol.ReportAgentHello,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload: []byte(`{
			"client_id": "` + clientID + `",
			"hostname": "AGENT-CMD-PC",
			"os_edition": "Windows 11 Education",
			"agent_version": "0.1.0",
			"current_ip": "192.168.1.102",
			"pe_ready": true,
			"mode": "WINDOWS"
		}`),
	}
	helloBytes, _ := json.Marshal(helloMsg)
	_ = conn.Write(ctx, websocket.MessageText, helloBytes)
	time.Sleep(50 * time.Millisecond)

	// In goroutine, Agent reads command and replies with CMD_RESULT
	go func() {
		_, cmdData, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var cmdMsg protocol.WSMessage
		_ = json.Unmarshal(cmdData, &cmdMsg)

		var pingPayload protocol.CmdPingPayload
		_ = json.Unmarshal(cmdMsg.Payload, &pingPayload)

		// Reply
		resultMsg := protocol.WSMessage{
			V:         1,
			Seq:       2,
			Event:     protocol.ReportCmdResult,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Payload: []byte(`{
				"cmd_id": "` + pingPayload.CmdID + `",
				"ok": true
			}`),
		}
		resBytes, _ := json.Marshal(resultMsg)
		_ = conn.Write(ctx, websocket.MessageText, resBytes)
	}()

	cmdID := "cmd_ping_001"
	expires := time.Now().Add(3 * time.Second)
	res, err := hws.SendCommandToAgent(ctx, clientID, protocol.CmdPing, protocol.CmdPingPayload{CmdID: cmdID}, cmdID, expires)
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}
	if !res.OK || res.CmdID != cmdID {
		t.Errorf("unexpected command result: %+v", res)
	}
}
