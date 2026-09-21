package integration

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"redcyberfox/agent/internal/collectors/passivenetwork"
	"redcyberfox/agent/internal/config"
	"redcyberfox/agent/internal/forwarder"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func buildTestModbusPacket() []byte {
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")

	// Ethernet (14) + IPv4 (20) + TCP (20) + Modbus payload (8)
	eth := make([]byte, 14)
	copy(eth[0:6], dstMAC)
	copy(eth[6:12], srcMAC)
	binary.BigEndian.PutUint16(eth[12:14], 0x0800)

	ip := make([]byte, 20)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], uint16(20+20+8))
	ip[8] = 64
	ip[9] = 6 // TCP
	copy(ip[12:16], net.ParseIP("192.168.1.50").To4())
	copy(ip[16:20], net.ParseIP("192.168.1.100").To4())

	tcp := make([]byte, 28)
	binary.BigEndian.PutUint16(tcp[0:2], 12345) // Client port
	binary.BigEndian.PutUint16(tcp[2:4], 502)   // Modbus port
	tcp[12] = 0x50                              // 20 bytes offset
	copy(tcp[20:], []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x06, 0x01, 0x03}) // MBAP + read holding registers

	res := append(eth, ip...)
	return append(res, tcp...)
}

func TestPassiveNetworkPipeline_Integration(t *testing.T) {
	var (
		mu       sync.Mutex
		received int
		lastBody []byte
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.URL.Path != "/v1/ingest" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		tenantID := r.URL.Query().Get("tenant_id")
		if tenantID != "test-tenant" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing or invalid tenant_id"))
			return
		}

		body, _ := io.ReadAll(r.Body)
		lastBody = body
		received++

		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	identityPath := filepath.Join(tmpDir, "identity.json")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initialize Storage
	db := storage.NewSQLiteStorage(dbPath, identityPath, 1024*1024)
	if err := db.Init(ctx); err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}
	defer db.Close()

	// 2. Initialize Config
	cfg := &config.Config{
		APIAddr:  ts.URL,
		TenantID: "test-tenant",
		SiteID:   "test-site",
	}

	// 3. Central Channel
	centralEvents := make(chan *events.CanonicalEvent, 100)

	// 4. Start Forwarder
	fwd := forwarder.NewForwarder(cfg.APIAddr, cfg.TenantID, db)
	if err := fwd.Start(ctx); err != nil {
		t.Fatalf("Failed to start forwarder: %v", err)
	}
	defer fwd.Stop()

	// 5. Ingestion Loop
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range centralEvents {
			if err := db.Store(ctx, ev); err != nil {
				t.Errorf("Store failed: %v", err)
			}
			fwd.Wakeup()
		}
	}()

	// 6. Create deterministic replay fixture and start Passive Network Collector
	fixture := passivenetwork.RawObservation{
		Data:      buildTestModbusPacket(),
		Timestamp: time.Now().UTC(),
		Interface: "tap0",
		LinkType:  "ethernet",
	}
	adapter := passivenetwork.NewReplayAdapter([]passivenetwork.RawObservation{fixture})
	collector := passivenetwork.NewCollector(cfg, adapter)

	if err := collector.Start(ctx, centralEvents); err != nil {
		t.Fatalf("Failed to start passive network collector: %v", err)
	}

	// 7. Wait for event to propagate through pipeline and forwarder
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		rec := received
		mu.Unlock()
		if rec > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Shutdown
	collector.Stop()
	close(centralEvents)
	wg.Wait()

	// 8. Verify Forwarded Event
	mu.Lock()
	defer mu.Unlock()

	if received != 1 {
		t.Fatalf("Expected 1 event forwarded, got %d", received)
	}

	var ev events.CanonicalEvent
	if err := json.Unmarshal(lastBody, &ev); err != nil {
		t.Fatalf("Failed to parse forwarded event: %v", err)
	}

	if ev.Category != "network" {
		t.Errorf("Expected category 'network', got %s", ev.Category)
	}
	if ev.Source != "passive_network" {
		t.Errorf("Expected source 'passive_network', got %s", ev.Source)
	}
	if ev.Protocol != "modbus" {
		t.Errorf("Expected protocol 'modbus', got %s", ev.Protocol)
	}
	if ev.Src != "192.168.1.50" {
		t.Errorf("Expected Src '192.168.1.50', got %s", ev.Src)
	}
	if ev.Dst != "192.168.1.100" {
		t.Errorf("Expected Dst '192.168.1.100', got %s", ev.Dst)
	}
	if ev.SeqNo == 0 {
		t.Errorf("Expected SeqNo > 0 assigned by SQLite, got 0")
	}
	if ev.Metadata["confidence"] != "known" {
		t.Errorf("Expected confidence 'known' from valid Modbus application framing, got %v", ev.Metadata["confidence"])
	}
	if ev.Metadata["modbus_function_name"] != "read_holding_registers" {
		t.Errorf("Expected function 'read_holding_registers', got %v", ev.Metadata["modbus_function_name"])
	}
}
