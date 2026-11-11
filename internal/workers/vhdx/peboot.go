package vhdx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/owner/back-ez/internal/protocol"
)

// Default PE Boot GUID in Windows BCD.
const DefaultPEGUID = "{7619d4e3-6532-4347-8a64-00e08174acbc}"

// PEBootConfig defines parameters for managing local Windows PE BCD ramdisk entries.
type PEBootConfig struct {
	DryRun           bool   `json:"dry_run"`
	AllowDestructive bool   `json:"allow_destructive"`
	VolumeGUID       string `json:"volume_guid"`
	ReserveRoot      string `json:"reserve_root"`       // e.g. "EZRestore"
	PEWIMRelPath     string `json:"pe_wim_rel_path"`    // e.g. "PE\boot.wim"
	PESDIRelPath     string `json:"pe_sdi_rel_path"`    // e.g. "boot\boot.sdi"
	PEGUID           string `json:"pe_guid"`            // Target BCD GUID for PE entry
	PhysicalDiskRoot string `json:"physical_disk_root"` // Local path for file existence check (e.g. C:\EZRestore)
}

// PEBootManager coordinates local WinPE BCD boot entries and one-time bootsequence.
type PEBootManager struct {
	mu       sync.Mutex
	cfg      PEBootConfig
	executor CommandExecutor
}

// NewPEBootManager creates a new PEBootManager.
func NewPEBootManager(cfg PEBootConfig, exec CommandExecutor) *PEBootManager {
	if cfg.ReserveRoot == "" {
		cfg.ReserveRoot = "EZRestore"
	}
	if cfg.PEWIMRelPath == "" {
		cfg.PEWIMRelPath = filepath.Join("PE", "boot.wim")
	}
	if cfg.PESDIRelPath == "" {
		cfg.PESDIRelPath = filepath.Join("boot", "boot.sdi")
	}
	if cfg.PEGUID == "" {
		cfg.PEGUID = DefaultPEGUID
	}
	if exec == nil {
		exec = NewDryRunExecutor()
	}

	return &PEBootManager{
		cfg:      cfg,
		executor: exec,
	}
}

// resolvePEPaths calculates volume/BCD paths for WIM and SDI.
func (m *PEBootManager) resolvePEPaths() (wimBCDPath, sdiBCDPath, wimDiskpartPath string) {
	relWIM := strings.TrimPrefix(filepath.Clean(m.cfg.PEWIMRelPath), "\\")
	relSDI := strings.TrimPrefix(filepath.Clean(m.cfg.PESDIRelPath), "\\")

	if m.cfg.VolumeGUID != "" {
		vol := strings.TrimSuffix(m.cfg.VolumeGUID, "\\")
		vol = strings.TrimSuffix(vol, "/")

		wimBCDPath = fmt.Sprintf("[%s]\\%s\\%s", vol, m.cfg.ReserveRoot, relWIM)
		sdiBCDPath = fmt.Sprintf("[%s]\\%s\\%s", vol, m.cfg.ReserveRoot, relSDI)
		wimDiskpartPath = fmt.Sprintf("%s\\%s\\%s", vol, m.cfg.ReserveRoot, relWIM)
	} else {
		wimBCDPath = fmt.Sprintf("[C:]\\%s\\%s", m.cfg.ReserveRoot, relWIM)
		sdiBCDPath = fmt.Sprintf("[C:]\\%s\\%s", m.cfg.ReserveRoot, relSDI)
		wimDiskpartPath = fmt.Sprintf("C:\\%s\\%s", m.cfg.ReserveRoot, relWIM)
	}
	return wimBCDPath, sdiBCDPath, wimDiskpartPath
}

// CheckPEReady verifies whether the PE image exists on disk and is ready for boot.
func (m *PEBootManager) CheckPEReady() (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. If physical disk root is specified, check physical file existence
	if m.cfg.PhysicalDiskRoot != "" {
		wimFile := filepath.Join(m.cfg.PhysicalDiskRoot, m.cfg.PEWIMRelPath)
		info, err := os.Stat(wimFile)
		if err != nil || info.IsDir() {
			return false, fmt.Sprintf("PE image not found at %s", wimFile)
		}
	}

	return true, "PE image is ready"
}

