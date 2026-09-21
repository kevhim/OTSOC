package passivenetwork

import (
	"encoding/binary"
	"fmt"
	"net"
)

// NormalizeRawObservation decodes a raw passive network observation into a NormalizedObservation.
// It also returns the application payload slice (if any) for protocol inspection.
//
// PASSIVE SAFETY & EVIDENCE-PRESERVING RULES:
// 1. Unknown values MUST remain empty / nil.
// 2. Malformed or truncated packets MUST NOT be reinterpreted as valid traffic.
// 3. Asset identity, vendor, or role MUST NOT be fabricated.
// 4. Memory ownership: The returned payload slice is a sub-slice of raw.Data and MUST NOT
//    be stored into long-lived structures.
func NormalizeRawObservation(raw RawObservation) (*NormalizedObservation, []byte) {
	obs := &NormalizedObservation{
		ObservedAt:   raw.Timestamp,
		Interface:    raw.Interface,
		Confidence:   ConfidenceUnknown,
		ProtocolHint: "unknown",
		Metadata:     make(map[string]interface{}),
		QualityFlags: make([]string, 0),
	}

	data := raw.Data
	if len(data) == 0 {
		obs.QualityFlags = append(obs.QualityFlags, QualityTruncatedPacket)
		obs.Confidence = ConfidenceUnsupported
		return obs, nil
	}

	offset := 0
	var etherType uint16

	if raw.LinkType == "raw_ip" {
		// Raw IP link (e.g. tun or raw IP capture)
		etherType = 0x0800
	} else {
		// Default: Ethernet II encapsulation
		if len(data) < 14 {
			obs.QualityFlags = append(obs.QualityFlags, QualityTruncatedPacket)
			obs.Confidence = ConfidenceUnsupported
			return obs, nil
		}

		obs.DstMAC = net.HardwareAddr(data[0:6]).String()
		obs.SrcMAC = net.HardwareAddr(data[6:12]).String()
		etherType = binary.BigEndian.Uint16(data[12:14])
		offset = 14

		// Check for 802.1Q VLAN encapsulation
		if etherType == 0x8100 {
			if len(data) < offset+4 {
				obs.QualityFlags = append(obs.QualityFlags, QualityTruncatedPacket)
				obs.Confidence = ConfidenceUnsupported
				return obs, nil
			}
			tci := binary.BigEndian.Uint16(data[offset : offset+2])
			vlanID := tci & 0x0FFF
			obs.VLANID = &vlanID
			etherType = binary.BigEndian.Uint16(data[offset+2 : offset+4])
			offset += 4
		}
	}

	// Route based on EtherType
	switch etherType {
	case 0x0806: // ARP
		return decodeARP(data, offset, obs)
	case 0x0800: // IPv4
		return decodeIPv4(data, offset, obs)
	default:
		obs.TransportProtocol = fmt.Sprintf("ETH_0x%04X", etherType)
		obs.Confidence = ConfidenceUnsupported
		obs.QualityFlags = append(obs.QualityFlags, QualityUnsupportedProtocol)
		return obs, nil
	}
}

func decodeARP(data []byte, offset int, obs *NormalizedObservation) (*NormalizedObservation, []byte) {
	// Standard IPv4-over-Ethernet ARP frame requires 28 bytes
	if len(data) < offset+28 {
		obs.QualityFlags = append(obs.QualityFlags, QualityTruncatedPacket)
		obs.Confidence = ConfidenceUnsupported
		return obs, nil
	}

	htype := binary.BigEndian.Uint16(data[offset : offset+2])
	ptype := binary.BigEndian.Uint16(data[offset+2 : offset+4])
	hlen := data[offset+4]
	plen := data[offset+5]
	oper := binary.BigEndian.Uint16(data[offset+6 : offset+8])

	// Validate Ethernet (1) + IPv4 (0x0800)
	if htype != 1 || ptype != 0x0800 || hlen != 6 || plen != 4 {
		obs.TransportProtocol = "ARP"
		obs.Confidence = ConfidenceUnsupported
		obs.QualityFlags = append(obs.QualityFlags, QualityUnsupportedProtocol)
		return obs, nil
	}

	sha := data[offset+8 : offset+14]
	spa := data[offset+14 : offset+18]
	tha := data[offset+18 : offset+24]
	tpa := data[offset+24 : offset+28]

	obs.TransportProtocol = "ARP"
	obs.Confidence = ConfidenceKnown
	obs.SrcMAC = net.HardwareAddr(sha).String()
	obs.DstMAC = net.HardwareAddr(tha).String()
	obs.SrcIP = net.IP(spa).String()
	obs.DstIP = net.IP(tpa).String()

	switch oper {
	case 1:
		obs.Metadata["arp_operation"] = "request"
	case 2:
		obs.Metadata["arp_operation"] = "reply"
	default:
		obs.Metadata["arp_operation"] = fmt.Sprintf("opcode_%d", oper)
	}

	return obs, nil
}

