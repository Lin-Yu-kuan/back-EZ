package vhdx

import "context"

// BCDBackend manages the dedicated BCD boot entry used by the VHDX chain.
// It never touches {default}: callers must create a new entry with CopyCurrent.
type BCDBackend interface {
	// Backup exports the current BCD store to path.
	Backup(ctx context.Context, path string) error
	// Restore imports a previously exported BCD store.
	Restore(ctx context.Context, path string) error
	// CopyCurrent creates a new boot entry from {current} and returns its GUID.
	CopyCurrent(ctx context.Context, description string) (string, error)
	// SetTarget points the entry at a VHDX device (device + osdevice + winload path).
	SetTarget(ctx context.Context, entryID, bcdDevice string) error
	// Verify checks that the entry's device/osdevice contain expectedDevice.
	Verify(ctx context.Context, entryID, expectedDevice string) error
	// Enumerate returns the raw `bcdedit /enum` output for a GUID or {current}.
	Enumerate(ctx context.Context, entryID string) (string, error)
	// SetBootSequence makes the entry a one-time next boot target.
	SetBootSequence(ctx context.Context, entryID string) error
	// DeleteEntry removes the boot entry (does not touch files).
	DeleteEntry(ctx context.Context, entryID string) error
}

// NewBCDBackend returns the real bcdedit-backed implementation.
func NewBCDBackend() BCDBackend {
	return newBCDEditBackend()
}
