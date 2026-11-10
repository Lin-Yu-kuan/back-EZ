package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/owner/back-ez/internal/hub/server"
	"github.com/owner/back-ez/internal/hub/store"
	"github.com/owner/back-ez/internal/hub/ws"
	"github.com/owner/back-ez/internal/protocol"
)

func setupMockAgentTestEnv(t *testing.T) (*ws.HubWS, *store.Store, string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "backez-mockagent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(dir, "hub.db")
	st, err := store.Open(dbPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("failed to open store: %v", err)
	}

	svc := server.NewService(st)
	srv := server.NewServer(svc)
	hws := ws.NewHubWS(svc, st)
	srv.RegisterWS(hws.HandleUIWS, hws.HandleAgentWS)

	ts := httptest.NewServer(srv.Handler())
	agentWSURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/agent"

	cleanup := func() {
		ts.Close()
		_ = st.Close()
		_ = os.RemoveAll(dir)
	}
	return hws, st, agentWSURL, cleanup
}

func waitForStatus(t *testing.T, st *store.Store, clientID string, want protocol.ClientStatus, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := st.GetClient(clientID)
		if err == nil && c.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	c, _ := st.GetClient(clientID)
	actual := "not_found"
	if c != nil {
		actual = string(c.Status)
	}
	t.Fatalf("timed out waiting for client %s to reach %s (current: %s)", clientID, want, actual)
}

func TestMockAgent_20Nodes_FullDeploymentFlow(t *testing.T) {
	_, st, agentWSURL, cleanup := setupMockAgentTestEnv(t)
	defer cleanup()

	const nodeCount = 20
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	agents := make([]*MockAgent, nodeCount)

	// 1. Launch 20 mock agents in PE mode (READY_FOR_CAST)
	for i := 0; i < nodeCount; i++ {
		id := fmt.Sprintf("cli_%016x", i+1)
		ip := fmt.Sprintf("192.168.1.%d", 101+i)
		mac := fmt.Sprintf("aa:bb:cc:11:22:%02x", i+1)

		a := NewMockAgent(id, ip, mac, protocol.AgentModePE, agentWSURL, nil)
		agents[i] = a

		wg.Add(1)
		go func(agent *MockAgent) {
			defer wg.Done()
			_ = agent.ConnectAndRun(ctx, false)
		}(a)
	}

	// Verify all 20 reached READY_FOR_CAST
	for _, a := range agents {
		waitForStatus(t, st, a.ID, protocol.ClientStatusReadyForCast, 3*time.Second)
	}

	// 2. Drive transition: READY_FOR_CAST -> DEPLOYING
	for _, a := range agents {
		if err := a.UpdateStatus(ctx, protocol.ClientStatusDeploying); err != nil {
			t.Fatalf("failed to transition to DEPLOYING: %v", err)
		}
	}
	for _, a := range agents {
		waitForStatus(t, st, a.ID, protocol.ClientStatusDeploying, 3*time.Second)
	}

	// 3. Drive transition: DEPLOYING -> VERIFYING
	for _, a := range agents {
		if err := a.UpdateStatus(ctx, protocol.ClientStatusVerifying); err != nil {
			t.Fatalf("failed to transition to VERIFYING: %v", err)
		}
	}
	for _, a := range agents {
		waitForStatus(t, st, a.ID, protocol.ClientStatusVerifying, 3*time.Second)
	}

	// 4. Drive transition: VERIFYING -> POST_RESTORING_NET
	for _, a := range agents {
		if err := a.UpdateStatus(ctx, protocol.ClientStatusPostRestoringNet); err != nil {
			t.Fatalf("failed to transition to POST_RESTORING_NET: %v", err)
		}
	}
	for _, a := range agents {
		waitForStatus(t, st, a.ID, protocol.ClientStatusPostRestoringNet, 3*time.Second)
	}

	cancel()
	wg.Wait()
}

func TestMockAgent_IllegalTransitionRejected(t *testing.T) {
	_, st, agentWSURL, cleanup := setupMockAgentTestEnv(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	agent := NewMockAgent("cli_illegal_test_01", "192.168.1.200", "aa:bb:cc:11:22:99", protocol.AgentModeWindows, agentWSURL, nil)

	go func() {
		_ = agent.ConnectAndRun(ctx, true) // triggerIllegal = true (ONLINE_WINDOWS -> DEPLOYING)
	}()

	time.Sleep(100 * time.Millisecond)

	// Status in DB must NOT be changed to DEPLOYING!
	c, err := st.GetClient(agent.ID)
	if err != nil {
		t.Fatalf("failed to get client: %v", err)
	}
	if c.Status == protocol.ClientStatusDeploying {
		t.Fatalf("RED LINE: Hub incorrectly allowed illegal transition to DEPLOYING!")
	}
	if c.Status != protocol.ClientStatusOnlineWindows {
		t.Errorf("expected status to remain ONLINE_WINDOWS, got %s", c.Status)
	}
}
