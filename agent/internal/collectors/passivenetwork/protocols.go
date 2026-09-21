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

// DefaultProtocolIdentifier provides port-based inferred hints and extension points
// for Modbus, DNP3, EtherNet/IP, and S7.
//
// Invariant: Port-based hints are STRICTLY ConfidenceInferred.
// Stronger confidence (ConfidenceKnown) requires verifiable application-layer framing/magic evidence.
// NO asset identity, vendor, or device role (e.g. "PLC" or "controller") may be inferred merely from ports.
type DefaultProtocolIdentifier struct{}

// NewDefaultProtocolIdentifier creates a new DefaultProtocolIdentifier.
func NewDefaultProtocolIdentifier() *DefaultProtocolIdentifier {
	return &DefaultProtocolIdentifier{}
}

// Identify performs passive protocol classification based on verified port heuristics.
func (p *DefaultProtocolIdentifier) Identify(obs *NormalizedObservation, payload []byte) (string, ConfidenceLevel) {
	if obs == nil {
		return "unknown", ConfidenceUnknown
	}

	if obs.TransportProtocol == "TCP" {
		switch {
		case obs.SrcPort == 502 || obs.DstPort == 502:
			// TCP 502: Modbus TCP standard port
			// Confidence is strictly Inferred in Phase 3.1A. Full MBAP parser belongs in later gates.
			return "modbus", ConfidenceInferred

		case obs.SrcPort == 20000 || obs.DstPort == 20000:
			// TCP 20000: DNP3 standard port
			return "dnp3", ConfidenceInferred

		case obs.SrcPort == 44818 || obs.DstPort == 44818:
			// TCP 44818: EtherNet/IP explicit messaging standard port
			return "ethernet_ip", ConfidenceInferred

		case obs.SrcPort == 102 || obs.DstPort == 102:
			// TCP 102: ISO-on-TCP / Siemens S7 standard port
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
