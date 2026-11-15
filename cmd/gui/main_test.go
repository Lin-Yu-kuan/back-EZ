package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owner/back-ez/internal/agent/service"
	"github.com/owner/back-ez/internal/protocol"
	"github.com/owner/back-ez/internal/workers/sysrestore"
	"github.com/owner/back-ez/internal/workers/vhdx"
)

const (
	testToken      = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testVolumeGUID = `\\?\Volume{11111111-2222-3333-4444-555555555555}\`
)

// readyCapabilities returns a fully-ready capability fixture.
func readyCapabilities() vhdx.Capabilities {
	return vhdx.Capabilities{
		CheckedAt:      "2026-11-12T05:00:00Z",
		Ready:          true,
		Edition:        "Professional",
		Build:          "26100",
		IsAdmin:        true,
		ReserveRoot:    `D:\EZRestore`,
		ReserveVolume:  testVolumeGUID,
		ReserveRelPath: "EZRestore",
		Items: []vhdx.Capability{
			{Key: "admin_token", OK: true, Required: true, Detail: "已以管理員權限執行（UAC 已提升）"},
			{Key: "real_execution_enabled", OK: true, Required: true, Detail: "真實寫入已啟用（測試替身）"},
		},
	}
}

// blockedCapabilities returns a capability fixture with one required blocker.
func blockedCapabilities() vhdx.Capabilities {
	caps := readyCapabilities()
	caps.Ready = false
	blocker := vhdx.Capability{
		Key:      "base_vhdx_present",
		OK:       false,
		Required: true,
		Detail:   "找不到 base.vhdx（D:\\EZRestore\\base.vhdx）",
		Fix:      "先建立保留區並放置 base.vhdx",
	}
	caps.Items = append(caps.Items, blocker)
	caps.Blockers = []vhdx.Capability{blocker}
	return caps
}

// testPanel wires a fully fake HTTP surface: a DryRun engine with a real
// base.vhdx inside t.TempDir(), a test AgentCore and an injected capability
// provider. Nothing here touches the real BCD or disk.
type testPanel struct {
	mux   *http.ServeMux
	token string
}

// panelOption customizes the fake HTTP surface (e.g. the System Restore module).
type panelOption func(*guiDeps)

func withSysRestore(service sysrestore.Service) panelOption {
	return func(deps *guiDeps) { deps.sysrestore = service }
}

func withDryRun(dryRun bool) panelOption {
	return func(deps *guiDeps) { deps.dryRun = dryRun }
}

func withShutdown(fn func()) panelOption {
	return func(deps *guiDeps) { deps.shutdown = fn }
}

func newTestPanel(t *testing.T, caps vhdx.Capabilities, opts ...panelOption) *testPanel {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "base.vhdx"), []byte("fake base vhdx"), 0o644); err != nil {
		t.Fatalf("create fake base.vhdx: %v", err)
	}

	cfg, cfgPath, err := service.LoadOrCreateConfig(dir)
	if err != nil {
		t.Fatalf("load/create agent config: %v", err)
	}
	core, err := service.NewAgentCore(*cfg, cfgPath, "test-password")
	if err != nil {
		t.Fatalf("new agent core: %v", err)
	}
	logger := service.NewOperationLogger(filepath.Join(dir, "logs", "operation.log"))
	core.SetLogger(logger)

	engine, err := vhdx.NewEngine(vhdx.Config{
		DryRun:               true,
		AllowDestructive:     false,
		RealExecutionEnabled: false,
		VolumeGUID:           caps.ReserveVolume,
		ReserveRoot:          dir,
		ReserveRelPath:       caps.ReserveRelPath,
		DataDir:              dir,
	}, vhdx.NewDryRunExecutor())
	if err != nil {
		t.Fatalf("new dry-run engine: %v", err)
	}
	if _, err := engine.EnsureBase("Base System", "test base"); err != nil {
		t.Fatalf("ensure base point: %v", err)
	}
	core.SetEngine(engine)

	fixedCaps := caps
	deps := &guiDeps{
		core:               core,
		logger:             logger,
		capabilityProvider: func() vhdx.Capabilities { return fixedCaps },
		token:              testToken,
		reserveRoot:        dir,
		port:               28888,
		engine:             engine,
	}
	for _, opt := range opts {
		opt(deps)
	}

	return &testPanel{mux: newMux(deps), token: testToken}
}

func (p *testPanel) request(t *testing.T, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	p.mux.ServeHTTP(rec, req)
	return rec
}

func (p *testPanel) authorized() map[string]string {
	return map[string]string{tokenHeader: p.token}
}

func decodeAPIError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) apiErrorPayload {
	t.Helper()

	if rec.Code != wantStatus {
		t.Fatalf("HTTP status = %d, want %d (body=%s)", rec.Code, wantStatus, rec.Body.String())
	}
	var resp struct {
		OK    bool            `json:"ok"`
		Error apiErrorPayload `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error response: %v (body=%s)", err, rec.Body.String())
	}
	if resp.OK {
		t.Fatalf("expected ok=false, got ok=true (body=%s)", rec.Body.String())
	}
	if wantCode != "" && resp.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", resp.Error.Code, wantCode)
	}
	return resp.Error
}

