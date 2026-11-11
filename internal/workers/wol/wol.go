package wol

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/owner/back-ez/internal/protocol"
)

// MagicPacketLength defines the standard WOL packet size (6 + 16*6 = 102 bytes).
const MagicPacketLength = 102

var (
	ErrInvalidMAC = errors.New(protocol.ErrInvalidArgument)
	ErrMACUnknown = errors.New(protocol.ErrMACUnknown)
)

// ParseMAC normalizes MAC strings formatted with colons or dashes.
func ParseMAC(macStr string) (net.HardwareAddr, error) {
	clean := strings.TrimSpace(macStr)
	clean = strings.ReplaceAll(clean, "-", ":")
	hw, err := net.ParseMAC(clean)
	if err != nil || len(hw) != 6 {
		return nil, ErrInvalidMAC
	}
	return hw, nil
}

// BuildMagicPacket constructs a 102-byte Wake-on-LAN magic packet.
// Structure: 6 bytes of 0xFF followed by 16 repetitions of the 6-byte MAC address.
func BuildMagicPacket(mac net.HardwareAddr) ([]byte, error) {
	if len(mac) != 6 {
		return nil, ErrInvalidMAC
	}

	packet := make([]byte, MagicPacketLength)

	// 6 bytes of 0xFF
	for i := 0; i < 6; i++ {
		packet[i] = 0xFF
	}

	// 16 repetitions of MAC
	for i := 0; i < 16; i++ {
		copy(packet[6+i*6:6+(i+1)*6], mac)
	}

	return packet, nil
}

// CalculateBroadcast computes the directed broadcast IP from an IP and its subnet mask.
func CalculateBroadcast(ip net.IP, mask net.IPMask) (net.IP, error) {
	ipv4 := ip.To4()
	if ipv4 == nil || len(mask) != 4 {
		return nil, errors.New("only IPv4 broadcast calculation is supported")
	}

	broadcast := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		broadcast[i] = ipv4[i] | ^mask[i]
	}
	return broadcast, nil
}

// CalculateBroadcastCIDR parses a CIDR string (e.g. "192.168.1.100/24") and calculates its broadcast address.
func CalculateBroadcastCIDR(cidr string) (net.IP, error) {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	return CalculateBroadcast(ip, ipNet.Mask)
}

// SendMagicPacket transmits a Magic Packet over UDP to a specified broadcast address and port.
func SendMagicPacket(broadcastIP net.IP, macStr string, port int) error {
	if port <= 0 {
		port = 9 // Default WOL port (SPEC §5.4)
	}

	mac, err := ParseMAC(macStr)
	if err != nil {
		return err
	}

	packet, err := BuildMagicPacket(mac)
	if err != nil {
		return err
	}

	addr := &net.UDPAddr{
		IP:   broadcastIP,
		Port: port,
	}

	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return fmt.Errorf("failed to open UDP connection: %w", err)
	}
	defer conn.Close()

	n, err := conn.Write(packet)
	if err != nil {
		return fmt.Errorf("failed to write UDP magic packet: %w", err)
	}
	if n != MagicPacketLength {
		return fmt.Errorf("incomplete packet sent: wrote %d bytes, want %d", n, MagicPacketLength)
	}

	return nil
}
