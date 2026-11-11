package service

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/owner/back-ez/internal/protocol"
)

func setupTestAgentCore(t *testing.T) (*AgentCore, string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "backez-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cfg, cfgPath, err := LoadOrCreateConfig(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("failed to load/create config: %v", err)
	}

	core, err := NewAgentCore(*cfg, cfgPath, "localpassword")
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("failed to initialize agent core: %v", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	return core, dir, cleanup
}

func sendIPCRequest(t *testing.T, conn net.Conn, req protocol.IPCRequest) protocol.IPCResponse {
	t.Helper()
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	_, err := conn.Write(data)
	if err != nil {
		t.Fatalf("failed to write to pipe: %v", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("failed to read from pipe: %v", err)
	}

	var resp protocol.IPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v, raw: %s", err, string(line))
	}
	return resp
}

func TestAgentCore_ClientIDPersistenceAndFormat(t *testing.T) {
	dir, err := os.MkdirTemp("", "backez-clientid-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	// First load
	cfg1, _, err := LoadOrCreateConfig(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateConfig 1 failed: %v", err)
	}

	// Verify format ^cli_[0-9a-f]{16}$ (SPEC §2.3)
	matched, _ := regexp.MatchString(`^cli_[0-9a-f]{16}$`, cfg1.ClientID)
	if !matched {
		t.Errorf("ClientID %s does not match required format ^cli_[0-9a-f]{16}$", cfg1.ClientID)
	}

	// Second load: MUST yield identical client_id across reboots/restarts
	cfg2, _, err := LoadOrCreateConfig(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateConfig 2 failed: %v", err)
	}

	if cfg1.ClientID != cfg2.ClientID {
		t.Fatalf("client_id changed on reload: %s vs %s", cfg1.ClientID, cfg2.ClientID)
	}
}

func TestAgentCore_IPCMethodsAndRateLimiting(t *testing.T) {
	core, _, cleanup := setupTestAgentCore(t)
	defer cleanup()

	// In-memory pipe listener
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()

	go func() {
		_ = core.RunIPCListener(l)
	}()

	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// 1. Read-only method: GetAgentInfo (NO password required)
	resp := sendIPCRequest(t, conn, protocol.IPCRequest{
		ID:     1,
		Method: "GetAgentInfo",
	})
	if !resp.OK {
		t.Errorf("expected GetAgentInfo to succeed without password, got %+v", resp.Error)
	}

	// 2. Read-only method: ListRestorePoints (NO password required)
	resp = sendIPCRequest(t, conn, protocol.IPCRequest{
		ID:     2,
		Method: "ListRestorePoints",
	})
	if !resp.OK {
		t.Errorf("expected ListRestorePoints to succeed, got %+v", resp.Error)
	}

	// 3. Mutating method WITHOUT password -> MUST fail with PASSWORD_REQUIRED
	resp = sendIPCRequest(t, conn, protocol.IPCRequest{
		ID:     3,
		Method: "CreateRestorePoint",
		Params: []byte(`{"name":"test"}`),
	})
	if resp.OK || resp.Error == nil || resp.Error.Code != protocol.ErrPasswordRequired {
		t.Errorf("expected PASSWORD_REQUIRED, got %+v", resp)
	}

	// 4. Mutating method with WRONG password -> 4 attempts (fail with UNAUTHORIZED)
	for i := 0; i < 4; i++ {
		resp = sendIPCRequest(t, conn, protocol.IPCRequest{
			ID:     int64(10 + i),
			Method: "CreateRestorePoint",
			Params: []byte(`{"password":"badpassword","name":"test"}`),
		})
		if resp.OK || resp.Error == nil || resp.Error.Code != protocol.ErrUnauthorized {
			t.Errorf("expected UNAUTHORIZED on attempt %d, got %+v", i+1, resp)
		}
	}

	// 5. 5th consecutive failure -> MUST trigger RATE_LIMITED lock (5 attempts / 5 mins)
	resp = sendIPCRequest(t, conn, protocol.IPCRequest{
		ID:     14,
		Method: "CreateRestorePoint",
		Params: []byte(`{"password":"badpassword","name":"test"}`),
	})
	if resp.OK || resp.Error == nil || resp.Error.Code != protocol.ErrRateLimited {
		t.Fatalf("expected RATE_LIMITED on 5th attempt, got %+v", resp)
	}

	// 6. Even with CORRECT password, while locked -> MUST still be RATE_LIMITED
	resp = sendIPCRequest(t, conn, protocol.IPCRequest{
		ID:     15,
		Method: "CreateRestorePoint",
		Params: []byte(`{"password":"localpassword","name":"test"}`),
	})
	if resp.OK || resp.Error == nil || resp.Error.Code != protocol.ErrRateLimited {
		t.Fatalf("expected RATE_LIMITED during lock period, got %+v", resp)
	}
}

func TestAgentCore_CommandExpirationAndPasswordSync(t *testing.T) {
	core, _, cleanup := setupTestAgentCore(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Expired command -> MUST be dropped / return expired
	expiredTime := time.Now().Add(-10 * time.Minute).Format(time.RFC3339)
	expiredCmd := protocol.WSMessage{
		V:         1,
		Seq:       1,
		Event:     protocol.CmdSyncPassword,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload: []byte(`{
			"cmd_id": "cmd_sync_old",
			"new_password": "newpassword123",
			"expires_at": "` + expiredTime + `"
		}`),
	}
	res, err := core.ExecuteCommand(ctx, expiredCmd)
	if err != nil {
		t.Fatalf("ExecuteCommand error: %v", err)
	}
	if res.OK || res.Error == nil || *res.Error != "command expired" {
		t.Errorf("expected expired command to fail with 'command expired', got %+v", res)
	}

	// 2. Valid command -> Sync password to "newpassword123"
	validTime := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	validCmd := protocol.WSMessage{
		V:         1,
		Seq:       2,
		Event:     protocol.CmdSyncPassword,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload: []byte(`{
			"cmd_id": "cmd_sync_new",
			"new_password": "newpassword123",
			"expires_at": "` + validTime + `"
		}`),
	}
	res, err = core.ExecuteCommand(ctx, validCmd)
	if err != nil || !res.OK {
		t.Fatalf("password sync failed: %v, res: %+v", err, res)
	}

	// 3. Test IPC with new password via pipe
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() { _ = core.RunIPCListener(l) }()

	conn, _ := net.Dial("tcp", l.Addr().String())
	defer conn.Close()

	ipcResp := sendIPCRequest(t, conn, protocol.IPCRequest{
		ID:     1,
		Method: "CreateRestorePoint",
		Params: []byte(`{"password":"newpassword123","name":"新還原點"}`),
	})
	if !ipcResp.OK {
		t.Errorf("expected new password to succeed, got %+v", ipcResp.Error)
	}
}

func TestAgentCore_CmdEnterPE(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := AgentConfig{
		ClientID:    "cli_0123456789abcdef",
		ReserveRoot: tmpDir,
	}
	core, err := NewAgentCore(cfg, filepath.Join(tmpDir, "agent.json"), "secret")
	if err != nil {
		t.Fatalf("failed to create agent core: %v", err)
	}

	ctx := context.Background()

	// 1. When pe_ready is false, command must fail with PE_NOT_READY
	core.SetPEReady(false)
	enterCmd := protocol.WSMessage{
		V:         1,
		Seq:       1,
		Event:     protocol.CmdEnterPE,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload: []byte(`{
			"cmd_id": "cmd_pe_01",
			"task_id": "task_01",
			"expires_at": "` + time.Now().Add(5*time.Minute).Format(time.RFC3339) + `"
		}`),
	}

	res, err := core.ExecuteCommand(ctx, enterCmd)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.OK || res.Error == nil || *res.Error != protocol.ErrPENotReady {
		t.Fatalf("expected PE_NOT_READY, got %+v", res)
	}

	// 2. When pe_ready is true, command succeeds and status transitions to ENTERING_PE
	core.SetPEReady(true)
	res, err = core.ExecuteCommand(ctx, enterCmd)
	if err != nil || !res.OK {
		t.Fatalf("expected CmdEnterPE to succeed, got res: %+v, err: %v", res, err)
	}

	infoAny, err := core.GetAgentInfo()
	if err != nil {
		t.Fatalf("GetAgentInfo error: %v", err)
	}
	info := infoAny.(map[string]any)
	if info["status"] != protocol.ClientStatusEnteringPE {
		t.Fatalf("expected status ENTERING_PE, got %v", info["status"])
	}
}

func TestAgentCore_CmdPrepareDeploy(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := AgentConfig{
		ClientID:    "cli_0123456789abcdef",
		ReserveRoot: tmpDir,
	}
	core, err := NewAgentCore(cfg, filepath.Join(tmpDir, "agent.json"), "secret")
	if err != nil {
		t.Fatalf("failed to create agent core: %v", err)
	}

	ctx := context.Background()

	prepCmd := protocol.WSMessage{
		V:         1,
		Seq:       1,
		Event:     protocol.CmdPrepareDeploy,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload: []byte(`{
			"cmd_id": "cmd_prep_01",
			"task_id": "task_deploy_01",
			"expires_at": "` + time.Now().Add(5*time.Minute).Format(time.RFC3339) + `"
		}`),
	}

	res, err := core.ExecuteCommand(ctx, prepCmd)
	if err != nil || !res.OK {
		t.Fatalf("expected CmdPrepareDeploy to succeed, got res: %+v, err: %v", res, err)
	}

	// Verify snapshot file exists in reserveRoot
	snapshotPath := filepath.Join(tmpDir, "network_identity.json")
	if _, err := os.Stat(snapshotPath); err != nil {
		t.Fatalf("expected network_identity.json to be created in reserveRoot, got err: %v", err)
	}
}
