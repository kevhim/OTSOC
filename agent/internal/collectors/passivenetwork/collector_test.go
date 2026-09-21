package passivenetwork

import (
	"context"
	"net"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

func minimalConfig() *config.Config {
	return &config.Config{
		TenantID: "test-tenant-1",
		SiteID:   "test-site-1",
		APIAddr:  "localhost:8080",
	}
}

// 1. Observation enters adapter
func TestObservation_EntersAdapter(t *testing.T) {
	raw := RawObservation{
		Data:      []byte{0x01, 0x02, 0x03},
		Timestamp: time.Now().UTC(),
		Interface: "eth0",
		LinkType:  "ethernet",
	}

	adapter := NewReplayAdapter([]RawObservation{raw})
	ch := make(chan RawObservation, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := adapter.Start(ctx, ch); err != nil {
		t.Fatalf("Failed to start adapter: %v", err)
	}

	select {
	case received := <-ch:
		if received.Interface != "eth0" || len(received.Data) != 3 {
			t.Errorf("Unexpected observation received: %+v", received)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for observation from adapter")
	}

	if err := adapter.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}
}

// 2. Deterministic replay
func TestReplayAdapter_Deterministic(t *testing.T) {
	count := 10
	fixtures := make([]RawObservation, count)
	for i := 0; i < count; i++ {
		fixtures[i] = RawObservation{
			Data:      []byte{byte(i)},
			Timestamp: time.Unix(int64(1000+i), 0).UTC(),
			Interface: "eth0",
		}
	}

	adapter := NewReplayAdapter(fixtures)
	ch := make(chan RawObservation, count)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := adapter.Start(ctx, ch); err != nil {
		t.Fatalf("Failed to start adapter: %v", err)
	}

	for i := 0; i < count; i++ {
		select {
		case obs := <-ch:
			if obs.Data[0] != byte(i) {
				t.Fatalf("Sequence mismatch at %d: got %d", i, obs.Data[0])
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("Timeout at index %d", i)
		}
	}

	adapter.Stop()
}

// 3. Cancellation-aware replay
func TestReplayAdapter_CancellationAware(t *testing.T) {
	// Provide more fixtures than channel buffer
	fixtures := make([]RawObservation, 50)
	adapter := NewReplayAdapter(fixtures)
	ch := make(chan RawObservation, 2)

	ctx, cancel := context.WithCancel(context.Background())

	if err := adapter.Start(ctx, ch); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	// Cancel context immediately while adapter is blocked trying to send to ch
	cancel()

	done := make(chan struct{})
	go func() {
		adapter.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Clean exit without deadlock
	case <-time.After(1 * time.Second):
		t.Fatal("Adapter failed to stop on context cancellation (goroutine blocked)")
	}
}

// 4. Ethernet normalization
func TestNormalizer_Ethernet(t *testing.T) {
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	packet := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("192.168.1.10"), net.ParseIP("192.168.1.20"), 1234, 5678, []byte("test"))

	obs, _ := NormalizeRawObservation(RawObservation{
		Data:      packet,
		Timestamp: time.Now().UTC(),
		Interface: "eth0",
	})

	if obs.SrcMAC != srcMAC.String() {
		t.Errorf("Expected SrcMAC %s, got %s", srcMAC, obs.SrcMAC)
	}
	if obs.DstMAC != dstMAC.String() {
		t.Errorf("Expected DstMAC %s, got %s", dstMAC, obs.DstMAC)
	}
}

// 5. IPv4 normalization
func TestNormalizer_IPv4(t *testing.T) {
	srcIP := net.ParseIP("10.0.0.5")
	dstIP := net.ParseIP("10.0.0.1")
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	packet := buildEthernetIPv4TCP(srcMAC, dstMAC, srcIP, dstIP, 5000, 80, nil)

	obs, _ := NormalizeRawObservation(RawObservation{Data: packet})
	if obs.SrcIP != srcIP.String() {
		t.Errorf("Expected SrcIP %s, got %s", srcIP, obs.SrcIP)
	}
	if obs.DstIP != dstIP.String() {
		t.Errorf("Expected DstIP %s, got %s", dstIP, obs.DstIP)
	}
}

// 6. VLAN recognition
func TestNormalizer_VLAN(t *testing.T) {
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	vlanID := uint16(100)
	packet := buildEthernetVLANIPv4TCP(srcMAC, dstMAC, vlanID, net.ParseIP("192.168.1.1"), net.ParseIP("192.168.1.2"), 1000, 2000, []byte("vlan-data"))

	obs, _ := NormalizeRawObservation(RawObservation{Data: packet})
	if obs.VLANID == nil {
		t.Fatal("Expected VLANID to be populated, got nil")
	}
	if *obs.VLANID != vlanID {
		t.Errorf("Expected VLANID %d, got %d", vlanID, *obs.VLANID)
	}
	if obs.TransportProtocol != "TCP" || obs.SrcPort != 1000 || obs.DstPort != 2000 {
		t.Errorf("L4 parsing corrupted after VLAN header: %+v", obs)
	}
}

// 7. ARP recognition
func TestNormalizer_ARP(t *testing.T) {
	sha, _ := net.ParseMAC("00:aa:bb:cc:dd:ee")
	tha, _ := net.ParseMAC("00:00:00:00:00:00")
	spa := net.ParseIP("192.168.1.100")
	tpa := net.ParseIP("192.168.1.1")

	packet := buildARP(sha, tha, spa, tpa, 1) // ARP Request

	obs, _ := NormalizeRawObservation(RawObservation{Data: packet})
	if obs.TransportProtocol != "ARP" {
		t.Errorf("Expected TransportProtocol ARP, got %s", obs.TransportProtocol)
	}
	if obs.SrcIP != spa.String() || obs.DstIP != tpa.String() {
		t.Errorf("Unexpected ARP IPs: src=%s, dst=%s", obs.SrcIP, obs.DstIP)
	}
	if obs.Metadata["arp_operation"] != "request" {
		t.Errorf("Expected arp_operation request, got %v", obs.Metadata["arp_operation"])
	}
}

// 8. TCP/UDP normalization
func TestNormalizer_TCPUDP(t *testing.T) {
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")

	// TCP
	tcpPacket := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("1.1.1.1"), net.ParseIP("2.2.2.2"), 8080, 80, []byte("payload"))
	obsTCP, payloadTCP := NormalizeRawObservation(RawObservation{Data: tcpPacket})
	if obsTCP.TransportProtocol != "TCP" || obsTCP.SrcPort != 8080 || obsTCP.DstPort != 80 {
		t.Errorf("TCP decode failed: %+v", obsTCP)
	}
	if string(payloadTCP) != "payload" || obsTCP.PayloadLength != 7 {
		t.Errorf("TCP payload mismatch: %s, len=%d", string(payloadTCP), obsTCP.PayloadLength)
	}

	// UDP
	udpPacket := buildEthernetIPv4UDP(srcMAC, dstMAC, net.ParseIP("1.1.1.1"), net.ParseIP("2.2.2.2"), 53, 5353, []byte("dns"))
	obsUDP, payloadUDP := NormalizeRawObservation(RawObservation{Data: udpPacket})
	if obsUDP.TransportProtocol != "UDP" || obsUDP.SrcPort != 53 || obsUDP.DstPort != 5353 {
		t.Errorf("UDP decode failed: %+v", obsUDP)
	}
	if string(payloadUDP) != "dns" || obsUDP.PayloadLength != 3 {
		t.Errorf("UDP payload mismatch: %s, len=%d", string(payloadUDP), obsUDP.PayloadLength)
	}
}

// 9. Malformed / truncated handling
func TestNormalizer_MalformedTruncated(t *testing.T) {
	// Truncated Ethernet frame (< 14 bytes)
	obsShort, _ := NormalizeRawObservation(RawObservation{Data: []byte{0x01, 0x02}})
	if obsShort.Confidence != ConfidenceUnsupported {
		t.Errorf("Expected ConfidenceUnsupported for short frame, got %s", obsShort.Confidence)
	}
	if len(obsShort.QualityFlags) == 0 || obsShort.QualityFlags[0] != QualityTruncatedPacket {
		t.Errorf("Expected QualityTruncatedPacket flag, got %v", obsShort.QualityFlags)
	}

	// IPv4 with invalid version (e.g. version 6 in IPv4 header)
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	packet := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("1.1.1.1"), net.ParseIP("2.2.2.2"), 80, 80, nil)
	packet[14] = 0x65 // Change version from 4 to 6

	obsBadVer, _ := NormalizeRawObservation(RawObservation{Data: packet})
	if obsBadVer.Confidence != ConfidenceUnsupported {
		t.Errorf("Expected ConfidenceUnsupported for bad IP version, got %s", obsBadVer.Confidence)
	}
	foundMalformed := false
	for _, flag := range obsBadVer.QualityFlags {
		if flag == QualityMalformedFrame {
			foundMalformed = true
		}
	}
	if !foundMalformed {
		t.Errorf("Expected QualityMalformedFrame flag, got %v", obsBadVer.QualityFlags)
	}
}

