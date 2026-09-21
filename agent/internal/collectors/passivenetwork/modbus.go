package passivenetwork

import (
	"encoding/binary"
	"fmt"
)

// Modbus quality flags.
const (
	QualityModbusInvalidMBAP         = "MODBUS_INVALID_MBAP"
	QualityModbusTruncatedPayload    = "MODBUS_TRUNCATED_PAYLOAD"
	QualityModbusUnsupportedFunction = "MODBUS_UNSUPPORTED_FUNCTION"
)

// Standard Modbus Function Codes recognized in Phase 3.1B.
const (
	ModbusFuncReadCoils              uint8 = 1
	ModbusFuncReadDiscreteInputs     uint8 = 2
	ModbusFuncReadHoldingRegisters   uint8 = 3
	ModbusFuncReadInputRegisters     uint8 = 4
	ModbusFuncWriteSingleCoil        uint8 = 5
	ModbusFuncWriteSingleRegister    uint8 = 6
	ModbusFuncWriteMultipleCoils     uint8 = 15
	ModbusFuncWriteMultipleRegisters uint8 = 16
)

// ModbusEvidence represents structured passive evidence decoded from a Modbus/TCP frame.
//
// PASSIVE INVARIANT:
// Under NO circumstances does ModbusEvidence infer or assign asset identity, vendor, or device role.
//
// STATE INVARIANT:
// REQUEST/RESPONSE CORRELATION = FUTURE PHASE
// Transaction correlation across multiple packets is deferred to a future phase.
// Only intra-packet context (ports, exception bit, PDU shape) is evaluated.
type ModbusEvidence struct {
	TransactionID   uint16  `json:"transaction_id"`
	ProtocolID      uint16  `json:"protocol_id"`
	Length          uint16  `json:"length"`
	UnitID          uint8   `json:"unit_id"`
	FunctionCode    uint8   `json:"function_code"`
	FunctionName    string  `json:"function_name"`
	IsException     bool    `json:"is_exception"`
	ExceptionCode   *uint8  `json:"exception_code,omitempty"`
	Direction       string  `json:"direction"` // "request", "response", or "unknown"
	StartingAddress *uint16 `json:"starting_address,omitempty"`
	Quantity        *uint16 `json:"quantity,omitempty"`
}

// FunctionNameFromCode returns a human-readable name for supported Modbus function codes.
func FunctionNameFromCode(fc uint8) (string, bool) {
	switch fc {
	case ModbusFuncReadCoils:
		return "read_coils", true
	case ModbusFuncReadDiscreteInputs:
		return "read_discrete_inputs", true
	case ModbusFuncReadHoldingRegisters:
		return "read_holding_registers", true
	case ModbusFuncReadInputRegisters:
		return "read_input_registers", true
	case ModbusFuncWriteSingleCoil:
		return "write_single_coil", true
	case ModbusFuncWriteSingleRegister:
		return "write_single_register", true
	case ModbusFuncWriteMultipleCoils:
		return "write_multiple_coils", true
	case ModbusFuncWriteMultipleRegisters:
		return "write_multiple_registers", true
	default:
		return fmt.Sprintf("unsupported_fc_%d", fc), false
	}
}

