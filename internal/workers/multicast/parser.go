package multicast

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	// Matches udpcast stats like: "bytes= 104857600 ( 25.50 %)  speed= 85.2 Mbps"
	statRegex = regexp.MustCompile(`(?i)\(\s*([0-9.]+)\s*%\)\s*speed=\s*([0-9.]+)\s*(?:Mbps|MB/s)?`)
	// Fallback simple percentage matcher: " 45 % "
	simplePercentRegex = regexp.MustCompile(`([0-9.]+)\s*%`)
)

// ParseStatLine parses a progress status line from udp-sender or udp-receiver.
func ParseStatLine(line string) (percent float64, speedMbps float64, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, 0, false
	}

	matches := statRegex.FindStringSubmatch(line)
	if len(matches) == 3 {
		p, err1 := strconv.ParseFloat(matches[1], 64)
		s, err2 := strconv.ParseFloat(matches[2], 64)
		if err1 == nil && err2 == nil {
			return p, s, true
		}
	}

	simpleMatches := simplePercentRegex.FindStringSubmatch(line)
	if len(simpleMatches) == 2 {
		p, err := strconv.ParseFloat(simpleMatches[1], 64)
		if err == nil {
			return p, 0, true
		}
	}

	return 0, 0, false
}

