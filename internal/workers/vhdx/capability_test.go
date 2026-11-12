package vhdx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeFile describes a fake filesystem entry seen by fakeProbe.Stat.
type fakeFile struct {
	isDir bool
}

// fakeProbe is a fully in-memory capabilityProbe. Tests never touch the real
// registry, BCD, disk or DLLs.
type fakeProbe struct {
	editionID  string
	build      string
	versionErr error

	admin    bool
	adminErr error

	bcdOut string
	bcdErr error

	files map[string]fakeFile

	volume    string
	volumeErr error

	free    uint64
	freeErr error

	hiberboot    uint32
	hiberbootSet bool
	hiberbootErr error

	bitLockerOut string
	bitLockerErr error

	virtOK     bool
	virtDetail string

	writableErr error
}

func (f *fakeProbe) CurrentVersion() (string, string, error) {
	return f.editionID, f.build, f.versionErr
}

func (f *fakeProbe) IsAdmin() (bool, error) { return f.admin, f.adminErr }

func (f *fakeProbe) BCDOutput() (string, error) { return f.bcdOut, f.bcdErr }

func (f *fakeProbe) Stat(path string) (bool, bool, error) {
	file, ok := f.files[path]
	if !ok {
		return false, false, nil
	}
	return true, file.isDir, nil
}

func (f *fakeProbe) VolumeNameForMountPoint(string) (string, error) {
	return f.volume, f.volumeErr
}

func (f *fakeProbe) FreeBytes(string) (uint64, error) { return f.free, f.freeErr }

func (f *fakeProbe) HiberbootEnabled() (uint32, bool, error) {
	return f.hiberboot, f.hiberbootSet, f.hiberbootErr
}

func (f *fakeProbe) BitLockerStatus() (string, error) {
	return f.bitLockerOut, f.bitLockerErr
}

func (f *fakeProbe) VirtualDiskAPI() (bool, string) { return f.virtOK, f.virtDetail }

func (f *fakeProbe) Writable(string) error { return f.writableErr }

