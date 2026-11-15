// Package dhcp implementa el protocolo DHCPv4 (RFC 2131/2132/3442/3396).
package dhcp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"sort"
)

type MessageType byte

const (
	Discover MessageType = 1
	Offer    MessageType = 2
	Request  MessageType = 3
	Decline  MessageType = 4
	Ack      MessageType = 5
	Nak      MessageType = 6
	Release  MessageType = 7
	Inform   MessageType = 8
)

func (m MessageType) String() string {
	switch m {
	case Discover:
		return "DISCOVER"
	case Offer:
		return "OFFER"
	case Request:
		return "REQUEST"
	case Decline:
		return "DECLINE"
	case Ack:
		return "ACK"
	case Nak:
		return "NAK"
	case Release:
		return "RELEASE"
	case Inform:
		return "INFORM"
	}
	return "?"
}

// Códigos de opción.
const (
	OptSubnetMask     = 1
	OptTimeOffset     = 2
	OptRouter         = 3
	OptDNS            = 6
	OptHostname       = 12
	OptDomainName     = 15
	OptMTU            = 26
	OptBroadcast      = 28
	OptStaticRoute    = 33
	OptNTP            = 42
	OptNetBIOSNS      = 44
	OptRequestedIP    = 50
	OptLeaseTime      = 51
	OptMsgType        = 53
	OptServerID       = 54
	OptParamList      = 55
	OptMessage        = 56
	OptRenewalTime    = 58
	OptRebindingTime  = 59
	OptVendorClass    = 60
	OptClientID       = 61
	OptTFTPServer     = 66
	OptBootFile       = 67
	OptDomainSearch   = 119
	OptClasslessRoute = 121
)

const (
	BootRequest = 1
	BootReply   = 2
)

var magicCookie = []byte{99, 130, 83, 99}

// Packet es un mensaje BOOTP/DHCP.
type Packet struct {
	Op, HType, HLen, Hops byte
	XID                   uint32
	Secs, Flags           uint16
	CIAddr, YIAddr        net.IP
	SIAddr, GIAddr        net.IP
	CHAddr                net.HardwareAddr
	SName, File           string
	Options               map[byte][]byte
}

func ip4(b []byte) net.IP { return net.IP(append([]byte(nil), b...)) }

func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// Parse decodifica un datagrama.
func Parse(b []byte) (*Packet, error) {
	if len(b) < 240 {
		return nil, errors.New("paquete demasiado corto")
	}
	if !bytes.Equal(b[236:240], magicCookie) {
		return nil, errors.New("sin magic cookie DHCP")
	}
	p := &Packet{
		Op: b[0], HType: b[1], HLen: b[2], Hops: b[3],
		XID:     binary.BigEndian.Uint32(b[4:8]),
		Secs:    binary.BigEndian.Uint16(b[8:10]),
		Flags:   binary.BigEndian.Uint16(b[10:12]),
		CIAddr:  ip4(b[12:16]),
		YIAddr:  ip4(b[16:20]),
		SIAddr:  ip4(b[20:24]),
		GIAddr:  ip4(b[24:28]),
		SName:   cstr(b[44:108]),
		File:    cstr(b[108:236]),
		Options: map[byte][]byte{},
	}
	hl := int(b[2])
	if hl > 16 {
		hl = 16
	}
	p.CHAddr = net.HardwareAddr(append([]byte(nil), b[28:28+hl]...))
	o := b[240:]
	for i := 0; i < len(o); {
		c := o[i]
		if c == 0 {
			i++
			continue
		}
		if c == 255 {
			break
		}
		if i+1 >= len(o) || i+2+int(o[i+1]) > len(o) {
			return nil, errors.New("opción truncada")
		}
		l := int(o[i+1])
		p.Options[c] = append(p.Options[c], o[i+2:i+2+l]...) // RFC 3396: concatenar
		i += 2 + l
	}
	return p, nil
}

// Marshal serializa el paquete.
func (p *Packet) Marshal() []byte {
	b := make([]byte, 240, 576)
	b[0], b[1], b[2], b[3] = p.Op, p.HType, p.HLen, p.Hops
	binary.BigEndian.PutUint32(b[4:], p.XID)
	binary.BigEndian.PutUint16(b[8:], p.Secs)
	binary.BigEndian.PutUint16(b[10:], p.Flags)
	for i, ip := range []net.IP{p.CIAddr, p.YIAddr, p.SIAddr, p.GIAddr} {
		if v := ip.To4(); v != nil {
			copy(b[12+4*i:], v)
		}
	}
	copy(b[28:44], p.CHAddr)
	copy(b[44:108], p.SName)
	copy(b[108:236], p.File)
	copy(b[236:], magicCookie)

	put := func(code byte, d []byte) {
		if code == 0 || code == 255 {
			return
		}
		for len(d) > 255 {
			b = append(b, code, 255)
			b = append(b, d[:255]...)
			d = d[255:]
		}
		b = append(b, code, byte(len(d)))
		b = append(b, d...)
	}
	if v, ok := p.Options[OptMsgType]; ok {
		put(OptMsgType, v)
	}
	codes := make([]int, 0, len(p.Options))
	for c := range p.Options {
		if c != OptMsgType {
			codes = append(codes, int(c))
		}
	}
	sort.Ints(codes)
	for _, c := range codes {
		put(byte(c), p.Options[byte(c)])
	}
	b = append(b, 255)
	for len(b) < 300 {
		b = append(b, 0)
	}
	return b
}

func (p *Packet) MsgType() MessageType {
	if v := p.Options[OptMsgType]; len(v) == 1 {
		return MessageType(v[0])
	}
	return 0
}

func (p *Packet) ipOpt(c byte) net.IP {
	if v := p.Options[c]; len(v) >= 4 {
		return ip4(v[:4])
	}
	return nil
}

func (p *Packet) RequestedIP() net.IP { return p.ipOpt(OptRequestedIP) }
func (p *Packet) ServerID() net.IP    { return p.ipOpt(OptServerID) }
func (p *Packet) Hostname() string    { return cstr(p.Options[OptHostname]) }
func (p *Packet) VendorClass() string { return cstr(p.Options[OptVendorClass]) }

func (p *Packet) RequestedLease() uint32 {
	if v := p.Options[OptLeaseTime]; len(v) == 4 {
		return binary.BigEndian.Uint32(v)
	}
	return 0
}

func (p *Packet) SetU32(c byte, v uint32) {
	p.Options[c] = binary.BigEndian.AppendUint32(nil, v)
}

func isZeroIP(ip net.IP) bool {
	v := ip.To4()
	return v == nil || (v[0] == 0 && v[1] == 0 && v[2] == 0 && v[3] == 0)
}
