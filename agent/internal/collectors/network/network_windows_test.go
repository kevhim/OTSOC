package network

import (
	"encoding/binary"
	"testing"
)

func TestParseTcpTable_IPv4(t *testing.T) {
	// Construct a fake MIB_TCPTABLE_OWNER_PID buffer
	// DWORD dwNumEntries = 1
	// MIB_TCPROW_OWNER_PID (24 bytes)
	// state, localaddr, localport, remoteaddr, remoteport, pid
	
	buf := make([]byte, 28)
	binary.LittleEndian.PutUint32(buf[0:4], 1) // 1 entry

	offset := 4
	binary.LittleEndian.PutUint32(buf[offset:offset+4], 2) // LISTEN
	
	// Local: 127.0.0.1 (0100007F in little endian) -> 0x7F000001
	// Actually inet_addr("127.0.0.1") = 0x0100007f (Little Endian bytes: 7F 00 00 01)
	buf[offset+4] = 127
	buf[offset+5] = 0
	buf[offset+6] = 0
	buf[offset+7] = 1

	// Local Port: 80 (0x0050, network byte order -> 0x50 0x00)
	buf[offset+8] = 0x00
	buf[offset+9] = 0x50
	buf[offset+10] = 0x00
	buf[offset+11] = 0x00

	// Remote Addr: 0.0.0.0
	// Remote Port: 0
	
	// PID: 1234
	binary.LittleEndian.PutUint32(buf[offset+20:offset+24], 1234)

	conns := parseTcpTable(buf, AF_INET)
	if len(conns) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(conns))
	}
	if conns[0].SrcIP != "127.0.0.1" || conns[0].SrcPort != 80 {
		t.Errorf("expected 127.0.0.1:80, got %s:%d", conns[0].SrcIP, conns[0].SrcPort)
	}
	if conns[0].State != "LISTEN" || conns[0].PID != 1234 {
		t.Errorf("expected LISTEN and PID 1234, got %s and %d", conns[0].State, conns[0].PID)
	}
}

func TestParseTcpTable_TruncatedBuffer(t *testing.T) {
	buf := make([]byte, 16)
	binary.LittleEndian.PutUint32(buf[0:4], 2) // claims 2 entries but buffer is small
	
	conns := parseTcpTable(buf, AF_INET)
	if len(conns) != 0 {
		t.Fatalf("expected 0 connections on truncated buffer, got %d", len(conns))
	}
}
