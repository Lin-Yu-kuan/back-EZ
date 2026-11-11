package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/owner/back-ez/internal/hub/store"
	"github.com/owner/back-ez/internal/protocol"
	"golang.org/x/crypto/bcrypt"
)

func setupTestServer(t *testing.T) (*Server, *store.Store, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "backez-server-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(dir, "hub.db")
	st, err := store.Open(dbPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("failed to open store: %v", err)
	}

	// Create default admin user
	hash, _ := bcrypt.GenerateFromPassword([]byte("adminpass"), 12)
	admin := store.AdminUser{
		ID:           "adm_test",
		Username:     "admin",
		PasswordHash: string(hash),
		CreatedAt:    "2026-11-10T00:00:00Z",
		UpdatedAt:    "2026-11-10T00:00:00Z",
	}
	_ = st.CreateAdmin(admin)

	svc := NewService(st)
	srv := NewServer(svc)

	cleanup := func() {
		_ = st.Close()
		_ = os.RemoveAll(dir)
	}
	return srv, st, cleanup
}

func doRequest(handler http.Handler, method, path string, body any, sessionCookie string, csrfToken string) *httptest.ResponseRecorder {
	var bodyReader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(raw)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")

	if sessionCookie != "" {
		req.AddCookie(&http.Cookie{
			Name:  "backez_session",
			Value: sessionCookie,
		})
	}
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func parseAPIResponse[T any](t *testing.T, rec *httptest.ResponseRecorder) (bool, *T, *protocol.APIError) {
	t.Helper()
	var raw struct {
		OK    bool               `json:"ok"`
		Data  *T                 `json:"data"`
		Error *protocol.APIError `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("failed to decode response: %v; raw body: %s", err, rec.Body.String())
	}
	return raw.OK, raw.Data, raw.Error
}

func TestServer_AuthAndCSRF(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	handler := srv.Handler()

	// 1. Unauthenticated request to /api/v1/clients -> 401 UNAUTHORIZED
	rec := doRequest(handler, http.MethodGet, "/api/v1/clients", nil, "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}
	ok, _, apiErr := parseAPIResponse[any](t, rec)
	if ok || apiErr == nil || apiErr.Code != protocol.ErrUnauthorized {
		t.Errorf("expected UNAUTHORIZED error code, got %+v", apiErr)
	}

	// 2. Login with wrong password -> 401
	rec = doRequest(handler, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "wrongpassword",
	}, "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for bad password, got %d", rec.Code)
	}

	// 3. Login with correct password -> 200 + Cookie + CSRF
	rec = doRequest(handler, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "adminpass",
	}, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}

	var sessionCookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "backez_session" {
			sessionCookie = c.Value
		}
	}
	if sessionCookie == "" {
		t.Fatalf("missing backez_session cookie in login response")
	}

	var loginData map[string]string
	_, data, _ := parseAPIResponse[map[string]string](t, rec)
	loginData = *data
	csrfToken := loginData["csrf_token"]
	if csrfToken == "" {
		t.Fatalf("missing csrf_token in login response")
	}

	// 4. Mutating request without CSRF -> 403 FORBIDDEN
	group := protocol.IPGroup{
		GroupID:   "grp_test",
		GroupName: "測試排",
		IPList:    []string{"192.168.1.101"},
	}
	rec = doRequest(handler, http.MethodPost, "/api/v1/groups", group, sessionCookie, "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 without CSRF token, got %d", rec.Code)
	}

	// 5. Mutating request with CSRF -> 200 OK
	rec = doRequest(handler, http.MethodPost, "/api/v1/groups", group, sessionCookie, csrfToken)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with valid CSRF, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// 6. Logout
	rec = doRequest(handler, http.MethodPost, "/api/v1/auth/logout", nil, sessionCookie, csrfToken)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for logout, got %d", rec.Code)
	}

	// 7. Request after logout -> 401 UNAUTHORIZED
	rec = doRequest(handler, http.MethodGet, "/api/v1/clients", nil, sessionCookie, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 after logout, got %d", rec.Code)
	}
}

func TestServer_ClientsAndGroups(t *testing.T) {
	srv, st, cleanup := setupTestServer(t)
	defer cleanup()
	handler := srv.Handler()

	// Authenticate
	loginRec := doRequest(handler, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "adminpass",
	}, "", "")
	var loginData map[string]string
	_, data, _ := parseAPIResponse[map[string]string](t, loginRec)
	loginData = *data
	csrfToken := loginData["csrf_token"]
	sessionCookie := loginRec.Result().Cookies()[0].Value

	// Insert client
	mac := "aa:bb:cc:11:22:33"
	src := protocol.MACSourceAgent
	c := protocol.ClientNode{
		ClientID:     "cli_0000000000000001",
		CurrentIP:    "192.168.1.101",
		MACAddress:   &mac,
		MACSource:    &src,
		Hostname:     "PC-01",
		OSEdition:    "Windows 11 Education",
		AgentVersion: "0.1.0",
		PEReady:      true,
		Status:       protocol.ClientStatusOnlineWindows,
		LastSeen:     "2026-11-10T00:00:00Z",
	}
	_ = st.UpsertClient(c)

	// GET /api/v1/clients
	rec := doRequest(handler, http.MethodGet, "/api/v1/clients", nil, sessionCookie, "")
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	_, clients, _ := parseAPIResponse[[]protocol.ClientNode](t, rec)
	if len(*clients) != 1 {
		t.Errorf("expected 1 client, got %d", len(*clients))
	}

	// GET /api/v1/clients/{ip}
	rec = doRequest(handler, http.MethodGet, "/api/v1/clients/192.168.1.101", nil, sessionCookie, "")
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for client IP, got %d", rec.Code)
	}

	// PATCH /api/v1/clients/{ip}/settings
	rec = doRequest(handler, http.MethodPatch, "/api/v1/clients/192.168.1.101/settings", map[string]bool{
		"auto_restore_on_reboot": true,
	}, sessionCookie, csrfToken)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for settings patch, got %d", rec.Code)
	}

	// POST /api/v1/groups with duplicate IP -> 409 IP_IN_OTHER_GROUP
	g1 := protocol.IPGroup{
		GroupID:   "grp_1",
		GroupName: "群組一",
		IPList:    []string{"192.168.1.101"},
	}
	doRequest(handler, http.MethodPost, "/api/v1/groups", g1, sessionCookie, csrfToken)

	g2 := protocol.IPGroup{
		GroupID:   "grp_2",
		GroupName: "群組二",
		IPList:    []string{"192.168.1.101", "192.168.1.102"},
	}
	rec = doRequest(handler, http.MethodPost, "/api/v1/groups", g2, sessionCookie, csrfToken)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 for duplicate IP across groups, got %d", rec.Code)
	}
	_, _, apiErr := parseAPIResponse[any](t, rec)
	if apiErr == nil || apiErr.Code != protocol.ErrIPInOtherGroup {
		t.Errorf("expected IP_IN_OTHER_GROUP, got %+v", apiErr)
	}
}

func TestServer_WOL_And_RestorePointBatch(t *testing.T) {
	srv, st, cleanup := setupTestServer(t)
	defer cleanup()
	handler := srv.Handler()

	// Authenticate
	loginRec := doRequest(handler, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "adminpass",
	}, "", "")
	var loginData map[string]string
	_, data, _ := parseAPIResponse[map[string]string](t, loginRec)
	loginData = *data
	csrfToken := loginData["csrf_token"]
	sessionCookie := loginRec.Result().Cookies()[0].Value

	// Setup clients: 192.168.1.101 with MAC, 192.168.1.102 without MAC
	mac := "aa:bb:cc:11:22:33"
	src := protocol.MACSourceAgent
	c1 := protocol.ClientNode{
		ClientID:     "cli_01",
		CurrentIP:    "192.168.1.101",
		MACAddress:   &mac,
		MACSource:    &src,
		Hostname:     "PC-01",
		OSEdition:    "Windows 11 Education",
		AgentVersion: "0.1.0",
		Status:       protocol.ClientStatusOffline,
		LastSeen:     "2026-11-10T00:00:00Z",
	}
	c2 := protocol.ClientNode{
		ClientID:     "cli_02",
		CurrentIP:    "192.168.1.102",
		Hostname:     "PC-02",
		OSEdition:    "Windows 11 Education",
		AgentVersion: "0.1.0",
		Status:       protocol.ClientStatusOffline,
		LastSeen:     "2026-11-10T00:00:00Z",
	}
	_ = st.UpsertClient(c1)
	_ = st.UpsertClient(c2)

	// POST /api/v1/power/wol
	wolReq := map[string]any{
		"targets": map[string]any{
			"ips": []string{"192.168.1.101", "192.168.1.102"},
		},
	}
	rec := doRequest(handler, http.MethodPost, "/api/v1/power/wol", wolReq, sessionCookie, csrfToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	_, batchData, _ := parseAPIResponse[protocol.BatchResponseData](t, rec)
	if len(batchData.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(batchData.Results))
	}

	// 101 should succeed, 102 should fail with MAC_UNKNOWN
	for _, res := range batchData.Results {
		if res.IP == "192.168.1.101" && !res.OK {
			t.Errorf("expected 192.168.1.101 to succeed")
		}
		if res.IP == "192.168.1.102" {
			if res.OK || res.Error == nil || res.Error.Code != protocol.ErrMACUnknown {
				t.Errorf("expected MAC_UNKNOWN for 192.168.1.102, got %+v", res.Error)
			}
		}
	}
}

func TestServer_DeployTaskLifecycle(t *testing.T) {
	srv, st, cleanup := setupTestServer(t)
	defer cleanup()
	handler := srv.Handler()

	// Authenticate
	loginRec := doRequest(handler, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "adminpass",
	}, "", "")
	var loginData map[string]string
	_, data, _ := parseAPIResponse[map[string]string](t, loginRec)
	loginData = *data
	csrfToken := loginData["csrf_token"]
	sessionCookie := loginRec.Result().Cookies()[0].Value

	// Prepare master & target with PE ready
	m := protocol.ClientNode{
		ClientID:  "cli_master",
		CurrentIP: "192.168.1.100",
		Hostname:  "Master",
		OSEdition: "Windows 11 Education",
		PEReady:   true,
		Status:    protocol.ClientStatusOnlineWindows,
		LastSeen:  "2026-11-10T00:00:00Z",
	}
	target := protocol.ClientNode{
		ClientID:  "cli_target",
		CurrentIP: "192.168.1.101",
		Hostname:  "Target",
		OSEdition: "Windows 11 Education",
		PEReady:   true,
		Status:    protocol.ClientStatusOnlineWindows,
		LastSeen:  "2026-11-10T00:00:00Z",
	}
	_ = st.UpsertClient(m)
	_ = st.UpsertClient(target)

	// POST /api/v1/deploy/start
	rec := doRequest(handler, http.MethodPost, "/api/v1/deploy/start", map[string]any{
		"master_ip":  "192.168.1.100",
		"target_ips": []string{"192.168.1.101"},
	}, sessionCookie, csrfToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	_, task, _ := parseAPIResponse[protocol.DeployTask](t, rec)
	if task.State != protocol.TaskStatePendingPrepare {
		t.Errorf("expected PENDING_PREPARE, got %s", task.State)
	}

	// Attempt duplicate deploy start -> 409 DEPLOY_IN_PROGRESS
	rec = doRequest(handler, http.MethodPost, "/api/v1/deploy/start", map[string]any{
		"master_ip":  "192.168.1.100",
		"target_ips": []string{"192.168.1.101"},
	}, sessionCookie, csrfToken)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 for duplicate deploy start, got %d", rec.Code)
	}

	// POST /api/v1/deploy/cancel
	rec = doRequest(handler, http.MethodPost, "/api/v1/deploy/cancel", map[string]string{
		"task_id": task.TaskID,
	}, sessionCookie, csrfToken)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for cancel, got %d", rec.Code)
	}

	// GET /api/v1/tasks/{task_id}
	rec = doRequest(handler, http.MethodGet, "/api/v1/tasks/"+task.TaskID, nil, sessionCookie, "")
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for get task, got %d", rec.Code)
	}
	_, cancelledTask, _ := parseAPIResponse[protocol.DeployTask](t, rec)
	if cancelledTask.State != protocol.TaskStateCancelled {
		t.Errorf("expected state CANCELLED, got %s", cancelledTask.State)
	}
}

func TestServer_AgentEndpoints(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()
	handler := srv.Handler()

	// POST /api/v1/agent/enroll (public endpoint)
	rec := doRequest(handler, http.MethodPost, "/api/v1/agent/enroll", map[string]string{
		"token": "valid_enroll_token_123",
	}, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for agent enroll, got %d", rec.Code)
	}
	_, certData, _ := parseAPIResponse[map[string]string](t, rec)
	if (*certData)["client_certificate"] == "" {
		t.Errorf("missing certificate in enroll response")
	}
}

func TestServer_CompleteDeployAndRestorePointReset(t *testing.T) {
	srv, st, cleanup := setupTestServer(t)
	defer cleanup()

	svc := srv.service

	// 1. Setup target client with old restore points
	targetIP := "192.168.1.55"
	targetNode := protocol.ClientNode{
		ClientID:  "cli_target_reset_55",
		CurrentIP: targetIP,
		Status:    protocol.ClientStatusOnlineWindows,
		PEReady:   true,
	}
	_ = st.UpsertClient(targetNode)

	oldPoints := []protocol.RestorePoint{
		{PointID: "rp_old_base", Depth: 0, IsBase: true, IsActive: false, State: protocol.RestorePointStateReady},
		{PointID: "rp_old_diff_1", Depth: 1, IsBase: false, IsActive: true, State: protocol.RestorePointStateReady},
	}
	_ = st.SaveRestorePoints(targetNode.ClientID, oldPoints)

	// Setup master
	masterIP := "192.168.1.10"
	masterNode := protocol.ClientNode{
		ClientID:  "cli_master_10",
		CurrentIP: masterIP,
		Status:    protocol.ClientStatusOnlineWindows,
		PEReady:   true,
	}
	_ = st.UpsertClient(masterNode)

	// 2. Create DeployTask
	task, err := svc.StartDeploy(masterIP, []string{targetIP})
	if err != nil {
		t.Fatalf("CreateDeploy failed: %v", err)
	}
	if !task.ResetsRestorePoints {
		t.Fatalf("expected ResetsRestorePoints to be true (Decision D8)")
	}

	// 3. CompleteDeploy
	if err := svc.CompleteDeploy(task.TaskID); err != nil {
		t.Fatalf("CompleteDeploy failed: %v", err)
	}

	// 4. Verify target client has only 1 fresh base restore point
	updatedTarget, err := st.GetClientByIP(targetIP)
	if err != nil {
		t.Fatalf("GetClientByIP failed: %v", err)
	}

	points, err := st.ListRestorePoints(updatedTarget.ClientID)
	if err != nil {
		t.Fatalf("ListRestorePoints failed: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected exactly 1 restore point after deploy, got %d: %+v", len(points), points)
	}
	if !points[0].IsBase || !points[0].IsActive || points[0].Depth != 0 {
		t.Fatalf("invalid new base point: %+v", points[0])
	}
	if points[0].CreatedBy != protocol.CreatedBySystem {
		t.Fatalf("expected CreatedBy SYSTEM, got %s", points[0].CreatedBy)
	}
	if updatedTarget.ActivePointID == nil || *updatedTarget.ActivePointID != points[0].PointID {
		t.Fatalf("client active_point_id not updated to new base point")
	}
}
