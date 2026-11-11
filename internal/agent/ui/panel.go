package ui

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/owner/back-ez/internal/agent/ipc"
)

// CheckWebView2Runtime checks whether the WebView2 runtime is installed on Windows.
func CheckWebView2Runtime() bool {
	// Query WebView2 registry key or check edge installation
	cmd := exec.Command("reg", "query", `HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`, "/v", "pv")
	out, err := cmd.Output()
	if err == nil && strings.Contains(string(out), "REG_SZ") {
		return true
	}

	cmdUser := exec.Command("reg", "query", `HKCU\Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`, "/v", "pv")
	outUser, errUser := cmdUser.Output()
	if errUser == nil && strings.Contains(string(outUser), "REG_SZ") {
		return true
	}

	return false
}

// GeneratePanelHTML generates the embedded lock panel HTML for WebView2.
func GeneratePanelHTML(info map[string]any) string {
	clientID, _ := info["client_id"].(string)
	hostname, _ := info["hostname"].(string)
	status, _ := info["status"].(string)

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-TW">
<head>
  <meta charset="UTF-8">
  <title>back-EZ 本機還原面板</title>
  <style>
    body {
      margin: 0;
      padding: 20px;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      background: #0f172a;
      color: #f8fafc;
    }
    .header {
      border-bottom: 1px solid #1e293b;
      padding-bottom: 12px;
      margin-bottom: 16px;
    }
    .title { font-size: 18px; font-weight: bold; }
    .subtitle { font-size: 12px; color: #94a3b8; }
    .card {
      background: #1e293b;
      border: 1px solid #334155;
      border-radius: 8px;
      padding: 12px;
      margin-bottom: 12px;
      font-size: 13px;
    }
    .row { display: flex; justify-content: space-between; margin-bottom: 6px; }
    .label { color: #94a3b8; }
    .val { font-weight: 600; }
    .badge {
      display: inline-block;
      padding: 2px 8px;
      border-radius: 9999px;
      font-size: 11px;
      background: #064e3b;
      color: #34d399;
    }
    .btn {
      width: 100%%;
      padding: 8px;
      background: #2563eb;
      color: white;
      border: none;
      border-radius: 6px;
      cursor: pointer;
      font-size: 13px;
      font-weight: 600;
    }
    .btn:hover { background: #1d4ed8; }
  </style>
</head>
<body>
  <div class="header">
    <div class="title">back-EZ 本機保護面板</div>
    <div class="subtitle">客戶端唯讀鎖定模式</div>
  </div>

  <div class="card">
    <div class="row"><span class="label">電腦名稱:</span><span class="val">%s</span></div>
    <div class="row"><span class="label">用戶端 ID:</span><span class="val" style="font-family:monospace">%s</span></div>
    <div class="row"><span class="label">狀態:</span><span class="badge">%s</span></div>
  </div>

  <div class="card" style="font-size:12px; color:#cbd5e1; line-height: 1.5;">
    🔒 <b>密碼鎖定保護中</b><br>
    預設為唯讀監控模式。切換、新增、刪除還原點需輸入管理員密碼。
  </div>
</body>
</html>`, hostname, clientID, status)
}

// PanelController handles the logic between UI window and Agent IPC Client.
type PanelController struct {
	client *ipc.Client
}

func NewPanelController(client *ipc.Client) *PanelController {
	return &PanelController{client: client}
}

func (p *PanelController) GetInitialData() (map[string]any, error) {
	return p.client.GetAgentInfo()
}

func (p *PanelController) PerformSwitch(password, pointID string) error {
	return p.client.SwitchRestorePoint(password, pointID)
}

func (p *PanelController) PerformCreate(password, name, desc string) error {
	return p.client.CreateRestorePoint(password, name, desc)
}
