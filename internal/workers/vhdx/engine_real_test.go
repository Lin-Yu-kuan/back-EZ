package vhdx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/owner/back-ez/internal/protocol"
)

const (
	realTestVolume = `\\?\Volume{11111111-2222-3333-4444-555555555555}\`
	realTestEntry  = "{aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}"
)

type fakeDisk struct {
	mu          sync.Mutex
	existing    map[string]bool
	baseCreates []string
	diffs       [][2]string
	deletes     []string
	createErr   error
	deleteErr   error
}

func (f *fakeDisk) CreateBase(_ context.Context, path string, _ uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.baseCreates = append(f.baseCreates, path)
	f.existing[path] = true
	return nil
}

func (f *fakeDisk) CreateDifferencing(_ context.Context, path, parentPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.diffs = append(f.diffs, [2]string{path, parentPath})
	f.existing[path] = true
	return nil
}

func (f *fakeDisk) Delete(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletes = append(f.deletes, path)
	delete(f.existing, path)
	return nil
}

func (f *fakeDisk) Exists(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.existing[path]
}

type bcdTargetCall struct {
	entryID string
	device  string
}

type fakeBCD struct {
	mu         sync.Mutex
	backups    []string
	restores   []string
	targets    []bcdTargetCall
	backupErr  error
	setErr     error
	verifyErr  error
	restoreErr error
	enumOutput string
	enumErr    error
	copyID     string
}

func (f *fakeBCD) Backup(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.backupErr != nil {
		return f.backupErr
	}
	f.backups = append(f.backups, path)
	return nil
}

func (f *fakeBCD) Restore(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.restoreErr != nil {
		return f.restoreErr
	}
	f.restores = append(f.restores, path)
	return nil
}

func (f *fakeBCD) CopyCurrent(context.Context, string) (string, error) {
	return f.copyID, nil
}

func (f *fakeBCD) SetTarget(_ context.Context, entryID, device string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.targets = append(f.targets, bcdTargetCall{entryID: entryID, device: device})
	return nil
}

func (f *fakeBCD) Verify(context.Context, string, string) error {
	return f.verifyErr
}

func (f *fakeBCD) Enumerate(context.Context, string) (string, error) {
	return f.enumOutput, f.enumErr
}

func (f *fakeBCD) SetBootSequence(context.Context, string) error { return nil }
func (f *fakeBCD) DeleteEntry(context.Context, string) error     { return nil }

func newRealTestEngine(t *testing.T) (*Engine, *fakeDisk, *fakeBCD, string) {
	t.Helper()

	dir := t.TempDir()
	engine, err := NewEngine(Config{
		DryRun:         true,
		VolumeGUID:     realTestVolume,
		ReserveRoot:    "EZRestore",
		ReserveRelPath: "EZRestore",
		DataDir:        dir,
	}, nil)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	if _, err := engine.InitBase("Base", "base", "base.vhdx", 4); err != nil {
		t.Fatalf("InitBase failed: %v", err)
	}

	disk := &fakeDisk{existing: map[string]bool{
		engine.physicalPath("base.vhdx"):   true,
		engine.physicalPath("live_a.vhdx"): true,
	}}
	bcd := &fakeBCD{copyID: realTestEntry}

	engine.disk = disk
	engine.bcd = bcd
	engine.cfg.DryRun = false
	engine.cfg.AllowDestructive = true
	engine.cfg.RealExecutionEnabled = true
	engine.live = &LiveLayer{
		File:          "live_a.vhdx",
		ParentPointID: "rp_base",
		BootEntryID:   realTestEntry,
		CreatedAt:     "2026-11-13T00:00:00Z",
	}

	return engine, disk, bcd, dir
}

func TestEngineCreateReal_CreatesPointAndNewLiveLayer(t *testing.T) {
	engine, disk, bcd, dir := newRealTestEngine(t)

	point, plan, err := engine.Create(CreateRequest{Name: "before update", Description: "test"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if point.VHDXFile != "live_a.vhdx" {
		t.Fatalf("point file = %q, want the previous live layer", point.VHDXFile)
	}
	if point.State != protocol.RestorePointStateCreating {
		t.Fatalf("point state = %s, want CREATING until reboot", point.State)
	}
	if point.Depth != 1 || !point.IsActive {
		t.Fatalf("unexpected point: %+v", point)
	}

	live := engine.Live()
	if live == nil {
		t.Fatalf("live layer was not recorded")
	}
	if live.ParentPointID != point.PointID {
		t.Fatalf("live parent = %q, want %q", live.ParentPointID, point.PointID)
	}
	if live.File == "live_a.vhdx" {
		t.Fatalf("live layer must be a new file")
	}

	if len(disk.diffs) != 1 {
		t.Fatalf("expected 1 differencing create, got %d", len(disk.diffs))
	}
	if !strings.HasSuffix(disk.diffs[0][1], "live_a.vhdx") {
		t.Fatalf("new live layer parent = %q, want live_a.vhdx", disk.diffs[0][1])
	}
	if len(bcd.targets) != 1 {
		t.Fatalf("expected 1 BCD target update, got %d", len(bcd.targets))
	}
	if bcd.targets[0].entryID != realTestEntry {
		t.Fatalf("BCD entry = %q", bcd.targets[0].entryID)
	}
	if !strings.Contains(bcd.targets[0].device, live.File) {
		t.Fatalf("BCD device %q does not reference live layer %q", bcd.targets[0].device, live.File)
	}
	if len(bcd.backups) != 1 {
		t.Fatalf("expected a BCD backup before the change")
	}
	if plan == nil || !plan.RequiresReboot {
		t.Fatalf("create plan must require a reboot")
	}

	if _, err := os.Stat(filepath.Join(dir, "live.json")); err != nil {
		t.Fatalf("live.json was not written: %v", err)
	}
	chainData, err := os.ReadFile(filepath.Join(dir, "chain.json"))
	if err != nil {
		t.Fatalf("chain.json was not written: %v", err)
	}
	if !strings.Contains(string(chainData), point.PointID) {
		t.Fatalf("chain.json does not contain the new point")
	}
}

func TestEngineCreateReal_RollsBackWhenBCDUpdateFails(t *testing.T) {
	engine, disk, bcd, _ := newRealTestEngine(t)
	bcd.setErr = errors.New("bcdedit /set failed")

	_, _, err := engine.Create(CreateRequest{Name: "will fail"})
	if err == nil {
		t.Fatalf("expected Create to fail")
	}
	if len(engine.List()) != 1 {
		t.Fatalf("chain must still contain only the base point, got %d", len(engine.List()))
	}
	live := engine.Live()
	if live.File != "live_a.vhdx" || live.ParentPointID != "rp_base" {
		t.Fatalf("live layer must be restored, got %+v", live)
	}
	if len(bcd.restores) != 1 {
		t.Fatalf("expected BCD restore after failure, got %v", bcd.restores)
	}
	if len(disk.deletes) != 1 {
		t.Fatalf("expected the new live file to be deleted, got %v", disk.deletes)
	}
}

func TestEngineSwitchReal_CreatesChildOfTarget(t *testing.T) {
	engine, disk, bcd, _ := newRealTestEngine(t)

	baseID := "rp_base"
	if err := engine.chain.AddPoint(&protocol.RestorePoint{
		PointID: "rp_old", Name: "old", VHDXFile: "diff_old.vhdx",
		ParentID: &baseID, Depth: 1, State: protocol.RestorePointStateReady,
		CreatedBy: protocol.CreatedByAdminLocal, CreatedAt: "2026-11-13T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed point: %v", err)
	}
	disk.existing[engine.physicalPath("diff_old.vhdx")] = true

	plan, err := engine.Switch("rp_old")
	if err != nil {
		t.Fatalf("Switch failed: %v", err)
	}
	live := engine.Live()
	if live.ParentPointID != "rp_old" {
		t.Fatalf("live parent = %q, want rp_old", live.ParentPointID)
	}
	if len(disk.diffs) != 1 || !strings.HasSuffix(disk.diffs[0][1], "diff_old.vhdx") {
		t.Fatalf("new live must be based on the target point: %v", disk.diffs)
	}
	if active := engine.GetActive(); active == nil || active.PointID != "rp_old" {
		t.Fatalf("active point = %+v, want rp_old", active)
	}
	if len(bcd.targets) != 1 || len(bcd.backups) != 1 {
		t.Fatalf("expected BCD backup and target update")
	}
	if plan == nil || !plan.RequiresReboot {
		t.Fatalf("switch plan must require a reboot")
	}
}

func TestEngineCreateReal_RejectsPendingReboot(t *testing.T) {
	engine, _, _, _ := newRealTestEngine(t)
	baseID := "rp_base"
	if err := engine.chain.AddPoint(&protocol.RestorePoint{
		PointID: "rp_pending", Name: "pending", VHDXFile: "live_a.vhdx",
		ParentID: &baseID, Depth: 1, State: protocol.RestorePointStateCreating,
	}); err != nil {
		t.Fatalf("seed point: %v", err)
	}

	_, _, err := engine.Create(CreateRequest{Name: "blocked"})
	var vhdxErr *VHDXError
	if !errors.As(err, &vhdxErr) || vhdxErr.Code != protocol.ErrInvalidState {
		t.Fatalf("expected INVALID_STATE while a reboot is pending, got %v", err)
	}
}

func TestEngineDeleteReal_RemovesFileAndNode(t *testing.T) {
	engine, disk, _, _ := newRealTestEngine(t)
	baseID := "rp_base"
	if err := engine.chain.AddPoint(&protocol.RestorePoint{
		PointID: "rp_leaf", Name: "leaf", VHDXFile: "diff_leaf.vhdx",
		ParentID: &baseID, Depth: 1, State: protocol.RestorePointStateReady,
	}); err != nil {
		t.Fatalf("seed point: %v", err)
	}
	disk.existing[engine.physicalPath("diff_leaf.vhdx")] = true

	if _, err := engine.Delete("rp_leaf"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if engine.Get("rp_leaf") != nil {
		t.Fatalf("point still exists in the chain")
	}
	if len(disk.deletes) != 1 {
		t.Fatalf("expected the VHDX to be deleted, got %v", disk.deletes)
	}
}

func TestEngineReconcile_MarksCreatingPointReadyWhenBootedFromLive(t *testing.T) {
	engine, _, bcd, _ := newRealTestEngine(t)
	baseID := "rp_base"
	if err := engine.chain.AddPoint(&protocol.RestorePoint{
		PointID: "rp_new", Name: "new", VHDXFile: "live_a.vhdx",
		ParentID: &baseID, Depth: 1, State: protocol.RestorePointStateCreating,
	}); err != nil {
		t.Fatalf("seed point: %v", err)
	}
	engine.chain.SetActive("rp_new")
	engine.live = &LiveLayer{
		File: "live_b.vhdx", ParentPointID: "rp_new", BootEntryID: realTestEntry,
	}
	bcd.enumOutput = `device vhd=[\\?\Volume{11111111-2222-3333-4444-555555555555}]\EZRestore\live_b.vhdx`

	engine.reconcileLiveLocked()

	point := engine.Get("rp_new")
	if point == nil || point.State != protocol.RestorePointStateReady {
		t.Fatalf("point state = %+v, want READY after reboot", point)
	}
}

func TestEngineReconcile_KeepsCreatingWhenNotBootedFromLive(t *testing.T) {
	engine, _, bcd, _ := newRealTestEngine(t)
	baseID := "rp_base"
	if err := engine.chain.AddPoint(&protocol.RestorePoint{
		PointID: "rp_new", Name: "new", VHDXFile: "live_a.vhdx",
		ParentID: &baseID, Depth: 1, State: protocol.RestorePointStateCreating,
	}); err != nil {
		t.Fatalf("seed point: %v", err)
	}
	engine.chain.SetActive("rp_new")
	engine.live = &LiveLayer{
		File: "live_b.vhdx", ParentPointID: "rp_new", BootEntryID: realTestEntry,
	}
	bcd.enumOutput = "device partition=C:"

	engine.reconcileLiveLocked()

	point := engine.Get("rp_new")
	if point == nil || point.State != protocol.RestorePointStateCreating {
		t.Fatalf("point must stay CREATING until the VHDX is booted, got %+v", point)
	}
	if !strings.Contains(engine.LoadWarning(), "不是從 VHDX") {
		t.Fatalf("expected a warning about not being booted from VHDX, got %q", engine.LoadWarning())
	}
}

func TestEngineRealPath_FailsClosedWhenDisabled(t *testing.T) {
	engine, _, _, _ := newRealTestEngine(t)
	engine.cfg.RealExecutionEnabled = false

	_, _, err := engine.Create(CreateRequest{Name: "blocked"})
	var vhdxErr *VHDXError
	if !errors.As(err, &vhdxErr) || vhdxErr.Code != protocol.ErrCapabilityUnsupported {
		t.Fatalf("expected CAPABILITY_UNSUPPORTED when real execution is disabled, got %v", err)
	}
}
