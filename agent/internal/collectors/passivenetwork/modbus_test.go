package passivenetwork

import (
	"context"
	"net"
	"testing"
	"time"

	"redcyberfox/pkg/events"
)

// Fixture 1: Valid Read Request (FC 3 Read Holding Registers)
func TestModbus_Fixture1_ValidRequest(t *testing.T) {
	// TxID: 1, ProtoID: 0, UnitID: 1, FC: 3, StartAddr: 100, Qty: 10
	payload := buildModbusTCPPayload(1, 0, 1, ModbusFuncReadHoldingRegisters, []byte{0x00, 0x64, 0x00, 0x0A})
	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		SrcPort:           49152,
		DstPort:           502,
	}

	evidence, conf, flags := DecodeModbusTCP(obs, payload)
	if conf != ConfidenceKnown {
		t.Fatalf("Expected ConfidenceKnown, got %s", conf)
	}
	if len(flags) > 0 {
		t.Errorf("Unexpected quality flags: %v", flags)
	}
	if evidence == nil {
		t.Fatal("Expected non-nil ModbusEvidence")
	}
	if evidence.TransactionID != 1 || evidence.ProtocolID != 0 || evidence.UnitID != 1 {
		t.Errorf("MBAP header mismatch: %+v", evidence)
	}
	if evidence.FunctionCode != 3 || evidence.FunctionName != "read_holding_registers" {
		t.Errorf("Function mismatch: code=%d, name=%s", evidence.FunctionCode, evidence.FunctionName)
	}
	if evidence.Direction != "request" {
		t.Errorf("Expected direction request, got %s", evidence.Direction)
	}
	if evidence.StartingAddress == nil || *evidence.StartingAddress != 100 {
		t.Errorf("Starting address mismatch: %v", evidence.StartingAddress)
	}
	if evidence.Quantity == nil || *evidence.Quantity != 10 {
		t.Errorf("Quantity mismatch: %v", evidence.Quantity)
	}
}

// Fixture 2: Valid Response (FC 3 Read Holding Registers Response)
func TestModbus_Fixture2_ValidResponse(t *testing.T) {
	// TxID: 1, ProtoID: 0, UnitID: 1, FC: 3, ByteCount: 2, Val: 0x1234
	payload := buildModbusTCPPayload(1, 0, 1, ModbusFuncReadHoldingRegisters, []byte{0x02, 0x12, 0x34})
	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		SrcPort:           502,
		DstPort:           49152,
	}

	evidence, conf, flags := DecodeModbusTCP(obs, payload)
	if conf != ConfidenceKnown {
		t.Fatalf("Expected ConfidenceKnown, got %s", conf)
	}
	if len(flags) > 0 {
		t.Errorf("Unexpected quality flags: %v", flags)
	}
	if evidence.Direction != "response" {
		t.Errorf("Expected direction response, got %s", evidence.Direction)
	}
	if evidence.IsException {
		t.Errorf("Expected non-exception response")
	}
}

// Fixture 3: Valid Read Function Request (FC 1 Read Coils)
func TestModbus_Fixture3_ValidReadFunctionRequest(t *testing.T) {
	// TxID: 2, ProtoID: 0, UnitID: 1, FC: 1, StartAddr: 0, Qty: 8
	payload := buildModbusTCPPayload(2, 0, 1, ModbusFuncReadCoils, []byte{0x00, 0x00, 0x00, 0x08})
	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		SrcPort:           12345,
		DstPort:           502,
	}

	evidence, conf, flags := DecodeModbusTCP(obs, payload)
	if conf != ConfidenceKnown {
		t.Fatalf("Expected ConfidenceKnown, got %s", conf)
	}
	if len(flags) > 0 {
		t.Errorf("Unexpected quality flags: %v", flags)
	}
	if evidence.FunctionName != "read_coils" {
		t.Errorf("Expected read_coils, got %s", evidence.FunctionName)
	}
	if evidence.StartingAddress == nil || *evidence.StartingAddress != 0 {
		t.Errorf("Starting address mismatch: %v", evidence.StartingAddress)
	}
}

