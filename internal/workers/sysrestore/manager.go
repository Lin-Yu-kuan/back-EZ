package sysrestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/owner/back-ez/internal/protocol"
	"golang.org/x/sys/windows"
)

const (
	statusTimeout  = 15 * time.Second
	listTimeout    = 15 * time.Second
	createTimeout  = 180 * time.Second
	restoreTimeout = 30 * time.Second
	enableTimeout  = 60 * time.Second
)

// commandRunner abstracts PowerShell so tests never touch the real system.
type commandRunner interface {
	Run(ctx context.Context, script string, env map[string]string) ([]byte, error)
}

type powerShellRunner struct{}

func (powerShellRunner) Run(ctx context.Context, script string, env map[string]string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if len(env) > 0 {
		cmd.Env = append(os.Environ(), flattenEnv(env)...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func flattenEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(env))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

// Manager implements Service on top of the built-in Windows System Restore.
type Manager struct {
	mu      sync.Mutex
	runner  commandRunner
	drive   string
	isAdmin func() bool
	now     func() time.Time
}

// NewManager returns the production manager (real PowerShell, real system).
func NewManager() *Manager {
	return &Manager{
		runner:  powerShellRunner{},
		drive:   systemDrive(),
		isAdmin: defaultIsAdmin,
		now:     time.Now,
	}
}

func defaultIsAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func systemDrive() string {
	if normalized, ok := normalizeDrive(os.Getenv("SystemDrive")); ok {
		return normalized
	}
	return `C:\`
}

// normalizeDrive accepts "C", "C:" or "C:\" and returns "C:\".
func normalizeDrive(value string) (string, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, `\`)

	if len(value) == 1 && isDriveLetter(value[0]) {
		return strings.ToUpper(value) + `:\`, true
	}
	if len(value) == 2 && value[1] == ':' && isDriveLetter(value[0]) {
		return strings.ToUpper(value[:1]) + `:\`, true
	}
	return "", false
}

func isDriveLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

type psResult struct {
	OK                bool    `json:"ok"`
	Error             string  `json:"error"`
	Admin             bool    `json:"admin"`
	CmdletsOK         bool    `json:"cmdlets_ok"`
	ProtectionEnabled bool    `json:"protection_enabled"`
	FreeBytes         uint64  `json:"free_bytes"`
	ThrottleMinutes   int     `json:"throttle_minutes"`
	ThrottlePresent   bool    `json:"throttle_present"`
	PointCount        int     `json:"point_count"`
	LastCreatedAt     string  `json:"last_created_at"`
	ListError         string  `json:"list_error"`
	Points            []Point `json:"points"`
	Point             *Point  `json:"point"`
}

func decodeResult(out []byte) (*psResult, error) {
	out = bytes.TrimPrefix(out, []byte{0xEF, 0xBB, 0xBF})
	out = bytes.TrimSpace(out)

	start := bytes.IndexByte(out, '{')
	end := bytes.LastIndexByte(out, '}')
	if start < 0 || end < start {
		return nil, fmt.Errorf("no JSON object in output: %q", truncate(string(out), 200))
	}

	var result psResult
	if err := json.Unmarshal(out[start:end+1], &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

// mapPowerShellError turns a cmdlet failure message into a SPEC error code.
// Message matching is best effort: unknown text still fails closed as INTERNAL.
func mapPowerShellError(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return newError(protocol.ErrInternal, "系統還原指令失敗（沒有錯誤訊息）")
	}

	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "access is denied"),
		strings.Contains(lower, "access denied"),
		strings.Contains(message, "存取被拒"),
		strings.Contains(message, "拒絕存取"),
		strings.Contains(message, "存取遭拒"):
		return newError(protocol.ErrForbidden, "需要管理員權限才能讀取或操作系統還原：%s", message)
	case strings.Contains(lower, "disabled"),
		strings.Contains(message, "已停用"),
		strings.Contains(message, "未啟用"):
		return newError(protocol.ErrInvalidState, "系統保護未開啟：%s", message)
	default:
		return newError(protocol.ErrInternal, "系統還原指令失敗：%s", message)
	}
}

func (m *Manager) run(ctx context.Context, timeout time.Duration, script string, env map[string]string) (*psResult, error) {
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out, err := m.runner.Run(childCtx, script, env)
	if err != nil {
		if errors.Is(childCtx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, newError(protocol.ErrInternal, "系統還原指令逾時（上限 %s）", timeout)
		}
		return nil, newError(protocol.ErrInternal, "無法執行 PowerShell：%v", err)
	}

	result, err := decodeResult(out)
	if err != nil {
		return nil, newError(protocol.ErrInternal, "無法解析系統還原輸出：%v", err)
	}
	if !result.OK {
		return nil, mapPowerShellError(result.Error)
	}
	return result, nil
}

// Status reports whether System Restore is usable and why not.
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(ctx)
}

func (m *Manager) statusLocked(ctx context.Context) Status {
	admin := m.isAdmin()
	status := Status{
		Drive:           m.drive,
		IsAdmin:         admin,
		ThrottleMinutes: DefaultThrottleMinutes,
	}

	result, err := m.run(ctx, statusTimeout, statusScript, map[string]string{"BACEZ_DRIVE": m.drive})
	if err != nil {
		status.Error = err.Error()
		status.Blockers = []Blocker{{
			Key:     "probe_failed",
			Message: err.Error(),
			Fix:     "以管理員身分執行 back-EZ，並確認 PowerShell 可用",
		}}
		return status
	}

	status.Supported = result.CmdletsOK
	status.ProtectionEnabled = result.ProtectionEnabled
	status.FreeBytes = result.FreeBytes
	status.PointCount = result.PointCount
	status.LastCreatedAt = result.LastCreatedAt
	if result.ThrottlePresent {
		status.ThrottleMinutes = result.ThrottleMinutes
	}
	if result.ListError != "" && status.Error == "" {
		status.Error = result.ListError
	}

	if status.ThrottleMinutes > 0 && status.LastCreatedAt != "" {
		if last, parseErr := time.Parse(time.RFC3339, status.LastCreatedAt); parseErr == nil {
			next := last.Add(time.Duration(status.ThrottleMinutes) * time.Minute)
			if m.now().Before(next) {
				status.NextAllowedAt = next.UTC().Format(time.RFC3339)
			}
		}
	}

	var blockers []Blocker
	add := func(key, message, fix string) {
		blockers = append(blockers, Blocker{Key: key, Message: message, Fix: fix})
	}
	if !status.Supported {
		add("unsupported",
			"此 Windows 缺少系統還原指令（Get-ComputerRestorePoint / Checkpoint-Computer）",
			"確認為 Windows 10/11 用戶端版本，且 PowerShell 可用")
	}
	if !admin {
		add("admin_required",
			"目前不是以管理員權限執行",
			"以滑鼠右鍵選擇「以系統管理員身分執行」啟動 back-EZ（或使用 啟動.bat）")
	}
	if !status.ProtectionEnabled {
		add("protection_disabled",
			"此電腦的系統保護（System Protection）未開啟",
			"按「開啟系統保護」後再建立還原點")
	}
	if status.FreeBytes < MinFreeBytes {
		add("insufficient_space",
			fmt.Sprintf("可用空間不足（%.1f GB，至少需要 1 GB）", float64(status.FreeBytes)/(1<<30)),
			"清理磁碟後再試")
	}
	if status.NextAllowedAt != "" {
		add("throttled",
			"Windows 限制還原點建立頻率，下次可建立時間："+status.NextAllowedAt,
			"等待時間到，或由系統管理員調整 SystemRestorePointCreationFrequency")
	}
	status.Blockers = blockers
	status.CanCreate = len(blockers) == 0
	status.CanRestore = admin && status.Supported && status.ProtectionEnabled && status.PointCount > 0
	return status
}

// List returns the existing Windows restore points (requires admin).
func (m *Manager) List(ctx context.Context) ([]Point, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listLocked(ctx)
}

func (m *Manager) listLocked(ctx context.Context) ([]Point, error) {
	if !m.isAdmin() {
		return nil, newError(protocol.ErrForbidden, "需要管理員權限才能讀取系統還原點")
	}
	result, err := m.run(ctx, listTimeout, listScript, map[string]string{"BACEZ_DRIVE": m.drive})
	if err != nil {
		return nil, err
	}
	if result.Points == nil {
		return []Point{}, nil
	}
	return result.Points, nil
}

// Create makes a new Windows restore point.
func (m *Manager) Create(ctx context.Context, description string) (Point, error) {
	description = strings.TrimSpace(description)
	if description == "" {
		return Point{}, newError(protocol.ErrInvalidArgument, "還原點說明不可為空")
	}
	if len([]rune(description)) > MaxDescriptionRunes {
		return Point{}, newError(protocol.ErrInvalidArgument, "還原點說明不可超過 %d 字", MaxDescriptionRunes)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	status := m.statusLocked(ctx)
	switch {
	case !status.IsAdmin:
		return Point{}, newError(protocol.ErrForbidden, "需要管理員權限才能建立系統還原點")
	case !status.Supported:
		return Point{}, newError(protocol.ErrCapabilityUnsupported, "此 Windows 不支援系統還原")
	case !status.ProtectionEnabled:
		return Point{}, newError(protocol.ErrInvalidState, "系統保護未開啟，請先開啟後再建立還原點")
	case status.NextAllowedAt != "":
		return Point{}, newError(protocol.ErrInvalidState,
			"Windows 限制建立頻率，最快 %s 之後才能再建立還原點", status.NextAllowedAt)
	}

	result, err := m.run(ctx, createTimeout, createScript, map[string]string{
		"BACEZ_DESC":  description,
		"BACEZ_DRIVE": m.drive,
	})
	if err != nil {
		return Point{}, err
	}
	if result.Point == nil {
		return Point{}, newError(protocol.ErrInternal, "還原點已建立，但無法取得其資訊；請按「重新整理」查看")
	}
	return *result.Point, nil
}

// Restore applies a Windows restore point. Windows will reboot the machine.
func (m *Manager) Restore(ctx context.Context, sequence int) error {
	if sequence <= 0 {
		return newError(protocol.ErrInvalidArgument, "還原點序號不正確")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.isAdmin() {
		return newError(protocol.ErrForbidden, "需要管理員權限才能執行系統還原")
	}

	points, err := m.listLocked(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, point := range points {
		if point.Sequence == sequence {
			found = true
			break
		}
	}
	if !found {
		return newError(protocol.ErrNotFound, "找不到序號 %d 的系統還原點", sequence)
	}

	_, err = m.run(ctx, restoreTimeout, restoreScript, map[string]string{
		"BACEZ_SEQ":   strconv.Itoa(sequence),
		"BACEZ_DRIVE": m.drive,
	})
	return err
}

// Enable turns System Protection on for the given drive.
func (m *Manager) Enable(ctx context.Context, drive string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.isAdmin() {
		return newError(protocol.ErrForbidden, "需要管理員權限才能開啟系統保護")
	}

	target := m.drive
	if normalized, ok := normalizeDrive(drive); ok {
		target = normalized
	}

	if _, err := m.run(ctx, enableTimeout, enableScript, map[string]string{"BACEZ_DRIVE": target}); err != nil {
		// Enable-ComputerRestore can fail when protection is already on; treat
		// "now enabled" as success instead of surfacing a confusing error.
		if status := m.statusLocked(ctx); status.ProtectionEnabled {
			return nil
		}
		return err
	}
	return nil
}

const statusScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }
$drive = $env:BACEZ_DRIVE
if ([string]::IsNullOrWhiteSpace($drive)) { $drive = 'C:\' }
$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
$cmdlets = @('Get-ComputerRestorePoint','Checkpoint-Computer','Restore-Computer','Enable-ComputerRestore') | ForEach-Object { [bool](Get-Command $_ -ErrorAction SilentlyContinue) }
$cmdletsOK = -not ($cmdlets -contains $false)
$srKey = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\SystemRestore'
$props = Get-ItemProperty -Path $srKey -ErrorAction SilentlyContinue
$disableSR = $null
$rps = $null
$freq = $null
if ($null -ne $props) {
  if ($null -ne $props.PSObject.Properties['DisableSR']) { $disableSR = $props.DisableSR }
  if ($null -ne $props.PSObject.Properties['RPSessionInterval']) { $rps = $props.RPSessionInterval }
  if ($null -ne $props.PSObject.Properties['SystemRestorePointCreationFrequency']) { $freq = $props.SystemRestorePointCreationFrequency }
}
$protected = $true
if ($null -ne $disableSR -and [int]$disableSR -eq 1) { $protected = $false }
if ($null -ne $rps -and [int]$rps -eq 0) { $protected = $false }
$free = [int64]0
try {
  $vol = Get-Volume -DriveLetter $drive.Substring(0,1) -ErrorAction Stop
  if ($null -ne $vol) { $free = [int64]$vol.SizeRemaining }
} catch { }
$points = @()
$last = ''
$listError = ''
if ($admin -and $cmdletsOK -and $protected) {
  try {
    $points = @(Get-ComputerRestorePoint -ErrorAction Stop)
    if ($points.Count -gt 0) {
      $p = $points | Sort-Object SequenceNumber -Descending | Select-Object -First 1
      if ($p.CreationTime -is [datetime]) { $last = $p.CreationTime.ToUniversalTime().ToString('o') } else { $last = [string]$p.CreationTime }
    }
  } catch {
    $listError = $_.Exception.Message
    if ($listError -match 'disabled') { $protected = $false }
  }
}
$freqValue = 0
$freqPresent = $false
if ($null -ne $freq) { $freqValue = [int]$freq; $freqPresent = $true }
[ordered]@{
  ok = $true
  admin = $admin
  cmdlets_ok = $cmdletsOK
  protection_enabled = $protected
  free_bytes = $free
  throttle_minutes = $freqValue
  throttle_present = $freqPresent
  point_count = $points.Count
  last_created_at = $last
  list_error = $listError
} | ConvertTo-Json -Compress -Depth 4
`

const listScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }
try {
  $points = @(Get-ComputerRestorePoint -ErrorAction Stop | ForEach-Object {
    $ct = $_.CreationTime
    if ($ct -is [datetime]) { $iso = $ct.ToUniversalTime().ToString('o') } else { $iso = [string]$ct }
    [ordered]@{
      sequence = [int]$_.SequenceNumber
      description = [string]$_.Description
      type = [int]$_.RestorePointType
      created_at = $iso
    }
  })
  [ordered]@{ ok = $true; points = @($points) } | ConvertTo-Json -Compress -Depth 5
} catch {
  [ordered]@{ ok = $false; error = $_.Exception.Message } | ConvertTo-Json -Compress
}
`

const createScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }
try {
  $desc = $env:BACEZ_DESC
  if ([string]::IsNullOrWhiteSpace($desc)) { throw 'restore point description is empty' }
  Checkpoint-Computer -Description $desc -RestorePointType 'MODIFY_SETTINGS' -ErrorAction Stop
  $point = $null
  $p = Get-ComputerRestorePoint -ErrorAction Stop | Sort-Object SequenceNumber -Descending | Select-Object -First 1
  if ($null -ne $p) {
    $ct = $p.CreationTime
    if ($ct -is [datetime]) { $iso = $ct.ToUniversalTime().ToString('o') } else { $iso = [string]$ct }
    $point = [ordered]@{
      sequence = [int]$p.SequenceNumber
      description = [string]$p.Description
      type = [int]$p.RestorePointType
      created_at = $iso
    }
  }
  [ordered]@{ ok = $true; point = $point } | ConvertTo-Json -Compress -Depth 5
} catch {
  [ordered]@{ ok = $false; error = $_.Exception.Message } | ConvertTo-Json -Compress
}
`

const restoreScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }
try {
  $seq = 0
  if (-not [int]::TryParse($env:BACEZ_SEQ, [ref]$seq) -or $seq -le 0) { throw 'invalid restore point sequence number' }
  Restore-Computer -RestorePoint $seq -Confirm:$false -ErrorAction Stop
  [ordered]@{ ok = $true } | ConvertTo-Json -Compress
} catch {
  [ordered]@{ ok = $false; error = $_.Exception.Message } | ConvertTo-Json -Compress
}
`

const enableScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }
try {
  $drive = $env:BACEZ_DRIVE
  if ([string]::IsNullOrWhiteSpace($drive)) { $drive = 'C:\' }
  Enable-ComputerRestore -Drive $drive -ErrorAction Stop
  [ordered]@{ ok = $true } | ConvertTo-Json -Compress
} catch {
  [ordered]@{ ok = $false; error = $_.Exception.Message } | ConvertTo-Json -Compress
}
`
