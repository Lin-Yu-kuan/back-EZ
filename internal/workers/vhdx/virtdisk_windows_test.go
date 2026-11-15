package vhdx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVirtualDiskBackend_CreateBaseAndDifferencing(t *testing.T) {
	dir := t.TempDir()
	backend := NewDiskBackend()
	ctx := context.Background()

	basePath := filepath.Join(dir, "base.vhdx")
	if err := backend.CreateBase(ctx, basePath, 8<<20); err != nil {
		t.Fatalf("CreateBase failed: %v", err)
	}
	info, err := os.Stat(basePath)
	if err != nil {
		t.Fatalf("base.vhdx was not created: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("base.vhdx is empty")
	}
	if !backend.Exists(basePath) {
		t.Fatalf("Exists(base) = false")
	}

	childPath := filepath.Join(dir, "diff.vhdx")
	if err := backend.CreateDifferencing(ctx, childPath, basePath); err != nil {
		t.Fatalf("CreateDifferencing failed: %v", err)
	}
	if !backend.Exists(childPath) {
		t.Fatalf("differencing disk was not created")
	}

	// Creating the same file twice must fail instead of silently overwriting.
	if err := backend.CreateDifferencing(ctx, childPath, basePath); err == nil {
		t.Fatalf("expected duplicate CreateDifferencing to fail")
	}

	if err := backend.Delete(ctx, childPath); err != nil {
		t.Fatalf("Delete(child) failed: %v", err)
	}
	if backend.Exists(childPath) {
		t.Fatalf("child still exists after Delete")
	}
	// Deleting a missing file is idempotent.
	if err := backend.Delete(ctx, childPath); err != nil {
		t.Fatalf("Delete(missing) should be idempotent, got %v", err)
	}

	if err := backend.Delete(ctx, basePath); err != nil {
		t.Fatalf("Delete(base) failed: %v", err)
	}
}

func TestVirtualDiskBackend_RejectsInvalidBaseSize(t *testing.T) {
	dir := t.TempDir()
	backend := NewDiskBackend()

	cases := []struct {
		name string
		size uint64
	}{
		{"below minimum", 1 << 20},
		{"not sector aligned", (4 << 20) + 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, "invalid.vhdx")
			err := backend.CreateBase(context.Background(), path, test.size)
			if err == nil {
				t.Fatalf("expected CreateBase(%d) to fail", test.size)
			}
			if backend.Exists(path) {
				t.Fatalf("invalid VHDX must not be created")
			}
		})
	}
}

func TestVirtualDiskBackend_RequiresExistingParent(t *testing.T) {
	dir := t.TempDir()
	backend := NewDiskBackend()

	err := backend.CreateDifferencing(
		context.Background(),
		filepath.Join(dir, "child.vhdx"),
		filepath.Join(dir, "missing-parent.vhdx"),
	)
	if err == nil {
		t.Fatalf("expected CreateDifferencing with a missing parent to fail")
	}
	if !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) {
		// We wrap the error into a readable message; only assert it failed.
		t.Logf("error (expected): %v", err)
	}
	if backend.Exists(filepath.Join(dir, "child.vhdx")) {
		t.Fatalf("child must not be created when the parent is missing")
	}
}
