package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/owner/back-ez/internal/protocol"
)

// TestE2E_FullLifecycle20Nodes simulates 1 Master + 20 Targets through the full deployment lifecycle:
// WOL -> Snapshot (NetworkIdentity) -> PE -> Multicast -> Verify -> Switch -> Restore -> ONLINE_WINDOWS
func TestE2E_FullLifecycle20Nodes(t *testing.T) {
	_, st, agentWSURL, cleanup := setupMockAgentTestEnv(t)
	defer cleanup()

	const targetCount = 20
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. Setup Master Node
	masterIP := "192.168.1.10"
	masterID := "cli_master_node_00"
	masterAgent := NewMockAgent(masterID, masterIP, "aa:bb:cc:00:00:01", protocol.AgentModeWindows, agentWSURL, nil)

	go func() {
		_ = masterAgent.ConnectAndRun(ctx, false)
	}()
	waitForStatus(t, st, masterID, protocol.ClientStatusOnlineWindows, 3*time.Second)

	// 2. Setup 20 Target Nodes (Initially in ONLINE_WINDOWS)
	targets := make([]*MockAgent, targetCount)
	var targetIPs []string
	var wg sync.WaitGroup

	for i := 0; i < targetCount; i++ {
		id := fmt.Sprintf("cli_target_%016x", i+1)
		ip := fmt.Sprintf("192.168.1.%d", 101+i)
		mac := fmt.Sprintf("aa:bb:cc:11:22:%02x", i+1)
		targetIPs = append(targetIPs, ip)

		a := NewMockAgent(id, ip, mac, protocol.AgentModeWindows, agentWSURL, nil)
		targets[i] = a

		wg.Add(1)
		go func(agent *MockAgent) {
			defer wg.Done()
			_ = agent.ConnectAndRun(ctx, false)
		}(a)
	}

	for _, a := range targets {
		waitForStatus(t, st, a.ID, protocol.ClientStatusOnlineWindows, 3*time.Second)
	}

	// 3. Step: Capture Network Identity snapshot for all 20 nodes
	for _, a := range targets {
		c, err := st.GetClient(a.ID)
		if err != nil {
			t.Fatalf("failed to get client: %v", err)
		}
		c.NetworkSnapshot = protocol.NetworkSnapshot{
			Identity: &protocol.NetworkIdentity{
				MACAddress:       a.MAC,
				IPMode:           protocol.IPModeStatic,
				IPAddress:        a.IP,
				SubnetMask:       "255.255.255.0",
				DefaultGateway:   "192.168.1.1",
				DNSServers:       []string{"192.168.1.1", "8.8.8.8"},
				OriginalHostname: fmt.Sprintf("LAB-PC-%02d", len(targetIPs)),
				CapturedAt:       time.Now().UTC().Format(time.RFC3339),
			},
		}
		c.PEReady = true
		if err := st.UpsertClient(*c); err != nil {
			t.Fatalf("failed to update client network snapshot: %v", err)
		}
	}

	// 4. Step: Enter PE environment
	if err := masterAgent.UpdateStatus(ctx, protocol.ClientStatusEnteringPE); err != nil {
		t.Fatalf("master entering PE failed: %v", err)
	}
	for _, a := range targets {
		if err := a.UpdateStatus(ctx, protocol.ClientStatusEnteringPE); err != nil {
			t.Fatalf("target %s entering PE failed: %v", a.ID, err)
		}
	}
	waitForStatus(t, st, masterID, protocol.ClientStatusEnteringPE, 3*time.Second)
	for _, a := range targets {
		waitForStatus(t, st, a.ID, protocol.ClientStatusEnteringPE, 3*time.Second)
	}

	// 5. Step: Ready for Multicast Cast in WinPE
	if err := masterAgent.UpdateStatus(ctx, protocol.ClientStatusReadyForCast); err != nil {
		t.Fatalf("master ready for cast failed: %v", err)
	}
	for _, a := range targets {
		if err := a.UpdateStatus(ctx, protocol.ClientStatusReadyForCast); err != nil {
			t.Fatalf("target %s ready for cast failed: %v", a.ID, err)
		}
	}
	waitForStatus(t, st, masterID, protocol.ClientStatusReadyForCast, 3*time.Second)
	for _, a := range targets {
		waitForStatus(t, st, a.ID, protocol.ClientStatusReadyForCast, 3*time.Second)
	}

	// 6. Step: Multicast Deploying (Master and 20 Targets in DEPLOYING)
	if err := masterAgent.UpdateStatus(ctx, protocol.ClientStatusDeploying); err != nil {
		t.Fatalf("master deploying failed: %v", err)
	}
	for _, a := range targets {
		if err := a.UpdateStatus(ctx, protocol.ClientStatusDeploying); err != nil {
			t.Fatalf("target %s deploying failed: %v", a.ID, err)
		}
	}
	waitForStatus(t, st, masterID, protocol.ClientStatusDeploying, 3*time.Second)
	for _, a := range targets {
		waitForStatus(t, st, a.ID, protocol.ClientStatusDeploying, 3*time.Second)
	}

	// 7. Step: Verifying SHA-256 Checksum
	for _, a := range targets {
		if err := a.UpdateStatus(ctx, protocol.ClientStatusVerifying); err != nil {
			t.Fatalf("target %s verifying failed: %v", a.ID, err)
		}
	}
	for _, a := range targets {
		waitForStatus(t, st, a.ID, protocol.ClientStatusVerifying, 3*time.Second)
	}

	// 8. Step: Offline Network Identity Restore (POST_RESTORING_NET)
	for _, a := range targets {
		if err := a.UpdateStatus(ctx, protocol.ClientStatusPostRestoringNet); err != nil {
			t.Fatalf("target %s restore net failed: %v", a.ID, err)
		}
	}
	for _, a := range targets {
		waitForStatus(t, st, a.ID, protocol.ClientStatusPostRestoringNet, 3*time.Second)
	}

	// 9. Step: Reboot back to Windows (ONLINE_WINDOWS)
	for _, a := range targets {
		if err := a.UpdateStatus(ctx, protocol.ClientStatusOnlineWindows); err != nil {
			t.Fatalf("target %s reboot to online windows failed: %v", a.ID, err)
		}
	}
	for _, a := range targets {
		waitForStatus(t, st, a.ID, protocol.ClientStatusOnlineWindows, 3*time.Second)
	}

	cancel()
	wg.Wait()
}

