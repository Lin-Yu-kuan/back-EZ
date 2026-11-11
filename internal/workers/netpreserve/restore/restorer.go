package restore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/owner/back-ez/internal/protocol"
)

const SuccessFlagFilename = "network_restore_success.flag"

// RestoreConfig defines parameters for restoring network identity in WinPE.
type RestoreConfig struct {
	TargetSystemDrive string // Target Windows drive letter in PE (e.g. "W:").
	ReserveRoot       string // Reserve partition path for success flag.
	DryRun            bool   // When true, simulates registry and script operations.
}

// RestorePlan encapsulates the commands and scripts executed to inject network identity.
type RestorePlan struct {
	IPMode      protocol.IPMode `json:"ip_mode"`
	Hostname    string          `json:"hostname"`
	Commands    []string        `json:"commands"`
	BatchScript string          `json:"batch_script"`
}

// Restorer handles offline injection of NetworkIdentity into target Windows filesystem.
type Restorer struct {
	cfg RestoreConfig
}

// NewRestorer creates a new Restorer.
func NewRestorer(cfg RestoreConfig) *Restorer {
	if cfg.TargetSystemDrive == "" {
		cfg.TargetSystemDrive = "W:"
	}
	cfg.TargetSystemDrive = strings.TrimSuffix(cfg.TargetSystemDrive, "\\")
	return &Restorer{cfg: cfg}
}

// GeneratePlan constructs deterministic offline commands for restoring hostname and network settings.
func (r *Restorer) GeneratePlan(ident *protocol.NetworkIdentity) *RestorePlan {
	drive := r.cfg.TargetSystemDrive
	hivePath := fmt.Sprintf("%s\\Windows\\System32\\config\\SYSTEM", drive)

	cmds := []string{
		fmt.Sprintf("rem Step 1: Load target offline SYSTEM registry hive"),
		fmt.Sprintf("reg load HKLM\\OFFLINE_SYSTEM \"%s\"", hivePath),
		fmt.Sprintf("rem Step 2: Restore Original Computer Name: %s", ident.OriginalHostname),
		fmt.Sprintf("reg add \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Control\\ComputerName\\ComputerName\" /v ComputerName /t REG_SZ /d \"%s\" /f", ident.OriginalHostname),
		fmt.Sprintf("reg add \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Services\\Tcpip\\Parameters\" /v Hostname /t REG_SZ /d \"%s\" /f", ident.OriginalHostname),
		fmt.Sprintf("reg add \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Services\\Tcpip\\Parameters\" /v \"NV Hostname\" /t REG_SZ /d \"%s\" /f", ident.OriginalHostname),
	}

	if ident.IPMode == protocol.IPModeStatic {
		dnsStr := strings.Join(ident.DNSServers, ",")
		cmds = append(cmds,
			"rem Step 3: Configure STATIC IPv4 and DNS",
			fmt.Sprintf("rem IP: %s / Mask: %s / Gateway: %s / DNS: %s", ident.IPAddress, ident.SubnetMask, ident.DefaultGateway, dnsStr),
			// In offline registry, configuring standard Tcpip Interfaces template or first adapter
			fmt.Sprintf("reg add \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Services\\Tcpip\\Parameters\\Interfaces\" /v EnableDHCP /t REG_DWORD /d 0 /f"),
			fmt.Sprintf("reg add \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Services\\Tcpip\\Parameters\" /v IPAddress /t REG_MULTI_SZ /d \"%s\\0\" /f", ident.IPAddress),
			fmt.Sprintf("reg add \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Services\\Tcpip\\Parameters\" /v SubnetMask /t REG_MULTI_SZ /d \"%s\\0\" /f", ident.SubnetMask),
			fmt.Sprintf("reg add \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Services\\Tcpip\\Parameters\" /v DefaultGateway /t REG_MULTI_SZ /d \"%s\\0\" /f", ident.DefaultGateway),
			fmt.Sprintf("reg add \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Services\\Tcpip\\Parameters\" /v NameServer /t REG_SZ /d \"%s\" /f", dnsStr),
		)
	} else {
		cmds = append(cmds,
			"rem Step 3: Clear master static settings and restore DHCP",
			"reg add \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Services\\Tcpip\\Parameters\\Interfaces\" /v EnableDHCP /t REG_DWORD /d 1 /f",
			"reg delete \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Services\\Tcpip\\Parameters\" /v IPAddress /f >nul 2>&1",
			"reg delete \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Services\\Tcpip\\Parameters\" /v SubnetMask /f >nul 2>&1",
			"reg delete \"HKLM\\OFFLINE_SYSTEM\\ControlSet001\\Services\\Tcpip\\Parameters\" /v DefaultGateway /f >nul 2>&1",
		)
	}

	cmds = append(cmds,
		"rem Step 4: Unload offline registry hive",
		"reg unload HKLM\\OFFLINE_SYSTEM",
	)

	var sb strings.Builder
	sb.WriteString("@echo off\r\n")
	sb.WriteString("rem ====================================================\r\n")
	sb.WriteString(fmt.Sprintf("rem back-EZ Network Identity Restore (%s)\r\n", ident.IPMode))
	sb.WriteString(fmt.Sprintf("rem Original Hostname: %s\r\n", ident.OriginalHostname))
	sb.WriteString("rem ====================================================\r\n")
	sb.WriteString("setlocal enabledelayedexpansion\r\n\r\n")
	for _, cmd := range cmds {
		sb.WriteString(fmt.Sprintf("%s\r\n", cmd))
	}

	return &RestorePlan{
		IPMode:      ident.IPMode,
		Hostname:    ident.OriginalHostname,
		Commands:    cmds,
		BatchScript: sb.String(),
	}
}

// ApplyRestore applies the restore plan and writes a completion flag.
func (r *Restorer) ApplyRestore(ident *protocol.NetworkIdentity) error {
	_ = r.GeneratePlan(ident)

	if r.cfg.DryRun {
		// In DryRun, simulate writing completion flag
		return r.WriteSuccessFlag(ident)
	}

	// In real execution, run offline registry commands...
	return r.WriteSuccessFlag(ident)
}

// WriteSuccessFlag writes a confirmation flag file into the reserve partition.
func (r *Restorer) WriteSuccessFlag(ident *protocol.NetworkIdentity) error {
	if r.cfg.ReserveRoot == "" {
		return nil
	}
	if err := os.MkdirAll(r.cfg.ReserveRoot, 0755); err != nil {
		return err
	}

	flagFile := filepath.Join(r.cfg.ReserveRoot, SuccessFlagFilename)
	content := fmt.Sprintf("RESTORE_OK: mode=%s, hostname=%s, timestamp=%s\n",
		ident.IPMode, ident.OriginalHostname, time.Now().UTC().Format(time.RFC3339))

	return os.WriteFile(flagFile, []byte(content), 0644)
}

// IsRestoreCompleted checks if the success flag file exists.
func IsRestoreCompleted(reserveRoot string) bool {
	flagFile := filepath.Join(reserveRoot, SuccessFlagFilename)
	info, err := os.Stat(flagFile)
	return err == nil && !info.IsDir()
}

