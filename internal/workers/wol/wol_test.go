package wol

import (
	"bytes"
	"net"
	"testing"
)

func TestBuildMagicPacket_ByteStructure(t *testing.T) {
	tests := []struct {
		name    string
		macStr  string
		wantMAC []byte
	}{
		{
			name:    "Standard colon MAC",
			macStr:  "AA:BB:CC:11:22:33",
			wantMAC: []byte{0xAA, 0xBB, 0xCC, 0x11, 0x22, 0x33},
		},
		{
			name:    "Lowercase colon MAC",
			macStr:  "00:11:22:33:44:55",
			wantMAC: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		},
		{
			name:    "Dash format MAC",
			macStr:  "12-34-56-78-9a-bc",
			wantMAC: []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mac, err := ParseMAC(tc.macStr)
			if err != nil {
				t.Fatalf("ParseMAC failed: %v", err)
			}

			packet, err := BuildMagicPacket(mac)
			if err != nil {
				t.Fatalf("BuildMagicPacket failed: %v", err)
			}

			// 1. Length must be exactly 102 bytes
			if len(packet) != MagicPacketLength {
				t.Fatalf("packet length = %d, want %d", len(packet), MagicPacketLength)
			}

			// 2. First 6 bytes must be 0xFF
			for i := 0; i < 6; i++ {
				if packet[i] != 0xFF {
					t.Errorf("byte %d is 0x%02X, want 0xFF", i, packet[i])
				}
			}

			// 3. Exactly 16 repetitions of 6-byte MAC
			for i := 0; i < 16; i++ {
				chunk := packet[6+i*6 : 6+(i+1)*6]
				if !bytes.Equal(chunk, tc.wantMAC) {
					t.Errorf("iteration %d MAC mismatch: got %X, want %X", i, chunk, tc.wantMAC)
				}
			}
		})
	}
}

func TestCalculateBroadcast_SubnetMasks(t *testing.T) {
	tests := []struct {
		name string
		cidr string
		want string
	}{
		{
			name: "Standard Class C /24",
			cidr: "192.168.1.100/24",
			want: "192.168.1.255",
		},
		{
			name: "Class B /16",
			cidr: "10.0.5.20/16",
			want: "10.0.255.255",
		},
		{
			name: "Custom Class B subnet /22 (4 subnets)",
			cidr: "172.16.4.10/22",
			want: "172.16.7.255",
		},
		{
			name: "Class A /8",
			cidr: "10.1.2.3/8",
			want: "10.255.255.255",
		},
		{
			name: "Small subnet /28",
			cidr: "192.168.50.17/28",
			want: "192.168.50.31",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bcast, err := CalculateBroadcastCIDR(tc.cidr)
			if err != nil {
				t.Fatalf("CalculateBroadcastCIDR failed: %v", err)
			}
			if bcast.String() != tc.want {
				t.Errorf("CalculateBroadcastCIDR(%s) = %s, want %s", tc.cidr, bcast.String(), tc.want)
			}
		})
	}
}

func TestParseMAC_Invalid(t *testing.T) {
	invalidMACs := []string{
		"",
		"invalid",
		"00:11:22:33:44",       // too short
		"00:11:22:33:44:55:66", // too long
		"GG:HH:II:JJ:KK:LL",    // non-hex
	}

	for _, m := range invalidMACs {
		_, err := ParseMAC(m)
		if err == nil {
			t.Errorf("expected error for invalid MAC %q, got nil", m)
		}
	}
}

func TestSendMagicPacket_LoopbackUDP(t *testing.T) {
	// Setup local UDP listener on random port
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen UDP: %v", err)
	}
	defer pc.Close()

	port := pc.LocalAddr().(*net.UDPAddr).Port

	macStr := "AA:BB:CC:11:22:33"
	bcastIP := net.ParseIP("127.0.0.1")

	err = SendMagicPacket(bcastIP, macStr, port)
	if err != nil {
		t.Fatalf("SendMagicPacket failed: %v", err)
	}

	buf := make([]byte, 256)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("failed to read UDP packet: %v", err)
	}

	if n != MagicPacketLength {
		t.Errorf("received packet length = %d, want %d", n, MagicPacketLength)
	}
}
