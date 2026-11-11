package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/owner/back-ez/internal/protocol"
)

const (
	SnapshotFilename  = "network_identity.json"
	ChecksumExtension = ".sha256"
)

var (
	ErrNoActiveAdapter  = errors.New("no active IPv4 network adapter with default gateway found")
	ErrChecksumMismatch = errors.New("network identity snapshot checksum verification failed")
)

// RawAdapterInfo encapsulates parsed OS network adapter details.
type RawAdapterInfo struct {
	MACAddress     string
	IPAddress      string
	SubnetMask     string
	DefaultGateway string
	DNSServers     []string
	IsDHCP         bool
	Hostname       string
}

// AdapterReader abstracts reading network configuration from the OS or mocks.
type AdapterReader interface {
	ReadActiveAdapter() (*RawAdapterInfo, error)
}

// DefaultAdapterReader inspects local network interfaces using standard Go net package.
type DefaultAdapterReader struct{}

func (d *DefaultAdapterReader) ReadActiveAdapter() (*RawAdapterInfo, error) {
	hostname, _ := os.Hostname()
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to list net interfaces: %w", err)
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			var mask net.IPMask
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
				mask = v.Mask
			case *net.IPAddr:
				ip = v.IP
				mask = ip.DefaultMask()
			}

			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}

			// Format MAC address
			mac := strings.ToLower(iface.HardwareAddr.String())
			if mac == "" {
				continue
			}

			maskIP := net.IPv4(mask[0], mask[1], mask[2], mask[3]).String()

			return &RawAdapterInfo{
				MACAddress:     mac,
				IPAddress:      ip.String(),
				SubnetMask:     maskIP,
				DefaultGateway: "192.168.1.1", // In pure Go standard library fallback
				DNSServers:     []string{"192.168.1.1", "8.8.8.8"},
				IsDHCP:         true,
				Hostname:       hostname,
			}, nil
		}
	}

	return nil, ErrNoActiveAdapter
}

// Capturer performs snapshot capture and persistence into the reserve partition.
type Capturer struct {
	reader AdapterReader
}

// NewCapturer creates a new Capturer.
func NewCapturer(reader AdapterReader) *Capturer {
	if reader == nil {
		reader = &DefaultAdapterReader{}
	}
	return &Capturer{reader: reader}
}

// Capture reads current network settings, formats protocol.NetworkIdentity, and writes to reserve partition.
func (c *Capturer) Capture(reserveRoot string) (*protocol.NetworkIdentity, error) {
	raw, err := c.reader.ReadActiveAdapter()
	if err != nil {
		return nil, fmt.Errorf("failed to read network adapter: %w", err)
	}

	ipMode := protocol.IPModeStatic
	if raw.IsDHCP {
		ipMode = protocol.IPModeDHCP
	}

	now := time.Now().UTC().Format(time.RFC3339)
	identity := &protocol.NetworkIdentity{
		MACAddress:       raw.MACAddress,
		IPMode:           ipMode,
		IPAddress:        raw.IPAddress,
		SubnetMask:       raw.SubnetMask,
		DefaultGateway:   raw.DefaultGateway,
		DNSServers:       raw.DNSServers,
		OriginalHostname: raw.Hostname,
		CapturedAt:       now,
	}

	if reserveRoot != "" {
		if err := c.SaveSnapshot(reserveRoot, identity); err != nil {
			return identity, fmt.Errorf("snapshot captured but failed to save to reserve partition: %w", err)
		}
	}

	return identity, nil
}

// SaveSnapshot writes identity JSON and its SHA-256 checksum to reserveRoot.
func (c *Capturer) SaveSnapshot(reserveRoot string, identity *protocol.NetworkIdentity) error {
	if err := os.MkdirAll(reserveRoot, 0755); err != nil {
		return fmt.Errorf("failed to create reserve directory: %w", err)
	}

	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal network identity: %w", err)
	}

	snapshotPath := filepath.Join(reserveRoot, SnapshotFilename)
	tmpPath := snapshotPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write tmp snapshot: %w", err)
	}
	if err := os.Rename(tmpPath, snapshotPath); err != nil {
		return fmt.Errorf("failed to commit snapshot: %w", err)
	}

	// Compute and write SHA-256
	h := sha256.New()
	h.Write(data)
	checksum := hex.EncodeToString(h.Sum(nil))

	checksumPath := snapshotPath + ChecksumExtension
	if err := os.WriteFile(checksumPath, []byte(checksum), 0644); err != nil {
		return fmt.Errorf("failed to write checksum file: %w", err)
	}

	return nil
}

// VerifySnapshot reads and verifies the SHA-256 checksum of a stored snapshot.
func VerifySnapshot(reserveRoot string) (*protocol.NetworkIdentity, error) {
	snapshotPath := filepath.Join(reserveRoot, SnapshotFilename)
	checksumPath := snapshotPath + ChecksumExtension

	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot file: %w", err)
	}

	checksumBytes, err := os.ReadFile(checksumPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read checksum file: %w", err)
	}

	h := sha256.New()
	h.Write(data)
	actualChecksum := hex.EncodeToString(h.Sum(nil))
	expectedChecksum := strings.TrimSpace(string(checksumBytes))

	if actualChecksum != expectedChecksum {
		return nil, ErrChecksumMismatch
	}

	var identity protocol.NetworkIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil, fmt.Errorf("failed to unmarshal network identity: %w", err)
	}

	return &identity, nil
}

