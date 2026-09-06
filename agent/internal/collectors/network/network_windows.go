package network

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"syscall"
	"unsafe"
)

var (
	iphlpapi = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	AF_INET  = 2
	AF_INET6 = 23
	
	TCP_TABLE_OWNER_PID_ALL = 5
	UDP_TABLE_OWNER_PID     = 1
	
	ERROR_INSUFFICIENT_BUFFER = 122
)

// Windows TCP states mapping
var tcpStates = map[uint32]string{
	1:  "CLOSED",
	2:  "LISTEN",
	3:  "SYN_SENT",
	4:  "SYN_RCVD",
	5:  "ESTABLISHED",
	6:  "FIN_WAIT1",
	7:  "FIN_WAIT2",
	8:  "CLOSE_WAIT",
	9:  "CLOSING",
	10: "LAST_ACK",
	11: "TIME_WAIT",
	12: "DELETE_TCB",
}

func osSpecificConnections() ([]Connection, []string, error) {
	var all []Connection
	var qualityFlags []string

	// 1. TCP IPv4
	tcp4, err := getTcpTable(AF_INET)
	if err != nil {
		qualityFlags = append(qualityFlags, "NETWORK_TCP4_FAILED")
	} else {
		all = append(all, tcp4...)
	}

	// 2. TCP IPv6
	tcp6, err := getTcpTable(AF_INET6)
	if err != nil {
		qualityFlags = append(qualityFlags, "NETWORK_TCP6_FAILED")
	} else {
		all = append(all, tcp6...)
	}

	// 3. UDP IPv4
	udp4, err := getUdpTable(AF_INET)
	if err != nil {
		qualityFlags = append(qualityFlags, "NETWORK_UDP4_FAILED")
	} else {
		all = append(all, udp4...)
	}

	// 4. UDP IPv6
	udp6, err := getUdpTable(AF_INET6)
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

func getTcpTable(family uint32) ([]Connection, error) {
	var buf []byte
	var size uint32
	
	// Initial call to get size
	ret, _, _ := procGetExtendedTcpTable.Call(
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(family),
		uintptr(TCP_TABLE_OWNER_PID_ALL),
		0,
	)

	if ret != ERROR_INSUFFICIENT_BUFFER {
		return nil, fmt.Errorf("GetExtendedTcpTable expected %d, got %d", ERROR_INSUFFICIENT_BUFFER, ret)
	}

	buf = make([]byte, size)
	ret, _, _ = procGetExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(family),
		uintptr(TCP_TABLE_OWNER_PID_ALL),
		0,
	)

	if ret != 0 {
		return nil, fmt.Errorf("GetExtendedTcpTable failed with code %d", ret)
	}

	return parseTcpTable(buf, family), nil
}

func getUdpTable(family uint32) ([]Connection, error) {
	var buf []byte
	var size uint32
	
	ret, _, _ := procGetExtendedUdpTable.Call(
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(family),
		uintptr(UDP_TABLE_OWNER_PID),
		0,
	)

	if ret != ERROR_INSUFFICIENT_BUFFER {
		return nil, fmt.Errorf("GetExtendedUdpTable expected %d, got %d", ERROR_INSUFFICIENT_BUFFER, ret)
	}

	buf = make([]byte, size)
	ret, _, _ = procGetExtendedUdpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(family),
		uintptr(UDP_TABLE_OWNER_PID),
		0,
	)

	if ret != 0 {
		return nil, fmt.Errorf("GetExtendedUdpTable failed with code %d", ret)
	}

	return parseUdpTable(buf, family), nil
}

