package vhdx_test

import (
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

	// Switch active back to rp_base so child 2 becomes non-active leaf
	_, err = eng.Switch("rp_base")
	if err != nil {
		t.Fatalf("failed to switch back to base: %v", err)
	}

	// 4. Now child 2 is non-active leaf, delete must succeed
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

	// Non-existent point
	_, err = eng.Switch("rp_not_exist")
	if err == nil {
		t.Fatalf("expected error switching to non-existent point")
	}
	var vErr *vhdx.VHDXError
	if !errors.As(err, &vErr) || vErr.Code != protocol.ErrNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}

	// Switch to rp_base
	plan, err := eng.Switch("rp_base")
	if err != nil {
		t.Fatalf("failed to switch to rp_base: %v", err)
	}
	if plan.Action != "SWITCH" {
		t.Fatalf("expected action SWITCH, got %s", plan.Action)
	}

	active := eng.GetActive()
	if active == nil || active.PointID != "rp_base" {
		t.Fatalf("expected active point to be rp_base, got %+v", active)
	}
	child1 := eng.Get("rp_child_1")
	if child1.IsActive {
		t.Fatalf("rp_child_1 should not be active after switch")
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

	cfg := vhdx.Config{
		DryRun:           false,
		AllowDestructive: true,
		VolumeGUID:       `\\?\Volume{11111111-2222-3333-4444-555555555555}\`,
		ReserveRoot:      "EZRestore",
		BCDEntryID:       "{default}",
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
		DryRun:      true,
		ReserveRoot: "EZRestore",
		DataDir:     tmpDir,
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