// Fixture 4: Valid Write Function Request (FC 6 Write Single Register)
func TestModbus_Fixture4_ValidWriteFunctionRequest(t *testing.T) {
	// TxID: 3, ProtoID: 0, UnitID: 1, FC: 6, Addr: 10, Val: 1000
	payload := buildModbusTCPPayload(3, 0, 1, ModbusFuncWriteSingleRegister, []byte{0x00, 0x0A, 0x03, 0xE8})
	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		SrcPort:           12345,
		DstPort:           502,
	}

	evidence, conf, flags := DecodeModbusTCP(obs, payload)
	if conf != ConfidenceKnown {
		t.Fatalf("Expected ConfidenceKnown, got %s", conf)
	}
	if len(flags) > 0 {
		t.Errorf("Unexpected quality flags: %v", flags)
	}
	if evidence.FunctionName != "write_single_register" {
		t.Errorf("Expected write_single_register, got %s", evidence.FunctionName)
	}
	if evidence.StartingAddress == nil || *evidence.StartingAddress != 10 {
		t.Errorf("Starting address mismatch: %v", evidence.StartingAddress)
	}
}

// Fixture 5: Invalid Protocol Identifier
func TestModbus_Fixture5_InvalidProtocolIdentifier(t *testing.T) {
	// ProtoID = 1 instead of 0
	payload := buildModbusTCPPayload(4, 1, 1, 3, []byte{0x00, 0x64, 0x00, 0x0A})
	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		DstPort:           502,
	}

	evidence, conf, flags := DecodeModbusTCP(obs, payload)
	if conf == ConfidenceKnown {
		t.Errorf("Invalid ProtocolID must not yield ConfidenceKnown")
	}
	if evidence != nil {
		t.Errorf("Expected nil evidence for invalid MBAP, got %+v", evidence)
	}
	foundFlag := false
	for _, f := range flags {
		if f == QualityModbusInvalidMBAP {
			foundFlag = true
		}
	}
	if !foundFlag {
		t.Errorf("Expected QualityModbusInvalidMBAP flag, got %v", flags)
	}
}

// Fixture 6: Invalid MBAP Length
func TestModbus_Fixture6_InvalidMBAPLength(t *testing.T) {
	// Manually construct payload with length = 1 (minimum is 2)
	payload := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x01}
	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		DstPort:           502,
	}

	evidence, conf, flags := DecodeModbusTCP(obs, payload)
	if conf == ConfidenceKnown {
		t.Errorf("Invalid MBAP length must not yield ConfidenceKnown")
	}
	if evidence != nil {
		t.Errorf("Expected nil evidence for invalid MBAP length, got %+v", evidence)
	}
	foundFlag := false
	for _, f := range flags {
		if f == QualityModbusInvalidMBAP {
			foundFlag = true
		}
	}
	if !foundFlag {
		t.Errorf("Expected QualityModbusInvalidMBAP flag, got %v", flags)
	}
}

// Fixture 7: Truncated MBAP
func TestModbus_Fixture7_TruncatedMBAP(t *testing.T) {
	// Only 4 bytes of MBAP
	payload := []byte{0x00, 0x01, 0x00, 0x00}
	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		DstPort:           502,
	}

	evidence, conf, flags := DecodeModbusTCP(obs, payload)
	if conf == ConfidenceKnown {
		t.Errorf("Truncated MBAP must not yield ConfidenceKnown")
	}
	if evidence != nil {
		t.Errorf("Expected nil evidence, got %+v", evidence)
	}
	foundFlag := false
	for _, f := range flags {
		if f == QualityModbusTruncatedPayload {
			foundFlag = true
		}
	}
	if !foundFlag {
		t.Errorf("Expected QualityModbusTruncatedPayload flag, got %v", flags)
	}
}

