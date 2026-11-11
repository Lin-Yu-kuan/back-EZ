package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogEntry represents an audit/operation log entry.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Status    string `json:"status"` // "SUCCESS" or "FAILED"
	Details   string `json:"details"`
}

// OperationLogger handles structured operation logging to both memory and disk file.
type OperationLogger struct {
	mu      sync.RWMutex
	logPath string
	entries []LogEntry
	maxMem  int
}

// NewOperationLogger creates an OperationLogger storing logs to logPath.
func NewOperationLogger(logPath string) *OperationLogger {
	if logPath == "" {
		logPath = "operation.log"
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)

	l := &OperationLogger{
		logPath: logPath,
		entries: make([]LogEntry, 0, 100),
		maxMem:  100,
	}

	// Record initial boot log
	l.Log("SYSTEM", "CORE", "SUCCESS", "back-EZ 核心記錄器已初始化")
	return l
}

// Log records a new operation log entry.
func (l *OperationLogger) Log(action, target, status, details string) {
	entry := LogEntry{
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		Action:    action,
		Target:    target,
		Status:    status,
		Details:   details,
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Append to memory
	if len(l.entries) >= l.maxMem {
		l.entries = l.entries[1:]
	}
	l.entries = append(l.entries, entry)

	// Append to disk file
	line := fmt.Sprintf("[%s] [%s] 目標: %s | 結果: %s | 明細: %s\n",
		entry.Timestamp, entry.Action, entry.Target, entry.Status, entry.Details)

	f, err := os.OpenFile(l.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		defer f.Close()
		_, _ = f.WriteString(line)
	}
}

// GetEntries returns all in-memory log entries in reverse chronological order.
func (l *OperationLogger) GetEntries() []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	res := make([]LogEntry, len(l.entries))
	for i, e := range l.entries {
		res[len(l.entries)-1-i] = e
	}
	return res
}

// GetEntriesJSON returns JSON representation of logs.
func (l *OperationLogger) GetEntriesJSON() ([]byte, error) {
	return json.Marshal(l.GetEntries())
}