// DecodeModbusTCP decodes a raw application payload into structured ModbusEvidence.
// It returns the decoded evidence, the evidence-based confidence level, and any quality flags.
//
// PASSIVE SAFETY & BOUNDEDNESS:
// 1. All offsets and lengths are strictly bounds-checked; this function NEVER panics.
// 2. No heap byte slices are allocated or retained; raw packet buffers are inspected synchronously.
// 3. Port 502 with non-Modbus payload NEVER yields ConfidenceKnown.
func DecodeModbusTCP(obs *NormalizedObservation, payload []byte) (*ModbusEvidence, ConfidenceLevel, []string) {
	flags := make([]string, 0)

	// MBAP Header requires at least 7 bytes (TransactionID:2, ProtocolID:2, Length:2, UnitID:1)
	if len(payload) < 7 {
		flags = append(flags, QualityModbusTruncatedPayload)
		return nil, ConfidenceInferred, flags
	}

	txID := binary.BigEndian.Uint16(payload[0:2])
	protoID := binary.BigEndian.Uint16(payload[2:4])
	mbapLen := binary.BigEndian.Uint16(payload[4:6])
	unitID := payload[6]

	// Protocol Identifier MUST be 0x0000 for standard Modbus TCP
	if protoID != 0 {
		flags = append(flags, QualityModbusInvalidMBAP)
		return nil, ConfidenceInferred, flags
	}

	// Length field in MBAP counts remaining bytes: UnitID (1) + PDU (min 1 byte for Function Code)
	// Therefore, minimum valid MBAP length is 2.
	if mbapLen < 2 {
		flags = append(flags, QualityModbusInvalidMBAP)
		return nil, ConfidenceInferred, flags
	}

	// Check if the payload contains at least the UnitID (byte 6) and Function Code (byte 7)
	if len(payload) < 8 {
		flags = append(flags, QualityModbusTruncatedPayload)
		return nil, ConfidenceInferred, flags
	}

	rawFC := payload[7]
	evidence := &ModbusEvidence{
		TransactionID: txID,
		ProtocolID:    protoID,
		Length:        mbapLen,
		UnitID:        unitID,
		FunctionCode:  rawFC,
		Direction:     "unknown",
	}

	// Direction heuristic based on port context
	if obs != nil {
		if obs.DstPort == 502 && obs.SrcPort != 502 {
			evidence.Direction = "request"
		} else if obs.SrcPort == 502 && obs.DstPort != 502 {
			evidence.Direction = "response"
		}
	}

	// Handle Modbus Exception responses (bit 7 set: FunctionCode >= 0x80)
	if rawFC >= 0x80 {
		evidence.IsException = true
		evidence.Direction = "response"
		origFC := rawFC & 0x7F
		name, _ := FunctionNameFromCode(origFC)
		evidence.FunctionName = name + "_exception"

		if len(payload) >= 9 {
			excCode := payload[8]
			evidence.ExceptionCode = &excCode
		} else {
			flags = append(flags, QualityModbusTruncatedPayload)
		}

		return evidence, ConfidenceKnown, flags
	}

	// Normal (non-exception) function code
	name, supported := FunctionNameFromCode(rawFC)
	evidence.FunctionName = name
	if !supported {
		flags = append(flags, QualityModbusUnsupportedFunction)
		// Even if the function code is unsupported, valid MBAP framing (protoID==0, mbapLen>=2)
		// establishes known Modbus/TCP framing.
		return evidence, ConfidenceKnown, flags
	}

	// Bounded function-specific parsing where safely establishable
	pduPayload := payload[8:]
	switch rawFC {
	case ModbusFuncReadCoils, ModbusFuncReadDiscreteInputs, ModbusFuncReadHoldingRegisters, ModbusFuncReadInputRegisters:
		if evidence.Direction == "request" {
			if len(pduPayload) >= 4 {
				startAddr := binary.BigEndian.Uint16(pduPayload[0:2])
				qty := binary.BigEndian.Uint16(pduPayload[2:4])
				evidence.StartingAddress = &startAddr
				evidence.Quantity = &qty
			} else {
				flags = append(flags, QualityModbusTruncatedPayload)
			}
		}

	case ModbusFuncWriteSingleCoil, ModbusFuncWriteSingleRegister:
		if len(pduPayload) >= 4 {
			addr := binary.BigEndian.Uint16(pduPayload[0:2])
			evidence.StartingAddress = &addr
		} else {
			flags = append(flags, QualityModbusTruncatedPayload)
		}

	case ModbusFuncWriteMultipleCoils, ModbusFuncWriteMultipleRegisters:
		if evidence.Direction == "request" {
			if len(pduPayload) >= 4 {
				startAddr := binary.BigEndian.Uint16(pduPayload[0:2])
				qty := binary.BigEndian.Uint16(pduPayload[2:4])
				evidence.StartingAddress = &startAddr
				evidence.Quantity = &qty
			} else {
				flags = append(flags, QualityModbusTruncatedPayload)
			}
		}
	}

	return evidence, ConfidenceKnown, flags
}