func TestSystemStatusEndpoint(t *testing.T) {
	panel := newTestPanel(t, readyCapabilities())

	rec := panel.request(t, http.MethodGet, "/api/system/status", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	for _, want := range []string{
		`"ok":true`,
		`"capabilities"`,
		`"ready":true`,
		`"engine_ready":true`,
		`"engine_warning"`,
		`"pending_reboot":false`,
		`"active_point_id":"rp_base"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("status JSON missing %q (body=%s)", want, body)
		}
	}

	var resp struct {
		OK   bool             `json:"ok"`
		Data systemStatusData `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if !resp.OK {
		t.Errorf("expected ok=true")
	}
	if !resp.Data.EngineReady {
		t.Errorf("expected engine_ready=true")
	}
	if resp.Data.EngineWarning != "" {
		t.Errorf("expected empty engine_warning, got %q", resp.Data.EngineWarning)
	}
	if resp.Data.PendingReboot {
		t.Errorf("expected pending_reboot=false")
	}
	if active, ok := resp.Data.ActivePointID.(string); !ok || active != "rp_base" {
		t.Errorf("active_point_id = %v, want rp_base", resp.Data.ActivePointID)
	}
	if !resp.Data.Capabilities.Ready {
		t.Errorf("expected capabilities.ready=true")
	}
	if resp.Data.Capabilities.Edition != "Professional" {
		t.Errorf("capabilities.edition = %q, want Professional", resp.Data.Capabilities.Edition)
	}
	if len(resp.Data.Capabilities.Items) != 2 {
		t.Errorf("capabilities.items length = %d, want 2", len(resp.Data.Capabilities.Items))
	}
}

func TestMutationRequiresToken(t *testing.T) {
	panel := newTestPanel(t, readyCapabilities())

	// No token at all.
	rec := panel.request(t, http.MethodPost, "/api/restore-points/create", `{"name":"x"}`, nil)
	errPayload := decodeAPIError(t, rec, http.StatusForbidden, protocol.ErrForbidden)
	if !strings.Contains(errPayload.Message, "X-BackEZ-Token") {
		t.Errorf("message should mention the missing token, got %q", errPayload.Message)
	}

	// Wrong token.
	rec = panel.request(t, http.MethodPost, "/api/restore-points/create", `{"name":"x"}`,
		map[string]string{tokenHeader: "deadbeef"})
	decodeAPIError(t, rec, http.StatusForbidden, protocol.ErrForbidden)

	// Valid token succeeds in DryRun mode.
	rec = panel.request(t, http.MethodPost, "/api/restore-points/create", `{"name":"x"}`, panel.authorized())
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var ok struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil {
		t.Fatalf("decode success response: %v", err)
	}
	if !ok.OK {
		t.Errorf("expected ok=true, body=%s", rec.Body.String())
	}
}

func TestMutationOriginCheck(t *testing.T) {
	panel := newTestPanel(t, readyCapabilities())

	// Cross-origin host must be rejected even with a valid token.
	headers := panel.authorized()
	headers["Origin"] = "http://evil.example"
	rec := panel.request(t, http.MethodPost, "/api/restore-points/create", `{"name":"x"}`, headers)
	decodeAPIError(t, rec, http.StatusForbidden, protocol.ErrForbidden)

	// Opaque "null" origin is rejected as well.
	headers = panel.authorized()
	headers["Origin"] = "null"
	rec = panel.request(t, http.MethodPost, "/api/restore-points/create", `{"name":"x"}`, headers)
	decodeAPIError(t, rec, http.StatusForbidden, protocol.ErrForbidden)

	// Same-origin request is allowed (httptest sets Host to "example.com").
	headers = panel.authorized()
	headers["Origin"] = "http://example.com"
	rec = panel.request(t, http.MethodPost, "/api/restore-points/create", `{"name":"same-origin"}`, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin POST status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestBlockedCapabilitiesRejectMutation(t *testing.T) {
	panel := newTestPanel(t, blockedCapabilities())

	rec := panel.request(t, http.MethodPost, "/api/restore-points/create",
		`{"name":"blocked"}`, panel.authorized())
	errPayload := decodeAPIError(t, rec, http.StatusConflict, protocol.ErrCapabilityUnsupported)

	if !strings.Contains(errPayload.Message, "base_vhdx_present") {
		t.Errorf("message should list blocker key, got %q", errPayload.Message)
	}
	if !strings.Contains(errPayload.Message, "找不到 base.vhdx") {
		t.Errorf("message should list blocker detail, got %q", errPayload.Message)
	}
	if errPayload.Details == nil {
		t.Errorf("expected blocker details in error object")
	}

	// The panel must not create anything when capabilities are blocked.
	rec = panel.request(t, http.MethodGet, "/api/restore-points", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/restore-points status = %d, want 200", rec.Code)
	}
	var points []protocol.RestorePoint
	if err := json.Unmarshal(rec.Body.Bytes(), &points); err != nil {
		t.Fatalf("decode restore points: %v", err)
	}
	if len(points) != 1 || points[0].PointID != "rp_base" {
		t.Errorf("restore points = %+v, want only the base point", points)
	}
}

func TestReadyCapabilitiesAllowCreate(t *testing.T) {
	panel := newTestPanel(t, readyCapabilities())

	rec := panel.request(t, http.MethodPost, "/api/restore-points/create",
		`{"name":"測試還原點","description":"更新前"}`, panel.authorized())
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var ok struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil || !ok.OK {
		t.Fatalf("create response = %s, want ok=true", rec.Body.String())
	}

	rec = panel.request(t, http.MethodGet, "/api/restore-points", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	var points []protocol.RestorePoint
	if err := json.Unmarshal(rec.Body.Bytes(), &points); err != nil {
		t.Fatalf("decode restore points: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("restore points length = %d, want 2 (%+v)", len(points), points)
	}
	found := false
	for _, p := range points {
		if p.Name == "測試還原點" {
			found = true
		}
	}
	if !found {
		t.Errorf("created restore point not found in %+v", points)
	}
}

func TestNonAdminMutationForbiddenBeforeCapabilityGate(t *testing.T) {
	caps := readyCapabilities()
	caps.IsAdmin = false
	panel := newTestPanel(t, caps)

	rec := panel.request(t, http.MethodPost, "/api/restore-points/create",
		`{"name":"no-admin"}`, panel.authorized())
	decodeAPIError(t, rec, http.StatusForbidden, protocol.ErrForbidden)
}

func TestRemovedEndpointsReturn404(t *testing.T) {
	panel := newTestPanel(t, readyCapabilities())

	for _, path := range []string{
		"/api/restore-points/overwrite",
		"/api/system/unlock-pro",
	} {
		rec := panel.request(t, http.MethodPost, path, `{"point_id":"rp_base"}`, panel.authorized())
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s status = %d, want 404", path, rec.Code)
		}
	}
}

func TestIndexServesPanelWithInjectedToken(t *testing.T) {
	panel := newTestPanel(t, readyCapabilities())

	rec := panel.request(t, http.MethodGet, "/", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	html := rec.Body.String()
	for _, want := range []string{
		`window.__EZ_TOKEN__ = "` + testToken + `"`,
		"/api/system/status",
		"系統能力檢測",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("panel HTML missing %q", want)
		}
	}
	for _, forbidden := range []string{"overwrite", "unlockPro"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("panel HTML must not contain %q", forbidden)
		}
	}

	// Home edition is derived from the capability report, not PowerShell.
	homeCaps := readyCapabilities()
	homeCaps.Edition = "Core"
	homePanel := newTestPanel(t, homeCaps)
	rec = homePanel.request(t, http.MethodGet, "/", "", nil)
	if !strings.Contains(rec.Body.String(), "家用版（Home）") {
		t.Errorf("Home edition panel must show the Home warning banner")
	}
}

func TestCapabilityCacheTTL(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	calls := 0
	cache := newCapabilityCache(`D:\EZRestore`, 30*time.Second, func(ctx context.Context, root string) vhdx.Capabilities {
		calls++
		return vhdx.Capabilities{CheckedAt: "checked", ReserveRoot: root}
	})
	cache.now = func() time.Time { return now }

	caps := cache.get()
	if caps.ReserveRoot != `D:\EZRestore` {
		t.Errorf("ReserveRoot = %q, want D:\\EZRestore", caps.ReserveRoot)
	}
	cache.get()
	if calls != 1 {
		t.Fatalf("detect calls = %d, want 1 (cached within TTL)", calls)
	}

	// Still cached at 29s.
	now = now.Add(29 * time.Second)
	cache.get()
	if calls != 1 {
		t.Fatalf("detect calls = %d, want 1 at 29s", calls)
	}

	// Refreshed once the 30s TTL expires.
	now = now.Add(2 * time.Second)
	cache.get()
	if calls != 2 {
		t.Fatalf("detect calls = %d, want 2 after TTL expiry", calls)
	}
}

func TestNewServerWiring(t *testing.T) {
	deps := &guiDeps{
		capabilityProvider: func() vhdx.Capabilities { return readyCapabilities() },
		token:              testToken,
		port:               28888,
	}
	server := newServer(deps)
	if server.Addr != "127.0.0.1:28888" {
		t.Errorf("Addr = %q, want 127.0.0.1:28888", server.Addr)
	}
	if server.Handler == nil {
		t.Fatalf("Handler must be wired")
	}
	if server.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout must be positive")
	}
}

func TestIsHomeEdition(t *testing.T) {
	tests := map[string]bool{
		"Core":               true,
		"CoreSingleLanguage": true,
		"Home":               true,
		"Professional":       false,
		"Enterprise":         false,
		"Education":          false,
		"":                   false,
	}
	for edition, want := range tests {
		if got := isHomeEdition(edition); got != want {
			t.Errorf("isHomeEdition(%q) = %t, want %t", edition, got, want)
		}
	}
}

func TestOriginAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/restore-points/create", nil)
	req.Host = "127.0.0.1:28888"

	tests := []struct {
		origin string
		want   bool
	}{
		{"", true},
		{"http://127.0.0.1:28888", true},
		{"http://127.0.0.1:28888/", true},
		{"http://evil.example", false},
		{"null", false},
		{"not a url", false},
	}
	for _, tt := range tests {
		req.Header.Set("Origin", tt.origin)
		if got := originAllowed(req); got != tt.want {
			t.Errorf("originAllowed(Origin=%q) = %t, want %t", tt.origin, got, tt.want)
		}
	}
}

// fakeSysRestore is an in-memory sysrestore.Service for HTTP tests.
type fakeSysRestore struct {
	status     sysrestore.Status
	points     []sysrestore.Point
	createErr  error
	restoreErr error
	enableErr  error

	created  []string
	restored []int
	enabled  []string
}

func (f *fakeSysRestore) Status(context.Context) sysrestore.Status {
	return f.status
}

func (f *fakeSysRestore) List(context.Context) ([]sysrestore.Point, error) {
	return f.points, nil
}

func (f *fakeSysRestore) Create(_ context.Context, description string) (sysrestore.Point, error) {
	if f.createErr != nil {
		return sysrestore.Point{}, f.createErr
	}
	f.created = append(f.created, description)
	return sysrestore.Point{Sequence: 42, Description: description, CreatedAt: "2026-11-12T05:00:00Z"}, nil
}

func (f *fakeSysRestore) Restore(_ context.Context, sequence int) error {
	if f.restoreErr != nil {
		return f.restoreErr
	}
	f.restored = append(f.restored, sequence)
	return nil
}

func (f *fakeSysRestore) Enable(_ context.Context, drive string) error {
	if f.enableErr != nil {
		return f.enableErr
	}
	f.enabled = append(f.enabled, drive)
	return nil
}

func readySysRestoreStatus() sysrestore.Status {
	return sysrestore.Status{
		Supported:         true,
		IsAdmin:           true,
		Drive:             `C:\`,
		ProtectionEnabled: true,
		FreeBytes:         100 << 30,
		ThrottleMinutes:   0,
		PointCount:        1,
		CanCreate:         true,
		CanRestore:        true,
	}
}

func TestSysRestoreStatusEndpoint(t *testing.T) {
	fake := &fakeSysRestore{status: readySysRestoreStatus()}
	panel := newTestPanel(t, readyCapabilities(), withSysRestore(fake))

	rec := panel.request(t, http.MethodGet, "/api/sysrestore/status", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		OK   bool              `json:"ok"`
		Data sysrestore.Status `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !body.OK || !body.Data.Supported || !body.Data.ProtectionEnabled || !body.Data.IsAdmin {
		t.Fatalf("unexpected status payload: %+v", body)
	}
}

func TestSysRestorePointsEndpoint(t *testing.T) {
	fake := &fakeSysRestore{
		status: readySysRestoreStatus(),
		points: []sysrestore.Point{{Sequence: 5, Description: "before update", CreatedAt: "2026-11-12T04:00:00Z"}},
	}
	panel := newTestPanel(t, readyCapabilities(), withSysRestore(fake))

	rec := panel.request(t, http.MethodGet, "/api/sysrestore/points", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var points []sysrestore.Point
	if err := json.Unmarshal(rec.Body.Bytes(), &points); err != nil {
		t.Fatalf("decode points: %v", err)
	}
	if len(points) != 1 || points[0].Sequence != 5 {
		t.Fatalf("unexpected points: %+v", points)
	}
}

func TestSysRestoreMutationsRequireToken(t *testing.T) {
	fake := &fakeSysRestore{status: readySysRestoreStatus()}
	panel := newTestPanel(t, readyCapabilities(), withSysRestore(fake))

	for _, headers := range []map[string]string{nil, map[string]string{tokenHeader: "wrong-token"}} {
		rec := panel.request(t, http.MethodPost, "/api/sysrestore/create", `{"description":"x"}`, headers)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 without a valid token, got %d", rec.Code)
		}
	}
	if len(fake.created) != 0 {
		t.Fatalf("mutations must not reach the manager without a token")
	}
}

func TestSysRestoreMutationsRequireRealMode(t *testing.T) {
	fake := &fakeSysRestore{status: readySysRestoreStatus()}
	panel := newTestPanel(t, readyCapabilities(), withSysRestore(fake), withDryRun(true))

	rec := panel.request(t, http.MethodPost, "/api/sysrestore/create", `{"description":"x"}`, panel.authorized())
	decodeAPIError(t, rec, http.StatusConflict, protocol.ErrInvalidState)
	if len(fake.created) != 0 {
		t.Fatalf("DryRun must not create a restore point")
	}
}

func TestSysRestoreMutationsRequireAdmin(t *testing.T) {
	status := readySysRestoreStatus()
	status.IsAdmin = false
	fake := &fakeSysRestore{status: status}
	panel := newTestPanel(t, readyCapabilities(), withSysRestore(fake))

	rec := panel.request(t, http.MethodPost, "/api/sysrestore/create", `{"description":"x"}`, panel.authorized())
	decodeAPIError(t, rec, http.StatusForbidden, protocol.ErrForbidden)
}

func TestSysRestoreCreateSuccess(t *testing.T) {
	fake := &fakeSysRestore{status: readySysRestoreStatus()}
	panel := newTestPanel(t, readyCapabilities(), withSysRestore(fake))

	rec := panel.request(t, http.MethodPost, "/api/sysrestore/create",
		`{"description":"安裝更新前"}`, panel.authorized())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.created) != 1 || fake.created[0] != "安裝更新前" {
		t.Fatalf("manager did not receive the description: %+v", fake.created)
	}

	var body struct {
		OK   bool             `json:"ok"`
		Data sysrestore.Point `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !body.OK || body.Data.Sequence != 42 {
		t.Fatalf("unexpected create response: %+v", body)
	}
}

func TestSysRestoreRestoreRequiresConfirmation(t *testing.T) {
	fake := &fakeSysRestore{status: readySysRestoreStatus()}
	panel := newTestPanel(t, readyCapabilities(), withSysRestore(fake))

	rec := panel.request(t, http.MethodPost, "/api/sysrestore/restore",
		`{"sequence":5,"confirm":"yes"}`, panel.authorized())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong confirmation, got %d", rec.Code)
	}
	if len(fake.restored) != 0 {
		t.Fatalf("restore must not run without RESTORE confirmation")
	}

	rec = panel.request(t, http.MethodPost, "/api/sysrestore/restore",
		`{"sequence":5,"confirm":"RESTORE"}`, panel.authorized())
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for confirmed restore, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.restored) != 1 || fake.restored[0] != 5 {
		t.Fatalf("unexpected restored sequences: %+v", fake.restored)
	}

	var body struct {
		Data struct {
			Rebooting bool `json:"rebooting"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode restore response: %v", err)
	}
	if !body.Data.Rebooting {
		t.Fatalf("restore response must flag rebooting")
	}
}

func TestSysRestoreEnable(t *testing.T) {
	fake := &fakeSysRestore{status: readySysRestoreStatus()}
	panel := newTestPanel(t, readyCapabilities(), withSysRestore(fake))

	rec := panel.request(t, http.MethodPost, "/api/sysrestore/enable", `{"drive":"C:\\"}`, panel.authorized())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.enabled) != 1 || fake.enabled[0] != `C:\` {
		t.Fatalf("unexpected enabled drives: %+v", fake.enabled)
	}
}

func TestSysRestoreRejectsCrossOrigin(t *testing.T) {
	fake := &fakeSysRestore{status: readySysRestoreStatus()}
	panel := newTestPanel(t, readyCapabilities(), withSysRestore(fake))

	headers := panel.authorized()
	headers["Origin"] = "http://evil.example"
	rec := panel.request(t, http.MethodPost, "/api/sysrestore/create", `{"description":"x"}`, headers)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin, got %d", rec.Code)
	}
}

func TestShutdownEndpoint(t *testing.T) {
	done := make(chan struct{})
	panel := newTestPanel(t, readyCapabilities(), withShutdown(func() { close(done) }))

	rec := panel.request(t, http.MethodPost, "/api/system/shutdown", "", panel.authorized())
	if rec.Code != http.StatusOK {
		t.Fatalf("shutdown status = %d, body=%s", rec.Code, rec.Body.String())
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("shutdown callback was not invoked")
	}

	// The endpoint must still require the per-launch token.
	rec = panel.request(t, http.MethodPost, "/api/system/shutdown", "", nil)
	decodeAPIError(t, rec, http.StatusForbidden, protocol.ErrForbidden)
}

func TestHealthEndpoint(t *testing.T) {
	panel := newTestPanel(t, readyCapabilities(), withDryRun(true))

	rec := panel.request(t, http.MethodGet, "/api/health", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
	var body struct {
		OK   bool `json:"ok"`
		Data struct {
			App    string `json:"app"`
			DryRun bool   `json:"dry_run"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if !body.OK || body.Data.App != "back-ez" || !body.Data.DryRun {
		t.Fatalf("unexpected health payload: %+v", body)
	}
}

func TestProbeInstanceDetectsBackEZ(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"data":{"app":"back-ez","dry_run":true}}`)
	}))
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	probe := probeInstance(addr, server.URL)
	if !probe.Exists {
		t.Fatalf("expected an existing back-EZ instance")
	}
	if !probe.DryRun {
		t.Fatalf("expected dry_run=true to be detected")
	}
}

func TestProbeInstanceRejectsNonBackEZ(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "this is not back-ez")
	}))
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	if probe := probeInstance(addr, server.URL); probe.Exists {
		t.Fatalf("a non back-ez HTTP server must not be treated as an instance")
	}
}

func TestProbeInstanceWhenNothingListens(t *testing.T) {
	if probe := probeInstance("127.0.0.1:1", "http://127.0.0.1:1"); probe.Exists {
		t.Fatalf("nothing listens on port 1")
	}
}
