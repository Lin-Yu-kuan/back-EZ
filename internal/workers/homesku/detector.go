package homesku

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// Edition represents the detected Windows edition.
type Edition string

const (
	EditionHome       Edition = "Home"
	EditionPro        Edition = "Pro"
	EditionEnterprise Edition = "Enterprise"
	EditionEducation  Edition = "Education"
	EditionUnknown    Edition = "Unknown"
)

// ProGVLKKey is Microsoft's official Generic Volume License Key for Windows 10/11 Pro.
// This key triggers an in-place upgrade from Home to Pro without reinstalling Windows.
const ProGVLKKey = "VK7JG-NPHTM-C97JM-9MPGT-3V66T"

// Detector detects the current Windows SKU edition.
type Detector struct{}

func NewDetector() *Detector {
	return &Detector{}
}

// DetectEdition checks registry EditionID or wmic/powershell to determine the Windows edition.
func (d *Detector) DetectEdition() (Edition, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", `(Get-ItemProperty -Path "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion").EditionID`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return EditionUnknown, fmt.Errorf("failed to query edition: %w", err)
	}

	editionID := strings.TrimSpace(string(out))
	switch {
	case strings.Contains(strings.ToLower(editionID), "core"),
		strings.Contains(strings.ToLower(editionID), "home"):
		return EditionHome, nil
	case strings.Contains(strings.ToLower(editionID), "pro"):
		return EditionPro, nil
	case strings.Contains(strings.ToLower(editionID), "enterprise"):
		return EditionEnterprise, nil
	case strings.Contains(strings.ToLower(editionID), "education"):
		return EditionEducation, nil
	default:
		return Edition(editionID), nil
	}
}

// IsHomeEdition returns true if the current system is Windows Home edition.
func (d *Detector) IsHomeEdition() bool {
	ed, err := d.DetectEdition()
	if err != nil {
		return false
	}
	return ed == EditionHome
}

// UnlockPro triggers Microsoft's official changepk.exe with explicit UAC elevation (-Verb RunAs).
func (d *Detector) UnlockPro() error {
	psScript := fmt.Sprintf(`Start-Process "changepk.exe" -ArgumentList "/ProductKey %s" -Verb RunAs`, ProGVLKKey)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

