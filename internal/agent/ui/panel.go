package ui

import (
	"fmt"
	"html"
	"os/exec"
	"strconv"
	"strings"

	"github.com/owner/back-ez/internal/agent/ipc"
)

// PanelData is the data injected into the full local dashboard.
//
// Token is the per-launch CSRF guard: every mutating fetch must send it back in
// the X-BackEZ-Token header (see cmd/gui). Edition/IsHome come from the local
// capability report so the panel never shells out to detect the OS edition.
type PanelData struct {
	Edition string
	IsHome  bool
	Token   string
}

// escapeHTML escapes dynamic text before it is embedded in the dashboard HTML
// or inside an inline HTML attribute. It must be used for every value that
// originates from the API (names, descriptions, capability details, log lines).
func escapeHTML(value string) string {
	return html.EscapeString(value)
}

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
</html>`, escapeHTML(hostname), escapeHTML(clientID), escapeHTML(status))
}

// fullPanelTemplate is the static dashboard skeleton. Dynamic values are
// replaced with strings.NewReplacer (never fmt.Sprintf: the CSS contains %
// characters that would be interpreted as formatting verbs).
const fullPanelTemplate = `<!DOCTYPE html>
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
    .badge-danger { background: #7f1d1d; color: #fca5a5; }
    .btn {
      padding: 8px 16px;
      border: none;
      border-radius: 6px;
      cursor: pointer;
      font-size: 13px;
      font-weight: 600;
      transition: all 0.2s;
    }
    .btn:disabled { opacity: 0.45; cursor: not-allowed; }
    .btn-primary { background: var(--primary); color: white; }
    .btn-primary:hover:enabled { background: var(--primary-hover); }
    .btn-danger { background: #7f1d1d; color: #fca5a5; }
    .btn-danger:hover:enabled { background: #991b1b; }
    .btn-outline { background: transparent; border: 1px solid var(--border); color: var(--text); }
    .btn-outline:hover:enabled { background: var(--border); }
    .home-warning {
      background: #451a03;
      border: 1px solid #b45309;
      border-radius: 8px;
      padding: 12px;
      margin-bottom: 16px;
    }
    .home-warning-title { font-weight: bold; color: #f59e0b; }
    .home-warning-text { font-size: 12px; color: #fde68a; margin-top: 4px; }
    .cap-section {
      background: var(--card-bg);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 16px;
      margin-bottom: 16px;
    }
    .cap-section h2 { margin: 0 0 10px; font-size: 15px; }
    .cap-summary { font-size: 13px; color: var(--text-muted); margin-bottom: 12px; }
    .cap-blockers {
      display: none;
      background: #450a0a;
      border: 1px solid #b91c1c;
      border-radius: 8px;
      padding: 12px;
      margin-bottom: 12px;
    }
    .cap-blockers-title { font-weight: bold; color: #f87171; margin-bottom: 4px; }
    .blocker-item { margin-top: 8px; padding-top: 8px; border-top: 1px solid #7f1d1d; }
    .cap-item {
      border: 1px solid var(--border);
      border-radius: 6px;
      padding: 10px 12px;
      margin-bottom: 8px;
      background: #0f172a;
    }
    .cap-item-bad { border-color: #b91c1c; }
    .cap-head { display: flex; align-items: center; gap: 6px; font-size: 13px; font-weight: 600; }
    .cap-key { font-family: monospace; }
    .cap-req { font-size: 11px; color: var(--warning); font-weight: normal; }
    .cap-opt { color: var(--text-muted); }
    .cap-detail { font-size: 12px; color: #cbd5e1; margin-top: 4px; word-break: break-all; }
    .cap-fix { font-size: 12px; color: #fde68a; margin-top: 4px; }
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
      max-width: 90%;
    }
    .modal h2 { margin: 0 0 16px; font-size: 17px; }
    .form-group { margin-bottom: 14px; }
    .form-group label { display: block; font-size: 12px; color: var(--text-muted); margin-bottom: 6px; }
    .form-group input {
      width: 100%;
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
      <p id="sysinfo">狀態載入中... | 系統版本: {{EDITION}}</p>
    </div>
    <div id="statusBadge" class="badge badge-secondary">狀態載入中</div>
  </div>

  {{HOME_BANNER}}

  <div style="background:#1e293b;border:1px solid var(--border);border-radius:8px;padding:10px 12px;margin-bottom:16px;font-size:12px;color:var(--text-muted);">
    🔐 本機變更操作以「管理員權限 + 一次性 X-BackEZ-Token + 同源檢查」保護；尚未導入密碼驗證（與 SPEC §3.4 差異，待 B 案補）。
  </div>

  <div class="cap-section">
    <h2>🧪 系統能力檢測</h2>
    <div id="capSummary" class="cap-summary">正在偵測系統能力...</div>
    <div id="capBlockers" class="cap-blockers"></div>
    <div id="capItems"></div>
  </div>

  <div class="actions-bar">
    <div style="font-size: 14px; font-weight: bold; color: var(--text-muted);">
      本機還原點清單 (<span id="rpCount">0</span>/5)
    </div>
    <div style="display: flex; gap: 8px;">
      <button class="btn btn-outline" onclick="openLogsModal()">📋 操作日誌</button>
      <button class="btn btn-outline" onclick="refreshAll()">🔄 重新整理</button>
      <button id="createBtn" class="btn btn-primary" onclick="openCreateModal()">➕ 新增還原點</button>
    </div>
  </div>

  <div id="rpList" class="rp-grid">
    <div style="text-align: center; color: var(--text-muted); padding: 40px;">讀取還原點中...</div>
  </div>

  <!-- Create Modal -->
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
    <div class="modal" style="width: 650px; max-width: 95%;">
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
    window.__EZ_TOKEN__ = {{TOKEN}};

    let sysReady = false;
    let sysBlockReason = '系統能力檢測尚未完成';
    let dryRunMode = false;

    // escapeHTML 是所有動態文字的必經出口；嚴禁把 API 回傳值直接串進 innerHTML。
    function escapeHTML(value) {
      if (value === null || value === undefined) return '';
      return String(value)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
    }

    function showToast(msg, isError) {
      const t = document.getElementById('toast');
      t.innerText = msg;
      t.style.borderColor = isError ? 'var(--danger)' : 'var(--success)';
      t.style.color = isError ? '#fca5a5' : '#86efac';
      t.style.display = 'block';
      setTimeout(function() { t.style.display = 'none'; }, 3000);
    }

    function formatAPIError(err, httpStatus) {
      if (!err || typeof err !== 'object') return 'HTTP ' + httpStatus;
      let text = err.message || '未知錯誤';
      if (err.code) text += '（' + err.code + '）';
      if (err.details && typeof err.details === 'string') text += '：' + err.details;
      return text;
    }

    async function apiPost(path, payload) {
      const res = await fetch(path, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-BackEZ-Token': window.__EZ_TOKEN__
        },
        body: JSON.stringify(payload)
      });
      let data = null;
      try { data = await res.json(); } catch (e) { data = null; }
      if (!res.ok || !data || data.ok !== true) {
        const err = (data && data.error) ? data.error : null;
        throw new Error(formatAPIError(err, res.status));
      }
      return data;
    }

    function renderCapabilities(status) {
      const caps = (status && status.capabilities) ? status.capabilities : {};
      const items = Array.isArray(caps.items) ? caps.items : [];
      const blockers = Array.isArray(caps.blockers) ? caps.blockers : [];
      const engineReady = !!(status && status.engine_ready);

      const badge = document.getElementById('statusBadge');
      if (!engineReady) {
        badge.textContent = '引擎未就緒';
        badge.className = 'badge badge-danger';
      } else if (!caps.ready) {
        badge.textContent = '能力不足';
        badge.className = 'badge badge-danger';
      } else {
        badge.textContent = '就緒';
        badge.className = 'badge';
      }

      let sysinfo = '系統版本: ' + (caps.edition || '未知');
      if (caps.build) sysinfo += ' (Build ' + caps.build + ')';
      sysinfo += ' | 管理員: ' + (caps.is_admin ? '是' : '否');
      sysinfo += ' | 引擎: ' + (engineReady ? '已就緒' : '未就緒');
      dryRunMode = !!(status && status.dry_run);
      sysinfo += ' | 模式: ' + (dryRunMode ? '預覽（DryRun，未修改磁碟/BCD）' : '實際');
      document.getElementById('sysinfo').textContent = sysinfo;

      sysReady = engineReady && !!caps.ready;
      sysBlockReason = '系統能力檢測未通過';

      const summary = document.getElementById('capSummary');
      if (caps.error) {
        sysBlockReason = '能力檢測錯誤：' + caps.error;
        summary.innerHTML = '<span style="color: #f87171;">能力檢測錯誤：' + escapeHTML(caps.error) + '</span>';
      } else if (sysReady) {
        sysBlockReason = '';
        summary.innerHTML = '<span style="color: #34d399;">✅ 系統能力檢測通過，可執行還原操作。</span>';
      } else {
        const reasonParts = blockers.map(function(b) { return (b.key || '') + '：' + (b.detail || ''); });
        if (!engineReady) reasonParts.push('VHDX 引擎未就緒');
        if (status && status.engine_warning) reasonParts.push('引擎警告：' + status.engine_warning);
        if (reasonParts.length > 0) sysBlockReason = reasonParts.join('；');
        summary.innerHTML = '<span style="color: #f87171;">❌ 系統能力檢測未通過，還原操作已停用。</span>';
      }

      const blockerBox = document.getElementById('capBlockers');
      if (blockers.length === 0) {
        blockerBox.style.display = 'none';
        blockerBox.innerHTML = '';
      } else {
        blockerBox.style.display = 'block';
        blockerBox.innerHTML = '<div class="cap-blockers-title">⛔ 阻塞項目（' + blockers.length + '）</div>' +
          blockers.map(function(b) {
            return '<div class="blocker-item">' +
              '<div class="cap-head">❌ <span class="cap-key">' + escapeHTML(b.key) + '</span></div>' +
              '<div class="cap-detail">' + escapeHTML(b.detail) + '</div>' +
              (b.fix ? '<div class="cap-fix">修正方式：' + escapeHTML(b.fix) + '</div>' : '') +
            '</div>';
          }).join('');
      }

      document.getElementById('capItems').innerHTML = items.map(function(it) {
        const fix = (it.fix && String(it.fix).trim() !== '') ? '<div class="cap-fix">修正方式：' + escapeHTML(it.fix) + '</div>' : '';
        const requiredLabel = it.required ? '<span class="cap-req">必要</span>' : '<span class="cap-req cap-opt">選用</span>';
        return '<div class="cap-item ' + (it.ok ? '' : 'cap-item-bad') + '">' +
          '<div class="cap-head">' + (it.ok ? '✅' : '❌') + ' <span class="cap-key">' + escapeHTML(it.key) + '</span> ' + requiredLabel + '</div>' +
          '<div class="cap-detail">' + escapeHTML(it.detail) + '</div>' + fix +
        '</div>';
      }).join('');

      updateActionAvailability();
    }

    function updateActionAvailability() {
      const createBtn = document.getElementById('createBtn');
      if (!createBtn) return;
      createBtn.disabled = !sysReady;
      createBtn.title = sysReady ? '' : ('無法執行：' + sysBlockReason);
    }

    async function loadStatus() {
      try {
        const res = await fetch('/api/system/status');
        let data = null;
        try { data = await res.json(); } catch (e) { data = null; }
        if (!res.ok || !data || data.ok !== true || !data.data) {
          throw new Error(formatAPIError(data && data.error, res.status));
        }
        renderCapabilities(data.data);
      } catch (err) {
        sysReady = false;
        sysBlockReason = '無法取得系統能力狀態：' + err.message;
        document.getElementById('capSummary').innerHTML = '<span style="color: #f87171;">' + escapeHTML(sysBlockReason) + '</span>';
        document.getElementById('statusBadge').textContent = '狀態未知';
        document.getElementById('statusBadge').className = 'badge badge-danger';
        updateActionAvailability();
      }
    }

    async function loadPoints() {
      const list = document.getElementById('rpList');
      try {
        const res = await fetch('/api/restore-points');
        let data = null;
        try { data = await res.json(); } catch (e) { data = null; }
        if (!res.ok || !Array.isArray(data)) {
          throw new Error(formatAPIError(data && data.error, res.status));
        }
        document.getElementById('rpCount').textContent = data.length;

        if (data.length === 0) {
          list.innerHTML = '<div style="text-align: center; color: var(--text-muted); padding: 40px;">目前無任何還原點</div>';
          return;
        }

        const disabledAttr = sysReady ? '' : ' disabled';
        const titleAttr = sysReady ? '' : ' title="' + escapeHTML('無法執行：' + sysBlockReason) + '"';

        list.innerHTML = data.map(function(p) {
          const activeClass = p.is_active ? 'active' : '';
          const activeBadge = p.is_active ? '<span class="badge">目前使用中 (Active)</span>' : '';
          const baseBadge = p.is_base ? '<span class="badge badge-secondary">Base 基準母體</span>' : '';
          const stateNote = (p.state && p.state !== 'READY') ? '<p style="color: #f87171;">狀態: ' + escapeHTML(p.state) + '</p>' : '';
          const switchBtn = !p.is_active
            ? '<button class="btn btn-primary" data-action="switch" data-point-id="' + escapeHTML(p.point_id) + '"' + disabledAttr + titleAttr + '>🔄 還原至此點</button>'
            : '';
          const deleteBtn = (!p.is_base && !p.is_active)
            ? '<button class="btn btn-danger" data-action="delete" data-point-id="' + escapeHTML(p.point_id) + '"' + disabledAttr + titleAttr + '>🗑️ 刪除</button>'
            : '';

          return '<div class="rp-card ' + activeClass + '">' +
            '<div class="rp-info">' +
              '<h3>' + escapeHTML(p.name) + ' ' + activeBadge + ' ' + baseBadge + '</h3>' +
              '<p>建立時間: ' + escapeHTML(new Date(p.created_at).toLocaleString()) + ' | 說明: ' + escapeHTML(p.description || '無備註') + '</p>' +
              stateNote +
            '</div>' +
            '<div class="rp-actions">' + switchBtn + deleteBtn + '</div>' +
          '</div>';
        }).join('');
      } catch (err) {
        document.getElementById('rpCount').textContent = '0';
        list.innerHTML = '<div style="color: #fca5a5; text-align: center; padding: 40px;">無法取得還原點清單：' + escapeHTML(err.message) + '</div>';
      }
    }

    function openCreateModal() {
      if (!sysReady) {
        showToast('無法執行：' + sysBlockReason, true);
        return;
      }
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
      if (!sysReady) {
        showToast('無法執行：' + sysBlockReason, true);
        return;
      }
      try {
        await apiPost('/api/restore-points/create', { name: name, description: desc });
        showToast(dryRunMode
          ? '預覽模式：已建立還原點計畫（未修改磁碟/BCD）'
          : '還原點建立成功！', false);
        closeCreateModal();
        refreshAll();
      } catch (err) {
        showToast('建立失敗: ' + err.message, true);
      }
    }

    async function switchPoint(pointId) {
      if (!sysReady) {
        showToast('無法執行：' + sysBlockReason, true);
        return;
      }
      if (!confirm('確定要將下次開機目標設定為此還原點嗎？重新開機後才會生效。')) return;
      try {
        await apiPost('/api/restore-points/switch', { point_id: pointId });
        showToast(dryRunMode
          ? '預覽模式：已設定下次開機目標（未修改 BCD）'
          : '已設定下次開機目標，請重新開機後生效。', false);
        refreshAll();
      } catch (err) {
        showToast('切換失敗: ' + err.message, true);
      }
    }

    async function deletePoint(pointId) {
      if (!sysReady) {
        showToast('無法執行：' + sysBlockReason, true);
        return;
      }
      if (!confirm('確定要永久刪除此還原點嗎？此動作無法復原！')) return;
      try {
        await apiPost('/api/restore-points/delete', { point_id: pointId });
        showToast(dryRunMode
          ? '預覽模式：已刪除還原點計畫（未刪除實體檔案）'
          : '還原點已刪除！', false);
        refreshAll();
      } catch (err) {
        showToast('刪除失敗: ' + err.message, true);
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
        let logs = null;
        try { logs = await res.json(); } catch (e) { logs = null; }
        if (!res.ok || !Array.isArray(logs)) {
          throw new Error(formatAPIError(logs && logs.error, res.status));
        }
        if (logs.length === 0) {
          container.innerHTML = '<div style="color: var(--text-muted); text-align: center;">尚無操作記錄</div>';
          return;
        }
        container.innerHTML = logs.map(function(l) {
          const color = l.status === 'SUCCESS' ? '#34d399' : '#f87171';
          return '<div style="margin-bottom: 8px; border-bottom: 1px solid #1e293b; padding-bottom: 6px;">' +
            '<span style="color: #94a3b8;">[' + escapeHTML(l.timestamp) + ']</span> ' +
            '<span style="font-weight: bold; color: #60a5fa;">[' + escapeHTML(l.action) + ']</span> ' +
            '目標: ' + escapeHTML(l.target) + ' | ' +
            '<span style="color: ' + color + '; font-weight: bold;">' + escapeHTML(l.status) + '</span><br>' +
            '<span style="color: #cbd5e1; margin-left: 10px;">說明: ' + escapeHTML(l.details) + '</span>' +
          '</div>';
        }).join('');
      } catch (err) {
        container.innerHTML = '<div style="color: var(--danger);">讀取日誌失敗: ' + escapeHTML(err.message) + '</div>';
      }
    }

    async function refreshAll() {
      // 先取得能力狀態再渲染清單，避免還原點按鈕用到過期的 sysReady。
      await loadStatus();
      await loadPoints();
    }

    document.getElementById('rpList').addEventListener('click', function(event) {
      const btn = event.target.closest('button[data-action]');
      if (!btn || btn.disabled) return;
      const pointId = btn.getAttribute('data-point-id') || '';
      const action = btn.getAttribute('data-action');
      if (action === 'switch') {
        switchPoint(pointId);
      } else if (action === 'delete') {
        deletePoint(pointId);
      }
    });

    // 初始載入
    refreshAll();
  </script>
</body>
</html>`

// GenerateFullPanelHTML generates the full local dashboard, injecting the
// capability-derived edition, the Home edition banner and the per-launch
// X-BackEZ-Token value.
func GenerateFullPanelHTML(d PanelData) string {
	homeBanner := ""
	if d.IsHome {
		homeBanner = `<div class="home-warning">
    <div class="home-warning-title">⚠️ 檢測到 Windows 家用版（Home）</div>
    <div class="home-warning-text">家用版不支援 VHDX 原生開機／還原，需要 Windows Pro／Enterprise／Education。本工具不提供 changepk 或其他解鎖方式；請改用支援的版本後再操作。</div>
  </div>`
	}

	replacer := strings.NewReplacer(
		"{{EDITION}}", escapeHTML(d.Edition),
		"{{HOME_BANNER}}", homeBanner,
		"{{TOKEN}}", strconv.Quote(d.Token),
	)
	return replacer.Replace(fullPanelTemplate)
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