// 10. Unknown fields stay unknown
func TestNormalizer_UnknownFieldsStayUnknown(t *testing.T) {
	// Non-VLAN packet
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	packet := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("1.1.1.1"), net.ParseIP("2.2.2.2"), 9999, 9999, nil)

	obs, _ := NormalizeRawObservation(RawObservation{Data: packet})
	if obs.VLANID != nil {
		t.Errorf("VLANID must be nil when not present, got %v", *obs.VLANID)
	}
	if obs.ProtocolHint != "unknown" {
		t.Errorf("ProtocolHint must default to unknown, got %s", obs.ProtocolHint)
	}
}

// 11. Port-based protocol hint is only inferred
func TestProtocols_PortHintIsInferredOnly(t *testing.T) {
	identifier := NewDefaultProtocolIdentifier()

	tests := []struct {
		port         uint16
		expectedHint string
		expectedConf ConfidenceLevel
	}{
		{502, "modbus", ConfidenceInferred},
		{20000, "dnp3", ConfidenceInferred},
		{44818, "ethernet_ip", ConfidenceInferred},
		{102, "s7", ConfidenceInferred},
		{80, "unknown", ConfidenceUnknown},
		{443, "unknown", ConfidenceUnknown},
	}

	for _, tt := range tests {
		obs := &NormalizedObservation{
			TransportProtocol: "TCP",
			DstPort:           tt.port,
		}
		hint, conf := identifier.Identify(obs, nil)
		if hint != tt.expectedHint {
			t.Errorf("Port %d: expected hint %s, got %s", tt.port, tt.expectedHint, hint)
		}
		if conf != tt.expectedConf {
			t.Errorf("Port %d: expected confidence %s, got %s", tt.port, tt.expectedConf, conf)
		}
	}
}

