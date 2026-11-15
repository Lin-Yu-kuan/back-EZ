package vhdx

import "context"

// DiskBackend creates and removes the virtual disk files used by the VHDX
// restore-point chain. It exists so the engine can be tested without touching
// real disks, and so the real implementation stays behind one small surface.
type DiskBackend interface {
	// CreateBase creates a new dynamic VHDX file of sizeBytes.
	CreateBase(ctx context.Context, path string, sizeBytes uint64) error
	// CreateDifferencing creates a child VHDX whose parent is parentPath.
	CreateDifferencing(ctx context.Context, path, parentPath string) error
	// Delete removes a VHDX file. It fails if the file is still attached.
	Delete(ctx context.Context, path string) error
	// Exists reports whether the VHDX file exists.
	Exists(path string) bool
}

// NewDiskBackend returns the real virtdisk.dll-backed implementation.
func NewDiskBackend() DiskBackend {
	return newVirtualDiskBackend()
}
