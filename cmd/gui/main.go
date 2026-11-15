package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/owner/back-ez/internal/agent/service"
	"github.com/owner/back-ez/internal/agent/ui"
	"github.com/owner/back-ez/internal/protocol"
	"github.com/owner/back-ez/internal/workers/sysrestore"
	"github.com/owner/back-ez/internal/workers/vhdx"
	"golang.org/x/sys/windows"
)

const (
	// defaultReserveRoot is the default reserve area.
	defaultReserveRoot = `D:\EZRestore`
	// defaultPort is the default localhost dashboard port.
	defaultPort = 28888
	// capabilityTTL is how long a capability report is reused before re-probing.
	capabilityTTL = 30 * time.Second
	// capabilityTimeout bounds the startup/refresh capability probe.
	capabilityTimeout = 3 * time.Second
	// maxRequestBody limits JSON request bodies accepted by the local API.
	maxRequestBody = 1 << 20
	// tokenHeader is the request header carrying the per-launch CSRF token.
	tokenHeader = "X-BackEZ-Token"
)

func openBrowser(url string) {
	// Try Edge in app mode first for a clean window experience
	edgePath := filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe")
	if _, err := os.Stat(edgePath); err == nil {
		cmd := exec.Command(edgePath, "--app="+url)
		if err := cmd.Start(); err == nil {
			return
		}
	}

	// Fallback to default browser
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	_ = cmd.Start()
}

var (
	user32DLL       = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW = user32DLL.NewProc("MessageBoxW")
)

// messageBox shows a blocking Windows dialog. This binary uses the GUI
// subsystem (no console), so fatal startup errors would otherwise be invisible.
func messageBox(title, text string) {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	textPtr, _ := windows.UTF16PtrFromString(text)
	_, _, _ = procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		0x00000010) // MB_ICONERROR
}

func fatalError(logger *service.OperationLogger, action, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if logger != nil {
		logger.Log("SYSTEM", action, "FAILED", message)
	}
	log.Printf("%s: %s", action, message)
	messageBox("back-EZ 啟動失敗", message)
	os.Exit(1)
}

// listenWithRetry tolerates the short window between killing a previous
// instance and the OS releasing its listening socket.
func listenWithRetry(addr string, attempts int, delay time.Duration) (net.Listener, error) {
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			return listener, nil
		}
		lastErr = err
		time.Sleep(delay)
	}
	return nil, lastErr
}

// instanceProbe describes an already-running back-EZ on the same port.
type instanceProbe struct {
	Exists bool
	DryRun bool
}

