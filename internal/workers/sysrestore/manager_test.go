package sysrestore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/owner/back-ez/internal/protocol"
)

type fakeCall struct {
	script string
	env    map[string]string
}

type fakeRunner struct {
	mu     sync.Mutex
	queue  []string
	calls  []fakeCall
	runErr error
}

func (f *fakeRunner) Run(_ context.Context, script string, env map[string]string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, fakeCall{script: script, env: env})
	if f.runErr != nil {
		return nil, f.runErr
	}
	if len(f.queue) == 0 {
		return nil, errors.New("fakeRunner: no queued response")
	}
	out := f.queue[0]
	f.queue = f.queue[1:]
	return []byte(out), nil
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func testManager(admin bool, now time.Time, queue ...string) (*Manager, *fakeRunner) {
	runner := &fakeRunner{queue: queue}
	return &Manager{
		runner:  runner,
		drive:   `C:\`,
		isAdmin: func() bool { return admin },
		now:     func() time.Time { return now },
	}, runner
}

func statusJSON(protection bool, pointCount int, lastCreated string, throttleMinutes int, throttlePresent bool, freeBytes uint64) string {
	return fmt.Sprintf(`{"ok":true,"admin":true,"cmdlets_ok":true,"protection_enabled":%t,`+
		`"free_bytes":%d,"throttle_minutes":%d,"throttle_present":%t,`+
		`"point_count":%d,"last_created_at":%q,"list_error":""}`,
		protection, freeBytes, throttleMinutes, throttlePresent, pointCount, lastCreated)
}

func TestStatus_ProtectionDisabledBlocksCreate(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	manager, _ := testManager(true, now, statusJSON(false, 0, "", 0, false, 100<<30))

	status := manager.Status(context.Background())

	if !status.Supported {
		t.Fatalf("expected supported status")
	}
	if status.ProtectionEnabled {
		t.Fatalf("expected protection to be disabled")
	}
	if status.CanCreate || status.CanRestore {
		t.Fatalf("disabled protection must block create and restore: %+v", status)
	}
	if !hasBlocker(status, "protection_disabled") {
		t.Fatalf("expected protection_disabled blocker, got %+v", status.Blockers)
	}
	// SystemRestorePointCreationFrequency absent => Windows default 24h.
	if status.ThrottleMinutes != DefaultThrottleMinutes {
		t.Fatalf("expected default throttle %d, got %d", DefaultThrottleMinutes, status.ThrottleMinutes)
	}
}

func TestStatus_ThrottleWindow(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	last := now.Add(-time.Hour).Format(time.RFC3339)
	manager, _ := testManager(true, now, statusJSON(true, 1, last, 1440, true, 100<<30))

	status := manager.Status(context.Background())

	if status.NextAllowedAt == "" {
		t.Fatalf("expected a throttle window")
	}
	if status.CanCreate {
		t.Fatalf("expected create to be blocked by throttle")
	}
	if !hasBlocker(status, "throttled") {
		t.Fatalf("expected throttled blocker, got %+v", status.Blockers)
	}
	if !status.CanRestore {
		t.Fatalf("throttle must not block restore")
	}
}

func TestStatus_NonAdminBlocksEverything(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	manager, _ := testManager(false, now, statusJSON(true, 0, "", 0, true, 100<<30))

	status := manager.Status(context.Background())

	if status.IsAdmin {
		t.Fatalf("expected non-admin status")
	}
	if status.CanCreate || status.CanRestore {
		t.Fatalf("non-admin must block create and restore")
	}
	if !hasBlocker(status, "admin_required") {
		t.Fatalf("expected admin_required blocker, got %+v", status.Blockers)
	}
}

func TestList_ParsesPoints(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	manager, runner := testManager(true, now,
		`{"ok":true,"points":[{"sequence":5,"description":"before update","type":0,"created_at":"2026-11-12T04:00:00Z"},{"sequence":6,"description":"after update","type":12,"created_at":"2026-11-12T05:00:00Z"}]}`)

	points, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(points))
	}
	if points[0].Sequence != 5 || points[0].Description != "before update" {
		t.Fatalf("unexpected first point: %+v", points[0])
	}
	if points[1].Type != 12 {
		t.Fatalf("expected type 12, got %d", points[1].Type)
	}
	if runner.callCount() != 1 {
		t.Fatalf("expected exactly one PowerShell call, got %d", runner.callCount())
	}
}

func TestList_NonAdminForbidden(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	manager, runner := testManager(false, now)

	_, err := manager.List(context.Background())
	var srErr *Error
	if !errors.As(err, &srErr) || srErr.Code != protocol.ErrForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("non-admin must not invoke PowerShell, got %d calls", runner.callCount())
	}
}

func TestCreate_PassesDescriptionViaEnvironment(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	manager, runner := testManager(true, now,
		statusJSON(true, 1, now.Add(-48*time.Hour).Format(time.RFC3339), 0, true, 100<<30),
		`{"ok":true,"point":{"sequence":7,"description":"安裝更新前","type":0,"created_at":"2026-11-12T12:00:00Z"}}`)

	point, err := manager.Create(context.Background(), "  安裝更新前  ")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if point.Sequence != 7 || point.Description != "安裝更新前" {
		t.Fatalf("unexpected point: %+v", point)
	}
	if runner.callCount() != 2 {
		t.Fatalf("expected status + create calls, got %d", runner.callCount())
	}
	createCall := runner.calls[1]
	if createCall.script != createScript {
		t.Fatalf("expected create script on second call")
	}
	if got := createCall.env["BACEZ_DESC"]; got != "安裝更新前" {
		t.Fatalf("description must be passed trimmed via env, got %q", got)
	}
	if got := createCall.env["BACEZ_DRIVE"]; got != `C:\` {
		t.Fatalf("expected drive env, got %q", got)
	}
}

func TestCreate_BlockedBeforeRunningCmdlet(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		status   string
		wantCode string
	}{
		{
			name:     "protection disabled",
			status:   statusJSON(false, 0, "", 0, false, 100<<30),
			wantCode: protocol.ErrInvalidState,
		},
		{
			name:     "throttled",
			status:   statusJSON(true, 1, now.Add(-time.Hour).Format(time.RFC3339), 1440, true, 100<<30),
			wantCode: protocol.ErrInvalidState,
		},
		{
			name:     "unsupported",
			status:   `{"ok":true,"admin":true,"cmdlets_ok":false,"protection_enabled":true,"free_bytes":107374182400,"throttle_minutes":0,"throttle_present":true,"point_count":0,"last_created_at":"","list_error":""}`,
			wantCode: protocol.ErrCapabilityUnsupported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, runner := testManager(true, now, test.status)
			_, err := manager.Create(context.Background(), "x")
			var srErr *Error
			if !errors.As(err, &srErr) || srErr.Code != test.wantCode {
				t.Fatalf("expected %s, got %v", test.wantCode, err)
			}
			if runner.callCount() != 1 {
				t.Fatalf("expected only the status probe, got %d calls", runner.callCount())
			}
		})
	}
}

func TestCreate_EmptyDescription(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	manager, runner := testManager(true, now)

	_, err := manager.Create(context.Background(), "   ")
	var srErr *Error
	if !errors.As(err, &srErr) || srErr.Code != protocol.ErrInvalidArgument {
		t.Fatalf("expected INVALID_ARGUMENT, got %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("validation must not invoke PowerShell")
	}
}

func TestRestore_RequiresKnownSequenceThenRuns(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	list := `{"ok":true,"points":[{"sequence":5,"description":"before","type":0,"created_at":"2026-11-12T04:00:00Z"}]}`

	t.Run("unknown sequence", func(t *testing.T) {
		manager, runner := testManager(true, now, list)
		err := manager.Restore(context.Background(), 6)
		var srErr *Error
		if !errors.As(err, &srErr) || srErr.Code != protocol.ErrNotFound {
			t.Fatalf("expected NOT_FOUND, got %v", err)
		}
		if runner.callCount() != 1 {
			t.Fatalf("must only list, got %d calls", runner.callCount())
		}
	})

	t.Run("valid sequence", func(t *testing.T) {
		manager, runner := testManager(true, now, list, `{"ok":true}`)
		if err := manager.Restore(context.Background(), 5); err != nil {
			t.Fatalf("Restore failed: %v", err)
		}
		if runner.callCount() != 2 {
			t.Fatalf("expected list + restore calls, got %d", runner.callCount())
		}
		if got := runner.calls[1].env["BACEZ_SEQ"]; got != "5" {
			t.Fatalf("expected BACEZ_SEQ=5, got %q", got)
		}
	})
}

func TestRestore_NonAdminForbidden(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	manager, runner := testManager(false, now)

	err := manager.Restore(context.Background(), 5)
	var srErr *Error
	if !errors.As(err, &srErr) || srErr.Code != protocol.ErrForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("non-admin must not invoke PowerShell")
	}
}

func TestEnable_AlreadyEnabledIsSuccess(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	manager, _ := testManager(true, now,
		`{"ok":false,"error":"System protection is already enabled."}`,
		statusJSON(true, 0, "", 0, true, 100<<30))

	if err := manager.Enable(context.Background(), `C:\`); err != nil {
		t.Fatalf("already-enabled Enable should succeed, got %v", err)
	}
}

func TestEnable_NonAdminForbidden(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	manager, _ := testManager(false, now)

	err := manager.Enable(context.Background(), `C:\`)
	var srErr *Error
	if !errors.As(err, &srErr) || srErr.Code != protocol.ErrForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

func TestDecodeResult_ToleratesBOMAndLeadingNoise(t *testing.T) {
	out := []byte("\xEF\xBB\xBFwarning: something\r\n{\"ok\":true,\"points\":[]}\r\n")
	result, err := decodeResult(out)
	if err != nil {
		t.Fatalf("decodeResult failed: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected ok result")
	}
}

func TestMapPowerShellError(t *testing.T) {
	tests := []struct {
		message  string
		wantCode string
	}{
		{"Access is denied", protocol.ErrForbidden},
		{"存取被拒。", protocol.ErrForbidden},
		{"System Restore is disabled on this computer", protocol.ErrInvalidState},
		{"系統還原已停用", protocol.ErrInvalidState},
		{"something unexpected", protocol.ErrInternal},
		{"", protocol.ErrInternal},
	}

	for _, test := range tests {
		err := mapPowerShellError(test.message)
		var srErr *Error
		if !errors.As(err, &srErr) || srErr.Code != test.wantCode {
			t.Fatalf("mapPowerShellError(%q) = %v, want %s", test.message, err, test.wantCode)
		}
	}
}

func TestNormalizeDrive(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"C", `C:\`, true},
		{"c:", `C:\`, true},
		{`D:\`, `D:\`, true},
		{"", "", false},
		{"CC", "", false},
		{"1:", "", false},
	}

	for _, test := range tests {
		got, ok := normalizeDrive(test.in)
		if got != test.want || ok != test.ok {
			t.Fatalf("normalizeDrive(%q) = (%q,%t), want (%q,%t)", test.in, got, ok, test.want, test.ok)
		}
	}
}

func TestStatus_ProbeFailureIsReported(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	manager, runner := testManager(true, now)
	runner.runErr = errors.New("powershell.exe not found")

	status := manager.Status(context.Background())
	if status.Error == "" {
		t.Fatalf("expected a probe error")
	}
	if !hasBlocker(status, "probe_failed") {
		t.Fatalf("expected probe_failed blocker, got %+v", status.Blockers)
	}
	if status.CanCreate || status.CanRestore {
		t.Fatalf("probe failure must fail closed")
	}
}

func hasBlocker(status Status, key string) bool {
	for _, blocker := range status.Blockers {
		if strings.EqualFold(blocker.Key, key) {
			return true
		}
	}
	return false
}
