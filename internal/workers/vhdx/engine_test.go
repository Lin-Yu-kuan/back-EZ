package vhdx_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owner/back-ez/internal/protocol"
	"github.com/owner/back-ez/internal/workers/vhdx"
)

func setupTestEngine(t *testing.T, volumeGUID string) (*vhdx.Engine, *vhdx.DryRunExecutor, string) {
	t.Helper()
	tmpDir := t.TempDir()
	executor := vhdx.NewDryRunExecutor()

	cfg := vhdx.Config{
		DryRun:           true,
		AllowDestructive: false,
		VolumeGUID:       volumeGUID,
		ReserveRoot:      "EZRestore",
		BCDEntryID:       "{default}",
		DataDir:          tmpDir,
	}

	eng, err := vhdx.NewEngine(cfg, executor)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	base, err := eng.InitBase("Base OS", "Clean install", "base.vhdx", 1024*1024*1024)
	if err != nil {
		t.Fatalf("failed to init base: %v", err)
	}
	if !base.IsBase || !base.IsActive || base.Depth != 0 {
		t.Fatalf("invalid base initialization: %+v", base)
	}

	return eng, executor, tmpDir
}

func TestEngine_CreateAndDepthLimit(t *testing.T) {
	eng, _, _ := setupTestEngine(t, `\\?\Volume{12345678-1234-1234-1234-123456789abc}\`)

	currentParent := "rp_base"
	for depth := 1; depth <= 5; depth++ {
		pointID := "rp_step_" + string(rune('0'+depth))
		req := vhdx.CreateRequest{
			PointID:     pointID,
			Name:        "Checkpoint " + pointID,
			Description: "Differential layer",
			ParentID:    &currentParent,
			CreatedBy:   protocol.CreatedByAdminLocal,
		}
		rp, plan, err := eng.Create(req)
		if err != nil {
			t.Fatalf("failed to create restore point at depth %d: %v", depth, err)
		}
		if rp.Depth != depth {
			t.Fatalf("expected depth %d, got %d", depth, rp.Depth)
		}
		if !rp.IsActive {
			t.Fatalf("newly created point must be active")
		}
		if plan == nil || len(plan.Steps) == 0 {
			t.Fatalf("execution plan should not be empty")
		}
		currentParent = rp.PointID
	}

	// Now at depth 5, attempting depth 6 must return RESTORE_POINT_LIMIT
	req6 := vhdx.CreateRequest{
		PointID:     "rp_step_6",
		Name:        "Exceeding Depth",
		Description: "Should fail",
		ParentID:    &currentParent,
		CreatedBy:   protocol.CreatedByAdminLocal,
	}
	_, _, err := eng.Create(req6)
	if err == nil {
		t.Fatalf("expected error when exceeding max depth, got nil")
	}

	var vErr *vhdx.VHDXError
	if !errors.As(err, &vErr) {
		t.Fatalf("expected VHDXError, got %T: %v", err, err)
	}
	if vErr.Code != protocol.ErrRestorePointLimit {
		t.Fatalf("expected error code %s, got %s", protocol.ErrRestorePointLimit, vErr.Code)
	}
}

func TestEngine_DeleteRules(t *testing.T) {
	eng, _, _ := setupTestEngine(t, `\\?\Volume{12345678-1234-1234-1234-123456789abc}\`)

	// 1. Cannot delete base point
	_, err := eng.Delete("rp_base")
	if err == nil {
		t.Fatalf("expected error deleting base point, got nil")
	}
	var vErr *vhdx.VHDXError
	if !errors.As(err, &vErr) || vErr.Code != protocol.ErrInvalidState {
		t.Fatalf("expected INVALID_STATE when deleting base point, got %v", err)
	}

	// Create child 1 (active)
	pBase := "rp_base"
	_, _, err = eng.Create(vhdx.CreateRequest{
		PointID:   "rp_child_1",
		Name:      "Child 1",
		ParentID:  &pBase,
		CreatedBy: protocol.CreatedByAdminLocal,
	})
	if err != nil {
		t.Fatalf("failed to create child 1: %v", err)
	}

	// 2. Cannot delete active point (rp_child_1 is currently active)
	_, err = eng.Delete("rp_child_1")
	if err == nil {
		t.Fatalf("expected error deleting active point, got nil")
	}
	if !errors.As(err, &vErr) || vErr.Code != protocol.ErrInvalidState {
		t.Fatalf("expected INVALID_STATE when deleting active point, got %v", err)
	}

	// Create child 2 (now child 2 is active, child 1 is inactive)
	pChild1 := "rp_child_1"
	_, _, err = eng.Create(vhdx.CreateRequest{
		PointID:   "rp_child_2",
		Name:      "Child 2",
		ParentID:  &pChild1,
		CreatedBy: protocol.CreatedByAdminLocal,
	})
	if err != nil {
		t.Fatalf("failed to create child 2: %v", err)
	}

	// 3. Cannot delete child 1 because child 2 is its child!
	_, err = eng.Delete("rp_child_1")
	if err == nil {
		t.Fatalf("expected error deleting point with children, got nil")
	}
	if !errors.As(err, &vErr) || vErr.Code != protocol.ErrInvalidState {
		t.Fatalf("expected INVALID_STATE when deleting point with children, got %v", err)
	}

	// Create a sibling of child 2 under child 1 so child 2 becomes a non-active leaf.
	_, _, err = eng.Create(vhdx.CreateRequest{
		PointID:   "rp_child_2b",
		Name:      "Child 2b",
		ParentID:  &pChild1,
		CreatedBy: protocol.CreatedByAdminLocal,
	})
	if err != nil {
		t.Fatalf("failed to create sibling child 2b: %v", err)
	}

	// 4. Now child 2 is a non-active leaf, delete must succeed
	deletePlan, err := eng.Delete("rp_child_2")
	if err != nil {
		t.Fatalf("failed to delete leaf point child 2: %v", err)
	}
	if deletePlan == nil || deletePlan.Action != "DELETE" {
		t.Fatalf("invalid delete plan: %+v", deletePlan)
	}

	// Confirm rp_child_2 is gone
	if eng.Get("rp_child_2") != nil {
		t.Fatalf("deleted point rp_child_2 should not exist in chain")
	}
}

func TestEngine_SwitchPoint(t *testing.T) {
	eng, _, _ := setupTestEngine(t, `\\?\Volume{12345678-1234-1234-1234-123456789abc}\`)

	pBase := "rp_base"
	_, _, err := eng.Create(vhdx.CreateRequest{
		PointID:   "rp_child_1",
		Name:      "Child 1",
		ParentID:  &pBase,
		CreatedBy: protocol.CreatedByAdminLocal,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// 建立同層兄弟節點，讓 child_2 成為 active、child_1 成為非 active 的葉節點
	_, _, err = eng.Create(vhdx.CreateRequest{
		PointID:   "rp_child_2",
		Name:      "Child 2",
		ParentID:  &pBase,
		CreatedBy: protocol.CreatedByAdminLocal,
	})
	if err != nil {
		t.Fatalf("create sibling failed: %v", err)
	}

	// Non-existent point
	_, err = eng.Switch("rp_not_exist")
	if err == nil {
		t.Fatalf("expected error switching to non-existent point")
	}
	var vErr *vhdx.VHDXError
	if !errors.As(err, &vErr) || vErr.Code != protocol.ErrNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}

	// 切換到仍有子節點的 base 必須被拒絕（避免污染子節點快照）
	_, err = eng.Switch("rp_base")
	if err == nil {
		t.Fatalf("expected error switching to point with children, got nil")
	}
	if !errors.As(err, &vErr) || vErr.Code != protocol.ErrInvalidState {
		t.Fatalf("expected INVALID_STATE when switching to point with children, got %v", err)
	}

	// Switch to leaf rp_child_1
	plan, err := eng.Switch("rp_child_1")
	if err != nil {
		t.Fatalf("failed to switch to rp_child_1: %v", err)
	}
	if plan.Action != "SWITCH" {
		t.Fatalf("expected action SWITCH, got %s", plan.Action)
	}

	active := eng.GetActive()
	if active == nil || active.PointID != "rp_child_1" {
		t.Fatalf("expected active point to be rp_child_1, got %+v", active)
	}
	child2 := eng.Get("rp_child_2")
	if child2.IsActive {
		t.Fatalf("rp_child_2 should not be active after switch")
	}
}

func TestEngine_DryRunBatchScriptGolden(t *testing.T) {
	eng, executor, _ := setupTestEngine(t, `\\?\Volume{11111111-2222-3333-4444-555555555555}\`)

	pBase := "rp_base"
	req := vhdx.CreateRequest{
		PointID:     "rp_golden_test",
		Name:        "Golden Checkpoint",
		Description: "Deterministic script verification",
		ParentID:    &pBase,
		CreatedBy:   protocol.CreatedByAdminRemote,
	}

	_, plan, err := eng.Create(req)
	if err != nil {
		t.Fatalf("failed to create restore point: %v", err)
	}

	script := plan.BatchScript()

	// Verify essential markers in DiskpartScript
	expectedDiskpart := `create vdisk file="\\?\Volume{11111111-2222-3333-4444-555555555555}\EZRestore\diff_golden_test.vhdx" parent="\\?\Volume{11111111-2222-3333-4444-555555555555}\EZRestore\base.vhdx"`
	if plan.DiskpartScript != expectedDiskpart {
		t.Fatalf("expected DiskpartScript %q, got %q", expectedDiskpart, plan.DiskpartScript)
	}

	// Verify essential markers in the generated batch script
	expectedSubstrings := []string{
		`rem back-EZ VHDX Execution Plan: CREATE (Target: rp_golden_test)`,
		`bcdedit /export "\\?\Volume{11111111-2222-3333-4444-555555555555}\EZRestore\bcd_backup_rp_golden_test.bcd"`,
		`diskpart /s "create_diff_rp_golden_test.txt"`,
		`bcdedit /set {default} device vhd=[\\?\Volume{11111111-2222-3333-4444-555555555555}]\EZRestore\diff_golden_test.vhdx`,
		`bcdedit /set {default} osdevice vhd=[\\?\Volume{11111111-2222-3333-4444-555555555555}]\EZRestore\diff_golden_test.vhdx`,
		`bcdedit /import "\\?\Volume{11111111-2222-3333-4444-555555555555}\EZRestore\bcd_backup_rp_golden_test.bcd"`,
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(script, sub) {
			t.Errorf("expected batch script to contain %q, but did not.\nFull script:\n%s", sub, script)
		}
	}

	// Verify executor recorded execution
	if len(executor.ExecutedPlans) != 1 {
		t.Fatalf("expected 1 executed plan in DryRunExecutor, got %d", len(executor.ExecutedPlans))
	}
}

func TestEngine_RollbackOnFailure(t *testing.T) {
	mockExec := vhdx.NewMockExecutor()
	mockExec.FailStep = 3 // Step 3 is BCD device update

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "base.vhdx"), []byte("vhdx"), 0644); err != nil {
		t.Fatalf("failed to create fake base.vhdx: %v", err)
	}

	cfg := vhdx.Config{
		DryRun:               true,
		AllowDestructive:     true,
		RealExecutionEnabled: false,
		VolumeGUID:           `\\?\Volume{11111111-2222-3333-4444-555555555555}\`,
		ReserveRoot:          "EZRestore",
		ReserveRelPath:       "EZRestore",
		BCDEntryID:           "{current}",
		DataDir:              tmpDir,
	}

	eng, err := vhdx.NewEngine(cfg, mockExec)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	_, _ = eng.InitBase("Base", "Base desc", "base.vhdx", 100)

	pBase := "rp_base"
	_, _, err = eng.Create(vhdx.CreateRequest{
		PointID:   "rp_fail_test",
		Name:      "Will fail at step 3",
		ParentID:  &pBase,
		CreatedBy: protocol.CreatedByAdminLocal,
	})
	if err == nil {
		t.Fatalf("expected failure at step 3, got nil")
	}

	// Ensure rollback log was registered
	if len(mockExec.RollbackLogs) == 0 {
		t.Fatalf("expected rollback action to be recorded when BCD update fails")
	}
	if !strings.Contains(mockExec.RollbackLogs[0], "bcdedit /import") {
		t.Fatalf("rollback should run bcdedit /import, got: %v", mockExec.RollbackLogs)
	}
}

func TestEngine_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := vhdx.Config{
		DryRun:         true,
		VolumeGUID:     `\\?\Volume{12345678-1234-1234-1234-123456789abc}\`,
		ReserveRoot:    "EZRestore",
		ReserveRelPath: "EZRestore",
		DataDir:        tmpDir,
	}

	eng1, err := vhdx.NewEngine(cfg, nil)
	if err != nil {
		t.Fatalf("eng1 creation failed: %v", err)
	}
	_, _ = eng1.InitBase("Base", "Base OS", "base.vhdx", 100)

	pBase := "rp_base"
	_, _, err = eng1.Create(vhdx.CreateRequest{
		PointID:   "rp_persisted",
		Name:      "Persisted Point",
		ParentID:  &pBase,
		CreatedBy: protocol.CreatedByAdminLocal,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Confirm chain.json exists
	metaFile := filepath.Join(tmpDir, "chain.json")
	if _, err := os.Stat(metaFile); err != nil {
		t.Fatalf("chain.json not found on disk: %v", err)
	}

	// Create second engine instance loading from same directory
	eng2, err := vhdx.NewEngine(cfg, nil)
	if err != nil {
		t.Fatalf("eng2 creation failed: %v", err)
	}

	list := eng2.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 restore points in eng2, got %d", len(list))
	}
	p := eng2.Get("rp_persisted")
	if p == nil || !p.IsActive || p.Depth != 1 {
		t.Fatalf("persisted point not recovered properly: %+v", p)
	}
}

func TestEngine_ResetAfterDeploy(t *testing.T) {
	eng, _, _ := setupTestEngine(t, `\\?\Volume{12345678-1234-1234-1234-123456789abc}\`)

	// Create 2 child points
	pBase := "rp_base"
	_, _, err := eng.Create(vhdx.CreateRequest{
		PointID:   "rp_child_1",
		Name:      "Child 1",
		ParentID:  &pBase,
		CreatedBy: protocol.CreatedByAdminLocal,
	})
	if err != nil {
		t.Fatalf("create child 1 failed: %v", err)
	}

	pChild1 := "rp_child_1"
	_, _, err = eng.Create(vhdx.CreateRequest{
		PointID:   "rp_child_2",
		Name:      "Child 2",
		ParentID:  &pChild1,
		CreatedBy: protocol.CreatedByAdminLocal,
	})
	if err != nil {
		t.Fatalf("create child 2 failed: %v", err)
	}

	// Verify we now have 3 points
	if len(eng.List()) != 3 {
		t.Fatalf("expected 3 points, got %d", len(eng.List()))
	}

	// Execute ResetAfterDeploy (Decision D8)
	newBase, plan, err := eng.ResetAfterDeploy("派送基準點", "測試派送重設", "base.vhdx", 2048)
	if err != nil {
		t.Fatalf("ResetAfterDeploy failed: %v", err)
	}

	if plan.Action != "RESET_AFTER_DEPLOY" {
		t.Fatalf("expected action RESET_AFTER_DEPLOY, got %s", plan.Action)
	}

	// Old diff files must be planned for deletion
	foundDel := false
	for _, s := range plan.Steps {
		if s.Category == "FILE" && strings.Contains(s.Command, "diff_child") {
			foundDel = true
			break
		}
	}
	if !foundDel {
		t.Fatalf("expected plan steps to contain diff cleanup commands")
	}

	// Chain must now contain ONLY the single new base point
	points := eng.List()
	if len(points) != 1 {
		t.Fatalf("expected exactly 1 point after reset, got %d", len(points))
	}
	if !points[0].IsBase || !points[0].IsActive || points[0].Depth != 0 {
		t.Fatalf("invalid new base state: %+v", points[0])
	}
	if points[0].CreatedBy != protocol.CreatedBySystem {
		t.Fatalf("expected CreatedBy SYSTEM, got %s", points[0].CreatedBy)
	}
	if points[0].PointID != newBase.PointID {
		t.Fatalf("expected point ID %s, got %s", newBase.PointID, points[0].PointID)
	}
}

func TestEngine_LegacyArrayMetadataMigration(t *testing.T) {
	tmpDir := t.TempDir()
	legacy := `[{"point_id":"rp_base","name":"Base System","is_base":true,"is_active":true,"state":"READY"}]`
	metaFile := filepath.Join(tmpDir, "chain.json")
	if err := os.WriteFile(metaFile, []byte(legacy), 0644); err != nil {
		t.Fatalf("failed to write legacy chain.json: %v", err)
	}

	cfg := vhdx.Config{DryRun: true, ReserveRoot: "EZRestore", DataDir: tmpDir}
	eng, err := vhdx.NewEngine(cfg, nil)
	if err != nil {
		t.Fatalf("NewEngine must tolerate legacy array metadata, got: %v", err)
	}
	if len(eng.List()) != 0 {
		t.Fatalf("legacy chain should start empty, got %d points", len(eng.List()))
	}
	if eng.LoadWarning() == "" {
		t.Fatalf("expected a load warning describing the legacy migration")
	}

	backups, _ := filepath.Glob(metaFile + ".legacy-*")
	if len(backups) != 1 {
		t.Fatalf("expected exactly one legacy backup, got %v", backups)
	}

	// 第二次啟動不應再遇到 legacy 格式，也不應重複備份。
	eng2, err := vhdx.NewEngine(cfg, nil)
	if err != nil {
		t.Fatalf("second NewEngine failed: %v", err)
	}
	if strings.Contains(eng2.LoadWarning(), "舊版") {
		t.Fatalf("legacy migration must only happen once, warning=%q", eng2.LoadWarning())
	}
	backups2, _ := filepath.Glob(metaFile + ".legacy-*")
	if len(backups2) != 1 {
		t.Fatalf("expected legacy backup count to stay 1, got %v", backups2)
	}
}

func TestEngine_EnsureBaseRequiresProvisionedDisk(t *testing.T) {
	tmpDir := t.TempDir()
	eng, err := vhdx.NewEngine(vhdx.Config{DryRun: true, ReserveRoot: "EZRestore", DataDir: tmpDir}, nil)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	_, err = eng.EnsureBase("", "")
	if err == nil {
		t.Fatalf("expected EnsureBase to fail without base.vhdx")
	}
	var vErr *vhdx.VHDXError
	if !errors.As(err, &vErr) || vErr.Code != protocol.ErrCapabilityUnsupported {
		t.Fatalf("expected CAPABILITY_UNSUPPORTED, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "base.vhdx"), []byte("vhdx"), 0644); err != nil {
		t.Fatalf("failed to create fake base.vhdx: %v", err)
	}
	base, err := eng.EnsureBase("", "")
	if err != nil {
		t.Fatalf("EnsureBase failed with base.vhdx present: %v", err)
	}
	if base == nil || !base.IsBase || base.Depth != 0 || base.SizeBytes != 4 {
		t.Fatalf("invalid base point: %+v", base)
	}
	if active := eng.GetActive(); active == nil || active.PointID != base.PointID {
		t.Fatalf("base point should be active, got %+v", active)
	}
}

func TestEngine_RealExecutionFailClosed(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "base.vhdx"), []byte("vhdx"), 0644); err != nil {
		t.Fatalf("failed to create fake base.vhdx: %v", err)
	}

	mockExec := vhdx.NewMockExecutor()
	cfg := vhdx.Config{
		DryRun:               false,
		AllowDestructive:     true,
		RealExecutionEnabled: false, // A 案：一律 fail-closed
		VolumeGUID:           `\\?\Volume{11111111-2222-3333-4444-555555555555}\`,
		ReserveRoot:          "EZRestore",
		ReserveRelPath:       "EZRestore",
		BCDEntryID:           "{current}",
		DataDir:              tmpDir,
	}
	eng, err := vhdx.NewEngine(cfg, mockExec)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	if _, err := eng.InitBase("Base", "Base", "base.vhdx", 4); err != nil {
		t.Fatalf("InitBase failed: %v", err)
	}

	pBase := "rp_base"
	_, _, err = eng.Create(vhdx.CreateRequest{
		PointID:   "rp_blocked",
		Name:      "Blocked",
		ParentID:  &pBase,
		CreatedBy: protocol.CreatedByAdminLocal,
	})
	if err == nil {
		t.Fatalf("expected real execution to be refused when RealExecutionEnabled=false")
	}
	var vErr *vhdx.VHDXError
	if !errors.As(err, &vErr) || vErr.Code != protocol.ErrCapabilityUnsupported {
		t.Fatalf("expected CAPABILITY_UNSUPPORTED, got %v", err)
	}
	if len(mockExec.ExecutedLogs) != 0 {
		t.Fatalf("executor must not run while real execution is disabled, got %v", mockExec.ExecutedLogs)
	}
}

func TestEngine_DeleteValidatesBeforeExecution(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "base.vhdx"), []byte("vhdx"), 0644); err != nil {
		t.Fatalf("failed to create fake base.vhdx: %v", err)
	}

	mockExec := vhdx.NewMockExecutor()
	cfg := vhdx.Config{
		DryRun:               false,
		AllowDestructive:     true,
		RealExecutionEnabled: false,
		VolumeGUID:           `\\?\Volume{11111111-2222-3333-4444-555555555555}\`,
		ReserveRoot:          "EZRestore",
		ReserveRelPath:       "EZRestore",
		BCDEntryID:           "{current}",
		DataDir:              tmpDir,
	}
	eng, err := vhdx.NewEngine(cfg, mockExec)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	if _, err := eng.InitBase("Base", "Base", "base.vhdx", 4); err != nil {
		t.Fatalf("InitBase failed: %v", err)
	}

	_, err = eng.Delete("rp_base")
	var vErr *vhdx.VHDXError
	if !errors.As(err, &vErr) || vErr.Code != protocol.ErrInvalidState {
		t.Fatalf("expected INVALID_STATE when deleting base, got %v", err)
	}
	if len(mockExec.ExecutedLogs) != 0 {
		t.Fatalf("executor must not run for rejected delete, got %v", mockExec.ExecutedLogs)
	}
}

func TestEngine_SwitchAndDeleteFailClosedWhenRealExecutionDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "base.vhdx"), []byte("vhdx"), 0644); err != nil {
		t.Fatalf("failed to create fake base.vhdx: %v", err)
	}

	baseID := "rp_base"
	seed := map[string]*protocol.RestorePoint{
		"rp_base": {
			PointID: "rp_base", Name: "Base", VHDXFile: "base.vhdx",
			IsBase: true, IsActive: true, State: protocol.RestorePointStateReady, Depth: 0,
		},
		"rp_child": {
			PointID: "rp_child", Name: "Child", VHDXFile: "diff_child.vhdx",
			ParentID: &baseID, IsActive: false, State: protocol.RestorePointStateReady, Depth: 1,
		},
	}
	data, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("failed to marshal seed chain: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "chain.json"), data, 0644); err != nil {
		t.Fatalf("failed to write seed chain: %v", err)
	}

	mockExec := vhdx.NewMockExecutor()
	cfg := vhdx.Config{
		DryRun:               false,
		AllowDestructive:     true,
		RealExecutionEnabled: false,
		VolumeGUID:           `\\?\Volume{11111111-2222-3333-4444-555555555555}\`,
		ReserveRoot:          "EZRestore",
		ReserveRelPath:       "EZRestore",
		BCDEntryID:           "{current}",
		DataDir:              tmpDir,
	}
	eng, err := vhdx.NewEngine(cfg, mockExec)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	var vErr *vhdx.VHDXError
	if _, err := eng.Switch("rp_child"); err == nil || !errors.As(err, &vErr) || vErr.Code != protocol.ErrCapabilityUnsupported {
		t.Fatalf("expected SWITCH to be refused with CAPABILITY_UNSUPPORTED, got %v", err)
	}
	if _, err := eng.Delete("rp_child"); err == nil || !errors.As(err, &vErr) || vErr.Code != protocol.ErrCapabilityUnsupported {
		t.Fatalf("expected DELETE to be refused with CAPABILITY_UNSUPPORTED, got %v", err)
	}
	if len(mockExec.ExecutedLogs) != 0 {
		t.Fatalf("executor must not run while real execution is disabled, got %v", mockExec.ExecutedLogs)
	}
}