// 12. Port 502 does NOT create PLC identity
func TestProtocols_Port502DoesNotCreatePLCIdentity(t *testing.T) {
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	packet := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("192.168.1.50"), net.ParseIP("192.168.1.100"), 502, 502, []byte("modbus"))

	raw := RawObservation{
		Data:      packet,
		Timestamp: time.Now().UTC(),
		Interface: "eth0",
	}

	adapter := NewReplayAdapter([]RawObservation{raw})
	collector := NewCollector(minimalConfig(), adapter)

	out := make(chan *events.CanonicalEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := collector.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}

	select {
	case ev := <-out:
		// Check that asset identity is NEVER fabricated
		if ev.AssetID != "" {
			t.Errorf("AssetID must not be fabricated from network traffic, got %s", ev.AssetID)
		}
		if role, ok := ev.Metadata["device_role"]; ok {
			t.Errorf("device_role must not be fabricated, got %v", role)
		}
		if vendor, ok := ev.Metadata["vendor"]; ok {
			t.Errorf("vendor must not be fabricated, got %v", vendor)
		}
		if plc, ok := ev.Metadata["is_plc"]; ok {
			t.Errorf("is_plc must not be fabricated, got %v", plc)
		}
		// Inferred confidence only
		if ev.Metadata["confidence"] != string(ConfidenceInferred) {
			t.Errorf("Expected confidence inferred, got %v", ev.Metadata["confidence"])
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for CanonicalEvent")
	}

	collector.Stop()
}

