package vhdx

// Capability detection for native VHDX boot / restore support.
//
// This file only reads system state (registry, BCD output, disk free space,
// DLL presence). It never creates, modifies or deletes VHDX files, BCD entries
// or partitions. All external calls are bounded by timeouts and every error is
// reported as a failed capability item instead of panicking.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	// defaultReserveRoot is used when the caller does not provide a reserve root.
	defaultReserveRoot = `D:\EZRestore`
	// reserveSpaceRequiredBytes is the minimum free space required in the reserve area (20 GiB).
	reserveSpaceRequiredBytes uint64 = 20 << 30
	// capabilityCommandTimeout bounds external command probes such as bcdedit and manage-bde.
	capabilityCommandTimeout = 2 * time.Second
)

// RealExecutionImplemented reports whether the A plan real (non-DryRun) write
// path is enabled. It stays false until the B plan lands.
var RealExecutionImplemented = false

// Capability is one capability check result. Key is stable machine-readable
// identifier; Detail explains the outcome and Fix tells the operator how to
// resolve a failing check.
type Capability struct {
	Key      string `json:"key"`
	OK       bool   `json:"ok"`
	Required bool   `json:"required"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix"`
}

// Capabilities is the full capability report returned to cmd/gui.
type Capabilities struct {
	CheckedAt      string       `json:"checked_at"`
	Ready          bool         `json:"ready"`
	Edition        string       `json:"edition"`
	Build          string       `json:"build"`
	IsAdmin        bool         `json:"is_admin"`
	ReserveRoot    string       `json:"reserve_root"`
	ReserveVolume  string       `json:"reserve_volume"`
	ReserveRelPath string       `json:"reserve_rel_path"`
	Items          []Capability `json:"items"`
	Blockers       []Capability `json:"blockers"`
	Error          string       `json:"error,omitempty"`
}

// capabilityProbe abstracts every external dependency of DetectCapabilities so
// tests can inject fakes and never touch the real machine.
type capabilityProbe interface {
	// CurrentVersion returns EditionID and CurrentBuildNumber from
	// HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion.
	CurrentVersion() (editionID string, build string, err error)
	// IsAdmin reports whether the current process token is elevated.
	IsAdmin() (bool, error)
	// BCDOutput returns the output of `bcdedit /enum {current}`.
	BCDOutput() (string, error)
	// Stat reports existence (and directory-ness) of path. A missing path is
	// reported as exists=false with a nil error.
	Stat(path string) (exists bool, isDir bool, err error)
	// VolumeNameForMountPoint returns the volume GUID path for a mount point
	// such as `D:\`.
	VolumeNameForMountPoint(mountPoint string) (string, error)
	// FreeBytes returns the free space available to the caller for path.
	FreeBytes(path string) (uint64, error)
	// HiberbootEnabled reads HiberbootEnabled (fast startup). present is false
	// when the value does not exist.
	HiberbootEnabled() (value uint32, present bool, err error)
	// BitLockerStatus returns the raw `manage-bde -status` output.
	BitLockerStatus() (string, error)
	// VirtualDiskAPI reports whether virtdisk.dll loads and exposes the
	// CreateVirtualDisk / OpenVirtualDisk procedures. It must not call them.
	VirtualDiskAPI() (ok bool, detail string)
	// Writable creates and removes a temporary file inside dir.
	Writable(dir string) error
}

// DetectCapabilities inspects the local machine for native VHDX boot / restore
// support. reserveRoot is the reserve area directory; when empty it defaults to
// `D:\EZRestore`.
func DetectCapabilities(ctx context.Context, reserveRoot string) Capabilities {
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	root := normalizeReserveRoot(reserveRoot)
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Capabilities{
			CheckedAt:   checkedAt,
			ReserveRoot: root,
			Error:       fmt.Sprintf("能力偵測未開始：%v", err),
		}
	}
	return detectWithProbe(newWindowsProbe(), reserveRoot)
}