const (
	testReserveRoot = `D:\EZRestore`
	testVolumeGUID  = `\\?\Volume{11111111-2222-3333-4444-555555555555}\`
)

// allOKProbe returns a probe for which every one of the 12 checks passes.
func allOKProbe() *fakeProbe {
	return &fakeProbe{
		editionID: "Professional",
		build:     "19045",
		admin:     true,
		bcdOut: "Windows Boot Loader\n-------------------\n" +
			"identifier              {current}\n" +
			`device                  vhd=[D:]\EZRestore\base.vhdx` + "\n" +
			`osdevice                vhd=[D:]\EZRestore\base.vhdx` + "\n",
		files: map[string]fakeFile{
			filepath.Join(testReserveRoot, "base.vhdx"):      {},
			filepath.Join(testReserveRoot, "PE", "boot.wim"): {},
		},
		volume:       testVolumeGUID,
		free:         100 << 30,
		hiberboot:    0,
		hiberbootSet: true,
		bitLockerOut: "Protection Status: Protection Off",
		virtOK:       true,
		virtDetail:   "virtdisk.dll 已載入，且 CreateVirtualDisk／OpenVirtualDisk 可解析",
	}
}

// withRealExecution temporarily flips the A-plan gate so required checks can all
// pass; it restores the previous value afterwards.
func withRealExecution(t *testing.T, enabled bool) {
	t.Helper()
	previous := RealExecutionImplemented
	RealExecutionImplemented = enabled
	t.Cleanup(func() { RealExecutionImplemented = previous })
}

func itemByKey(t *testing.T, caps Capabilities, key string) Capability {
	t.Helper()
	for _, item := range caps.Items {
		if item.Key == key {
			return item
		}
	}
	t.Fatalf("capability item %q not found", key)
	return Capability{}
}

func isBlocker(caps Capabilities, key string) bool {
	for _, blocker := range caps.Blockers {
		if blocker.Key == key {
			return true
		}
	}
	return false
}

func TestDetectWithProbe_AllOK(t *testing.T) {
	withRealExecution(t, true)

	caps := detectWithProbe(allOKProbe(), testReserveRoot)

	if !caps.Ready {
		t.Fatalf("expected Ready=true, blockers=%+v", caps.Blockers)
	}
	if len(caps.Items) != 12 {
		t.Fatalf("expected 12 items, got %d", len(caps.Items))
	}
	if len(caps.Blockers) != 0 {
		t.Fatalf("expected 0 blockers, got %+v", caps.Blockers)
	}
	if caps.Error != "" {
		t.Fatalf("expected empty Error, got %q", caps.Error)
	}
	if caps.ReserveRoot != testReserveRoot {
		t.Fatalf("ReserveRoot = %q, want %q", caps.ReserveRoot, testReserveRoot)
	}
	if caps.ReserveVolume != testVolumeGUID {
		t.Fatalf("ReserveVolume = %q, want %q", caps.ReserveVolume, testVolumeGUID)
	}
	if caps.ReserveRelPath != "EZRestore" {
		t.Fatalf("ReserveRelPath = %q, want %q", caps.ReserveRelPath, "EZRestore")
	}
	if caps.Edition != "Professional" {
		t.Fatalf("Edition = %q, want Professional", caps.Edition)
	}
	if caps.Build != "19045" {
		t.Fatalf("Build = %q, want 19045", caps.Build)
	}
	if !caps.IsAdmin {
		t.Fatal("expected IsAdmin=true")
	}
	if _, err := time.Parse(time.RFC3339, caps.CheckedAt); err != nil {
		t.Fatalf("CheckedAt %q is not RFC3339: %v", caps.CheckedAt, err)
	}

	expectedOrder := []string{
		"os_edition_supported",
		"admin_token",
		"vhdx_native_boot",
		"base_vhdx_present",
		"reserve_volume_resolved",
		"reserve_writable",
		"reserve_space",
		"fast_startup_disabled",
		"bitlocker_disabled",
		"virtdisk_api",
		"pe_image_present",
		"real_execution_enabled",
	}
	for i, key := range expectedOrder {
		if caps.Items[i].Key != key {
			t.Fatalf("items[%d].Key = %q, want %q", i, caps.Items[i].Key, key)
		}
		if !caps.Items[i].OK {
			t.Fatalf("item %q expected OK=true, got detail=%q fix=%q", key, caps.Items[i].Detail, caps.Items[i].Fix)
		}
	}

	required := []string{
		"os_edition_supported",
		"admin_token",
		"vhdx_native_boot",
		"base_vhdx_present",
		"reserve_volume_resolved",
		"reserve_writable",
		"reserve_space",
		"fast_startup_disabled",
		"real_execution_enabled",
	}
	for _, key := range required {
		if item := itemByKey(t, caps, key); !item.Required {
			t.Fatalf("item %q should be Required", key)
		}
	}
	for _, key := range []string{"bitlocker_disabled", "virtdisk_api", "pe_image_present"} {
		if item := itemByKey(t, caps, key); item.Required {
			t.Fatalf("item %q should not be Required", key)
		}
	}
}

func TestDetectWithProbe_EachRequiredFailureBlocks(t *testing.T) {
	requiredKeys := []string{
		"os_edition_supported",
		"admin_token",
		"vhdx_native_boot",
		"base_vhdx_present",
		"reserve_volume_resolved",
		"reserve_writable",
		"reserve_space",
		"fast_startup_disabled",
		"real_execution_enabled",
	}

	for _, key := range requiredKeys {
		t.Run(key, func(t *testing.T) {
			withRealExecution(t, true)
			probe := allOKProbe()

			switch key {
			case "os_edition_supported":
				probe.editionID = "Core"
			case "admin_token":
				probe.admin = false
			case "vhdx_native_boot":
				probe.bcdOut = "Windows Boot Loader\nidentifier {current}\ndevice partition=C:\n"
			case "base_vhdx_present":
				delete(probe.files, filepath.Join(testReserveRoot, "base.vhdx"))
			case "reserve_volume_resolved":
				probe.volume = ""
				probe.volumeErr = errors.New("volume lookup failed")
			case "reserve_writable":
				probe.writableErr = errors.New("access is denied")
			case "reserve_space":
				probe.free = 5 << 30
			case "fast_startup_disabled":
				probe.hiberboot = 1
				probe.hiberbootSet = true
			case "real_execution_enabled":
				withRealExecution(t, false)
			}

			caps := detectWithProbe(probe, testReserveRoot)
			if caps.Ready {
				t.Fatalf("expected Ready=false when %q fails", key)
			}
			if !isBlocker(caps, key) {
				t.Fatalf("expected %q in blockers, got %+v", key, caps.Blockers)
			}
			item := itemByKey(t, caps, key)
			if item.OK {
				t.Fatalf("expected item %q OK=false", key)
			}
			if !item.Required {
				t.Fatalf("expected item %q Required=true", key)
			}
			if strings.TrimSpace(item.Detail) == "" {
				t.Fatalf("expected non-empty Detail for failing %q", key)
			}
			if strings.TrimSpace(item.Fix) == "" {
				t.Fatalf("expected non-empty Fix for failing %q", key)
			}
			for _, blocker := range caps.Blockers {
				if !blocker.Required {
					t.Fatalf("blocker %q must be Required", blocker.Key)
				}
			}
		})
	}
}

func TestDetectWithProbe_SimulatedCurrentMachine(t *testing.T) {
	withRealExecution(t, false)

	probe := allOKProbe()
	probe.editionID = "Professional"
	probe.build = "19045"
	probe.admin = true // elevated is irrelevant for this scenario; volume resolves fine either way
	probe.bcdOut = "Windows Boot Loader\nidentifier {current}\ndevice partition=C:\n"
	delete(probe.files, filepath.Join(testReserveRoot, "base.vhdx"))
	probe.free = 120 << 30
	probe.hiberboot = 1
	probe.hiberbootSet = true

	caps := detectWithProbe(probe, testReserveRoot)
	if caps.Ready {
		t.Fatal("simulated machine must not be Ready")
	}
	for _, key := range []string{
		"vhdx_native_boot",
		"base_vhdx_present",
		"fast_startup_disabled",
		"real_execution_enabled",
	} {
		if !isBlocker(caps, key) {
			t.Fatalf("expected %q in blockers, got %+v", key, caps.Blockers)
		}
	}
	if item := itemByKey(t, caps, "os_edition_supported"); !item.OK {
		t.Fatalf("Windows Pro must pass os_edition_supported, got %+v", item)
	}
	for _, blocker := range caps.Blockers {
		if !blocker.Required {
			t.Fatalf("blocker %q must be Required", blocker.Key)
		}
	}
}

func TestDetectWithProbe_HomeEditionUnsupported(t *testing.T) {
	withRealExecution(t, true)

	probe := allOKProbe()
	probe.editionID = "Core"
	caps := detectWithProbe(probe, testReserveRoot)

	item := itemByKey(t, caps, "os_edition_supported")
	if item.OK {
		t.Fatal("Home edition must not pass os_edition_supported")
	}
	if !strings.Contains(item.Fix, "changepk") {
		t.Fatalf("Fix should warn against changepk, got %q", item.Fix)
	}
	if caps.Ready {
		t.Fatal("Home edition must not be Ready")
	}
	if !isBlocker(caps, "os_edition_supported") {
		t.Fatalf("expected os_edition_supported in blockers, got %+v", caps.Blockers)
	}
}

func TestDetectWithProbe_OptionalFailuresDoNotBlock(t *testing.T) {
	withRealExecution(t, true)

	probe := allOKProbe()
	probe.bitLockerOut = "Protection Status: Protection On"
	probe.virtOK = false
	probe.virtDetail = ""
	delete(probe.files, filepath.Join(testReserveRoot, "PE", "boot.wim"))

	caps := detectWithProbe(probe, testReserveRoot)
	if !caps.Ready {
		t.Fatalf("optional failures must not block, blockers=%+v", caps.Blockers)
	}
	if len(caps.Blockers) != 0 {
		t.Fatalf("expected 0 blockers, got %+v", caps.Blockers)
	}
	for _, key := range []string{"bitlocker_disabled", "virtdisk_api", "pe_image_present"} {
		item := itemByKey(t, caps, key)
		if item.OK {
			t.Fatalf("expected optional item %q OK=false", key)
		}
		if item.Required {
			t.Fatalf("optional item %q must not be Required", key)
		}
		if strings.TrimSpace(item.Detail) == "" || strings.TrimSpace(item.Fix) == "" {
			t.Fatalf("optional failing item %q needs Detail and Fix", key)
		}
	}
}

func TestDetectWithProbe_NonAdminBitLockerNeedsAdmin(t *testing.T) {
	withRealExecution(t, true)

	probe := allOKProbe()
	probe.admin = false
	caps := detectWithProbe(probe, testReserveRoot)

	item := itemByKey(t, caps, "bitlocker_disabled")
	if item.OK {
		t.Fatal("non-admin cannot confirm bitlocker state")
	}
	if !strings.Contains(item.Detail, "需管理員權限確認") {
		t.Fatalf("Detail = %q, want admin-required message", item.Detail)
	}
	if !isBlocker(caps, "admin_token") {
		t.Fatalf("expected admin_token in blockers, got %+v", caps.Blockers)
	}
}

func TestDetectWithProbe_MissingReserveDirFix(t *testing.T) {
	withRealExecution(t, true)

	probe := allOKProbe()
	probe.writableErr = os.ErrNotExist
	caps := detectWithProbe(probe, testReserveRoot)

	item := itemByKey(t, caps, "reserve_writable")
	if item.OK {
		t.Fatal("missing reserve directory must not be writable")
	}
	if !strings.Contains(item.Fix, "先建立保留區") {
		t.Fatalf("Fix = %q, want reserve-area creation hint", item.Fix)
	}
	if !isBlocker(caps, "reserve_writable") {
		t.Fatalf("expected reserve_writable in blockers, got %+v", caps.Blockers)
	}
}

func TestDetectWithProbe_EmptyReserveRootDefaults(t *testing.T) {
	withRealExecution(t, true)

	caps := detectWithProbe(allOKProbe(), "")
	if caps.ReserveRoot != defaultReserveRoot {
		t.Fatalf("ReserveRoot = %q, want %q", caps.ReserveRoot, defaultReserveRoot)
	}
	if caps.ReserveRelPath != "EZRestore" {
		t.Fatalf("ReserveRelPath = %q, want EZRestore", caps.ReserveRelPath)
	}
	if !caps.Ready {
		t.Fatalf("expected Ready=true for default reserve root, blockers=%+v", caps.Blockers)
	}
}

func TestDetectCapabilities_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	caps := DetectCapabilities(ctx, "")
	if caps.Error == "" {
		t.Fatal("expected Error for cancelled context")
	}
	if caps.Ready {
		t.Fatal("cancelled context must not be Ready")
	}
	if caps.CheckedAt == "" {
		t.Fatal("CheckedAt must be set even when detection cannot start")
	}
	if _, err := time.Parse(time.RFC3339, caps.CheckedAt); err != nil {
		t.Fatalf("CheckedAt %q is not RFC3339: %v", caps.CheckedAt, err)
	}
}
