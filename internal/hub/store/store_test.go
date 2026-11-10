package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/owner/back-ez/internal/protocol"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "backez-store-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(dir, "test.db")
	st, err := Open(dbPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("failed to open test store: %v", err)
	}

	cleanup := func() {
		_ = st.Close()
		_ = os.RemoveAll(dir)
	}
	return st, cleanup
}

func TestStore_AdminAndSession(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	admin := AdminUser{
		ID:           "adm_01",
		Username:     "admin",
		PasswordHash: "$2a$12$e8Y6b...",
		CreatedAt:    "2026-11-10T00:00:00Z",
		UpdatedAt:    "2026-11-10T00:00:00Z",
	}

	if err := st.CreateAdmin(admin); err != nil {
		t.Fatalf("failed to create admin: %v", err)
	}

	got, err := st.GetAdminByUsername("admin")
	if err != nil {
		t.Fatalf("failed to get admin: %v", err)
	}
	if got.ID != admin.ID {
		t.Errorf("expected admin ID %s, got %s", admin.ID, got.ID)
	}

	// Update password
	if err := st.UpdateAdminPassword(admin.ID, "$2a$12$newhash...", "2026-11-10T01:00:00Z"); err != nil {
		t.Fatalf("failed to update password: %v", err)
	}
	updated, _ := st.GetAdminByUsername("admin")
	if updated.PasswordHash != "$2a$12$newhash..." {
		t.Errorf("password hash was not updated")
	}

	// Session
	sess := Session{
		Token:        "sess_token_123",
		AdminID:      admin.ID,
		CSRFToken:    "csrf_token_456",
		ExpiresAt:    "2026-11-10T08:00:00Z",
		CreatedAt:    "2026-11-10T00:00:00Z",
		LastActiveAt: "2026-11-10T00:00:00Z",
	}
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	gotSess, err := st.GetSession("sess_token_123")
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if gotSess.CSRFToken != sess.CSRFToken {
		t.Errorf("expected CSRF token %s, got %s", sess.CSRFToken, gotSess.CSRFToken)
	}

	// Delete session
	if err := st.DeleteSession(sess.Token); err != nil {
		t.Fatalf("failed to delete session: %v", err)
	}
	if _, err := st.GetSession(sess.Token); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for deleted session, got %v", err)
	}
}

func TestStore_IPGroup_UniqueIPConstraint(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	group1 := protocol.IPGroup{
		GroupID:   "grp_row_1",
		GroupName: "第一排",
		IPList:    []string{"192.168.1.101", "192.168.1.102"},
		CreatedAt: "2026-11-10T00:00:00Z",
	}
	if err := st.CreateOrUpdateGroup(group1); err != nil {
		t.Fatalf("failed to create group1: %v", err)
	}

	g1, err := st.GetGroup("grp_row_1")
	if err != nil {
		t.Fatalf("failed to get group1: %v", err)
	}
	if len(g1.IPList) != 2 {
		t.Errorf("expected 2 IPs in group1, got %d", len(g1.IPList))
	}

	// Attempt to add 192.168.1.101 to group2 -> MUST fail with ErrIPInOtherGroup
	group2 := protocol.IPGroup{
		GroupID:   "grp_row_2",
		GroupName: "第二排",
		IPList:    []string{"192.168.1.101", "192.168.1.103"},
		CreatedAt: "2026-11-10T00:00:00Z",
	}
	err = st.CreateOrUpdateGroup(group2)
	if !errors.Is(err, ErrIPInOtherGroup) {
		t.Fatalf("expected ErrIPInOtherGroup, got %v", err)
	}

	// Verify group2 was NOT created
	if _, err := st.GetGroup("grp_row_2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected group2 not found, got %v", err)
	}
}

func TestStore_Client_StableIDAcrossIPChanges(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	mac := "aa:bb:cc:11:22:33"
	src := protocol.MACSourceAgent
	client := protocol.ClientNode{
		ClientID:            "cli_9f2c4b1e8a7d4c3f",
		CurrentIP:           "192.168.1.101",
		MACAddress:          &mac,
		MACSource:           &src,
		Hostname:            "LAB-PC-01",
		OSEdition:           "Windows 11 Education",
		AgentVersion:        "0.1.0",
		PEReady:             true,
		Status:              protocol.ClientStatusOnlineWindows,
		AutoRestoreOnReboot: true,
		LastSeen:            "2026-11-10T00:00:00Z",
	}

	if err := st.UpsertClient(client); err != nil {
		t.Fatalf("failed to upsert client: %v", err)
	}

	// Add restore point
	points := []protocol.RestorePoint{
		{
			PointID:     "rp_base",
			Name:        "Base",
			Description: "Base image",
			VHDXFile:    "base.vhdx",
			Depth:       0,
			SizeBytes:   32212254720,
			IsBase:      true,
			IsActive:    true,
			State:       protocol.RestorePointStateReady,
			CreatedBy:   protocol.CreatedBySystem,
			CreatedAt:   "2026-11-10T00:00:00Z",
		},
	}
	if err := st.SaveRestorePoints(client.ClientID, points); err != nil {
		t.Fatalf("failed to save restore points: %v", err)
	}

	// Change client IP (e.g. DHCP renewed)
	client.CurrentIP = "192.168.1.150"
	client.LastSeen = "2026-11-10T00:05:00Z"
	if err := st.UpsertClient(client); err != nil {
		t.Fatalf("failed to update client with new IP: %v", err)
	}

	// Find by stable client_id
	byID, err := st.GetClient(client.ClientID)
	if err != nil {
		t.Fatalf("failed to get client by ID: %v", err)
	}
	if byID.CurrentIP != "192.168.1.150" {
		t.Errorf("expected updated IP 192.168.1.150, got %s", byID.CurrentIP)
	}
	if len(byID.RestorePointIDs) != 1 || byID.RestorePointIDs[0] != "rp_base" {
		t.Errorf("restore points broken across IP change: %+v", byID.RestorePointIDs)
	}

	// Find by new IP
	byNewIP, err := st.GetClientByIP("192.168.1.150")
	if err != nil {
		t.Fatalf("failed to get client by new IP: %v", err)
	}
	if byNewIP.ClientID != client.ClientID {
		t.Errorf("client ID mismatch: %s vs %s", byNewIP.ClientID, client.ClientID)
	}
}

