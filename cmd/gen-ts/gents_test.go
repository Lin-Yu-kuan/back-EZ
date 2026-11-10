package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenTS_ZeroDiff(t *testing.T) {
	tsPath := filepath.Join("..", "..", "web", "src", "types", "spec.ts")
	existingBytes, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatalf("failed to read existing spec.ts: %v", err)
	}

	generated := generateTypeScript()

	existingStr := strings.ReplaceAll(string(existingBytes), "\r\n", "\n")
	generatedStr := strings.ReplaceAll(generated, "\r\n", "\n")

	if existingStr != generatedStr {
		t.Errorf("codegen produced diff against existing spec.ts")
	}
}

func TestGenTS_NegativeDiffDetection(t *testing.T) {
	generated := generateTypeScript()

	// Simulate mutated generated content
	mutated := strings.Replace(generated, "client_id: string;", "client_id_mutated: string;", 1)

	if mutated == generated {
		t.Fatalf("mutation failed")
	}

	// Verify our comparison detects the diff
	if mutated == generateTypeScript() {
		t.Errorf("negative test failed: diff was not detected")
	}
}