func decodeIPv4(data []byte, offset int, obs *NormalizedObservation) (*NormalizedObservation, []byte) {
	if len(data) < offset+20 {
		obs.QualityFlags = append(obs.QualityFlags, QualityTruncatedPacket)
		obs.Confidence = ConfidenceUnsupported
		return obs, nil
	}

	ver := data[offset] >> 4
	if ver != 4 {
		obs.QualityFlags = append(obs.QualityFlags, QualityMalformedFrame)
		obs.Confidence = ConfidenceUnsupported
		return obs, nil
	}

	ihl := int(data[offset]&0x0F) * 4
	if ihl < 20 || len(data) < offset+ihl {
		obs.QualityFlags = append(obs.QualityFlags, QualityMalformedFrame)
		obs.Confidence = ConfidenceUnsupported
		return obs, nil
	}

	protocol := data[offset+9]
	obs.SrcIP = net.IP(data[offset+12 : offset+16]).String()
	obs.DstIP = net.IP(data[offset+16 : offset+20]).String()

	l4Offset := offset + ihl
	l4Data := data[l4Offset:]

	switch protocol {
	case 6: // TCP
		return decodeTCP(l4Data, obs)
	case 17: // UDP
		return decodeUDP(l4Data, obs)
	case 1: // ICMP
		return decodeICMP(l4Data, obs)
	default:
		obs.TransportProtocol = fmt.Sprintf("IP_%d", protocol)
		obs.Confidence = ConfidenceUnsupported
		obs.QualityFlags = append(obs.QualityFlags, QualityUnsupportedProtocol)
		return obs, nil
	}
}

func decodeTCP(l4 []byte, obs *NormalizedObservation) (*NormalizedObservation, []byte) {
	if len(l4) < 20 {
		obs.QualityFlags = append(obs.QualityFlags, QualityTruncatedPacket)
		obs.Confidence = ConfidenceUnsupported
		return obs, nil
	}

	obs.TransportProtocol = "TCP"
	obs.SrcPort = binary.BigEndian.Uint16(l4[0:2])
	obs.DstPort = binary.BigEndian.Uint16(l4[2:4])

	dataOffset := int(l4[12]>>4) * 4
	if dataOffset < 20 || len(l4) < dataOffset {
		obs.QualityFlags = append(obs.QualityFlags, QualityMalformedFrame)
		obs.Confidence = ConfidenceUnsupported
		return obs, nil
	}

	obs.Confidence = ConfidenceKnown
	payload := l4[dataOffset:]
	obs.PayloadLength = len(payload)
	return obs, payload
}

func decodeUDP(l4 []byte, obs *NormalizedObservation) (*NormalizedObservation, []byte) {
	if len(l4) < 8 {
		obs.QualityFlags = append(obs.QualityFlags, QualityTruncatedPacket)
		obs.Confidence = ConfidenceUnsupported
		return obs, nil
	}

	obs.TransportProtocol = "UDP"
	obs.SrcPort = binary.BigEndian.Uint16(l4[0:2])
	obs.DstPort = binary.BigEndian.Uint16(l4[2:4])

	obs.Confidence = ConfidenceKnown
	payload := l4[8:]
	obs.PayloadLength = len(payload)
	return obs, payload
}

func decodeICMP(l4 []byte, obs *NormalizedObservation) (*NormalizedObservation, []byte) {
	if len(l4) < 4 {
		obs.QualityFlags = append(obs.QualityFlags, QualityTruncatedPacket)
		obs.Confidence = ConfidenceUnsupported
		return obs, nil
	}

	obs.TransportProtocol = "ICMP"
	obs.Confidence = ConfidenceKnown
	obs.Metadata["icmp_type"] = int(l4[0])
	obs.Metadata["icmp_code"] = int(l4[1])
	obs.PayloadLength = len(l4)
	return obs, l4
}