func TestStore_ConcurrentWrites(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	const workers = 10
	const writesPerWorker = 20

	var wg sync.WaitGroup
	errCh := make(chan error, workers*writesPerWorker)

	for i := 0; i < workers; i++ {
		workerID := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writesPerWorker; j++ {
				ip := fmt.Sprintf("192.168.%d.%d", workerID+1, j+1)
				clientID := fmt.Sprintf("cli_%02d_%04d_abcdef", workerID, j)
				c := protocol.ClientNode{
					ClientID:     clientID,
					CurrentIP:    ip,
					Hostname:     fmt.Sprintf("PC-%d-%d", workerID, j),
					OSEdition:    "Windows 11 Pro",
					AgentVersion: "0.1.0",
					Status:       protocol.ClientStatusOnlineWindows,
					LastSeen:     time.Now().UTC().Format(time.RFC3339),
				}
				if err := st.UpsertClient(c); err != nil {
					errCh <- err
					return
				}

				audit := AuditEntry{
					Timestamp: time.Now().UTC().Format(time.RFC3339),
					Actor:     "tester",
					Action:    "CLIENT_UPDATE",
					TargetIP:  ip,
					Success:   true,
				}
				if err := st.RecordAudit(audit); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent write error: %v", err)
		}
	}

	clients, err := st.ListClients("")
	if err != nil {
		t.Fatalf("failed to list clients: %v", err)
	}
	if len(clients) != workers*writesPerWorker {
		t.Errorf("expected %d clients, got %d", workers*writesPerWorker, len(clients))
	}
}

func TestStore_DeployTaskAndAudit(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	task := protocol.DeployTask{
		TaskID:              "task_001",
		State:               protocol.TaskStatePendingPrepare,
		MasterIP:            "192.168.1.100",
		TargetIPs:           []string{"192.168.1.101", "192.168.1.102"},
		ImageSHA256:         "abcdef1234567890",
		ImageBytes:          1024000,
		ImagePath:           "base.vhdx",
		ResetsRestorePoints: true,
		CreatedAt:           "2026-11-10T00:00:00Z",
	}

	if err := st.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	gotTask, err := st.GetTask(task.TaskID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if gotTask.State != protocol.TaskStatePendingPrepare {
		t.Errorf("expected state PENDING_PREPARE, got %s", gotTask.State)
	}

	// Update task
	task.State = protocol.TaskStateCasting
	started := "2026-11-10T00:01:00Z"
	task.StartedAt = &started
	if err := st.UpdateTask(task); err != nil {
		t.Fatalf("failed to update task: %v", err)
	}

	updatedTask, _ := st.GetTask(task.TaskID)
	if updatedTask.State != protocol.TaskStateCasting || updatedTask.StartedAt == nil {
		t.Errorf("task was not updated correctly: %+v", updatedTask)
	}

	// Audit log
	audit := AuditEntry{
		Timestamp: "2026-11-10T00:00:00Z",
		Actor:     "admin",
		Action:    "DEPLOY_START",
		TargetIP:  "192.168.1.100",
		Details:   `{"task_id":"task_001"}`,
		Success:   true,
	}
	if err := st.RecordAudit(audit); err != nil {
		t.Fatalf("failed to record audit: %v", err)
	}

	audits, err := st.ListAudit(10, 0)
	if err != nil {
		t.Fatalf("failed to list audit: %v", err)
	}
	if len(audits) != 1 || audits[0].Action != "DEPLOY_START" {
		t.Errorf("unexpected audit log: %+v", audits)
	}
}

func TestStore_SchemaVersionUpgrade(t *testing.T) {
	dir, err := os.MkdirTemp("", "backez-migration-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)
	dbPath := filepath.Join(dir, "migrate.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	_ = st.Close()

	// Re-open existing database to verify idempotent migration execution
	st2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to re-open store: %v", err)
	}
	defer st2.Close()

	var version int
	row := st2.DB().QueryRow(`SELECT MAX(version) FROM schema_version`)
	if err := row.Scan(&version); err != nil {
		t.Fatalf("failed to query schema version: %v", err)
	}
	if version < 1 {
		t.Errorf("expected schema version >= 1, got %d", version)
	}
}
