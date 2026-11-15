package vhdx

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const bcdCommandTimeout = 20 * time.Second

// bcdRunner abstracts bcdedit so tests never modify the real boot store.
type bcdRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type bcdEditRunner struct{}

func (bcdEditRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	childCtx, cancel := context.WithTimeout(ctx, bcdCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(childCtx, "bcdedit.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("bcdedit %s failed: %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

type bcdEditBackend struct {
	runner bcdRunner
}

func newBCDEditBackend() BCDBackend {
	return &bcdEditBackend{runner: bcdEditRunner{}}
}

// entryIDPattern matches BCD GUIDs such as {12345678-1234-1234-1234-123456789abc}.
var entryIDPattern = regexp.MustCompile(`\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}`)

func validEntryID(entryID string) bool {
	return entryIDPattern.FindString(entryID) == entryID
}

func validEntryRef(entryID string) bool {
	return entryID == "{current}" || validEntryID(entryID)
}

func (b *bcdEditBackend) Backup(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("BCD backup path must not be empty")
	}
	_, err := b.runner.Run(ctx, "/export", path)
	return err
}

func (b *bcdEditBackend) Restore(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("BCD backup path must not be empty")
	}
	_, err := b.runner.Run(ctx, "/import", path)
	return err
}

func (b *bcdEditBackend) CopyCurrent(ctx context.Context, description string) (string, error) {
	description = strings.TrimSpace(strings.ReplaceAll(description, `"`, `'`))
	if description == "" {
		description = "back-EZ restore point"
	}

	output, err := b.runner.Run(ctx, "/copy", "{current}", "/d", description)
	if err != nil {
		return "", err
	}
	match := entryIDPattern.FindString(string(output))
	if match == "" {
		return "", fmt.Errorf("bcdedit /copy did not return a boot entry GUID: %s", strings.TrimSpace(string(output)))
	}
	return match, nil
}

func (b *bcdEditBackend) SetTarget(ctx context.Context, entryID, bcdDevice string) error {
	if !validEntryID(entryID) {
		return fmt.Errorf("invalid BCD entry id %q", entryID)
	}
	bcdDevice = strings.TrimSpace(bcdDevice)
	if !strings.HasPrefix(bcdDevice, "vhd=[") {
		return fmt.Errorf("invalid VHD device string %q", bcdDevice)
	}

	steps := [][]string{
		{"/set", entryID, "device", bcdDevice},
		{"/set", entryID, "osdevice", bcdDevice},
		{"/set", entryID, "path", `\windows\system32\winload.efi`},
	}
	for _, args := range steps {
		if _, err := b.runner.Run(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

func (b *bcdEditBackend) Verify(ctx context.Context, entryID, expectedDevice string) error {
	if !validEntryID(entryID) {
		return fmt.Errorf("invalid BCD entry id %q", entryID)
	}
	if strings.TrimSpace(expectedDevice) == "" {
		return fmt.Errorf("expected VHD device must not be empty")
	}

	text, err := b.Enumerate(ctx, entryID)
	if err != nil {
		return err
	}
	if !strings.Contains(text, expectedDevice) {
		return fmt.Errorf("BCD entry %s does not reference %s", entryID, expectedDevice)
	}
	if !strings.Contains(strings.ToLower(text), "winload") {
		return fmt.Errorf("BCD entry %s does not point at winload", entryID)
	}
	return nil
}

func (b *bcdEditBackend) Enumerate(ctx context.Context, entryID string) (string, error) {
	if !validEntryRef(entryID) {
		return "", fmt.Errorf("invalid BCD entry reference %q", entryID)
	}
	output, err := b.runner.Run(ctx, "/enum", entryID)
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func (b *bcdEditBackend) SetBootSequence(ctx context.Context, entryID string) error {
	if !validEntryID(entryID) {
		return fmt.Errorf("invalid BCD entry id %q", entryID)
	}
	_, err := b.runner.Run(ctx, "/bootsequence", entryID)
	return err
}

func (b *bcdEditBackend) DeleteEntry(ctx context.Context, entryID string) error {
	if !validEntryID(entryID) {
		return fmt.Errorf("invalid BCD entry id %q", entryID)
	}
	_, err := b.runner.Run(ctx, "/delete", entryID, "/f")
	return err
}