// probeInstance checks whether another back-EZ already serves this port. It
// reads /api/system/status so a non-back-ez process holding the port is not
// mistaken for an existing instance.
func probeInstance(addr, baseURL string) instanceProbe {
	conn, err := net.DialTimeout("tcp", addr, 700*time.Millisecond)
	if err != nil {
		return instanceProbe{}
	}
	_ = conn.Close()

	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(baseURL + "/api/health")
	if err != nil {
		return instanceProbe{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return instanceProbe{}
	}

	var body struct {
		OK   bool `json:"ok"`
		Data struct {
			App    string `json:"app"`
			DryRun bool   `json:"dry_run"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil || !body.OK {
		return instanceProbe{}
	}
	if body.Data.App != "back-ez" {
		return instanceProbe{}
	}
	return instanceProbe{Exists: true, DryRun: body.Data.DryRun}
}

// openExistingInstance opens the already-running panel, or explains why the
// requested mode cannot reuse it. Returns true when the process should exit.
func openExistingInstance(logger *service.OperationLogger, probe instanceProbe, dryRun bool, serverURL string, noBrowser bool) {
	if probe.DryRun != dryRun {
		fatalError(logger, "INSTANCE_CONFLICT",
			"偵測到已在執行的 back-EZ（目前 DryRun=%t，本次 DryRun=%t）。\n\n"+
				"請先關閉既有面板（面板右上角「結束 back-EZ」或工作管理員），或重新執行「啟動.bat」讓它關閉舊的再開新的。",
			probe.DryRun, dryRun)
	}
	log.Printf("偵測到已在執行的 back-EZ（%s），直接開啟既有面板", serverURL)
	if !noBrowser {
		go openBrowser(serverURL)
	}
}

// capabilityCache memoizes vhdx.DetectCapabilities for capabilityTTL so every
// request does not spawn bcdedit/manage-bde probes.
type capabilityCache struct {
	mu       sync.Mutex
	root     string
	ttl      time.Duration
	now      func() time.Time
	detect   func(context.Context, string) vhdx.Capabilities
	cached   vhdx.Capabilities
	cachedAt time.Time
	valid    bool
}

func newCapabilityCache(root string, ttl time.Duration, detect func(context.Context, string) vhdx.Capabilities) *capabilityCache {
	if detect == nil {
		detect = vhdx.DetectCapabilities
	}
	return &capabilityCache{
		root:   root,
		ttl:    ttl,
		now:    time.Now,
		detect: detect,
	}
}

// get returns the cached capability report, re-probing it when the 30s TTL has
// expired. The mutex is held during detection so concurrent requests do not
// spawn duplicate bcdedit/manage-bde calls.
func (c *capabilityCache) get() vhdx.Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.valid && c.now().Sub(c.cachedAt) < c.ttl {
		return c.cached
	}

	ctx, cancel := context.WithTimeout(context.Background(), capabilityTimeout)
	defer cancel()

	c.cached = c.detect(ctx, c.root)
	c.cachedAt = c.now()
	c.valid = true
	return c.cached
}

// newToken generates a random 32-byte hex token for the X-BackEZ-Token guard.
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// guiDeps carries the dependencies injected into the HTTP handlers. Tests build
// it with a fake capabilityProvider and a DryRun engine.
type guiDeps struct {
	core               *service.AgentCore
	logger             *service.OperationLogger
	capabilityProvider func() vhdx.Capabilities
	sysrestore         sysrestore.Service
	token              string
	reserveRoot        string
	port               int
	engine             *vhdx.Engine
	dryRun             bool
	shutdown           func()
}

func (d *guiDeps) currentCapabilities() vhdx.Capabilities {
	if d.capabilityProvider != nil {
		return d.capabilityProvider()
	}
	return vhdx.Capabilities{}
}

func (d *guiDeps) currentEngine() *vhdx.Engine {
	if d.engine != nil {
		return d.engine
	}
	if d.core != nil {
		return d.core.Engine()
	}
	return nil
}

// systemStatusData is the data payload of GET /api/system/status.
type systemStatusData struct {
	Capabilities  vhdx.Capabilities `json:"capabilities"`
	EngineReady   bool              `json:"engine_ready"`
	EngineWarning string            `json:"engine_warning"`
	DryRun        bool              `json:"dry_run"`
	PendingReboot bool              `json:"pending_reboot"`
	ActivePointID any               `json:"active_point_id"`
}

// apiErrorPayload is the unified error object returned by the local API.
type apiErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAPISuccess(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, map[string]any{
		"ok": false,
		"error": apiErrorPayload{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

func decodeJSONBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("empty request body")
	}
	return json.NewDecoder(io.LimitReader(r.Body, maxRequestBody)).Decode(dst)
}

// originAllowed implements the same-origin check: a request without an Origin
// header is allowed (same-origin fetches from the local panel omit it for GET,
// but browsers send it for cross-origin requests); when Origin is present its
// host must match r.Host.
func originAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// tokenMatches compares the request token with the per-launch token in constant
// time. An empty configured token always fails closed.
func tokenMatches(got, want string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// isHomeEdition reports whether the detected Windows edition is a Home SKU
// (EditionID "Core" / "CoreSingleLanguage" / "...Home..."), without shelling
// out to PowerShell.
func isHomeEdition(edition string) bool {
	normalized := strings.ToLower(strings.TrimSpace(edition))
	return strings.Contains(normalized, "core") || strings.Contains(normalized, "home")
}

// blockerSummary renders blocker Key/Detail pairs for humans and API messages.
func blockerSummary(blockers []vhdx.Capability) string {
	if len(blockers) == 0 {
		return "無"
	}
	parts := make([]string, 0, len(blockers))
	for _, b := range blockers {
		part := b.Key
		if detail := strings.TrimSpace(b.Detail); detail != "" {
			part += "：" + detail
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "；")
}

// isKnownErrorCode reports whether code is part of the closed SPEC error set.
func isKnownErrorCode(code string) bool {
	switch code {
	case protocol.ErrUnauthorized,
		protocol.ErrForbidden,
		protocol.ErrRateLimited,
		protocol.ErrInvalidArgument,
		protocol.ErrNotFound,
		protocol.ErrInvalidState,
		protocol.ErrIPInOtherGroup,
		protocol.ErrRestorePointLimit,
		protocol.ErrDeployInProgress,
		protocol.ErrMACUnknown,
		protocol.ErrPENotReady,
		protocol.ErrCapabilityUnsupported,
		protocol.ErrImageVerifyFailed,
		protocol.ErrInternal,
		protocol.ErrPasswordRequired:
		return true
	}
	return false
}

// errorCode extracts a protocol error code from an engine/core error.
func errorCode(err error) string {
	if err == nil {
		return protocol.ErrInternal
	}
	var vhdxErr *vhdx.VHDXError
	if errors.As(err, &vhdxErr) && vhdxErr.Code != "" {
		return vhdxErr.Code
	}
	if isKnownErrorCode(err.Error()) {
		return err.Error()
	}
	return protocol.ErrInternal
}

// errorMessage renders a human-readable message for an engine/core error.
func errorMessage(err error) string {
	if err == nil {
		return "未知錯誤"
	}
	var vhdxErr *vhdx.VHDXError
	if errors.As(err, &vhdxErr) && strings.TrimSpace(vhdxErr.Message) != "" {
		return vhdxErr.Message
	}
	switch errorCode(err) {
	case protocol.ErrInvalidArgument:
		return "參數錯誤"
	case protocol.ErrForbidden:
		return "操作被拒絕"
	case protocol.ErrCapabilityUnsupported:
		return "目前環境不支援此操作"
	case protocol.ErrInvalidState:
		return "目前狀態不允許此操作"
	case protocol.ErrNotFound:
		return "找不到指定的還原點"
	case protocol.ErrRestorePointLimit:
		return "已達還原點數量上限"
	case protocol.ErrInternal:
		return "內部錯誤"
	default:
		return err.Error()
	}
}

// statusForError maps a core error to an HTTP status code.
func statusForError(err error) int {
	switch errorCode(err) {
	case protocol.ErrInvalidArgument:
		return http.StatusBadRequest
	case protocol.ErrForbidden:
		return http.StatusForbidden
	case protocol.ErrCapabilityUnsupported, protocol.ErrInvalidState, protocol.ErrRestorePointLimit:
		return http.StatusConflict
	case protocol.ErrNotFound:
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func writeCoreError(w http.ResponseWriter, err error) {
	writeAPIError(w, statusForError(err), errorCode(err), errorMessage(err), nil)
}

// guardMutation enforces the local mutation protections, in order:
// Origin host check, X-BackEZ-Token, admin token, capability gate, engine gate.
func (d *guiDeps) guardMutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r) {
			writeAPIError(w, http.StatusForbidden, protocol.ErrForbidden,
				"拒絕跨來源請求：Origin 與本機介面主機不一致", nil)
			return
		}
		if !tokenMatches(r.Header.Get(tokenHeader), d.token) {
			writeAPIError(w, http.StatusForbidden, protocol.ErrForbidden,
				"缺少或無效的 X-BackEZ-Token，請重新整理面板後再試", nil)
			return
		}
		if d.core == nil {
			writeAPIError(w, http.StatusInternalServerError, protocol.ErrInternal, "Agent 核心未初始化", nil)
			return
		}
		caps := d.currentCapabilities()
		if !caps.IsAdmin {
			writeAPIError(w, http.StatusForbidden, protocol.ErrForbidden,
				"需要管理員權限才能執行還原操作", nil)
			return
		}
		if !caps.Ready {
			writeAPIError(w, http.StatusConflict, protocol.ErrCapabilityUnsupported,
				"目前環境不支援還原操作："+blockerSummary(caps.Blockers), caps.Blockers)
			return
		}
		if d.currentEngine() == nil {
			writeAPIError(w, http.StatusConflict, protocol.ErrCapabilityUnsupported,
				"VHDX 引擎未就緒，無法執行還原操作", nil)
			return
		}
		next(w, r)
	}
}

// writeSysRestoreError maps a sysrestore error to the unified API response.
func writeSysRestoreError(w http.ResponseWriter, err error) {
	var srErr *sysrestore.Error
	if errors.As(err, &srErr) {
		status := http.StatusInternalServerError
		switch srErr.Code {
		case protocol.ErrForbidden:
			status = http.StatusForbidden
		case protocol.ErrInvalidArgument:
			status = http.StatusBadRequest
		case protocol.ErrNotFound:
			status = http.StatusNotFound
		case protocol.ErrCapabilityUnsupported, protocol.ErrInvalidState:
			status = http.StatusConflict
		}
		writeAPIError(w, status, srErr.Code, srErr.Message, nil)
		return
	}
	writeAPIError(w, http.StatusInternalServerError, protocol.ErrInternal, err.Error(), nil)
}

// guardSysRestore protects the built-in Windows System Restore endpoints.
// It is deliberately independent from the VHDX capability gate: this path is
// the low-risk backup and must stay usable while VHDX is not provisioned.
func (d *guiDeps) guardSysRestore(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r) {
			writeAPIError(w, http.StatusForbidden, protocol.ErrForbidden,
				"拒絕跨來源請求：Origin 與本機介面主機不一致", nil)
			return
		}
		if !tokenMatches(r.Header.Get(tokenHeader), d.token) {
			writeAPIError(w, http.StatusForbidden, protocol.ErrForbidden,
				"缺少或無效的 X-BackEZ-Token，請重新整理面板後再試", nil)
			return
		}
		if d.sysrestore == nil {
			writeAPIError(w, http.StatusInternalServerError, protocol.ErrInternal,
				"Windows 系統還原模組未初始化", nil)
			return
		}
		if d.dryRun {
			writeAPIError(w, http.StatusConflict, protocol.ErrInvalidState,
				"目前是預覽模式（DryRun）：請用「實際模式」啟動 back-EZ 才能建立或還原 Windows 系統還原點", nil)
			return
		}
		status := d.sysrestore.Status(r.Context())
		if !status.IsAdmin {
			writeAPIError(w, http.StatusForbidden, protocol.ErrForbidden,
				"需要管理員權限才能操作 Windows 系統還原", nil)
			return
		}
		next(w, r)
	}
}

// guardLocal enforces Origin + X-BackEZ-Token for local control endpoints
// that do not touch restore points (for example, shutting the panel down).
func (d *guiDeps) guardLocal(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r) {
			writeAPIError(w, http.StatusForbidden, protocol.ErrForbidden,
				"拒絕跨來源請求：Origin 與本機介面主機不一致", nil)
			return
		}
		if !tokenMatches(r.Header.Get(tokenHeader), d.token) {
			writeAPIError(w, http.StatusForbidden, protocol.ErrForbidden,
				"缺少或無效的 X-BackEZ-Token，請重新整理面板後再試", nil)
			return
		}
		next(w, r)
	}
}

// handleHealth is a cheap liveness probe used for single-instance detection.
// It must not run capability probes or touch the filesystem.
func (d *guiDeps) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"data": map[string]any{
			"app":     "back-ez",
			"dry_run": d.dryRun,
		},
	})
}

// handleShutdown stops the local server. The request is answered first so the
// panel can show a message; the process then exits cleanly.
func (d *guiDeps) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeAPISuccess(w)
	if d.shutdown == nil {
		return
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		d.shutdown()
	}()
}

// handleSysRestoreStatus returns the built-in System Restore capability report.
func (d *guiDeps) handleSysRestoreStatus(w http.ResponseWriter, r *http.Request) {
	if d.sysrestore == nil {
		writeAPIError(w, http.StatusInternalServerError, protocol.ErrInternal,
			"Windows 系統還原模組未初始化", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"data": d.sysrestore.Status(r.Context()),
	})
}

// handleSysRestorePoints lists the existing Windows restore points.
func (d *guiDeps) handleSysRestorePoints(w http.ResponseWriter, r *http.Request) {
	if d.sysrestore == nil {
		writeAPIError(w, http.StatusInternalServerError, protocol.ErrInternal,
			"Windows 系統還原模組未初始化", nil)
		return
	}
	points, err := d.sysrestore.List(r.Context())
	if err != nil {
		writeSysRestoreError(w, err)
		return
	}
	if points == nil {
		points = []sysrestore.Point{}
	}
	writeJSON(w, http.StatusOK, points)
}

// handleSysRestoreEnable turns System Protection on for the system drive.
func (d *guiDeps) handleSysRestoreEnable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Drive string `json:"drive"`
	}
	// Body is optional: an empty request enables the system drive.
	if r.Body != nil {
		_ = decodeJSONBody(r, &body)
	}
	if err := d.sysrestore.Enable(r.Context(), body.Drive); err != nil {
		writeSysRestoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"data": d.sysrestore.Status(r.Context()),
	})
}

// handleSysRestoreCreate creates a Windows restore point.
func (d *guiDeps) handleSysRestoreCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Description string `json:"description"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, protocol.ErrInvalidArgument,
			"參數錯誤：無法解析請求內容", nil)
		return
	}
	point, err := d.sysrestore.Create(r.Context(), body.Description)
	if err != nil {
		writeSysRestoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": point})
}

// handleSysRestoreRestore applies a Windows restore point (the PC will reboot).
// The caller must type RESTORE, checked again on the server side.
func (d *guiDeps) handleSysRestoreRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Sequence int    `json:"sequence"`
		Confirm  string `json:"confirm"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, protocol.ErrInvalidArgument,
			"參數錯誤：無法解析請求內容", nil)
		return
	}
	if strings.TrimSpace(body.Confirm) != "RESTORE" {
		writeAPIError(w, http.StatusBadRequest, protocol.ErrInvalidArgument,
			"請輸入 RESTORE 以確認執行系統還原", nil)
		return
	}
	if err := d.sysrestore.Restore(r.Context(), body.Sequence); err != nil {
		writeSysRestoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"data": map[string]any{"rebooting": true},
	})
}

// handleIndex serves the local dashboard, injecting the capability-derived
// edition and the per-launch token. Home detection uses the capability report
// only; it never calls PowerShell.
func (d *guiDeps) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	caps := d.currentCapabilities()
	edition := strings.TrimSpace(caps.Edition)
	if edition == "" {
		edition = "Unknown"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	page := ui.GenerateFullPanelHTML(ui.PanelData{
		Edition: edition,
		IsHome:  isHomeEdition(caps.Edition),
		Token:   d.token,
	})
	_, _ = io.WriteString(w, page)
}

// handleSystemStatus returns the capability report plus the current engine and
// chain state. It is the single honest status source for the panel.
func (d *guiDeps) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	caps := d.currentCapabilities()
	engine := d.currentEngine()

	data := systemStatusData{
		Capabilities:  caps,
		EngineReady:   engine != nil,
		EngineWarning: "",
		DryRun:        engine != nil && engine.DryRun(),
		PendingReboot: false,
		ActivePointID: nil,
	}
	if engine != nil {
		data.EngineWarning = engine.LoadWarning()
	}
	if d.core != nil {
		if raw, err := d.core.GetStatus(); err == nil {
			if status, ok := raw.(map[string]any); ok {
				if v, exists := status["active_point_id"]; exists {
					data.ActivePointID = v
				}
				if v, exists := status["pending_reboot"]; exists {
					if pending, ok := v.(bool); ok {
						data.PendingReboot = pending
					}
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

// handleListRestorePoints returns a bare JSON array on success (the legacy
// panel contract) or the unified error object on failure.
func (d *guiDeps) handleListRestorePoints(w http.ResponseWriter, r *http.Request) {
	if d.core == nil {
		writeAPIError(w, http.StatusInternalServerError, protocol.ErrInternal, "Agent 核心未初始化", nil)
		return
	}
	points, err := d.core.ListRestorePoints()
	if err != nil {
		writeCoreError(w, err)
		return
	}
	if points == nil {
		points = []protocol.RestorePoint{}
	}
	writeJSON(w, http.StatusOK, points)
}

// handleLogs returns the in-memory operation log entries as a JSON array.
func (d *guiDeps) handleLogs(w http.ResponseWriter, r *http.Request) {
	if d.logger == nil {
		writeJSON(w, http.StatusOK, []service.LogEntry{})
		return
	}
	writeJSON(w, http.StatusOK, d.logger.GetEntries())
}

func (d *guiDeps) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, protocol.ErrInvalidArgument, "參數錯誤：無法解析請求內容", nil)
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeAPIError(w, http.StatusBadRequest, protocol.ErrInvalidArgument, "參數錯誤：還原點名稱不可為空", nil)
		return
	}
	if err := d.core.CreateRestorePoint(body.Name, body.Description); err != nil {
		writeCoreError(w, err)
		return
	}
	writeAPISuccess(w)
}

func (d *guiDeps) handleSwitch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PointID string `json:"point_id"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, protocol.ErrInvalidArgument, "參數錯誤：無法解析請求內容", nil)
		return
	}
	if strings.TrimSpace(body.PointID) == "" {
		writeAPIError(w, http.StatusBadRequest, protocol.ErrInvalidArgument, "參數錯誤：必須提供 point_id", nil)
		return
	}
	if err := d.core.SwitchRestorePoint(body.PointID); err != nil {
		writeCoreError(w, err)
		return
	}
	writeAPISuccess(w)
}

