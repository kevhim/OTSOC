package network

import (
	"strings"
	"testing"
)

func TestParseProcNetReader_IPv4_TCP(t *testing.T) {
	fakeProcNet := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode                                                     
   0: 0100007F:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 39864 1 0000000000000000 100 0 0 10 0
   1: 0100007F:C2E5 0100007F:0050 01 00000000:00000000 00:00000000 00000000     0        0 39865 1 0000000000000000 20 4 30 10 -1
`

	conns, err := parseProcNetReader(strings.NewReader(fakeProcNet), "TCP", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(conns) != 2 {
		t.Fatalf("expected 2 connections, got %d", len(conns))
	}

	if conns[0].SrcIP != "127.0.0.1" || conns[0].SrcPort != 80 {
		t.Errorf("expected 127.0.0.1:80, got %s:%d", conns[0].SrcIP, conns[0].SrcPort)
	}
	if conns[0].State != "LISTEN" {
		t.Errorf("expected LISTEN, got %s", conns[0].State)
	}

	if conns[1].SrcPort != 49893 || conns[1].State != "ESTABLISHED" {
		t.Errorf("expected port 49893 and state ESTABLISHED")
	}
}

func TestParseProcNetReader_IPv6_TCP(t *testing.T) {
	fakeProcNet := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 41103 1 0000000000000000 100 0 0 10 0
`
	conns, err := parseProcNetReader(strings.NewReader(fakeProcNet), "TCP", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(conns) != 1 {
		t.Fatalf("expected 1 connection")
	}
	if conns[0].SrcIP != "::" || conns[0].SrcPort != 443 {
		t.Errorf("expected :: and port 443, got %s:%d", conns[0].SrcIP, conns[0].SrcPort)
	}
}