// detectWithProbe runs all capability checks against p. It is the injectable
// test seam: production code passes newWindowsProbe().
func detectWithProbe(p capabilityProbe, reserveRoot string) Capabilities {
	root := normalizeReserveRoot(reserveRoot)
	caps := Capabilities{
		CheckedAt:   time.Now().UTC().Format(time.RFC3339),
		ReserveRoot: root,
	}

	editionID, build, versionErr := p.CurrentVersion()
	if versionErr != nil || strings.TrimSpace(editionID) == "" {
		caps.Edition = "Unknown"
	} else {
		caps.Edition = editionID
	}
	caps.Build = build

	admin, adminErr := p.IsAdmin()
	caps.IsAdmin = admin

	items := make([]Capability, 0, 12)
	add := func(key string, required, ok bool, detail, fix string) {
		items = append(items, Capability{
			Key:      key,
			OK:       ok,
			Required: required,
			Detail:   detail,
			Fix:      fix,
		})
	}

	// 1. Windows edition must be a SKU that supports native VHDX boot.
	switch {
	case versionErr != nil:
		add("os_edition_supported", true, false,
			fmt.Sprintf("無法讀取 Windows 版本資訊：%v", versionErr),
			"確認可讀取 HKLM\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion，並以管理員身分執行；Home 版需 Windows Pro/Enterprise/Education；請勿使用 changepk 破解升級")
	case isSupportedEdition(editionID):
		add("os_edition_supported", true, true,
			fmt.Sprintf("Windows 版本支援 VHDX 原生開機／還原（EditionID=%s）", editionID), "")
	default:
		add("os_edition_supported", true, false,
			fmt.Sprintf("目前 Windows 版本不支援 VHDX 原生開機／還原（EditionID=%s）", editionID),
			"需 Windows Pro/Enterprise/Education；請勿使用 changepk 破解升級")
	}

	// 2. Elevated token is required for BCD and disk operations.
	switch {
	case adminErr != nil:
		add("admin_token", true, false,
			fmt.Sprintf("無法查詢目前權限：%v", adminErr),
			"確認目前帳號可查詢 Process Token，並以管理員身分執行 back-EZ")
	case admin:
		add("admin_token", true, true, "已以管理員權限執行（UAC 已提升）", "")
	default:
		add("admin_token", true, false,
			"未以管理員權限執行（UAC token 未提升）",
			"以滑鼠右鍵選擇「以系統管理員身分執行」啟動 back-EZ")
	}

	// 3. The current BCD boot entry must already use native VHDX boot.
	bcdOut, bcdErr := p.BCDOutput()
	switch {
	case bcdErr != nil:
		add("vhdx_native_boot", true, false,
			"無法讀取 BCD（通常需要管理員權限）",
			"請以系統管理員身分執行 back-EZ，並確認 `bcdedit /enum {current}` 可正常執行")
	case strings.Contains(strings.ToLower(bcdOut), "vhd="):
		add("vhdx_native_boot", true, true,
			"BCD 目前開機項已設定 vhd=，支援 VHDX 原生開機", "")
	default:
		add("vhdx_native_boot", true, false,
			"BCD 目前開機項未設定 vhd=，尚未使用 VHDX 原生開機",
			"以管理員權限執行 bcdedit，將開機項 device/osdevice 指向 base.vhdx（B 案真實寫入）")
	}

	// 4. base.vhdx must exist in the reserve area.
	basePath := filepath.Join(root, "base.vhdx")
	baseExists, baseIsDir, baseErr := p.Stat(basePath)
	switch {
	case baseErr != nil:
		add("base_vhdx_present", true, false,
			fmt.Sprintf("無法檢查 base.vhdx：%v", baseErr),
			"確認保留區可讀取，並以管理員權限執行")
	case !baseExists:
		add("base_vhdx_present", true, false,
			fmt.Sprintf("找不到 base.vhdx（%s）", basePath),
			"先建立保留區並放置 base.vhdx（可由母機派送或手動建立）")
	case baseIsDir:
		add("base_vhdx_present", true, false,
			fmt.Sprintf("base.vhdx 是目錄而非檔案（%s）", basePath),
			"移除同名目錄，改放置 base.vhdx 虛擬磁碟檔案")
	default:
		add("base_vhdx_present", true, true,
			fmt.Sprintf("找到 base.vhdx（%s）", basePath), "")
	}

	// 5. Resolve the reserve volume GUID and its relative path.
	volumeRoot := filepath.VolumeName(root) + `\`
	volume, volumeErr := p.VolumeNameForMountPoint(volumeRoot)
	switch {
	case volumeErr != nil:
		add("reserve_volume_resolved", true, false,
			fmt.Sprintf("無法解析保留區磁碟區 %q：%v", volumeRoot, volumeErr),
			"確認保留區位於本機固定磁碟（NTFS），並以管理員權限執行")
	case strings.TrimSpace(volume) == "":
		add("reserve_volume_resolved", true, false,
			fmt.Sprintf("保留區磁碟區 %q 解析結果為空", volumeRoot),
			"確認保留區位於本機固定磁碟（NTFS），並以管理員權限執行")
	default:
		caps.ReserveVolume = volume
		caps.ReserveRelPath = reserveRelPath(root)
		add("reserve_volume_resolved", true, true,
			fmt.Sprintf("保留區磁碟區解析成功：%s（相對路徑 %s）", volume, caps.ReserveRelPath), "")
	}

	// 6. The reserve directory must exist and be writable.
	writeErr := p.Writable(root)
	switch {
	case writeErr == nil:
		add("reserve_writable", true, true,
			fmt.Sprintf("可在保留區建立與刪除暫存檔（%s）", root), "")
	case errors.Is(writeErr, os.ErrNotExist):
		add("reserve_writable", true, false,
			fmt.Sprintf("保留區目錄不存在（%s）", root),
			"先建立保留區")
	default:
		add("reserve_writable", true, false,
			fmt.Sprintf("保留區不可寫入（%s）：%v", root, writeErr),
			"確認保留區存在且具備寫入權限（NTFS ACL／防寫保護）")
	}

	// 7. The reserve area needs at least 20 GiB free.
	freeBytes, freeErr := p.FreeBytes(root)
	switch {
	case freeErr != nil:
		add("reserve_space", true, false,
			fmt.Sprintf("無法查詢保留區可用空間：%v", freeErr),
			"確認保留區所在磁碟可存取，至少需要 20 GiB 可用空間")
	case freeBytes < reserveSpaceRequiredBytes:
		add("reserve_space", true, false,
			fmt.Sprintf("保留區可用空間不足：%.1f GiB（需 ≥ 20 GiB）", bytesToGiB(freeBytes)),
			"清理保留區或更換磁碟，至少需要 20 GiB 可用空間")
	default:
		add("reserve_space", true, true,
			fmt.Sprintf("保留區可用空間充足：%.1f GiB（需 ≥ 20 GiB）", bytesToGiB(freeBytes)), "")
	}

	// 8. Fast startup (hiberboot) must be disabled for native VHDX switching.
	hiberboot, hiberbootPresent, hiberbootErr := p.HiberbootEnabled()
	switch {
	case hiberbootErr != nil:
		add("fast_startup_disabled", true, false,
			fmt.Sprintf("無法讀取快速啟動設定：%v", hiberbootErr),
			"控制台→電源選項→關閉快速啟動")
	case !hiberbootPresent || hiberboot == 0:
		add("fast_startup_disabled", true, true,
			"快速啟動已關閉（HiberbootEnabled 未設定或為 0）", "")
	default:
		add("fast_startup_disabled", true, false,
			fmt.Sprintf("快速啟動已開啟（HiberbootEnabled=%d）", hiberboot),
			"控制台→電源選項→關閉快速啟動")
	}

	// 9. BitLocker must not block offline VHDX handling (optional).
	switch {
	case adminErr != nil || !admin:
		add("bitlocker_disabled", false, false,
			"需管理員權限確認",
			"以管理員身分執行 back-EZ 以確認 BitLocker 狀態")
	default:
		bitLockerOut, bitLockerErr := p.BitLockerStatus()
		switch {
		case bitLockerErr != nil:
			add("bitlocker_disabled", false, false,
				fmt.Sprintf("BitLocker 狀態未知（manage-bde 執行失敗：%v）", bitLockerErr),
				"手動執行 manage-bde -status 確認保護狀態")
		case bitLockerProtectionOff(bitLockerOut):
			add("bitlocker_disabled", false, true,
				"BitLocker 未啟用保護", "")
		case bitLockerProtectionOn(bitLockerOut):
			add("bitlocker_disabled", false, false,
				"BitLocker 保護已開啟，離線掛載／還原可能受阻",
				"以 manage-bde -protectors -disable 暫停保護，或先解密保留區磁碟")
		default:
			add("bitlocker_disabled", false, false,
				"BitLocker 狀態未知（無法從 manage-bde 輸出判斷）",
				"手動執行 manage-bde -status 確認保護狀態")
		}
	}

	// 10. virtdisk.dll must expose CreateVirtualDisk / OpenVirtualDisk (optional).
	virtOK, virtDetail := p.VirtualDiskAPI()
	if virtOK {
		if strings.TrimSpace(virtDetail) == "" {
			virtDetail = "virtdisk.dll 已載入，且 CreateVirtualDisk／OpenVirtualDisk 可解析"
		}
		add("virtdisk_api", false, true, virtDetail, "")
	} else {
		if strings.TrimSpace(virtDetail) == "" {
			virtDetail = "virtdisk.dll 無法載入或缺少必要程序"
		}
		add("virtdisk_api", false, false, virtDetail,
			"確認系統為 Windows 10 1809 以上（virtdisk.dll／CreateVirtualDisk 可用）")
	}

	// 11. The local PE image must be present (optional).
	pePath := filepath.Join(root, "PE", "boot.wim")
	peExists, peIsDir, peErr := p.Stat(pePath)
	switch {
	case peErr != nil:
		add("pe_image_present", false, false,
			fmt.Sprintf("無法檢查 PE 映像：%v", peErr),
			fmt.Sprintf("確認保留區可讀取，並建立 PE 映像：%s", pePath))
	case !peExists:
		add("pe_image_present", false, false,
			fmt.Sprintf("找不到 PE 映像（%s）", pePath),
			fmt.Sprintf("建立 PE 映像：%s", pePath))
	case peIsDir:
		add("pe_image_present", false, false,
			fmt.Sprintf("PE 映像路徑是目錄而非檔案（%s）", pePath),
			fmt.Sprintf("移除同名目錄，改放置 boot.wim：%s", pePath))
	default:
		add("pe_image_present", false, true,
			fmt.Sprintf("找到 PE 映像（%s）", pePath), "")
	}

	// 12. Real writes are not implemented yet (A plan gate).
	if RealExecutionImplemented {
		add("real_execution_enabled", true, true, "真實寫入已啟用（B 案）", "")
	} else {
		add("real_execution_enabled", true, false,
			"A 案尚未啟用真實寫入（B 案）",
			"待 B 案啟用真實寫入後本項才會通過；現階段僅支援 DryRun")
	}

	caps.Items = items
	for _, item := range items {
		if item.Required && !item.OK {
			caps.Blockers = append(caps.Blockers, item)
		}
	}
	caps.Ready = len(caps.Blockers) == 0
	return caps
}

// normalizeReserveRoot trims and cleans the reserve root, falling back to
// defaultReserveRoot when empty.
func normalizeReserveRoot(reserveRoot string) string {
	root := strings.TrimSpace(reserveRoot)
	if root == "" {
		return defaultReserveRoot
	}
	return filepath.Clean(root)
}

// reserveRelPath returns the reserve root without its drive/volume prefix and
// leading separators, e.g. `D:\EZRestore` -> `EZRestore`.
func reserveRelPath(root string) string {
	cleaned := filepath.Clean(root)
	rel := strings.TrimPrefix(cleaned, filepath.VolumeName(cleaned))
	return strings.TrimLeft(rel, `\`)
}

// isSupportedEdition reports whether the registry EditionID denotes a SKU that
// supports native VHDX boot: Professional, Enterprise or Education (including
// their *N variants).
func isSupportedEdition(editionID string) bool {
	normalized := strings.ToLower(strings.TrimSpace(editionID))
	return strings.Contains(normalized, "professional") ||
		strings.Contains(normalized, "enterprise") ||
		strings.Contains(normalized, "education")
}

// bytesToGiB converts a byte count to GiB for human-readable detail text.
func bytesToGiB(b uint64) float64 {
	return float64(b) / float64(uint64(1)<<30)
}

// bitLockerProtectionOff reports whether manage-bde output indicates that
// BitLocker protection is disabled.
func bitLockerProtectionOff(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "protection off") ||
		strings.Contains(lower, "fully decrypted") ||
		strings.Contains(output, "已關閉") ||
		strings.Contains(output, "完全解密")
}

// bitLockerProtectionOn reports whether manage-bde output indicates that
// BitLocker protection is enabled.
func bitLockerProtectionOn(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "protection on") ||
		strings.Contains(lower, "protection status: on") ||
		strings.Contains(output, "已開啟")
}

// windowsProbe is the production capabilityProbe backed by the Windows API and
// registry.
type windowsProbe struct{}

// newWindowsProbe returns the default, real-machine capability probe.
func newWindowsProbe() *windowsProbe {
	return &windowsProbe{}
}

var _ capabilityProbe = (*windowsProbe)(nil)

func (windowsProbe) CurrentVersion() (string, string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "", "", err
	}
	defer key.Close()

	editionID, _, err := key.GetStringValue("EditionID")
	if err != nil {
		return "", "", err
	}

	build, _, buildErr := key.GetStringValue("CurrentBuildNumber")
	if buildErr != nil || build == "" {
		if currentBuild, _, err := key.GetStringValue("CurrentBuild"); err == nil {
			build = currentBuild
		}
	}
	return editionID, build, nil
}

func (windowsProbe) IsAdmin() (bool, error) {
	return windows.GetCurrentProcessToken().IsElevated(), nil
}

func (windowsProbe) BCDOutput() (string, error) {
	return runCapabilityCommand(capabilityCommandTimeout, "bcdedit", "/enum", "{current}")
}

func (windowsProbe) Stat(path string) (bool, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, false, nil
		}
		return false, false, err
	}
	return true, info.IsDir(), nil
}

func (windowsProbe) VolumeNameForMountPoint(mountPoint string) (string, error) {
	mountPtr, err := windows.UTF16PtrFromString(mountPoint)
	if err != nil {
		return "", err
	}
	var buffer [windows.MAX_PATH + 1]uint16
	if err := windows.GetVolumeNameForVolumeMountPoint(mountPtr, &buffer[0], uint32(len(buffer))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:]), nil
}

func (windowsProbe) FreeBytes(path string) (uint64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &freeBytesAvailable, &totalBytes, &totalFreeBytes); err != nil {
		return 0, err
	}
	return freeBytesAvailable, nil
}

func (windowsProbe) HiberbootEnabled() (uint32, bool, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Session Manager\Power`, registry.QUERY_VALUE)
	if err != nil {
		return 0, false, err
	}
	defer key.Close()

	value, _, err := key.GetIntegerValue("HiberbootEnabled")
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return uint32(value), true, nil
}

func (windowsProbe) BitLockerStatus() (string, error) {
	return runCapabilityCommand(capabilityCommandTimeout, "manage-bde", "-status")
}

func (windowsProbe) VirtualDiskAPI() (bool, string) {
	dll := windows.NewLazySystemDLL("virtdisk.dll")
	if err := dll.Load(); err != nil {
		return false, fmt.Sprintf("無法載入 virtdisk.dll：%v", err)
	}
	for _, name := range []string{"CreateVirtualDisk", "OpenVirtualDisk"} {
		if err := dll.NewProc(name).Find(); err != nil {
			return false, fmt.Sprintf("virtdisk.dll 缺少 %s：%v", name, err)
		}
	}
	return true, "virtdisk.dll 已載入，且 CreateVirtualDisk／OpenVirtualDisk 可解析"
}

func (windowsProbe) Writable(dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".back-ez-write-probe-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

// runCapabilityCommand runs an external probe command with a hard timeout and a
// hidden console window.
func runCapabilityCommand(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return string(out), fmt.Errorf("%s 逾時（%s）：%w", name, timeout, ctxErr)
	}
	if err != nil {
		return string(out), fmt.Errorf("%s 執行失敗：%w", name, err)
	}
	return string(out), nil
}
