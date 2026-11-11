package restore_test

import (
	"strings"
	"testing"

	"github.com/owner/back-ez/internal/protocol"
	"github.com/owner/back-ez/internal/workers/netpreserve/restore"
)

func TestRestorer_StaticGolden(t *testing.T) {
	cfg := restore.RestoreConfig{
		TargetSystemDrive: "W:",
		DryRun:            true,
	}
	r := restore.NewRestorer(cfg)

	ident := &protocol.NetworkIdentity{
		MACAddress:       "aa:bb:cc:11:22:33",
		IPMode:           protocol.IPModeStatic,
		IPAddress:        "192.168.1.101",
		SubnetMask:       "255.255.255.0",
		DefaultGateway:   "192.168.1.1",
		DNSServers:       []string{"192.168.1.1", "8.8.8.8"},
		OriginalHostname: "LAB-PC-01",
	}

	plan := r.GeneratePlan(ident)

	expectedSnippets := []string{
		`reg load HKLM\OFFLINE_SYSTEM "W:\Windows\System32\config\SYSTEM"`,
		`reg add "HKLM\OFFLINE_SYSTEM\ControlSet001\Control\ComputerName\ComputerName" /v ComputerName /t REG_SZ /d "LAB-PC-01" /f`,
		`reg add "HKLM\OFFLINE_SYSTEM\ControlSet001\Services\Tcpip\Parameters" /v Hostname /t REG_SZ /d "LAB-PC-01" /f`,
		`reg add "HKLM\OFFLINE_SYSTEM\ControlSet001\Services\Tcpip\Parameters\Interfaces" /v EnableDHCP /t REG_DWORD /d 0 /f`,
		`reg add "HKLM\OFFLINE_SYSTEM\ControlSet001\Services\Tcpip\Parameters" /v IPAddress /t REG_MULTI_SZ /d "192.168.1.101\0" /f`,
		`reg add "HKLM\OFFLINE_SYSTEM\ControlSet001\Services\Tcpip\Parameters" /v SubnetMask /t REG_MULTI_SZ /d "255.255.255.0\0" /f`,
		`reg add "HKLM\OFFLINE_SYSTEM\ControlSet001\Services\Tcpip\Parameters" /v DefaultGateway /t REG_MULTI_SZ /d "192.168.1.1\0" /f`,
		`reg add "HKLM\OFFLINE_SYSTEM\ControlSet001\Services\Tcpip\Parameters" /v NameServer /t REG_SZ /d "192.168.1.1,8.8.8.8" /f`,
		`reg unload HKLM\OFFLINE_SYSTEM`,
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(plan.BatchScript, snippet) {
			t.Errorf("expected plan to contain %q, but did not.\nFull script:\n%s", snippet, plan.BatchScript)
		}
	}
}

func TestRestorer_DHCPGolden(t *testing.T) {
	cfg := restore.RestoreConfig{
		TargetSystemDrive: "W:",
		DryRun:            true,
	}
	r := restore.NewRestorer(cfg)

	ident := &protocol.NetworkIdentity{
		MACAddress:       "00:11:22:33:44:55",
		IPMode:           protocol.IPModeDHCP,
		IPAddress:        "10.0.0.50",
		SubnetMask:       "255.255.0.0",
		DefaultGateway:   "10.0.0.1",
		DNSServers:       []string{"10.0.0.1"},
		OriginalHostname: "DHCP-ROOM-05",
	}

	plan := r.GeneratePlan(ident)

	expectedSnippets := []string{
		`reg load HKLM\OFFLINE_SYSTEM "W:\Windows\System32\config\SYSTEM"`,
		`reg add "HKLM\OFFLINE_SYSTEM\ControlSet001\Control\ComputerName\ComputerName" /v ComputerName /t REG_SZ /d "DHCP-ROOM-05" /f`,
		`reg add "HKLM\OFFLINE_SYSTEM\ControlSet001\Services\Tcpip\Parameters\Interfaces" /v EnableDHCP /t REG_DWORD /d 1 /f`,
		`reg delete "HKLM\OFFLINE_SYSTEM\ControlSet001\Services\Tcpip\Parameters" /v IPAddress /f >nul 2>&1`,
		`reg delete "HKLM\OFFLINE_SYSTEM\ControlSet001\Services\Tcpip\Parameters" /v SubnetMask /f >nul 2>&1`,
		`reg delete "HKLM\OFFLINE_SYSTEM\ControlSet001\Services\Tcpip\Parameters" /v DefaultGateway /f >nul 2>&1`,
		`reg unload HKLM\OFFLINE_SYSTEM`,
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(plan.BatchScript, snippet) {
			t.Errorf("expected DHCP plan to contain %q, but did not.\nFull script:\n%s", snippet, plan.BatchScript)
		}
	}
}

func TestRestorer_ApplyAndSuccessFlag(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := restore.RestoreConfig{
		ReserveRoot: tmpDir,
		DryRun:      true,
	}
	r := restore.NewRestorer(cfg)

	if restore.IsRestoreCompleted(tmpDir) {
		t.Fatalf("expected restore not completed before apply")
	}

	ident := &protocol.NetworkIdentity{
		IPMode:           protocol.IPModeStatic,
		OriginalHostname: "TEST-HOST",
	}

	if err := r.ApplyRestore(ident); err != nil {
		t.Fatalf("ApplyRestore failed: %v", err)
	}

	if !restore.IsRestoreCompleted(tmpDir) {
		t.Fatalf("expected restore completed after apply")
	}
}

