package ui

import (
	"strings"
	"testing"
)

func TestPanelHTMLGeneration(t *testing.T) {
	info := map[string]any{
		"client_id": "cli_9f2c4b1e8a7d4c3f",
		"hostname":  "LAB-PC-01",
		"status":    "ONLINE_WINDOWS",
	}

	html := GeneratePanelHTML(info)

	if !strings.Contains(html, "LAB-PC-01") {
		t.Errorf("HTML missing hostname")
	}
	if !strings.Contains(html, "cli_9f2c4b1e8a7d4c3f") {
		t.Errorf("HTML missing client_id")
	}
	if !strings.Contains(html, "ONLINE_WINDOWS") {
		t.Errorf("HTML missing status")
	}
	if !strings.Contains(html, "密碼鎖定保護中") {
		t.Errorf("HTML missing lock protection note")
	}
}
