package passivenetwork

import (
	"encoding/binary"
	"net"
)

// Helper functions to generate deterministic packet fixtures using standard library only.

func buildEthernetHeader(srcMAC, dstMAC net.HardwareAddr, etherType uint16) []byte {
	buf := make([]byte, 14)
	copy(buf[0:6], dstMAC)
	copy(buf[6:12], srcMAC)
	binary.BigEndian.PutUint16(buf[12:14], etherType)
	return buf
}

func buildVLANHeader(srcMAC, dstMAC net.HardwareAddr, vlanID uint16, innerEtherType uint16) []byte {
	buf := make([]byte, 18)
	copy(buf[0:6], dstMAC)
	copy(buf[6:12], srcMAC)
	binary.BigEndian.PutUint16(buf[12:14], 0x8100) // 802.1Q
	binary.BigEndian.PutUint16(buf[14:16], vlanID&0x0FFF)
	binary.BigEndian.PutUint16(buf[16:18], innerEtherType)
	return buf
}

func buildIPv4Header(srcIP, dstIP net.IP, protocol byte, payloadLen int) []byte {
	buf := make([]byte, 20)
	buf[0] = 0x45 // Version 4, IHL 5 (20 bytes)
	buf[1] = 0x00
	totalLen := 20 + payloadLen
	binary.BigEndian.PutUint16(buf[2:4], uint16(totalLen))
	binary.BigEndian.PutUint16(buf[4:6], 0x1234) // ID
	binary.BigEndian.PutUint16(buf[6:8], 0x0000) // Flags / Frag
	buf[8] = 64                                  // TTL
	buf[9] = protocol
	binary.BigEndian.PutUint16(buf[10:12], 0x0000) // Checksum (ignored in passive decode)
	copy(buf[12:16], srcIP.To4())
	copy(buf[16:20], dstIP.To4())
	return buf
}

func buildTCPHeader(srcPort, dstPort uint16, payload []byte) []byte {
	buf := make([]byte, 20+len(payload))
	binary.BigEndian.PutUint16(buf[0:2], srcPort)
	binary.BigEndian.PutUint16(buf[2:4], dstPort)
	binary.BigEndian.PutUint32(buf[4:8], 1000) // Seq
	binary.BigEndian.PutUint32(buf[8:12], 0)   // Ack
	buf[12] = 0x50                             // Data offset 5 (20 bytes)
	buf[13] = 0x02                             // Flags: SYN
	binary.BigEndian.PutUint16(buf[14:16], 65535)
	binary.BigEndian.PutUint16(buf[16:18], 0)
	binary.BigEndian.PutUint16(buf[18:20], 0)
	copy(buf[20:], payload)
	return buf
}

func buildUDPHeader(srcPort, dstPort uint16, payload []byte) []byte {
	buf := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint16(buf[0:2], srcPort)
	binary.BigEndian.PutUint16(buf[2:4], dstPort)
	binary.BigEndian.PutUint16(buf[4:6], uint16(8+len(payload)))
	binary.BigEndian.PutUint16(buf[6:8], 0)
	copy(buf[8:], payload)
	return buf
}

func buildARP(sha, tha net.HardwareAddr, spa, tpa net.IP, oper uint16) []byte {
	eth := buildEthernetHeader(sha, tha, 0x0806)
	arp := make([]byte, 28)
	binary.BigEndian.PutUint16(arp[0:2], 1)      // Ethernet
	binary.BigEndian.PutUint16(arp[2:4], 0x0800) // IPv4
	arp[4] = 6                                  // HLEN
	arp[5] = 4                                  // PLEN
	binary.BigEndian.PutUint16(arp[6:8], oper)
	copy(arp[8:14], sha)
	copy(arp[14:18], spa.To4())
	copy(arp[18:24], tha)
	copy(arp[24:28], tpa.To4())

	res := append(eth, arp...)
	return res
}

func buildEthernetIPv4TCP(srcMAC, dstMAC net.HardwareAddr, srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	tcp := buildTCPHeader(srcPort, dstPort, payload)
	ip := buildIPv4Header(srcIP, dstIP, 6, len(tcp))
	eth := buildEthernetHeader(srcMAC, dstMAC, 0x0800)

	res := append(eth, ip...)
	res = append(res, tcp...)
	return res
}

func buildEthernetIPv4UDP(srcMAC, dstMAC net.HardwareAddr, srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	udp := buildUDPHeader(srcPort, dstPort, payload)
	ip := buildIPv4Header(srcIP, dstIP, 17, len(udp))
	eth := buildEthernetHeader(srcMAC, dstMAC, 0x0800)

	res := append(eth, ip...)
	res = append(res, udp...)
	return res
}

func buildEthernetVLANIPv4TCP(srcMAC, dstMAC net.HardwareAddr, vlanID uint16, srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	tcp := buildTCPHeader(srcPort, dstPort, payload)
	ip := buildIPv4Header(srcIP, dstIP, 6, len(tcp))
	vlan := buildVLANHeader(srcMAC, dstMAC, vlanID, 0x0800)

	res := append(vlan, ip...)
	res = append(res, tcp...)
	return res
}
