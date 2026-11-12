package homesku

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
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
	if edition := DetectEditionFromString(editionID); edition != EditionUnknown {
		return edition, nil
	}
	// Preserve the historical fallback: unrecognized EditionID strings are
	// returned verbatim instead of being collapsed to Unknown.
	return Edition(editionID), nil
}

// DetectEditionFromString maps a raw registry EditionID value to a normalized
// Edition. It performs no I/O and is the single source of the edition mapping.
func DetectEditionFromString(editionID string) Edition {
	switch {
	case strings.Contains(strings.ToLower(editionID), "core"),
		strings.Contains(strings.ToLower(editionID), "home"):
		return EditionHome
	case strings.Contains(strings.ToLower(editionID), "pro"):
		return EditionPro
	case strings.Contains(strings.ToLower(editionID), "enterprise"):
		return EditionEnterprise
	case strings.Contains(strings.ToLower(editionID), "education"):
		return EditionEducation
	default:
		return EditionUnknown
	}
}

// DetectEditionFromRegistry reads the EditionID value directly from the
// registry (no PowerShell child process) and normalizes it with
// DetectEditionFromString.
func DetectEditionFromRegistry() (Edition, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return EditionUnknown, fmt.Errorf("failed to open CurrentVersion registry key: %w", err)
	}
	defer key.Close()

	editionID, _, err := key.GetStringValue("EditionID")
	if err != nil {
		return EditionUnknown, fmt.Errorf("failed to read registry EditionID: %w", err)
	}
	return DetectEditionFromString(strings.TrimSpace(editionID)), nil
}

// IsHomeEdition returns true if the current system is Windows Home edition.
func (d *Detector) IsHomeEdition() bool {
	ed, err := d.DetectEdition()
	if err != nil {
		return false
	}
	return ed == EditionHome
}
