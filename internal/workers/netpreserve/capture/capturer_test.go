package capture_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/owner/back-ez/internal/protocol"
	"github.com/owner/back-ez/internal/workers/netpreserve/capture"
)

type mockAdapterReader struct {
	raw *capture.RawAdapterInfo
	err error
}

func (m *mockAdapterReader) ReadActiveAdapter() (*capture.RawAdapterInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.raw, nil
}

func TestCapturer_CaptureStatic(t *testing.T) {
	tmpDir := t.TempDir()

	mock := &mockAdapterReader{
		raw: &capture.RawAdapterInfo{
			MACAddress:     "aa:bb:cc:11:22:33",
			IPAddress:      "192.168.1.101",
			SubnetMask:     "255.255.255.0",
			DefaultGateway: "192.168.1.1",
			DNSServers:     []string{"192.168.1.1", "8.8.8.8"},
			IsDHCP:         false,
			Hostname:       "LAB-PC-01",
		},
	}

	c := capture.NewCapturer(mock)
	ident, err := c.Capture(tmpDir)
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}

	if ident.IPMode != protocol.IPModeStatic {
		t.Fatalf("expected STATIC, got %s", ident.IPMode)
	}
	if ident.OriginalHostname != "LAB-PC-01" {
		t.Fatalf("expected hostname LAB-PC-01, got %s", ident.OriginalHostname)
	}
	if ident.IPAddress != "192.168.1.101" {
		t.Fatalf("expected IP 192.168.1.101, got %s", ident.IPAddress)
	}

	// Verify local snapshot integrity with checksum
	verified, err := capture.VerifySnapshot(tmpDir)
	if err != nil {
		t.Fatalf("verify snapshot failed: %v", err)
	}
	if verified.IPAddress != ident.IPAddress || verified.OriginalHostname != ident.OriginalHostname {
		t.Fatalf("verified snapshot does not match captured: %+v vs %+v", verified, ident)
	}
}

func TestCapturer_CaptureDHCP(t *testing.T) {
	tmpDir := t.TempDir()

	mock := &mockAdapterReader{
		raw: &capture.RawAdapterInfo{
			MACAddress:     "00:11:22:33:44:55",
			IPAddress:      "10.0.0.45",
			SubnetMask:     "255.255.0.0",
			DefaultGateway: "10.0.0.1",
			DNSServers:     []string{"10.0.0.1"},
			IsDHCP:         true,
			Hostname:       "DHCP-CLIENT-02",
		},
	}

	c := capture.NewCapturer(mock)
	ident, err := c.Capture(tmpDir)
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}

	if ident.IPMode != protocol.IPModeDHCP {
		t.Fatalf("expected DHCP, got %s", ident.IPMode)
	}
	if ident.OriginalHostname != "DHCP-CLIENT-02" {
		t.Fatalf("expected hostname DHCP-CLIENT-02, got %s", ident.OriginalHostname)
	}

	// Tamper file to test checksum verification failure
	snapshotFile := filepath.Join(tmpDir, capture.SnapshotFilename)
	_ = os.WriteFile(snapshotFile, []byte(`{"tampered": true}`), 0644)

	_, err = capture.VerifySnapshot(tmpDir)
	if err == nil {
		t.Fatalf("expected error on tampered snapshot, got nil")
	}
	if err != capture.ErrChecksumMismatch {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
}