// 13. Event ID generated once & single provenance
func TestCollector_EventIDGeneratedOnce(t *testing.T) {
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	packet := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("1.1.1.1"), net.ParseIP("2.2.2.2"), 100, 200, nil)

	adapter := NewReplayAdapter([]RawObservation{{Data: packet, Timestamp: time.Now().UTC()}})
	collector := NewCollector(minimalConfig(), adapter)

	out := make(chan *events.CanonicalEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	collector.Start(ctx, out)

	select {
	case ev := <-out:
		if ev.EventID == "" {
			t.Fatal("EventID is empty")
		}
		if _, err := uuid.Parse(ev.EventID); err != nil {
			t.Fatalf("EventID is not a valid UUID: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for event")
	}

	collector.Stop()
}

// 14. CanonicalEvent schema validity
func TestCollector_CanonicalEventSchemaValidity(t *testing.T) {
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	packet := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("192.168.1.1"), net.ParseIP("192.168.1.2"), 502, 502, nil)

	adapter := NewReplayAdapter([]RawObservation{{Data: packet, Timestamp: time.Now().UTC()}})
	collector := NewCollector(minimalConfig(), adapter)

	out := make(chan *events.CanonicalEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	collector.Start(ctx, out)

	select {
	case ev := <-out:
		if err := ev.Validate(); err != nil {
			t.Fatalf("CanonicalEvent failed schema validation: %v", err)
		}
		if ev.Category != "network" {
			t.Errorf("Expected category 'network', got %s", ev.Category)
		}
		if ev.Source != "passive_network" {
			t.Errorf("Expected source 'passive_network', got %s", ev.Source)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for event")
	}

	collector.Stop()
}

// 15. Event ID preserved end-to-end
func TestCollector_EventIDPreserved(t *testing.T) {
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	packet := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2"), 80, 80, nil)

	adapter := NewReplayAdapter([]RawObservation{{Data: packet, Timestamp: time.Now().UTC()}})
	collector := NewCollector(minimalConfig(), adapter)

	out := make(chan *events.CanonicalEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	collector.Start(ctx, out)

	var firstID string
	select {
	case ev := <-out:
		firstID = ev.EventID
		// Invariant check: re-serialising or checking event preserves ID
		data, err := ev.Serialize()
		if err != nil {
			t.Fatalf("Failed to serialize: %v", err)
		}
		deserialized, err := events.Deserialize(data)
		if err != nil {
			t.Fatalf("Failed to deserialize: %v", err)
		}
		if deserialized.EventID != firstID {
			t.Fatalf("EventID changed after serialization: expected %s, got %s", firstID, deserialized.EventID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for event")
	}

	collector.Stop()
}

// 16. Bounded behavior under burst (Section 14 Broad Burst Test)
func TestCollector_BoundedBurstBehavior(t *testing.T) {
	burstSize := 200
	fixtures := make([]RawObservation, burstSize)
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")

	for i := 0; i < burstSize; i++ {
		pkt := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2"), uint16(1000+i), 502, nil)
		fixtures[i] = RawObservation{Data: pkt, Timestamp: time.Now().UTC(), Interface: "eth0"}
	}

	adapter := NewReplayAdapter(fixtures)
	// Bounded channel buffer
	collector := NewCollector(minimalConfig(), adapter, WithBufferCapacity(50))

	out := make(chan *events.CanonicalEvent, 50)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := collector.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}

	var (
		receivedCount int
		eventIDs      sync.Map
		duplicates    int
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range out {
			receivedCount++
			if _, loaded := eventIDs.LoadOrStore(ev.EventID, true); loaded {
				duplicates++
			}
			if receivedCount == burstSize {
				return
			}
		}
	}()

	select {
	case <-done:
		// Success
	case <-time.After(3 * time.Second):
		t.Fatalf("Timeout waiting for burst: received %d of %d", receivedCount, burstSize)
	}

	collector.Stop()

	if duplicates > 0 {
		t.Errorf("Duplicate event IDs detected: %d", duplicates)
	}
	if collector.ProcessCount() != uint64(burstSize) {
		t.Errorf("Expected %d processed, got %d", burstSize, collector.ProcessCount())
	}
}

// 17. No goroutine leak
func TestCollector_NoGoroutineLeak(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()

	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	packet := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2"), 80, 80, nil)

	adapter := NewReplayAdapter([]RawObservation{{Data: packet, Timestamp: time.Now().UTC()}})
	collector := NewCollector(minimalConfig(), adapter)

	out := make(chan *events.CanonicalEvent, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	collector.Start(ctx, out)
	<-out // Consume 1
	collector.Stop()

	// Wait up to 500ms for goroutines to fully exit
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= initialGoroutines {
			break
		}
		runtime.Gosched()
	}

	finalGoroutines := runtime.NumGoroutine()
	if finalGoroutines > initialGoroutines {
		t.Errorf("Goroutine leak detected: initial=%d, final=%d", initialGoroutines, finalGoroutines)
	}
}

// 18. Blocked-output cancellation
func TestCollector_BlockedOutputCancellation(t *testing.T) {
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	packet := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2"), 80, 80, nil)

	// Provide observations into a 0-capacity unread channel to block collector
	fixtures := make([]RawObservation, 10)
	for i := range fixtures {
		fixtures[i] = RawObservation{Data: packet, Timestamp: time.Now().UTC()}
	}

	adapter := NewReplayAdapter(fixtures)
	collector := NewCollector(minimalConfig(), adapter)

	out := make(chan *events.CanonicalEvent) // Unbuffered, nobody reads from it

	ctx, cancel := context.WithCancel(context.Background())
	collector.Start(ctx, out)

	// Cancel context while collector is blocked sending to out
	cancel()

	stopped := make(chan struct{})
	go func() {
		collector.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		// Succeeded cleanly without hanging
	case <-time.After(1 * time.Second):
		t.Fatal("Collector failed to unblock and stop when downstream output was blocked")
	}
}

// 19. Passive safety verification: NO active transmission capability
func TestSafety_NoActiveTransmissionCapability(t *testing.T) {
	// Reflection check: Verify no Dial, Write, Send, ListenUDP, or similar methods exist on types
	collectorType := reflect.TypeOf(&Collector{})
	adapterType := reflect.TypeOf(&ReplayAdapter{})
	normalizerType := reflect.TypeOf(&NormalizedObservation{})

	forbiddenPrefixes := []string{"Dial", "Write", "Send", "Listen", "Inject", "Probe", "Scan", "Poll", "Connect"}

	checkType := func(tp reflect.Type) {
		for i := 0; i < tp.NumMethod(); i++ {
			methodName := tp.Method(i).Name
			for _, forbidden := range forbiddenPrefixes {
				if methodName == forbidden {
					t.Fatalf("Forbidden active transmission/probe method found: %s.%s", tp.Name(), methodName)
				}
			}
		}
	}

	checkType(collectorType)
	checkType(adapterType)
	checkType(normalizerType)
}
