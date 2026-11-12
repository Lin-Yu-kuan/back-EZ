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

func TestFullPanelHTMLRemovesOverwriteAndUnlock(t *testing.T) {
	html := GenerateFullPanelHTML(PanelData{Edition: "Professional", Token: "test-token"})

	for _, forbidden := range []string{"覆蓋此點", "overwritePoint", "/api/restore-points/overwrite", "unlockPro", "/api/system/unlock-pro"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("full panel HTML must not contain %q", forbidden)
		}
	}
}

func TestFullPanelHTMLHasCapabilitySectionAndToken(t *testing.T) {
	html := GenerateFullPanelHTML(PanelData{Edition: "Professional", IsHome: false, Token: "abc123"})

	for _, want := range []string{
		"/api/system/status",
		"/api/restore-points",
		`id="capSummary"`,
		`id="capBlockers"`,
		`id="capItems"`,
		`id="statusBadge"`,
		`window.__EZ_TOKEN__ = "abc123"`,
		"X-BackEZ-Token",
		"已設定下次開機目標，請重新開機後生效",
		"尚未導入密碼驗證",
		"escapeHTML",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("full panel HTML missing %q", want)
		}
	}

	// Token must be encoded by the Go side (strconv.Quote), never hand-concatenated.
	if strings.Contains(html, "window.__EZ_TOKEN__ = abc123") {
		t.Errorf("token must be quoted as a JS string literal")
	}
	if strings.Contains(html, "{{TOKEN}}") || strings.Contains(html, "{{EDITION}}") || strings.Contains(html, "{{HOME_BANNER}}") {
		t.Errorf("full panel HTML still contains unreplaced placeholders")
	}
}

func TestFullPanelHTMLHomeBanner(t *testing.T) {
	plain := GenerateFullPanelHTML(PanelData{Edition: "Professional", IsHome: false, Token: "t"})
	if strings.Contains(plain, "家用版（Home）") {
		t.Errorf("non-Home panel must not show the Home warning banner")
	}

	home := GenerateFullPanelHTML(PanelData{Edition: "Core", IsHome: true, Token: "t"})
	if !strings.Contains(home, "家用版（Home）") {
		t.Errorf("Home panel must show the Home warning banner")
	}
	if !strings.Contains(home, "不提供 changepk") {
		t.Errorf("Home banner must state that changepk unlocking is not provided")
	}
}

func TestFullPanelHTMLUsesEscapeHTMLForDynamicFields(t *testing.T) {
	html := GenerateFullPanelHTML(PanelData{Edition: "Professional", Token: "t"})

	for _, want := range []string{
		"escapeHTML(p.name)",
		"escapeHTML(p.description",
		"escapeHTML(p.point_id)",
		"escapeHTML(it.detail)",
		"escapeHTML(it.fix)",
		"escapeHTML(b.detail)",
		"escapeHTML(b.fix)",
		"escapeHTML(l.details)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("full panel HTML missing escaped dynamic field %q", want)
		}
	}
}

func TestFullPanelHTMLEscapesEdition(t *testing.T) {
	html := GenerateFullPanelHTML(PanelData{Edition: `<script>alert(1)</script>`, Token: "t"})

	if strings.Contains(html, `<script>alert(1)</script>`) {
		t.Fatalf("edition string was not escaped before embedding in HTML")
	}
	if !strings.Contains(html, `&lt;script&gt;alert(1)&lt;/script&gt;`) {
		t.Fatalf("expected escaped edition string in HTML")
	}
}

func TestEscapeHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"plain", "Base System", "Base System"},
		{"script tag", `<script>alert("x")</script>`, `&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;`},
		{"single quote", `O'Brien`, `O&#39;Brien`},
		{"ampersand", "a & b", "a &amp; b"},
		{"attribute break", `" onmouseover="alert(1)`, `&#34; onmouseover=&#34;alert(1)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeHTML(tt.input); got != tt.want {
				t.Errorf("escapeHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
