package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOperationLogger(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test_operation.log")

	logger := NewOperationLogger(logFile)
	logger.Log("CREATE", "rp_test_1", "SUCCESS", "測試新增還原點")
	logger.Log("SWITCH", "rp_base", "SUCCESS", "測試切換至 Base")

	entries := logger.GetEntries()
	if len(entries) < 3 { // init + 2 logs
		t.Fatalf("expected at least 3 entries, got %d", len(entries))
	}

	if entries[0].Action != "SWITCH" {
		t.Errorf("expected newest entry to be SWITCH, got %s", entries[0].Action)
	}

	data, err := os.ReadFile(logFile)
	if err != nil || len(data) == 0 {
		t.Fatalf("expected log file on disk to be written, err: %v", err)
	}
}

