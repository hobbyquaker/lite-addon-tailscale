package proxyproto

import (
	"bytes"
	"net/netip"
	"testing"
)

func TestHeaderRoundTrip(t *testing.T) {
	for _, c := range []struct{ src, dst string }{
		{"10.77.0.2:51234", "10.77.0.1:80"},
		{"[fd77::2]:40000", "[fd77::1]:443"},
	} {
		src, dst := netip.MustParseAddrPort(c.src), netip.MustParseAddrPort(c.dst)
		h := Header(src, dst)
		if !bytes.HasPrefix(h, []byte("\r\n\r\n\x00\r\nQUIT\n")) || h[12] != 0x21 {
			t.Fatalf("header %x", h)
		}
		gs, gd, err := Read(bytes.NewReader(append(h, []byte("GET / HTTP/1.1\r\n")...)))
		if err != nil || gs != src || gd != dst {
			t.Fatalf("%s: %v %s %s", c.src, err, gs, gd)
		}
	}
	if _, _, err := Read(bytes.NewReader([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))); err != ErrNotProxy {
		t.Fatalf("no header: %v", err)
	}
	// LOCAL: zero addresses, no error
	local := append([]byte("\r\n\r\n\x00\r\nQUIT\n"), 0x20, 0x00, 0, 0)
	if s, d, err := Read(bytes.NewReader(local)); err != nil || s.IsValid() || d.IsValid() {
		t.Fatalf("local: %v %s %s", err, s, d)
	}
}

func TestHeaderTLS(t *testing.T) {
	src, dst := netip.MustParseAddrPort("100.64.0.2:51234"), netip.MustParseAddrPort("100.64.0.1:443")
	h := HeaderTLS(src, dst, "TLSv1.3")
	in, err := ReadInfo(bytes.NewReader(append(h, 'G')))
	if err != nil || in.Src != src || in.Dst != dst || !in.TLS || in.TLSVersion != "TLSv1.3" {
		t.Fatalf("%v %+v", err, in)
	}
	in, err = ReadInfo(bytes.NewReader(HeaderTLS(src, dst, "")))
	if err != nil || !in.TLS || in.TLSVersion != "" {
		t.Fatalf("%v %+v", err, in)
	}
	in, err = ReadInfo(bytes.NewReader(Header(src, dst)))
	if err != nil || in.TLS {
		t.Fatalf("plain: %v %+v", err, in)
	}
	// an IPv4-mapped source is written as IPv4
	m := netip.MustParseAddrPort("[::ffff:100.64.0.2]:5")
	if hh := Header(m, dst); hh[13] != 0x11 {
		t.Fatalf("mapped: %x", hh[13])
	}
}