// Fixture 8: Truncated Function Payload
func TestModbus_Fixture8_TruncatedFunctionPayload(t *testing.T) {
	// Valid MBAP with FC 3 request, but only 1 byte of PDU payload instead of 4
	payload := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x03, 0x01, 0x03, 0x00}
	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		SrcPort:           12345,
		DstPort:           502,
	}

	evidence, conf, flags := DecodeModbusTCP(obs, payload)
	if conf != ConfidenceKnown {
		t.Fatalf("Valid MBAP framing should yield ConfidenceKnown, got %s", conf)
	}
	if evidence == nil {
		t.Fatal("Expected non-nil evidence")
	}
	foundFlag := false
	for _, f := range flags {
		if f == QualityModbusTruncatedPayload {
			foundFlag = true
		}
	}
	if !foundFlag {
		t.Errorf("Expected QualityModbusTruncatedPayload flag for truncated PDU, got %v", flags)
	}
}

// Fixture 9: Unsupported Function Code
func TestModbus_Fixture9_UnsupportedFunctionCode(t *testing.T) {
	// FC = 69 (unsupported)
	payload := buildModbusTCPPayload(5, 0, 1, 69, []byte{0x01, 0x02})
	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		DstPort:           502,
	}

	evidence, conf, flags := DecodeModbusTCP(obs, payload)
	if conf != ConfidenceKnown {
		t.Fatalf("Valid MBAP framing should yield ConfidenceKnown, got %s", conf)
	}
	if evidence == nil {
		t.Fatal("Expected non-nil evidence")
	}
	if evidence.FunctionCode != 69 {
		t.Errorf("FunctionCode mismatch: %d", evidence.FunctionCode)
	}
	foundFlag := false
	for _, f := range flags {
		if f == QualityModbusUnsupportedFunction {
			foundFlag = true
		}
	}
	if !foundFlag {
		t.Errorf("Expected QualityModbusUnsupportedFunction flag, got %v", flags)
	}
}

// Fixture 10: TCP/502 traffic that is NOT valid Modbus (False-Positive Safety)
func TestModbus_Fixture10_TCP502NonModbusTraffic(t *testing.T) {
	// HTTP GET request to port 502
	payload := []byte("GET /index.html HTTP/1.1\r\nHost: plc.local\r\n\r\n")
	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		DstPort:           502,
	}

	evidence, conf, flags := DecodeModbusTCP(obs, payload)
	if conf == ConfidenceKnown {
		t.Fatalf("HTTP traffic to port 502 must NOT yield ConfidenceKnown!")
	}
	if evidence != nil {
		t.Errorf("Expected nil evidence for non-Modbus payload on port 502, got %+v", evidence)
	}
	foundFlag := false
	for _, f := range flags {
		if f == QualityModbusInvalidMBAP {
			foundFlag = true
		}
	}
	if !foundFlag {
		t.Errorf("Expected QualityModbusInvalidMBAP flag for HTTP traffic, got %v", flags)
	}
}

// Exception Response Test
func TestModbus_ExceptionResponse(t *testing.T) {
	// FC 0x83 (Read Holding Registers Exception) with ExceptionCode 0x02 (Illegal Data Address)
	payload := buildModbusTCPPayload(10, 0, 1, 0x83, []byte{0x02})
	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		SrcPort:           502,
		DstPort:           12345,
	}

	evidence, conf, flags := DecodeModbusTCP(obs, payload)
	if conf != ConfidenceKnown {
		t.Fatalf("Expected ConfidenceKnown, got %s", conf)
	}
	if len(flags) > 0 {
		t.Errorf("Unexpected quality flags: %v", flags)
	}
	if !evidence.IsException {
		t.Errorf("Expected is_exception = true")
	}
	if evidence.Direction != "response" {
		t.Errorf("Expected direction response, got %s", evidence.Direction)
	}
	if evidence.ExceptionCode == nil || *evidence.ExceptionCode != 2 {
		t.Errorf("Exception code mismatch: %v", evidence.ExceptionCode)
	}
	if evidence.FunctionName != "read_holding_registers_exception" {
		t.Errorf("FunctionName mismatch: %s", evidence.FunctionName)
	}
}