func (d *guiDeps) handleDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PointID string `json:"point_id"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, protocol.ErrInvalidArgument, "參數錯誤：無法解析請求內容", nil)
		return
	}
	if strings.TrimSpace(body.PointID) == "" {
		writeAPIError(w, http.StatusBadRequest, protocol.ErrInvalidArgument, "參數錯誤：必須提供 point_id", nil)
		return
	}
	if err := d.core.DeleteRestorePoint(body.PointID); err != nil {
		writeCoreError(w, err)
		return
	}
	writeAPISuccess(w)
}

// newMux wires the local dashboard routes. It is separated from main so tests
// can exercise the HTTP surface through httptest without a real listener.
func newMux(deps *guiDeps) *http.ServeMux {
	mux := http.NewServeMux()

	// "/{$}" matches only the exact root so removed/no-longer-served paths
	// (e.g. /api/restore-points/overwrite) return 404 instead of the panel or 405.
	mux.HandleFunc("GET /{$}", deps.handleIndex)
	mux.HandleFunc("GET /api/system/status", deps.handleSystemStatus)
	mux.HandleFunc("GET /api/restore-points", deps.handleListRestorePoints)
	mux.HandleFunc("GET /api/logs", deps.handleLogs)
	mux.HandleFunc("GET /api/health", deps.handleHealth)
	mux.HandleFunc("POST /api/system/shutdown", deps.guardLocal(deps.handleShutdown))

	mux.HandleFunc("POST /api/restore-points/create", deps.guardMutation(deps.handleCreate))
	mux.HandleFunc("POST /api/restore-points/switch", deps.guardMutation(deps.handleSwitch))
	mux.HandleFunc("POST /api/restore-points/delete", deps.guardMutation(deps.handleDelete))

	// Built-in Windows System Restore (backup path, independent of VHDX).
	mux.HandleFunc("GET /api/sysrestore/status", deps.handleSysRestoreStatus)
	mux.HandleFunc("GET /api/sysrestore/points", deps.handleSysRestorePoints)
	mux.HandleFunc("POST /api/sysrestore/enable", deps.guardSysRestore(deps.handleSysRestoreEnable))
	mux.HandleFunc("POST /api/sysrestore/create", deps.guardSysRestore(deps.handleSysRestoreCreate))
	mux.HandleFunc("POST /api/sysrestore/restore", deps.guardSysRestore(deps.handleSysRestoreRestore))

	return mux
}

// newServer builds the localhost HTTP server. The caller runs Serve and calls
// Shutdown on interrupt.
func newServer(deps *guiDeps) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", deps.port),
		Handler:           newMux(deps),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func logCapabilities(logger *service.OperationLogger, caps vhdx.Capabilities) {
	if logger == nil {
		return
	}
	if caps.Error != "" {
		logger.Log("SYSTEM", "CAPABILITY", "FAILED", caps.Error)
		return
	}
	if caps.Ready {
		logger.Log("SYSTEM", "CAPABILITY", "SUCCESS", fmt.Sprintf(
			"能力檢測通過（Edition=%s, Build=%s, 管理員=%t, 保留區=%s）",
			caps.Edition, caps.Build, caps.IsAdmin, caps.ReserveRoot))
		return
	}
	logger.Log("SYSTEM", "CAPABILITY", "FAILED", fmt.Sprintf(
		"能力檢測未通過（%d 項阻塞）：%s", len(caps.Blockers), blockerSummary(caps.Blockers)))
}

func main() {
	reserveRoot := flag.String("reserve", defaultReserveRoot, "Reserve root directory path")
	port := flag.Int("port", defaultPort, "Local GUI Web dashboard port")
	dryRun := flag.Bool("dryrun", true, "Simulate BCD/disk operations without modifying real disk (default: true)")
	iUnderstandRisk := flag.Bool("i-understand-risk", false, "Acknowledge destructive operations when running with -dryrun=false")
	noBrowser := flag.Bool("no-browser", false, "Do not open the dashboard browser window automatically")
	flag.Parse()

	allowDestructive := !*dryRun && *iUnderstandRisk
	if !*dryRun && !*iUnderstandRisk {
		log.Printf("警告：已指定 -dryrun=false 但未提供 -i-understand-risk，破壞性操作維持停用（fail-closed）")
	}

	log.Printf("啟動 back-EZ 本機秒級還原整合系統 (DryRun=%t, AllowDestructive=%t)...", *dryRun, allowDestructive)

	// 1. 初始化日誌記錄器
	logFile := filepath.Join(*reserveRoot, "logs", "operation.log")
	logger := service.NewOperationLogger(logFile)
	logger.Log("SYSTEM", "APP_START", "SUCCESS", fmt.Sprintf(
		"back-EZ 控制台已啟動 (模式: DryRun=%t, AllowDestructive=%t, 保留區: %s)",
		*dryRun, allowDestructive, *reserveRoot))

	// 2. 能力偵測（第一道 gate；3 秒 timeout、30 秒快取）
	capCache := newCapabilityCache(*reserveRoot, capabilityTTL, nil)
	caps := capCache.get()
	logCapabilities(logger, caps)
	homeEdition := isHomeEdition(caps.Edition)
	log.Printf("能力檢測結果: Ready=%t Edition=%s Build=%s 管理員=%t 阻塞=%d 項",
		caps.Ready, caps.Edition, caps.Build, caps.IsAdmin, len(caps.Blockers))

	// 3. 依能力報告建立 vhdx.Engine（磁碟區 GUID 與相對路徑皆來自能力報告）
	var exec vhdx.CommandExecutor
	if *dryRun {
		exec = vhdx.NewDryRunExecutor()
	} else {
		exec = &vhdx.RealExecutor{}
	}

	engineCfg := vhdx.Config{
		DryRun:               *dryRun,
		AllowDestructive:     allowDestructive,
		RealExecutionEnabled: vhdx.RealExecutionImplemented,
		VolumeGUID:           caps.ReserveVolume,
		ReserveRoot:          *reserveRoot,
		ReserveRelPath:       caps.ReserveRelPath,
		BCDEntryID:           "{default}",
		DataDir:              *reserveRoot,
	}
	engine, engineErr := vhdx.NewEngine(engineCfg, exec)
	if engineErr != nil {
		engine = nil
		logger.Log("SYSTEM", "VHDX_ENGINE", "FAILED", fmt.Sprintf("初始化 VHDX 引擎失敗: %v", engineErr))
		log.Printf("初始化 VHDX 引擎失敗（GUI 繼續以受限模式啟動）: %v", engineErr)
	} else {
		engineDetail := "底層 VHDX/BCD 引擎已初始化（尚未實際修改磁碟/BCD）"
		if caps.Ready {
			engineDetail += "，能力檢測通過"
		} else {
			engineDetail += "；能力尚未就緒，所有還原操作都會被拒絕"
		}
		logger.Log("SYSTEM", "VHDX_ENGINE", "SUCCESS", engineDetail)
		// EnsureBase 失敗只記錄，不中止 GUI：能力不足時面板要能顯示原因。
		if _, err := engine.EnsureBase("Base System", "Original Clean Base Image"); err != nil {
			logger.Log("SYSTEM", "ENSURE_BASE", "FAILED", fmt.Sprintf("無法確保基準還原點（GUI 繼續啟動）: %v", err))
		} else {
			logger.Log("SYSTEM", "ENSURE_BASE", "SUCCESS", "基準還原點已就緒")
		}
	}

	// 4. 每次啟動產生一次性本機 token；同時作為 IPC 初始密碼，不再使用硬編碼 "admin"。
	token, err := newToken()
	if err != nil {
		fatalError(logger, "TOKEN", "無法產生本機存取 token: %v", err)
	}

	// 5. 初始化 AgentCore（鏈的唯一狀態來源仍是 vhdx.Engine）
	cfg, cfgPath, err := service.LoadOrCreateConfig(*reserveRoot)
	if err != nil {
		fatalError(logger, "CONFIG", "無法載入 Agent 設定: %v", err)
	}
	core, err := service.NewAgentCore(*cfg, cfgPath, token)
	if err != nil {
		fatalError(logger, "CORE", "無法初始化 Agent 核心: %v", err)
	}
	core.SetLogger(logger)
	if engine != nil {
		core.SetEngine(engine)
	}

	// 6. PE 開機管理員的 DryRun/AllowDestructive 一律跟隨 engine，避免面板模式與
	//    實際可執行性不一致；B 案啟用真實寫入前一律 fail-closed。
	peDryRun, peAllowDestructive := true, false
	if engine != nil {
		peDryRun = engine.DryRun()
		peAllowDestructive = engine.AllowDestructive() && vhdx.RealExecutionImplemented
	}
	peReserveRelPath := strings.TrimSpace(caps.ReserveRelPath)
	if peReserveRelPath == "" {
		peReserveRelPath = filepath.Base(filepath.Clean(*reserveRoot))
	}
	core.SetPEBootManager(vhdx.NewPEBootManager(vhdx.PEBootConfig{
		DryRun:           peDryRun,
		AllowDestructive: peAllowDestructive,
		VolumeGUID:       caps.ReserveVolume,
		ReserveRoot:      peReserveRelPath,
		PhysicalDiskRoot: *reserveRoot,
	}, exec))

	// 7. 組裝 HTTP 依賴（token 已在啟動時產生）
	deps := &guiDeps{
		core:               core,
		logger:             logger,
		capabilityProvider: capCache.get,
		sysrestore:         sysrestore.NewManager(),
		token:              token,
		reserveRoot:        *reserveRoot,
		port:               *port,
		engine:             engine,
		dryRun:             *dryRun,
	}
	server := newServer(deps)

	// 8. 若已有 back-EZ 在跑，直接開既有面板；避免第二次啟動因埠被佔用而無聲結束。
	serverURL := fmt.Sprintf("http://127.0.0.1:%d", *port)
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	if probe := probeInstance(addr, serverURL); probe.Exists {
		openExistingInstance(logger, probe, *dryRun, serverURL, *noBrowser)
		return
	}

	shutdownCh := make(chan struct{})
	var shutdownOnce sync.Once
	deps.shutdown = func() {
		shutdownOnce.Do(func() { close(shutdownCh) })
	}

	listener, err := listenWithRetry(addr, 5, 400*time.Millisecond)
	if err != nil {
		if probe := probeInstance(addr, serverURL); probe.Exists {
			openExistingInstance(logger, probe, *dryRun, serverURL, *noBrowser)
			return
		}
		fatalError(logger, "HTTP_LISTEN",
			"無法監聽本地通訊埠 %s：%v\n\n可能是其他程式佔用此埠；請關閉該程式或使用 -port 指定其他埠。",
			addr, err)
	}

	log.Printf("本地控制台已啟動: %s", serverURL)
	logger.Log("SYSTEM", "HTTP_SERVER", "SUCCESS", fmt.Sprintf(
		"本機控制台已啟動：%s（Edition=%s, Home=%t, 能力就緒=%t）",
		serverURL, caps.Edition, homeEdition, caps.Ready))

	// 9. 啟動 HTTP 伺服器
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP 伺服器錯誤: %v", err)
		}
	}()

	// 10. 自動彈出桌面應用視窗（-no-browser 供自動化驗證使用）
	if !*noBrowser {
		go openBrowser(serverURL)
	}

	// 11. 等待中斷訊號或面板「結束 back-EZ」指令
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sigCh:
	case <-shutdownCh:
		log.Println("收到面板結束指令...")
	}

	log.Println("正在安全關閉 back-EZ 控制台...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP 伺服器關閉失敗: %v", err)
	}
	logger.Log("SYSTEM", "APP_STOP", "SUCCESS", "back-EZ 控制台已安全關閉")
}
