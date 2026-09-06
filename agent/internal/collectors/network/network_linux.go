package network

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

var tcpStates = map[string]string{
	"01": "ESTABLISHED",
	"02": "SYN_SENT",
	"03": "SYN_RECV",
	"04": "FIN_WAIT1",
	"05": "FIN_WAIT2",
	"06": "TIME_WAIT",
	"07": "CLOSE",
	"08": "CLOSE_WAIT",
	"09": "LAST_ACK",
	"0A": "LISTEN",
	"0B": "CLOSING",
}

func osSpecificConnections() ([]Connection, []string, error) {
	var all []Connection
	var qualityFlags []string

	tcp4, err := parseProcNetFile("/proc/net/tcp", "TCP", false)
	if err != nil {
		qualityFlags = append(qualityFlags, "NETWORK_TCP4_FAILED")
	} else {
		all = append(all, tcp4...)
	}

	tcp6, err := parseProcNetFile("/proc/net/tcp6", "TCP", true)
	if err != nil {
		qualityFlags = append(qualityFlags, "NETWORK_TCP6_FAILED")
	} else {
		all = append(all, tcp6...)
	}

	udp4, err := parseProcNetFile("/proc/net/udp", "UDP", false)
	if err != nil {
		qualityFlags = append(qualityFlags, "NETWORK_UDP4_FAILED")
	} else {
		all = append(all, udp4...)
	}

	udp6, err := parseProcNetFile("/proc/net/udp6", "UDP", true)
	if err != nil {
		qualityFlags = append(qualityFlags, "NETWORK_UDP6_FAILED")
	} else {
		all = append(all, udp6...)
	}

	if len(qualityFlags) == 4 {
		return nil, nil, fmt.Errorf("complete failure retrieving network tables")
	}

	return all, qualityFlags, nil
}

func parseProcNetFile(path, protocol string, isIPv6 bool) ([]Connection, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseProcNetReader(f, protocol, isIPv6)
}

func parseProcNetReader(r io.Reader, protocol string, isIPv6 bool) ([]Connection, error) {
	var conns []Connection
	scanner := bufio.NewScanner(r)
	
	// Skip header
	if scanner.Scan() {
		_ = scanner.Text()
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		srcIP, srcPort := parseIPPort(fields[1], isIPv6)
		dstIP, dstPort := parseIPPort(fields[2], isIPv6)
		
		stateHex := fields[3]
		state := tcpStates[stateHex]

		conns = append(conns, Connection{
			Protocol: protocol,
			SrcIP:    srcIP,
			SrcPort:  srcPort,
			DstIP:    dstIP,
			DstPort:  dstPort,
			State:    state,
			PID:      0, // PID attribution omitted as per contract bounds on Linux
		})
	}
	return conns, scanner.Err()
}

func parseIPPort(s string, isIPv6 bool) (string, uint16) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return "", 0
	}

	portUint, _ := strconv.ParseUint(parts[1], 16, 16)
	port := uint16(portUint)

	ipHex := parts[0]
	if isIPv6 {
		if len(ipHex) != 32 {
			return "", port
		}
		// Decode IPv6 Little Endian per 4-byte block
		var ip [16]byte
		for i := 0; i < 4; i++ {
			chunk, _ := strconv.ParseUint(ipHex[i*8:(i+1)*8], 16, 32)
			ip[i*4] = byte(chunk)
			ip[i*4+1] = byte(chunk >> 8)
			ip[i*4+2] = byte(chunk >> 16)
			ip[i*4+3] = byte(chunk >> 24)
		}
		return netip.AddrFrom16(ip).String(), port
	}

	if len(ipHex) != 8 {
		return "", port
	}
	ipUint, _ := strconv.ParseUint(ipHex, 16, 32)
	ip := [4]byte{
		byte(ipUint),
		byte(ipUint >> 8),
		byte(ipUint >> 16),
		byte(ipUint >> 24),
	}
	return fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], ip[3]), port
}