// False-Positive Safety: Port 502 with valid Modbus MUST NEVER fabricate PLC identity
func TestModbus_FalsePositiveSafety_NoPLCIdentityFabricated(t *testing.T) {
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	payload := buildModbusTCPPayload(1, 0, 1, ModbusFuncReadHoldingRegisters, []byte{0x00, 0x64, 0x00, 0x0A})
	pkt := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("192.168.1.50"), net.ParseIP("192.168.1.100"), 49152, 502, payload)

	raw := RawObservation{
		Data:      pkt,
		Timestamp: time.Now().UTC(),
		Interface: "eth0",
	}

	adapter := NewReplayAdapter([]RawObservation{raw})
	collector := NewCollector(minimalConfig(), adapter)

	out := make(chan *events.CanonicalEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := collector.Start(ctx, out); err != nil {
		t.Fatalf("Collector start failed: %v", err)
	}

	select {
	case ev := <-out:
		// AC-3 / Safety assertion:
		if ev.AssetID != "" {
			t.Errorf("AssetID MUST NOT be fabricated from Modbus traffic, got %s", ev.AssetID)
		}
		if ev.Metadata["device_role"] != nil {
			t.Errorf("device_role MUST NOT be fabricated, got %v", ev.Metadata["device_role"])
		}
		if ev.Metadata["vendor"] != nil {
			t.Errorf("vendor MUST NOT be fabricated, got %v", ev.Metadata["vendor"])
		}
		if ev.Metadata["is_plc"] != nil {
			t.Errorf("is_plc MUST NOT be fabricated, got %v", ev.Metadata["is_plc"])
		}
		// But protocol evidence MUST be present and ConfidenceKnown!
		if ev.Protocol != "modbus" {
			t.Errorf("Expected protocol 'modbus', got %s", ev.Protocol)
		}
		if ev.Metadata["confidence"] != string(ConfidenceKnown) {
			t.Errorf("Expected confidence 'known', got %v", ev.Metadata["confidence"])
		}
		if ev.Metadata["modbus_function_name"] != "read_holding_registers" {
			t.Errorf("Expected function 'read_holding_registers', got %v", ev.Metadata["modbus_function_name"])
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for CanonicalEvent")
	}

	collector.Stop()
}

// Security: Parser Panic Safety on Arbitrary / Malformed Input
func TestModbus_Security_PanicSafety(t *testing.T) {
	testInputs := [][]byte{
		{},
		{0x00},
		{0x00, 0x01},
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
		{0x00, 0x01, 0x00, 0x00, 0xFF, 0xFF, 0x01, 0x03}, // Giant length field
		{0x00, 0x01, 0x00, 0x00, 0x00, 0x02, 0x01, 0xFF}, // Unknown high function code
	}

	obs := &NormalizedObservation{
		TransportProtocol: "TCP",
		DstPort:           502,
	}

	for i, input := range testInputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Parser panicked on test input %d: %v", i, r)
				}
			}()
			DecodeModbusTCP(obs, input)
		}()
	}
}

// CanonicalEvent schema validation for Modbus event
func TestModbus_CanonicalEventIntegration(t *testing.T) {
	srcMAC, _ := net.ParseMAC("00:11:22:33:44:55")
	dstMAC, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	payload := buildModbusTCPPayload(100, 0, 1, ModbusFuncWriteSingleCoil, []byte{0x00, 0x05, 0xFF, 0x00})
	pkt := buildEthernetIPv4TCP(srcMAC, dstMAC, net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2"), 12345, 502, payload)

	adapter := NewReplayAdapter([]RawObservation{{Data: pkt, Timestamp: time.Now().UTC()}})
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
		if ev.Protocol != "modbus" {
			t.Errorf("Expected protocol modbus, got %s", ev.Protocol)
		}
		if ev.Metadata["modbus_function_name"] != "write_single_coil" {
			t.Errorf("Expected write_single_coil, got %v", ev.Metadata["modbus_function_name"])
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for event")
	}

	collector.Stop()
}
