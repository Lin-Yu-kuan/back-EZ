package vhdx

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type fakeBCDRunner struct {
	mu      sync.Mutex
	calls   [][]string
	respond func(args []string) ([]byte, error)
}

func (f *fakeBCDRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()

	if f.respond != nil {
		return f.respond(args)
	}
	return nil, nil
}

func newTestBCDBackend(respond func(args []string) ([]byte, error)) (*bcdEditBackend, *fakeBCDRunner) {
	runner := &fakeBCDRunner{respond: respond}
	return &bcdEditBackend{runner: runner}, runner
}

func TestBCDCopyCurrentParsesGUID(t *testing.T) {
	const guid = "{11111111-2222-3333-4444-555555555555}"
	backend, runner := newTestBCDBackend(func(args []string) ([]byte, error) {
		return []byte("已將項目複製到 " + guid + "。\r\n"), nil
	})

	entryID, err := backend.CopyCurrent(context.Background(), "back-EZ test")
	if err != nil {
		t.Fatalf("CopyCurrent failed: %v", err)
	}
	if entryID != guid {
		t.Fatalf("entryID = %q, want %q", entryID, guid)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one bcdedit call, got %d", len(runner.calls))
	}
	args := runner.calls[0]
	if args[0] != "/copy" || args[1] != "{current}" || args[2] != "/d" || args[3] != "back-EZ test" {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestBCDCopyCurrentRejectsMissingGUID(t *testing.T) {
	backend, _ := newTestBCDBackend(func(args []string) ([]byte, error) {
		return []byte("The operation completed successfully."), nil
	})

	if _, err := backend.CopyCurrent(context.Background(), "x"); err == nil {
		t.Fatalf("expected an error when bcdedit does not return a GUID")
	}
}

func TestBCDSetTargetWritesDeviceOSDeviceAndPath(t *testing.T) {
	const guid = "{aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}"
	const device = `vhd=[\\?\Volume{11111111-2222-3333-4444-555555555555}]\EZRestore\live.vhdx`
	backend, runner := newTestBCDBackend(nil)

	if err := backend.SetTarget(context.Background(), guid, device); err != nil {
		t.Fatalf("SetTarget failed: %v", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 bcdedit calls, got %d", len(runner.calls))
	}
	if runner.calls[0][2] != "device" || runner.calls[0][3] != device {
		t.Fatalf("unexpected device args: %v", runner.calls[0])
	}
	if runner.calls[1][2] != "osdevice" || runner.calls[1][3] != device {
		t.Fatalf("unexpected osdevice args: %v", runner.calls[1])
	}
	if runner.calls[2][2] != "path" || !strings.Contains(runner.calls[2][3], "winload") {
		t.Fatalf("unexpected path args: %v", runner.calls[2])
	}
}

func TestBCDSetTargetRejectsInvalidInput(t *testing.T) {
	backend, runner := newTestBCDBackend(nil)

	if err := backend.SetTarget(context.Background(), "not-a-guid", `vhd=[\\?\Volume{x}]\a.vhdx`); err == nil {
		t.Fatalf("expected invalid entry id to fail")
	}
	if err := backend.SetTarget(context.Background(), "{aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}", `C:\a.vhdx`); err == nil {
		t.Fatalf("expected non-vhd device to fail")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("invalid input must not call bcdedit, got %v", runner.calls)
	}
}

func TestBCDVerifyFindsDeviceAndWinload(t *testing.T) {
	const guid = "{aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}"
	const device = `vhd=[\\?\Volume{11111111-2222-3333-4444-555555555555}]\EZRestore\live.vhdx`

	backend, _ := newTestBCDBackend(func(args []string) ([]byte, error) {
		return []byte("device                  " + device + "\r\n" +
			"path                    \\windows\\system32\\winload.efi\r\n"), nil
	})

	if err := backend.Verify(context.Background(), guid, device); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	otherDevice := `vhd=[\\?\Volume{99999999-2222-3333-4444-555555555555}]\EZRestore\other.vhdx`
	if err := backend.Verify(context.Background(), guid, otherDevice); err == nil {
		t.Fatalf("expected Verify to fail for a different device")
	}
}

func TestBCDVerifyRequiresWinload(t *testing.T) {
	const guid = "{aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}"
	const device = `vhd=[\\?\Volume{11111111-2222-3333-4444-555555555555}]\EZRestore\live.vhdx`

	backend, _ := newTestBCDBackend(func(args []string) ([]byte, error) {
		return []byte("device " + device + "\r\n"), nil
	})
	if err := backend.Verify(context.Background(), guid, device); err == nil {
		t.Fatalf("expected Verify to fail when winload is missing")
	}
}

func TestBCDBackupRestoreBootSequenceAndDelete(t *testing.T) {
	const guid = "{aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}"
	backend, runner := newTestBCDBackend(nil)
	ctx := context.Background()

	if err := backend.Backup(ctx, `D:\EZRestore\bcd.bak`); err != nil {
		t.Fatalf("Backup failed: %v", err)
	}
	if err := backend.Restore(ctx, `D:\EZRestore\bcd.bak`); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if err := backend.SetBootSequence(ctx, guid); err != nil {
		t.Fatalf("SetBootSequence failed: %v", err)
	}
	if err := backend.DeleteEntry(ctx, guid); err != nil {
		t.Fatalf("DeleteEntry failed: %v", err)
	}

	want := [][]string{
		{"/export", `D:\EZRestore\bcd.bak`},
		{"/import", `D:\EZRestore\bcd.bak`},
		{"/bootsequence", guid},
		{"/delete", guid, "/f"},
	}
	if len(runner.calls) != len(want) {
		t.Fatalf("expected %d calls, got %d: %v", len(want), len(runner.calls), runner.calls)
	}
	for i := range want {
		if strings.Join(runner.calls[i], " ") != strings.Join(want[i], " ") {
			t.Errorf("call %d = %v, want %v", i, runner.calls[i], want[i])
		}
	}
}

func TestBCDRunnerErrorIsPropagated(t *testing.T) {
	const guid = "{aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}"
	backend, _ := newTestBCDBackend(func(args []string) ([]byte, error) {
		return []byte("Access is denied."), errors.New("exit status 1")
	})

	if err := backend.SetBootSequence(context.Background(), guid); err == nil {
		t.Fatalf("expected runner error to be propagated")
	}
}

func TestBCDEnumerateAllowsCurrentAndGUID(t *testing.T) {
	backend, runner := newTestBCDBackend(func(args []string) ([]byte, error) {
		return []byte("device vhd=[\\\\?\\Volume{11111111-2222-3333-4444-555555555555}]\\EZRestore\\live.vhdx"), nil
	})

	if _, err := backend.Enumerate(context.Background(), "{current}"); err != nil {
		t.Fatalf("Enumerate({current}) failed: %v", err)
	}
	if _, err := backend.Enumerate(context.Background(), "{aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}"); err != nil {
		t.Fatalf("Enumerate(GUID) failed: %v", err)
	}
	if _, err := backend.Enumerate(context.Background(), "not-a-guid"); err == nil {
		t.Fatalf("expected invalid entry reference to fail")
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 bcdedit calls, got %d", len(runner.calls))
	}
}