func parseTcpTable(buf []byte, family uint32) []Connection {
	if len(buf) < 4 {
		return nil
	}
	numEntries := *(*uint32)(unsafe.Pointer(&buf[0]))
	var conns []Connection

	if family == AF_INET {
		// MIB_TCPTABLE_OWNER_PID
		// DWORD dwNumEntries;
		// MIB_TCPROW_OWNER_PID table[ANY_SIZE];
		
		// MIB_TCPROW_OWNER_PID struct size is 24 bytes
		offset := uint32(4)
		for i := uint32(0); i < numEntries; i++ {
			if offset+24 > uint32(len(buf)) {
				break
			}
			
			state := *(*uint32)(unsafe.Pointer(&buf[offset]))
			localAddr := *(*uint32)(unsafe.Pointer(&buf[offset+4]))
			localPort := *(*uint32)(unsafe.Pointer(&buf[offset+8]))
			remoteAddr := *(*uint32)(unsafe.Pointer(&buf[offset+12]))
			remotePort := *(*uint32)(unsafe.Pointer(&buf[offset+16]))
			pid := *(*uint32)(unsafe.Pointer(&buf[offset+20]))

			conns = append(conns, Connection{
				Protocol: "TCP",
				SrcIP:    ip4ToString(localAddr),
				SrcPort:  ntohs(localPort),
				DstIP:    ip4ToString(remoteAddr),
				DstPort:  ntohs(remotePort),
				State:    tcpStates[state],
				PID:      pid,
			})
			offset += 24
		}
	} else if family == AF_INET6 {
		// MIB_TCP6TABLE_OWNER_PID
		// DWORD dwNumEntries;
		// MIB_TCP6ROW_OWNER_PID table[ANY_SIZE];
		
		// MIB_TCP6ROW_OWNER_PID is 56 bytes
		offset := uint32(4)
		for i := uint32(0); i < numEntries; i++ {
			if offset+56 > uint32(len(buf)) {
				break
			}

			// memory alignment requires 4-byte boundaries, we'll read fields directly
			// 0-16: LocalAddr (16 bytes)
			// 16-20: LocalScopeId
			// 20-24: LocalPort
			// 24-40: RemoteAddr (16 bytes)
			// 40-44: RemoteScopeId
			// 44-48: RemotePort
			// 48-52: State
			// 52-56: OwningPid

			localAddr := buf[offset : offset+16]
			localPort := *(*uint32)(unsafe.Pointer(&buf[offset+20]))
			remoteAddr := buf[offset+24 : offset+40]
			remotePort := *(*uint32)(unsafe.Pointer(&buf[offset+44]))
			state := *(*uint32)(unsafe.Pointer(&buf[offset+48]))
			pid := *(*uint32)(unsafe.Pointer(&buf[offset+52]))

			conns = append(conns, Connection{
				Protocol: "TCP",
				SrcIP:    ip6ToString(localAddr),
				SrcPort:  ntohs(localPort),
				DstIP:    ip6ToString(remoteAddr),
				DstPort:  ntohs(remotePort),
				State:    tcpStates[state],
				PID:      pid,
			})
			offset += 56
		}
	}
	return conns
}

func parseUdpTable(buf []byte, family uint32) []Connection {
	if len(buf) < 4 {
		return nil
	}
	numEntries := *(*uint32)(unsafe.Pointer(&buf[0]))
	var conns []Connection

	if family == AF_INET {
		// MIB_UDPTABLE_OWNER_PID
		// MIB_UDPROW_OWNER_PID size = 12
		offset := uint32(4)
		for i := uint32(0); i < numEntries; i++ {
			if offset+12 > uint32(len(buf)) {
				break
			}
			localAddr := *(*uint32)(unsafe.Pointer(&buf[offset]))
			localPort := *(*uint32)(unsafe.Pointer(&buf[offset+4]))
			pid := *(*uint32)(unsafe.Pointer(&buf[offset+8]))

			conns = append(conns, Connection{
				Protocol: "UDP",
				SrcIP:    ip4ToString(localAddr),
				SrcPort:  ntohs(localPort),
				DstIP:    "0.0.0.0",
				DstPort:  0,
				PID:      pid,
			})
			offset += 12
		}
	} else if family == AF_INET6 {
		// MIB_UDP6TABLE_OWNER_PID
		// MIB_UDP6ROW_OWNER_PID size = 28
		offset := uint32(4)
		for i := uint32(0); i < numEntries; i++ {
			if offset+28 > uint32(len(buf)) {
				break
			}
			localAddr := buf[offset : offset+16]
			localPort := *(*uint32)(unsafe.Pointer(&buf[offset+20]))
			pid := *(*uint32)(unsafe.Pointer(&buf[offset+24]))

			conns = append(conns, Connection{
				Protocol: "UDP",
				SrcIP:    ip6ToString(localAddr),
				SrcPort:  ntohs(localPort),
				DstIP:    "::",
				DstPort:  0,
				PID:      pid,
			})
			offset += 28
		}
	}
	return conns
}

func ntohs(port uint32) uint16 {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(port))
	return binary.LittleEndian.Uint16(b)
}

func ip4ToString(ip uint32) string {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, ip)
	return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
}

func ip6ToString(ip []byte) string {
	var a [16]byte
	copy(a[:], ip)
	return netip.AddrFrom16(a).String()
}