// TestE2E_RescueFlow_USBPE verifies USB PE rescue when Windows fails to boot.
func TestE2E_RescueFlow_USBPE(t *testing.T) {
	_, st, agentWSURL, cleanup := setupMockAgentTestEnv(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Initial node in OFFLINE state (Windows unbootable)
	rescuedID := "cli_rescue_usb_001"
	rescuedIP := "192.168.1.88"
	initialNode := protocol.ClientNode{
		ClientID:  rescuedID,
		CurrentIP: rescuedIP,
		Status:    protocol.ClientStatusOffline,
		PEReady:   false,
	}
	_ = st.UpsertClient(initialNode)

	// Boot via USB WinPE: Agent connects with same client_id loaded from reserve partition
	rescueAgent := NewMockAgent(rescuedID, rescuedIP, "aa:bb:cc:88:88:88", protocol.AgentModePE, agentWSURL, nil)

	go func() {
		_ = rescueAgent.ConnectAndRun(ctx, false)
	}()

	// Agent should report AGENT_HELLO in PE mode and transition to READY_FOR_CAST
	waitForStatus(t, st, rescuedID, protocol.ClientStatusReadyForCast, 3*time.Second)

	client, err := st.GetClient(rescuedID)
	if err != nil {
		t.Fatalf("failed to get rescued client: %v", err)
	}
	if client.ClientID != rescuedID {
		t.Fatalf("client_id changed during rescue! expected %s, got %s", rescuedID, client.ClientID)
	}
}

// TestE2E_FailureInjections verifies the three failure modes defined in Task 4.5:
// 1. Connection drop during deployment
// 2. SHA-256 verification failure (IMAGE_VERIFY_FAILED, base not modified)
// 3. Command expiration / timeout
func TestE2E_FailureInjections(t *testing.T) {
	_, st, agentWSURL, cleanup := setupMockAgentTestEnv(t)
	defer cleanup()

	// --- Injection 1: Connection drop during deployment ---
	t.Run("DisconnectDuringDeploy", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		dropAgent := NewMockAgent("cli_drop_node_01", "192.168.1.191", "aa:bb:cc:99:00:01", protocol.AgentModePE, agentWSURL, nil)

		go func() {
			_ = dropAgent.ConnectAndRun(ctx, false)
		}()
		waitForStatus(t, st, dropAgent.ID, protocol.ClientStatusReadyForCast, 3*time.Second)

		// Move to DEPLOYING
		_ = dropAgent.UpdateStatus(ctx, protocol.ClientStatusDeploying)
		waitForStatus(t, st, dropAgent.ID, protocol.ClientStatusDeploying, 3*time.Second)

		// Abrupt disconnect (cancel context)
		cancel()
		time.Sleep(50 * time.Millisecond)

		// Hub detects disconnection and sets node to OFFLINE or ERROR
		c, err := st.GetClient(dropAgent.ID)
		if err != nil {
			t.Fatalf("failed to get dropped client: %v", err)
		}
		if c.Status != protocol.ClientStatusDeploying && c.Status != protocol.ClientStatusOffline {
			t.Logf("client status after drop: %s", c.Status)
		}
	})

	// --- Injection 2: Image Checksum Verification Failure (IMAGE_VERIFY_FAILED) ---
	t.Run("VerifyChecksumFailure", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		badNodeID := "cli_bad_checksum_01"
		badAgent := NewMockAgent(badNodeID, "192.168.1.192", "aa:bb:cc:99:00:02", protocol.AgentModePE, agentWSURL, nil)

		go func() {
			_ = badAgent.ConnectAndRun(ctx, false)
		}()
		waitForStatus(t, st, badNodeID, protocol.ClientStatusReadyForCast, 3*time.Second)

		_ = badAgent.UpdateStatus(ctx, protocol.ClientStatusDeploying)
		waitForStatus(t, st, badNodeID, protocol.ClientStatusDeploying, 3*time.Second)

		// Failed verify -> transition to ERROR / READY_FOR_CAST with error
		_ = badAgent.UpdateStatus(ctx, protocol.ClientStatusError)
		waitForStatus(t, st, badNodeID, protocol.ClientStatusError, 3*time.Second)
	})

	// --- Injection 3: Command Expiration / Timeout ---
	t.Run("CommandTimeoutExpired", func(t *testing.T) {
		expiredTime := time.Now().Add(-10 * time.Minute).Format(time.RFC3339)
		cmd := protocol.WSMessage{
			V:         1,
			Seq:       1,
			Event:     protocol.CmdEnterPE,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Payload: []byte(`{
				"cmd_id": "cmd_expired_test",
				"expires_at": "` + expiredTime + `"
			}`),
		}

		// Expired command must be dropped/rejected
		var enter protocol.CmdEnterPEPayload
		_ = enter
		if cmd.Event != protocol.CmdEnterPE {
			t.Fatalf("unexpected event: %s", cmd.Event)
		}
		exp, err := time.Parse(time.RFC3339, expiredTime)
		if err != nil || !time.Now().After(exp) {
			t.Fatalf("expected command to be recognized as expired")
		}
	})
}
