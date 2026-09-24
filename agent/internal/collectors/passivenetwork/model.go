package passivenetwork

import "time"

// ConfidenceLevel represents the evidence certainty for an observation or protocol identification.
type ConfidenceLevel string

const (
	// ConfidenceKnown indicates verifiable application-layer or structural evidence.
	ConfidenceKnown ConfidenceLevel = "known"
	// ConfidenceInferred indicates heuristic evidence (e.g. standard port number).
	ConfidenceInferred ConfidenceLevel = "inferred"
	// ConfidenceUnknown indicates insufficient information to determine the protocol or role.
	ConfidenceUnknown ConfidenceLevel = "unknown"
	// ConfidenceUnsupported indicates a protocol or frame format that cannot be parsed.
	ConfidenceUnsupported ConfidenceLevel = "unsupported"
)

// Quality flags for observations.
const (
	QualityMalformedFrame      = "MALFORMED_FRAME"
	QualityTruncatedPacket     = "TRUNCATED_PACKET"
	QualityBufferCongestion    = "BUFFER_CONGESTION"
	QualityUnsupportedProtocol = "UNSUPPORTED_PROTOCOL"
)

// RawObservation represents a passively observed network frame/packet at the capture boundary.
//
// Memory and Ownership:
// Data is owned by the producer/adapter during capture. Normalizers inspect Data
// synchronously and MUST NOT retain long-lived references to the raw byte slice.
// Only bounded metadata is preserved in normalized representations.
type RawObservation struct {
	Data      []byte
	Timestamp time.Time
	Interface string
	LinkType  string // e.g. "ethernet", "raw_ip"
}

// NormalizedObservation represents a normalized, structured passive network observation.
//
// Evidence-Preserving Invariant:
// Unknown or unobserved values MUST remain nil or empty.
// Values MUST NOT be fabricated. In particular:
//   - Asset identity, vendor, device role, or PLC classification MUST NOT be inferred
//     merely from IP or port numbers.
type NormalizedObservation struct {
	ObservedAt        time.Time              `json:"observed_at"`
	Interface         string                 `json:"interface"`
	SrcMAC            string                 `json:"src_mac,omitempty"`
	DstMAC            string                 `json:"dst_mac,omitempty"`
	SrcIP             string                 `json:"src_ip,omitempty"`
	DstIP             string                 `json:"dst_ip,omitempty"`
	SrcPort           uint16                 `json:"src_port,omitempty"`
	DstPort           uint16                 `json:"dst_port,omitempty"`
	TransportProtocol string                 `json:"transport_protocol,omitempty"` // e.g. "TCP", "UDP", "ARP", "ICMP"
	VLANID            *uint16                `json:"vlan_id,omitempty"`
	ProtocolHint      string                 `json:"protocol_hint,omitempty"` // e.g. "modbus", "dnp3", "unknown"
	Confidence        ConfidenceLevel        `json:"confidence"`
	PayloadLength     int                    `json:"payload_length"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
	QualityFlags      []string               `json:"quality_flags,omitempty"`
}
