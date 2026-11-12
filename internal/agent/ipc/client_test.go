package ipc

import (
	"errors"
	"net"
	"testing"

	"github.com/owner/back-ez/internal/protocol"
)

type mockDelegate struct{}

func (m *mockDelegate) GetAgentInfo() (any, error) {
	return map[string]any{"client_id": "cli_test_123", "status": "ONLINE_WINDOWS"}, nil
}
func (m *mockDelegate) GetStatus() (any, error) {
	return map[string]any{"status": "ONLINE_WINDOWS"}, nil
}
func (m *mockDelegate) ListRestorePoints() ([]protocol.RestorePoint, error) {
	return []protocol.RestorePoint{{PointID: "rp_base", Name: "Base", Depth: 0}}, nil
}
func (m *mockDelegate) CreateRestorePoint(name, desc string) error { return nil }
func (m *mockDelegate) SwitchRestorePoint(pointID string) error    { return nil }
func (m *mockDelegate) DeleteRestorePoint(pointID string) error    { return nil }
func (m *mockDelegate) SetAutoRestore(enabled bool) error          { return nil }
func (m *mockDelegate) SetHubEndpoint(hubURL, token string) error  { return nil }
func (m *mockDelegate) UninstallService() error                    { return nil }

func setupTestIPC(t *testing.T) (*Client, *Server, func()) {
	t.Helper()

	serverConn, clientConn := net.Pipe()

	server, err := NewServer(&mockDelegate{}, "correctpass")
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	go server.HandleConn(serverConn)

	client := NewClient(clientConn)

	cleanup := func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	}
	return client, server, cleanup
}

func TestIPCClient_ReadOnlyMethodsWithoutPassword(t *testing.T) {
	client, _, cleanup := setupTestIPC(t)
	defer cleanup()

	// 1. GetAgentInfo
	info, err := client.GetAgentInfo()
	if err != nil {
		t.Fatalf("GetAgentInfo failed: %v", err)
	}
	if info["client_id"] != "cli_test_123" {
		t.Errorf("unexpected client_id: %v", info["client_id"])
	}

	// 2. GetStatus
	status, err := client.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if status["status"] != "ONLINE_WINDOWS" {
		t.Errorf("unexpected status: %v", status["status"])
	}

	// 3. ListRestorePoints
	points, err := client.ListRestorePoints()
	if err != nil {
		t.Fatalf("ListRestorePoints failed: %v", err)
	}
	if len(points) != 1 || points[0].PointID != "rp_base" {
		t.Errorf("unexpected points: %+v", points)
	}
}

func TestIPCClient_MutatingMethodsRequirePasswordAndRateLimit(t *testing.T) {
	client, _, cleanup := setupTestIPC(t)
	defer cleanup()

	// 1. Without password -> PASSWORD_REQUIRED
	err := client.CreateRestorePoint("", "NewRP", "Desc")
	if err == nil || !errors.Is(err, errors.New(protocol.ErrPasswordRequired)) && err.Error() != protocol.ErrPasswordRequired {
		t.Errorf("expected PASSWORD_REQUIRED, got %v", err)
	}

	// 2. 4 failed attempts with wrong password
	for i := 0; i < 4; i++ {
		err = client.CreateRestorePoint("wrong", "NewRP", "Desc")
		if err == nil || err.Error() != protocol.ErrUnauthorized {
			t.Errorf("attempt %d: expected UNAUTHORIZED, got %v", i+1, err)
		}
	}

	// 3. 5th failed attempt -> RATE_LIMITED
	err = client.CreateRestorePoint("wrong", "NewRP", "Desc")
	if err == nil || err.Error() != protocol.ErrRateLimited {
		t.Fatalf("5th attempt: expected RATE_LIMITED, got %v", err)
	}

	// 4. Even with correct password, now locked out
	err = client.CreateRestorePoint("correctpass", "NewRP", "Desc")
	if err == nil || err.Error() != protocol.ErrRateLimited {
		t.Fatalf("expected RATE_LIMITED during lockout, got %v", err)
	}
}

func TestIPCClient_SuccessfulMutatingActionWithPassword(t *testing.T) {
	client, _, cleanup := setupTestIPC(t)
	defer cleanup()

	err := client.CreateRestorePoint("correctpass", "新還原點", "說明")
	if err != nil {
		t.Fatalf("expected success with correct password, got %v", err)
	}

	err = client.SwitchRestorePoint("correctpass", "rp_base")
	if err != nil {
		t.Fatalf("expected switch success with correct password, got %v", err)
	}
}