// SetupPEBootEntry creates or updates the BCD ramdiskoptions and PE osloader entry.
func (m *PEBootManager) SetupPEBootEntry() (*ExecutionPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	wimBCD, sdiBCD, _ := m.resolvePEPaths()
	backupBCDPath := fmt.Sprintf("C:\\%s\\bcd_backup_pe_setup.bcd", m.cfg.ReserveRoot)
	if m.cfg.VolumeGUID != "" {
		vol := strings.TrimSuffix(m.cfg.VolumeGUID, "\\")
		backupBCDPath = fmt.Sprintf("%s\\%s\\bcd_backup_pe_setup.bcd", vol, m.cfg.ReserveRoot)
	}

	guid := m.cfg.PEGUID

	steps := []ExecutionStep{
		{
			Order:       1,
			Category:    "BACKUP",
			Command:     fmt.Sprintf("bcdedit /export \"%s\"", backupBCDPath),
			Description: fmt.Sprintf("Backup current BCD store before PE configuration to %s", backupBCDPath),
		},
		{
			Order:       2,
			Category:    "BCD",
			Command:     "bcdedit /create {ramdiskoptions} /d \"back-EZ WinPE Ramdisk\"",
			Description: "Create {ramdiskoptions} object if not already present",
		},
		{
			Order:       3,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /set {ramdiskoptions} ramdisksdidevice partition=%s", sdiBCD),
			Description: fmt.Sprintf("Set ramdisksdidevice to %s", sdiBCD),
		},
		{
			Order:       4,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /set {ramdiskoptions} ramdisksdipath \\%s", strings.TrimPrefix(filepath.Clean(m.cfg.PESDIRelPath), "\\")),
			Description: "Set ramdisksdipath for boot.sdi",
		},
		{
			Order:       5,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /create %s /d \"back-EZ WinPE Recovery\" /application osloader", guid),
			Description: fmt.Sprintf("Create PE osloader entry %s", guid),
		},
		{
			Order:       6,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /set %s device ramdisk=%s,{ramdiskoptions}", guid, wimBCD),
			Description: fmt.Sprintf("Set PE boot entry device ramdisk to %s", wimBCD),
		},
		{
			Order:       7,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /set %s osdevice ramdisk=%s,{ramdiskoptions}", guid, wimBCD),
			Description: fmt.Sprintf("Set PE boot entry osdevice ramdisk to %s", wimBCD),
		},
		{
			Order:       8,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /set %s path \\windows\\system32\\boot\\winload.efi", guid),
			Description: "Set PE boot loader path for UEFI",
		},
		{
			Order:       9,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /set %s systemroot \\windows", guid),
			Description: "Set systemroot to \\windows",
		},
		{
			Order:       10,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /set %s winpe yes", guid),
			Description: "Enable winpe flag",
		},
		{
			Order:       11,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /set %s detecthal yes", guid),
			Description: "Enable detecthal flag",
		},
	}

	plan := &ExecutionPlan{
		Action:        "SETUP_PE_ENTRY",
		TargetPointID: guid,
		BackupBCDPath: backupBCDPath,
		Steps:         steps,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := m.executePlan(plan); err != nil {
		return plan, err
	}
	return plan, nil
}

// TriggerOneTimePEBoot configures the BCD bootsequence to boot into PE once and then reboots.
func (m *PEBootManager) TriggerOneTimePEBoot() (*ExecutionPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	backupBCDPath := fmt.Sprintf("C:\\%s\\bcd_backup_onetime_pe.bcd", m.cfg.ReserveRoot)
	if m.cfg.VolumeGUID != "" {
		vol := strings.TrimSuffix(m.cfg.VolumeGUID, "\\")
		backupBCDPath = fmt.Sprintf("%s\\%s\\bcd_backup_onetime_pe.bcd", vol, m.cfg.ReserveRoot)
	}

	guid := m.cfg.PEGUID

	steps := []ExecutionStep{
		{
			Order:       1,
			Category:    "BACKUP",
			Command:     fmt.Sprintf("bcdedit /export \"%s\"", backupBCDPath),
			Description: fmt.Sprintf("Backup current BCD store before setting one-time boot to %s", backupBCDPath),
		},
		{
			Order:       2,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /bootsequence %s", guid),
			Description: fmt.Sprintf("Configure one-time bootsequence to PE entry %s", guid),
		},
		{
			Order:       3,
			Category:    "SYSTEM",
			Command:     "shutdown /r /t 0 /f",
			Description: "Trigger immediate system reboot into Windows PE",
		},
	}

	plan := &ExecutionPlan{
		Action:        "ONETIME_PE_BOOT",
		TargetPointID: guid,
		BackupBCDPath: backupBCDPath,
		Steps:         steps,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := m.executePlan(plan); err != nil {
		return plan, err
	}
	return plan, nil
}

func (m *PEBootManager) executePlan(plan *ExecutionPlan) error {
	if m.cfg.DryRun {
		return m.executor.Simulate(plan)
	}
	if !m.cfg.AllowDestructive {
		return &VHDXError{
			Code:    protocol.ErrForbidden,
			Message: "destructive boot operations disabled; AllowDestructive must be true",
		}
	}
	return m.executor.Execute(plan)
}

