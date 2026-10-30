// Package proxyproto writes and reads the HAProxy PROXY protocol v2 header: the one the addon
// puts in front of every connection it hands to the web server's Unix socket, so that lighttpd
// takes the peer's tailnet address as the client's. A connection the addon terminated TLS on
// carries the SSL TLV (PP2_TYPE_SSL with PP2_CLIENT_SSL), which lighttpd's mod_extforward turns
// into the scheme https. The reader is for the tests' stub and for a development stand-in of the
// web server.
package proxyproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
)

var signature = []byte("\r\n\r\n\x00\r\nQUIT\n")

// The SSL TLV (haproxy.org/download/2.9/doc/proxy-protocol.txt, 2.2.5).
const (
	tlvSSL        = 0x20
	tlvClientSSL  = 0x01
	tlvSSLVersion = 0x21
)

// Header is a PROXY v2 header for a proxied TCP connection, plain.
func Header(src, dst netip.AddrPort) []byte {
	return header(src, dst, false, "")
}

// HeaderTLS is a PROXY v2 header for a connection whose client side was TLS, terminated by the
// sender: the SSL TLV says so, with the TLS version when known ("TLSv1.3", "" for unknown).
func HeaderTLS(src, dst netip.AddrPort, tlsVersion string) []byte {
	return header(src, dst, true, tlsVersion)
}

func header(src, dst netip.AddrPort, ssl bool, tlsVersion string) []byte {
	h := append([]byte{}, signature...)
	var addr []byte
	if src.Addr().Unmap().Is4() && dst.Addr().Unmap().Is4() {
		h = append(h, 0x21, 0x11) // PROXY, TCP over IPv4
		s, d := src.Addr().Unmap().As4(), dst.Addr().Unmap().As4()
		addr = append(addr, s[:]...)
		addr = append(addr, d[:]...)
	} else {
		h = append(h, 0x21, 0x21) // PROXY, TCP over IPv6
		s, d := src.Addr().As16(), dst.Addr().As16()
		addr = append(addr, s[:]...)
		addr = append(addr, d[:]...)
	}
	addr = binary.BigEndian.AppendUint16(addr, src.Port())
	addr = binary.BigEndian.AppendUint16(addr, dst.Port())
	if ssl {
		// client (1 byte): PP2_CLIENT_SSL; verify (4 bytes): non-zero, no client certificate
		// was checked; then the optional version sub-TLV
		val := []byte{tlvClientSSL, 0, 0, 0, 1}
		if tlsVersion != "" {
			val = append(val, tlvSSLVersion)
			val = binary.BigEndian.AppendUint16(val, uint16(len(tlsVersion)))
			val = append(val, tlsVersion...)
		}
		addr = append(addr, tlvSSL)
		addr = binary.BigEndian.AppendUint16(addr, uint16(len(val)))
		addr = append(addr, val...)
	}
	h = binary.BigEndian.AppendUint16(h, uint16(len(addr)))
	return append(h, addr...)
}

// ErrNotProxy is a stream that does not start with the signature.
var ErrNotProxy = errors.New("no PROXY v2 header")

// Info is what a header said.
type Info struct {
	Src, Dst netip.AddrPort
	// TLS: the SSL TLV with the client flag was present.
	TLS bool
	// TLSVersion from the version sub-TLV, "" without.
	TLSVersion string
}

// Read reads one header from the start of a stream: the claimed source and destination. A LOCAL
// command answers zero addresses and ok.
func Read(r io.Reader) (src, dst netip.AddrPort, err error) {
	in, err := ReadInfo(r)
	return in.Src, in.Dst, err
}

// ReadInfo reads one header with its TLVs.
func ReadInfo(r io.Reader) (Info, error) {
	var in Info
	var fixed [16]byte
	if _, err := io.ReadFull(r, fixed[:]); err != nil {
		return in, err
	}
	if string(fixed[:12]) != string(signature) || fixed[12]>>4 != 2 {
		return in, ErrNotProxy
	}
	n := int(binary.BigEndian.Uint16(fixed[14:16]))
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return in, err
	}
	if fixed[12]&0x0f == 0 { // LOCAL
		return in, nil
	}
	var rest []byte
	switch fixed[13] {
	case 0x11:
		if n < 12 {
			return in, fmt.Errorf("short IPv4 header: %d", n)
		}
		in.Src = netip.AddrPortFrom(netip.AddrFrom4([4]byte(body[0:4])), binary.BigEndian.Uint16(body[8:10]))
		in.Dst = netip.AddrPortFrom(netip.AddrFrom4([4]byte(body[4:8])), binary.BigEndian.Uint16(body[10:12]))
		rest = body[12:]
	case 0x21:
		if n < 36 {
			return in, fmt.Errorf("short IPv6 header: %d", n)
		}
		in.Src = netip.AddrPortFrom(netip.AddrFrom16([16]byte(body[0:16])), binary.BigEndian.Uint16(body[32:34]))
		in.Dst = netip.AddrPortFrom(netip.AddrFrom16([16]byte(body[16:32])), binary.BigEndian.Uint16(body[34:36]))
		rest = body[36:]
	default:
		return in, fmt.Errorf("unsupported family %#x", fixed[13])
	}
	for len(rest) >= 3 {
		typ := rest[0]
		l := int(binary.BigEndian.Uint16(rest[1:3]))
		if len(rest) < 3+l {
			return in, errors.New("truncated TLV")
		}
		val := rest[3 : 3+l]
		rest = rest[3+l:]
		if typ != tlvSSL || len(val) < 5 {
			continue
		}
		in.TLS = val[0]&tlvClientSSL != 0
		sub := val[5:]
		for len(sub) >= 3 {
			st := sub[0]
			sl := int(binary.BigEndian.Uint16(sub[1:3]))
			if len(sub) < 3+sl {
				break
			}
			if st == tlvSSLVersion {
				in.TLSVersion = string(sub[3 : 3+sl])
			}
			sub = sub[3+sl:]
		}
	}
	return in, nil
}

// AddrPortOf is a net.Addr as an AddrPort (zero for anything that is no TCP/UDP address).
func AddrPortOf(a net.Addr) netip.AddrPort {
	switch t := a.(type) {
	case *net.TCPAddr:
		return t.AddrPort()
	case *net.UDPAddr:
		return t.AddrPort()
	}
	ap, _ := netip.ParseAddrPort(a.String())
	return ap
}
