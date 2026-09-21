package passivenetwork

// ProtocolIdentifier defines an extension point for identifying OT/industrial protocols.
//
// PASSIVE SAFETY INVARIANT:
// Protocol identification MUST be strictly passive observation of received data.
// NO active discovery, polling, querying, or handshakes are permitted.
type ProtocolIdentifier interface {
	// Identify inspects the normalized observation and raw application payload.
	// It returns a protocol hint string and the corresponding confidence level.
	Identify(obs *NormalizedObservation, payload []byte) (protocolHint string, confidence ConfidenceLevel)
}

// DefaultProtocolIdentifier provides passive protocol identification and decoding
// for Modbus/TCP, as well as heuristic extension points for DNP3, EtherNet/IP, and S7.
//
// INVARIANTS:
// 1. Port-based hints without framing evidence are STRICTLY ConfidenceInferred.
// 2. Stronger confidence (ConfidenceKnown) requires verifiable application-layer framing evidence.
// 3. NO asset identity, vendor, or device role (e.g. "PLC" or "controller") may be inferred merely from ports or function codes.
type DefaultProtocolIdentifier struct{}

// NewDefaultProtocolIdentifier creates a new DefaultProtocolIdentifier.
func NewDefaultProtocolIdentifier() *DefaultProtocolIdentifier {
	return &DefaultProtocolIdentifier{}
}

// Identify performs passive protocol classification and application-layer decoding.
func (p *DefaultProtocolIdentifier) Identify(obs *NormalizedObservation, payload []byte) (string, ConfidenceLevel) {
	if obs == nil {
		return "unknown", ConfidenceUnknown
	}

	if obs.TransportProtocol == "TCP" {
		switch {
		case obs.SrcPort == 502 || obs.DstPort == 502:
			// Modbus/TCP candidate
			evidence, conf, flags := DecodeModbusTCP(obs, payload)
			for _, flag := range flags {
				obs.QualityFlags = append(obs.QualityFlags, flag)
			}

			if evidence != nil {
				if obs.Metadata == nil {
					obs.Metadata = make(map[string]interface{})
				}
				obs.Metadata["protocol_evidence"] = evidence
				obs.Metadata["modbus_transaction_id"] = evidence.TransactionID
				obs.Metadata["modbus_protocol_id"] = evidence.ProtocolID
				obs.Metadata["modbus_length"] = evidence.Length
				obs.Metadata["modbus_unit_id"] = evidence.UnitID
				obs.Metadata["modbus_function_code"] = evidence.FunctionCode
				obs.Metadata["modbus_function_name"] = evidence.FunctionName
				obs.Metadata["modbus_direction"] = evidence.Direction
				obs.Metadata["modbus_is_exception"] = evidence.IsException

				if evidence.StartingAddress != nil {
					obs.Metadata["modbus_starting_address"] = *evidence.StartingAddress
				}
				if evidence.Quantity != nil {
					obs.Metadata["modbus_quantity"] = *evidence.Quantity
				}
				if evidence.ExceptionCode != nil {
					obs.Metadata["modbus_exception_code"] = *evidence.ExceptionCode
				}
			}

			return "modbus", conf

		case obs.SrcPort == 20000 || obs.DstPort == 20000:
			// TCP 20000: DNP3 standard port (inferred only in Phase 3.1)
			return "dnp3", ConfidenceInferred

		case obs.SrcPort == 44818 || obs.DstPort == 44818:
			// TCP 44818: EtherNet/IP explicit messaging standard port (inferred only)
			return "ethernet_ip", ConfidenceInferred

		case obs.SrcPort == 102 || obs.DstPort == 102:
			// TCP 102: ISO-on-TCP / Siemens S7 standard port (inferred only)
			return "s7", ConfidenceInferred
		}
	} else if obs.TransportProtocol == "UDP" {
		switch {
		case obs.SrcPort == 20000 || obs.DstPort == 20000:
			// UDP 20000: DNP3 standard port
			return "dnp3", ConfidenceInferred

		case obs.SrcPort == 2222 || obs.DstPort == 2222:
			// UDP 2222: EtherNet/IP implicit I/O standard port
			return "ethernet_ip", ConfidenceInferred
		}
	}

	return "unknown", ConfidenceUnknown
}
