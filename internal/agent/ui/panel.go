package ui

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/owner/back-ez/internal/agent/ipc"
)

// CheckWebView2Runtime checks whether the WebView2 runtime is installed on Windows.
func CheckWebView2Runtime() bool {
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

// GeneratePanelHTML generates the backward-compatible lock panel HTML.
func GeneratePanelHTML(info map[string]any) string {
	clientID, _ := info["client_id"].(string)
	hostname, _ := info["hostname"].(string)
	status, _ := info["status"].(string)

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-TW">
<head>
  <meta charset="UTF-8">
  <title>back-EZ 本機還原面板</title>
</head>
<body>
  <div>電腦名稱: %s</div>
  <div>用戶端 ID: %s</div>
  <div>狀態: %s</div>
  <div>密碼鎖定保護中</div>
</body>
</html>`, hostname, clientID, status)
}

// GenerateFullPanelHTML generates a full-featured dashboard for the standalone local desktop UI.
func GenerateFullPanelHTML(isHome bool, edition string) string {
	homeWarningBadge := ""
	if isHome {
		homeWarningBadge = `
		<div style="background: #451a03; border: 1px solid #b45309; border-radius: 8px; padding: 12px; margin-bottom: 16px;">
			<div style="display: flex; justify-content: space-between; align-items: center;">
				<div>
					<div style="font-weight: bold; color: #f59e0b;">⚠️ 檢測到 Windows 家用版（Home）</div>
					<div style="font-size: 12px; color: #fde68a; margin-top: 4px;">微軟原生限制家用版無法直接由 VHDX 開機。建議點擊右側按鈕進行 3 分鐘免重灌原位解鎖 Pro 核心。</div>
				</div>
				<button class="btn btn-warning" onclick="unlockPro()" style="padding: 6px 14px; font-size: 12px; width: auto;">一鍵解鎖 Pro</button>
			</div>
		</div>`
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-TW">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>back-EZ 本機秒級還原控制台</title>
  <style>
    :root {
      --bg: #0f172a;
      --card-bg: #1e293b;
      --border: #334155;
      --text: #f8fafc;
      --text-muted: #94a3b8;
      --primary: #3b82f6;
      --primary-hover: #2563eb;
      --success: #10b981;
      --danger: #ef4444;
      --warning: #f59e0b;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      padding: 24px;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Microsoft JhengHei", sans-serif;
      background: var(--bg);
      color: var(--text);
    }
    .header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      border-bottom: 1px solid var(--border);
      padding-bottom: 16px;
      margin-bottom: 20px;
    }
    .title-group h1 { margin: 0; font-size: 20px; display: flex; align-items: center; gap: 8px; }
    .title-group p { margin: 4px 0 0; font-size: 13px; color: var(--text-muted); }
    .badge {
      display: inline-block;
      padding: 3px 10px;
      border-radius: 9999px;
      font-size: 12px;
      font-weight: 500;
      background: #064e3b;
      color: #34d399;
    }
    .badge-secondary { background: #334155; color: #cbd5e1; }
    .btn {
      padding: 8px 16px;
      border: none;
      border-radius: 6px;
      cursor: pointer;
      font-size: 13px;
      font-weight: 600;
      transition: all 0.2s;
    }
    .btn-primary { background: var(--primary); color: white; }
    .btn-primary:hover { background: var(--primary-hover); }
    .btn-warning { background: var(--warning); color: #000; font-weight: bold; }
    .btn-warning:hover { opacity: 0.9; }
    .btn-danger { background: #7f1d1d; color: #fca5a5; }
    .btn-danger:hover { background: #991b1b; }
    .btn-outline { background: transparent; border: 1px solid var(--border); color: var(--text); }
    .btn-outline:hover { background: var(--border); }
    .actions-bar {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 16px;
    }
    .rp-grid { display: grid; gap: 12px; }
    .rp-card {
      background: var(--card-bg);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 16px;
      display: flex;
      justify-content: space-between;
      align-items: center;
      transition: border-color 0.2s;
    }
    .rp-card.active {
      border-color: var(--success);
      box-shadow: 0 0 0 1px var(--success);
    }
    .rp-info h3 { margin: 0 0 6px 0; font-size: 16px; display: flex; align-items: center; gap: 8px; }
    .rp-info p { margin: 0; font-size: 12px; color: var(--text-muted); }
    .rp-actions { display: flex; gap: 8px; }
    .modal-overlay {
      display: none;
      position: fixed;
      inset: 0;
      background: rgba(0,0,0,0.7);
      align-items: center;
      justify-content: center;
      z-index: 1000;
    }
    .modal {
      background: #1e293b;
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 20px;
      width: 400px;
      max-width: 90%%;
    }
    .modal h2 { margin: 0 0 16px; font-size: 17px; }
    .form-group { margin-bottom: 14px; }
    .form-group label { display: block; font-size: 12px; color: var(--text-muted); margin-bottom: 6px; }
    .form-group input {
      width: 100%%;
      padding: 8px 12px;
      background: #0f172a;
      border: 1px solid var(--border);
      border-radius: 6px;
      color: white;
      font-size: 14px;
    }
    .modal-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 20px; }
    #toast {
      position: fixed;
      bottom: 24px;
      right: 24px;
      background: #1e293b;
      border: 1px solid var(--border);
      padding: 12px 20px;
      border-radius: 6px;
      font-size: 13px;
      box-shadow: 0 10px 15px -3px rgba(0,0,0,0.5);
      display: none;
      z-index: 2000;
    }
  </style>
</head>
<body>
  <div class="header">
    <div class="title-group">
      <h1>🛡️ back-EZ 本機秒級還原控制台</h1>
      <p id="sysinfo">載入中... | 系統版本: %s</p>
    </div>
    <div id="statusBadge" class="badge">常駐就緒</div>
  </div>

  %s

  <div class="actions-bar">
    <div style="font-size: 14px; font-weight: bold; color: var(--text-muted);">
      本機還原點清單 (<span id="rpCount">0</span>/5)
    </div>
    <div style="display: flex; gap: 8px;">
      <button class="btn btn-outline" onclick="openLogsModal()">📋 操作日誌</button>
      <button class="btn btn-outline" onclick="loadPoints()">🔄 重新整理</button>
      <button class="btn btn-primary" onclick="openCreateModal()">➕ 新增還原點</button>
    </div>
  </div>

  <div id="rpList" class="rp-grid">
    <div style="text-align: center; color: var(--text-muted); padding: 40px;">讀取還原點中...</div>
  </div>

  <!-- Modal -->
  <div id="createModal" class="modal-overlay">
    <div class="modal">
      <h2>新增本機還原點</h2>
      <div class="form-group">
        <label>還原點名稱</label>
        <input type="text" id="rpName" placeholder="例如：安裝更新前乾淨狀態">
      </div>
      <div class="form-group">
        <label>備註說明（選填）</label>
        <input type="text" id="rpDesc" placeholder="說明此狀態的主要用途">
      </div>
      <div class="modal-actions">
        <button class="btn btn-outline" onclick="closeCreateModal()">取消</button>
        <button class="btn btn-primary" onclick="submitCreate()">確認建立</button>
      </div>
    </div>
  </div>

  <!-- Logs Modal -->
  <div id="logsModal" class="modal-overlay">
    <div class="modal" style="width: 650px; max-width: 95%%;">
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
        <h2 style="margin: 0;">📋 本機操作審計日誌</h2>
        <button class="btn btn-outline" onclick="loadLogs()" style="padding: 4px 10px; font-size: 12px;">重新整理</button>
      </div>
      <div id="logsList" style="max-height: 400px; overflow-y: auto; font-family: monospace; font-size: 12px; background: #0f172a; padding: 12px; border-radius: 6px; border: 1px solid var(--border);">
        載入日誌中...
      </div>
      <div class="modal-actions">
        <button class="btn btn-outline" onclick="closeLogsModal()">關閉</button>
      </div>
    </div>
  </div>

  <div id="toast"></div>

  <script>
    function showToast(msg, isError) {
      const t = document.getElementById('toast');
      t.innerText = msg;
      t.style.borderColor = isError ? 'var(--danger)' : 'var(--success)';
      t.style.color = isError ? '#fca5a5' : '#86efac';
      t.style.display = 'block';
      setTimeout(() => { t.style.display = 'none'; }, 3000);
    }

    async function loadPoints() {
      try {
        const res = await fetch('/api/restore-points');
        const data = await res.json();
        const list = document.getElementById('rpList');
        document.getElementById('rpCount').innerText = data.length;

        if (data.length === 0) {
          list.innerHTML = '<div style="text-align: center; color: var(--text-muted); padding: 40px;">目前無任何還原點</div>';
          return;
        }

        list.innerHTML = data.map(function(p) {
          var activeClass = p.is_active ? 'active' : '';
          var activeBadge = p.is_active ? '<span class="badge">目前使用中 (Active)</span>' : '';
          var baseBadge = p.is_base ? '<span class="badge badge-secondary">Base 基準母體</span>' : '';
          var switchBtn = !p.is_active ? '<button class="btn btn-primary" onclick="switchPoint(\'' + p.point_id + '\')">🔄 還原至此點</button>' : '';
          var overwriteBtn = '<button class="btn btn-outline" onclick="overwritePoint(\'' + p.point_id + '\')" title="將目前的系統狀態覆蓋更新至此還原點">💾 覆蓋此點</button>';
          var deleteBtn = (!p.is_base && !p.is_active) ? '<button class="btn btn-danger" onclick="deletePoint(\'' + p.point_id + '\')">🗑️ 刪除</button>' : '';

          return '<div class="rp-card ' + activeClass + '">' +
            '<div class="rp-info">' +
              '<h3>' + p.name + ' ' + activeBadge + ' ' + baseBadge + '</h3>' +
              '<p>建立時間: ' + new Date(p.created_at).toLocaleString() + ' | 說明: ' + (p.description || '無備註') + '</p>' +
            '</div>' +
            '<div class="rp-actions">' +
              switchBtn + overwriteBtn + deleteBtn +
            '</div>' +
          '</div>';
        }).join('');
      } catch (err) {
        showToast('無法取得還原點清單: ' + err.message, true);
      }
    }

    function openCreateModal() {
      document.getElementById('rpName').value = '';
      document.getElementById('rpDesc').value = '';
      document.getElementById('createModal').style.display = 'flex';
    }

    function closeCreateModal() {
      document.getElementById('createModal').style.display = 'none';
    }

    async function submitCreate() {
      const name = document.getElementById('rpName').value.trim();
      const desc = document.getElementById('rpDesc').value.trim();
      if (!name) {
        alert('請輸入還原點名稱！');
        return;
      }
      try {
        const res = await fetch('/api/restore-points/create', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name, description: desc })
        });
        const data = await res.json();
        if (data.ok) {
          showToast('還原點建立成功！');
          closeCreateModal();
          loadPoints();
        } else {
          showToast('建立失敗: ' + (data.error || '未知錯誤'), true);
        }
      } catch (err) {
        showToast('發送失敗: ' + err.message, true);
      }
    }

    async function switchPoint(pointId) {
      if (!confirm('確定要還原到此狀態嗎？系統將切換開機指標至該還原點，重新開機後生效！')) return;
      try {
        const res = await fetch('/api/restore-points/switch', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ point_id: pointId })
        });
        const data = await res.json();
        if (data.ok) {
          showToast('已成功切換還原點開機指標！');
          loadPoints();
        } else {
          showToast('切換失敗: ' + (data.error || '未知錯誤'), true);
        }
      } catch (err) {
        showToast('發送失敗: ' + err.message, true);
      }
    }

    async function overwritePoint(pointId) {
      if (!confirm('確定要以「目前系統的最新狀態」覆蓋更新此還原點嗎？')) return;
      try {
        const res = await fetch('/api/restore-points/overwrite', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ point_id: pointId })
        });
        const data = await res.json();
        if (data.ok) {
          showToast('還原點已成功覆蓋更新！');
          loadPoints();
        } else {
          showToast('覆蓋失敗: ' + (data.error || '未知錯誤'), true);
        }
      } catch (err) {
        showToast('發送失敗: ' + err.message, true);
      }
    }

    async function deletePoint(pointId) {
      if (!confirm('確定要永久刪除此還原點嗎？此動作無法復原！')) return;
      try {
        const res = await fetch('/api/restore-points/delete', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ point_id: pointId })
        });
        const data = await res.json();
        if (data.ok) {
          showToast('還原點已成功刪除！');
          loadPoints();
        } else {
          showToast('刪除失敗: ' + (data.error || '未知錯誤'), true);
        }
      } catch (err) {
        showToast('發送失敗: ' + err.message, true);
      }
    }

    async function unlockPro() {
      if (!confirm('微軟官方原生工具將為您原位解鎖 Pro 核心功能，完全免重灌。\n是否立即啟動解鎖精靈？')) return;
      try {
        const res = await fetch('/api/system/unlock-pro', { method: 'POST' });
        const data = await res.json();
        if (data.ok) {
          alert('已啟動微軟官方解鎖程序！請依照畫面提示完成啟用與重新啟動。');
        } else {
          showToast('啟動解鎖失敗: ' + data.error, true);
        }
      } catch (err) {
        showToast('發送失敗: ' + err.message, true);
      }
    }

    function openLogsModal() {
      document.getElementById('logsModal').style.display = 'flex';
      loadLogs();
    }

    function closeLogsModal() {
      document.getElementById('logsModal').style.display = 'none';
    }

    async function loadLogs() {
      const container = document.getElementById('logsList');
      try {
        const res = await fetch('/api/logs');
        const logs = await res.json();
        if (!logs || logs.length === 0) {
          container.innerHTML = '<div style="color: var(--text-muted); text-align: center;">尚無操作記錄</div>';
          return;
        }
        container.innerHTML = logs.map(function(l) {
          var color = l.status === 'SUCCESS' ? '#34d399' : '#f87171';
          return '<div style="margin-bottom: 8px; border-bottom: 1px solid #1e293b; padding-bottom: 6px;">' +
            '<span style="color: #94a3b8;">[' + l.timestamp + ']</span> ' +
            '<span style="font-weight: bold; color: #60a5fa;">[' + l.action + ']</span> ' +
            '目標: ' + l.target + ' | ' +
            '<span style="color: ' + color + '; font-weight: bold;">' + l.status + '</span><br>' +
            '<span style="color: #cbd5e1; margin-left: 10px;">說明: ' + l.details + '</span>' +
          '</div>';
        }).join('');
      } catch (err) {
        container.innerHTML = '<div style="color: var(--danger);">讀取日誌失敗: ' + err.message + '</div>';
      }
    }

    // 初始載入
    loadPoints();
  </script>
</body>
</html>`, edition, homeWarningBadge)
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

func (p *PanelController) PerformDelete(password, pointID string) error {
	return p.client.DeleteRestorePoint(password, pointID)
}

func (p *PanelController) PerformOverwrite(password, pointID string) error {
	return p.client.OverwriteRestorePoint(password, pointID)
}
