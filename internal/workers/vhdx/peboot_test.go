package vhdx_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owner/back-ez/internal/workers/vhdx"
)

func TestPEBoot_CheckPEReady(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := vhdx.PEBootConfig{
		DryRun:           true,
		ReserveRoot:      "EZRestore",
		PEWIMRelPath:     filepath.Join("PE", "boot.wim"),
		PhysicalDiskRoot: tmpDir,
	}
	manager := vhdx.NewPEBootManager(cfg, nil)

	// 1. Initial state: boot.wim not yet created -> pe_ready = false
	ready, reason := manager.CheckPEReady()
	if ready {
		t.Fatalf("expected pe_ready to be false when boot.wim missing, got true")
	}
	if !strings.Contains(reason, "not found") {
		t.Fatalf("unexpected reason: %s", reason)
	}

	// 2. Create mock boot.wim -> pe_ready = true
	peDir := filepath.Join(tmpDir, "PE")
	if err := os.MkdirAll(peDir, 0755); err != nil {
		t.Fatalf("failed to create PE dir: %v", err)
	}
	wimPath := filepath.Join(peDir, "boot.wim")
	if err := os.WriteFile(wimPath, []byte("MOCK_WIM_DATA"), 0644); err != nil {
		t.Fatalf("failed to write mock boot.wim: %v", err)
	}

	ready, _ = manager.CheckPEReady()
	if !ready {
		t.Fatalf("expected pe_ready to be true after boot.wim created")
	}
}

func TestPEBoot_SetupPEBootEntryGolden(t *testing.T) {
	dryExec := vhdx.NewDryRunExecutor()
	cfg := vhdx.PEBootConfig{
		DryRun:       true,
		VolumeGUID:   `\\?\Volume{aaaa1111-bb22-cc33-dd44-eeee55556666}\`,
		ReserveRoot:  "EZRestore",
		PEWIMRelPath: filepath.Join("PE", "boot.wim"),
		PESDIRelPath: filepath.Join("boot", "boot.sdi"),
		PEGUID:       "{7619d4e3-6532-4347-8a64-00e08174acbc}",
	}
	manager := vhdx.NewPEBootManager(cfg, dryExec)

	plan, err := manager.SetupPEBootEntry()
	if err != nil {
		t.Fatalf("failed to setup PE boot entry: %v", err)
	}

	if plan.Action != "SETUP_PE_ENTRY" {
		t.Fatalf("expected action SETUP_PE_ENTRY, got %s", plan.Action)
	}

	script := plan.BatchScript()

	expectedSubstrings := []string{
		`bcdedit /create {ramdiskoptions} /d "back-EZ WinPE Ramdisk"`,
		`bcdedit /set {ramdiskoptions} ramdisksdidevice partition=[\\?\Volume{aaaa1111-bb22-cc33-dd44-eeee55556666}]\EZRestore\boot\boot.sdi`,
		`bcdedit /set {ramdiskoptions} ramdisksdipath \boot\boot.sdi`,
		`bcdedit /create {7619d4e3-6532-4347-8a64-00e08174acbc} /d "back-EZ WinPE Recovery" /application osloader`,
		`bcdedit /set {7619d4e3-6532-4347-8a64-00e08174acbc} device ramdisk=[\\?\Volume{aaaa1111-bb22-cc33-dd44-eeee55556666}]\EZRestore\PE\boot.wim,{ramdiskoptions}`,
		`bcdedit /set {7619d4e3-6532-4347-8a64-00e08174acbc} osdevice ramdisk=[\\?\Volume{aaaa1111-bb22-cc33-dd44-eeee55556666}]\EZRestore\PE\boot.wim,{ramdiskoptions}`,
		`bcdedit /set {7619d4e3-6532-4347-8a64-00e08174acbc} path \windows\system32\boot\winload.efi`,
		`bcdedit /set {7619d4e3-6532-4347-8a64-00e08174acbc} winpe yes`,
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(script, sub) {
			t.Errorf("expected script to contain %q, but did not.\nScript:\n%s", sub, script)
		}
	}
}

func TestPEBoot_TriggerOneTimeBootAndRollback(t *testing.T) {
	mockExec := vhdx.NewMockExecutor()
	mockExec.FailStep = 2 // Step 2 is bootsequence

	cfg := vhdx.PEBootConfig{
		DryRun:           false,
		AllowDestructive: true,
		VolumeGUID:       `\\?\Volume{aaaa1111-bb22-cc33-dd44-eeee55556666}\`,
		ReserveRoot:      "EZRestore",
		PEGUID:           "{7619d4e3-6532-4347-8a64-00e08174acbc}",
	}
	manager := vhdx.NewPEBootManager(cfg, mockExec)

	plan, err := manager.TriggerOneTimePEBoot()
	if err == nil {
		t.Fatalf("expected error on step 2 failure, got nil")
	}

	if plan == nil || plan.Action != "ONETIME_PE_BOOT" {
		t.Fatalf("invalid plan: %+v", plan)
	}

	// Verify rollback occurred
	if len(mockExec.RollbackLogs) == 0 {
		t.Fatalf("expected BCD rollback to be executed on failure")
	}
	if !strings.Contains(mockExec.RollbackLogs[0], "bcdedit /import") {
		t.Fatalf("rollback should run bcdedit /import, got: %s", mockExec.RollbackLogs[0])
	}
}

