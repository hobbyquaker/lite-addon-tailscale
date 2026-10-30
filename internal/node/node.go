// Package node is the Tailscale node inside the addon: tsnet (Tailscale's userspace stack, no TUN
// device, no capability) with its state in the addon's configuration directory. It connects to the
// login server the user chose - Tailscale's, or a Headscale -, follows the IPN bus for the login
// URL and the state, listens on the node's tailnet address for the web (80, and 443 with the
// tailnet's certificate where there is one) and hands every connection to the system's web ingress
// socket with a PROXY v2 header naming the peer's tailnet address - the SSL TLV set for 443, so the
// system treats the request as https. Who may connect is checked per connection with WhoIs.
package node

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"

	"github.com/hobbyquaker/lite-addon-tailscale/internal/config"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/proxyproto"
)

// DefaultSocket is the system's web ingress socket for this addon.
const DefaultSocket = "/run/lighttpd-addons/tailscale/proxy.sock"

// Ranges are the tailnet's addresses, the ones the manifest declares for the ingress: a peer
// outside them is never claimed.
var Ranges = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// Options configure the node.
type Options struct {
	// StateDir holds tsnet's state (the node's keys).
	StateDir string
	// Socket is the web ingress socket (DefaultSocket).
	Socket string
	Log    func(format string, args ...any)
	// Config answers the current configuration: who may connect and whether 80 redirects are read
	// per connection, so a change on the page takes effect at once.
	Config func() config.Config
	// QRSize is the login QR code's edge in pixels (240 by default).
	QRSize int
	// DialTimeout for the socket, 5 s by default.
	DialTimeout time.Duration
	// TSLog takes tsnet's own verbose log (nil: discarded).
	TSLog func(format string, args ...any)
}

// Peer is one device on the tailnet as the page sees it.
type Peer struct {
	Name          string   `json:"name"`
	DNSName       string   `json:"dns_name"`
	OS            string   `json:"os"`
	User          string   `json:"user,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Addresses     []string `json:"addresses"`
	Online        bool     `json:"online"`
	LastSeen      int64    `json:"last_seen,omitempty"`
	LastHandshake int64    `json:"last_handshake,omitempty"`
	RxBytes       int64    `json:"rx_bytes"`
	TxBytes       int64    `json:"tx_bytes"`
	// Path is "direct <endpoint>", "relay <region>" or "" (no connection yet).
	Path string `json:"path,omitempty"`
	// Allowed: the who-may-connect rule admits the peer.
	Allowed bool `json:"allowed"`
	// Connections open from the peer to the web server right now.
	Connections int  `json:"connections"`
	Expired     bool `json:"expired,omitempty"`
}

// Self is this node.
type Self struct {
	Name       string   `json:"name"`
	DNSName    string   `json:"dns_name"`
	Addresses  []string `json:"addresses"`
	User       string   `json:"user,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	KeyExpiry  int64    `json:"key_expiry,omitempty"`
	Expired    bool     `json:"expired,omitempty"`
	Tailnet    string   `json:"tailnet,omitempty"`
	MagicDNS   string   `json:"magic_dns,omitempty"`
	CertDomain string   `json:"cert_domain,omitempty"`
	// Relay is the DERP region this node prefers.
	Relay string `json:"relay,omitempty"`
	// Online: the control server sees the node.
	Online bool `json:"online"`
}

// Status is what the page shows.
type Status struct {
	// Running: the node process is started (connected or trying).
	Running bool `json:"running"`
	// State is Tailscale's backend state: NoState, NeedsLogin, NeedsMachineAuth, Starting, Running, Stopped.
	State string `json:"state"`
	// LoginURL is the interactive login's URL while one is open, with its QR code (a PNG data URL).
	LoginURL string   `json:"login_url,omitempty"`
	LoginQR  string   `json:"login_qr,omitempty"`
	Error    string   `json:"error,omitempty"`
	Health   []string `json:"health,omitempty"`
	Self     *Self    `json:"self,omitempty"`
	Peers    []Peer   `json:"peers"`
	// HTTPS: the 443 listener is up with the tailnet's certificate.
	HTTPS bool `json:"https"`
	// HTTPSError says why 443 is not served although HTTPS is wanted ("" while it is, or while
	// nothing is known yet).
	HTTPSError string `json:"https_error,omitempty"`
	// Connections open to the web server, Forwarded since the start, Refused since the start.
	Connections int    `json:"connections"`
	Forwarded   int64  `json:"forwarded"`
	Refused     int64  `json:"refused"`
	SocketError string `json:"socket_error,omitempty"`
	// Version is the Tailscale client version embedded in the addon.
	Version string `json:"version,omitempty"`
	Since   int64  `json:"since,omitempty"`
	// ControlURL in use ("" = Tailscale).
	ControlURL string `json:"control_url"`
	// HasIdentity: the node holds a key for the login server (logged in, or logged in before and
	// not logged out). The login server can only change without one.
	HasIdentity bool `json:"has_identity"`
}

// Node is the tsnet server with its listeners.
type Node struct {
	o  Options
	mu sync.Mutex
	// per start
	srv       *tsnet.Server
	lc        *local.Client
	cancel    context.CancelFunc
	cfg       config.Config
	state     string
	loginURL  string
	lastErr   string
	httpsErr  string
	since     time.Time
	https     atomic.Bool
	lns       []net.Listener
	certHost  string
	open      map[netip.Addr]*int32
	openMu    sync.Mutex
	forwarded atomic.Int64
	refused   atomic.Int64
	sockErr   atomic.Value
}

// New makes a node that is not running.
func New(o Options) *Node {
	if o.Socket == "" {
		o.Socket = DefaultSocket
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if o.QRSize == 0 {
		o.QRSize = 240
	}
	if o.DialTimeout == 0 {
		o.DialTimeout = 5 * time.Second
	}
	if o.Config == nil {
		d := config.Default
		o.Config = func() config.Config { return d }
	}
	n := &Node{o: o, open: map[netip.Addr]*int32{}, state: "NoState"}
	n.sockErr.Store("")
	return n
}

// Start brings the node up with the configuration: the login server, the hostname and - for the
// registration - an auth key. Without a key and without a stored identity the node asks for an
// interactive login (Status.LoginURL).
func (n *Node) Start(authKey string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.srv != nil {
		return errors.New("the node is running")
	}
	c := n.o.Config()
	srv := &tsnet.Server{
		Dir:        n.o.StateDir,
		Hostname:   c.Hostname,
		ControlURL: c.ControlURL,
		AuthKey:    authKey,
		Logf:       n.o.TSLog,
		// UserLogf carries the login URL ("… or go to: https://login…"): a capability for a few
		// minutes, which the journal must not hold; the bus gives it to the page instead
		UserLogf: func(format string, args ...any) {
			line := fmt.Sprintf(format, args...)
			if strings.Contains(line, "go to:") || strings.Contains(line, "://") {
				return
			}
			n.o.Log("tailscale: %s", line)
		},
	}
	if srv.Logf == nil {
		srv.Logf = func(string, ...any) {}
	}
	if err := srv.Start(); err != nil {
		return fmt.Errorf("tsnet: %w", err)
	}
	lc, err := srv.LocalClient()
	if err != nil {
		srv.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.srv, n.lc, n.cancel, n.cfg, n.since, n.lastErr, n.loginURL, n.httpsErr, n.certHost = srv, lc, cancel, c, time.Now(), "", "", "", ""
	n.https.Store(false)
	go n.watch(ctx, lc)
	go n.serve(ctx, srv, c)
	n.o.Log("node started: hostname %s, login server %s", c.Hostname, orTailscale(c.ControlURL))
	return nil
}

func orTailscale(u string) string {
	if u == "" {
		return "Tailscale"
	}
	return u
}

// Apply follows a changed configuration: off → stop; on → start; a changed hostname is set on
// the running node; a changed login server or HTTPS switch restarts it.
func (n *Node) Apply(c config.Config) error {
	n.mu.Lock()
	running := n.srv != nil
	prev := n.cfg
	lc := n.lc
	n.mu.Unlock()
	want := c.Consented && c.Enabled
	switch {
	case !want && running:
		n.Stop()
		return nil
	case want && !running:
		return n.Start("")
	case want && running:
		if prev.ControlURL != c.ControlURL || prev.HTTPS != c.HTTPS {
			n.Stop()
			return n.Start("")
		}
		if prev.Hostname != c.Hostname && lc != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			mp := &ipn.MaskedPrefs{HostnameSet: true}
			mp.Hostname = c.Hostname
			if _, err := lc.EditPrefs(ctx, mp); err != nil {
				return fmt.Errorf("hostname: %w", err)
			}
			n.o.Log("hostname set to %s", c.Hostname)
		}
		n.mu.Lock()
		n.cfg = c
		n.mu.Unlock()
	}
	return nil
}

// watch follows the IPN bus: the state, the login URL, errors.
func (n *Node) watch(ctx context.Context, lc *local.Client) {
	for ctx.Err() == nil {
		w, err := lc.WatchIPNBus(ctx, ipn.NotifyInitialState)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		for {
			nt, err := w.Next()
			if err != nil {
				break
			}
			n.mu.Lock()
			if nt.State != nil {
				n.state = nt.State.String()
				if *nt.State == ipn.Running {
					n.loginURL = ""
				}
			}
			if nt.BrowseToURL != nil {
				n.loginURL = *nt.BrowseToURL
			}
			if nt.LoginFinished != nil {
				n.loginURL = ""
			}
			if nt.ErrMessage != nil {
				n.lastErr = *nt.ErrMessage
			}
			n.mu.Unlock()
		}
		w.Close()
	}
}

// serve opens the listeners on the node's tailnet address: 80 always, 443 when the tailnet
// offers a certificate and HTTPS is wanted.
func (n *Node) serve(ctx context.Context, srv *tsnet.Server, c config.Config) {
	ln, err := srv.Listen("tcp", ":80")
	if err != nil {
		n.setErr("listen on 80: " + err.Error())
		return
	}
	n.mu.Lock()
	n.lns = append(n.lns, ln)
	n.mu.Unlock()
	go n.accept(ln, false)
	if !c.HTTPS {
		return
	}
	// the certificate comes with the tailnet's HTTPS feature (MagicDNS + HTTPS on in the admin
	// console); Headscale has none - checked once the node runs, then every half minute
	for ctx.Err() == nil {
		n.mu.Lock()
		state := n.state
		n.mu.Unlock()
		if state == ipn.Running.String() {
			if d := srv.CertDomains(); len(d) > 0 {
				tl, err := srv.ListenTLS("tcp", ":443")
				if err != nil {
					n.mu.Lock()
					n.httpsErr = err.Error()
					n.mu.Unlock()
					n.o.Log("https: %v", err)
					return
				}
				n.mu.Lock()
				n.lns = append(n.lns, tl)
				n.certHost = d[0]
				n.httpsErr = ""
				n.mu.Unlock()
				n.https.Store(true)
				n.o.Log("https on the tailnet: %s", strings.Join(d, ", "))
				go n.accept(tl, true)
				return
			}
			n.mu.Lock()
			n.httpsErr = "no-certificate"
			n.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

func (n *Node) setErr(s string) {
	n.mu.Lock()
	n.lastErr = s
	n.mu.Unlock()
	n.o.Log("node: %s", s)
}

func (n *Node) accept(ln net.Listener, tls bool) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go n.forward(conn, tls)
	}
}

// inRanges says whether the address lies in the tailnet's ranges.
func inRanges(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range Ranges {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// forward checks who the peer is, then hands the connection to the web server's ingress socket.
func (n *Node) forward(c net.Conn, isTLS bool) {
	defer c.Close()
	src := proxyproto.AddrPortOf(c.RemoteAddr())
	dst := proxyproto.AddrPortOf(c.LocalAddr())
	n.mu.Lock()
	lc := n.lc
	n.mu.Unlock()
	if lc == nil {
		return
	}
	cfg := n.o.Config()
	if !inRanges(src.Addr()) {
		n.refused.Add(1)
		n.o.Log("refused %s: outside the tailnet's address ranges", src.Addr())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	who, err := lc.WhoIs(ctx, c.RemoteAddr().String())
	cancel()
	if err != nil {
		n.refused.Add(1)
		n.o.Log("refused %s: who is it? %v", src.Addr(), err)
		return
	}
	login := ""
	if who.UserProfile != nil {
		login = who.UserProfile.LoginName
	}
	var tags []string
	if who.Node != nil && who.Node.Tags != nil {
		tags = append(tags, who.Node.Tags...)
	}
	if !Allowed(cfg)(login, tags) {
		n.refused.Add(1)
		n.o.Log("refused %s (%s %v): not on the list of who may connect", src.Addr(), login, tags)
		return
	}
	tlsVersion := ""
	if isTLS {
		if tc, ok := c.(*tls.Conn); ok {
			hctx, hcancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := tc.HandshakeContext(hctx)
			hcancel()
			if err != nil {
				n.o.Log("tls handshake with %s: %v", src.Addr(), err)
				return
			}
			tlsVersion = tls.VersionName(tc.ConnectionState().Version)
		}
	}
	// 80 while 443 has the certificate and the redirect is wanted: one answer, no forward
	if !isTLS && cfg.Redirect && n.https.Load() {
		n.redirect(c)
		return
	}
	d := net.Dialer{Timeout: n.o.DialTimeout}
	up, err := d.DialContext(context.Background(), "unix", n.o.Socket)
	if err != nil {
		n.sockErr.Store(err.Error())
		n.o.Log("web server socket %s: %v", n.o.Socket, err)
		return
	}
	defer up.Close()
	n.sockErr.Store("")
	var hdr []byte
	if isTLS {
		hdr = proxyproto.HeaderTLS(src, dst, tlsVersion)
	} else {
		hdr = proxyproto.Header(src, dst)
	}
	if _, err := up.Write(hdr); err != nil {
		return
	}
	n.forwarded.Add(1)
	n.count(src.Addr(), 1)
	defer n.count(src.Addr(), -1)
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); closeWrite(up); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); closeWrite(c); done <- struct{}{} }()
	<-done
	<-done
}

// redirect answers the first request on a plain connection with 308 to https://<certificate
// name>/…; it reads the request line and the headers, nothing more.
func (n *Node) redirect(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	n.mu.Lock()
	host := n.certHost
	n.mu.Unlock()
	if host == "" {
		host = req.Host
	}
	target := "https://" + host + req.URL.RequestURI()
	fmt.Fprintf(c, "HTTP/1.1 308 Permanent Redirect\r\nLocation: %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", target)
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

func (n *Node) count(a netip.Addr, d int32) {
	n.openMu.Lock()
	defer n.openMu.Unlock()
	p, ok := n.open[a]
	if !ok {
		p = new(int32)
		n.open[a] = p
	}
	*p += d
}

func (n *Node) openFor(a netip.Addr) int {
	n.openMu.Lock()
	defer n.openMu.Unlock()
	if p, ok := n.open[a]; ok {
		return int(*p)
	}
	return 0
}

func (n *Node) client() (*local.Client, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.lc == nil {
		return nil, errors.New("the node is not running")
	}
	return n.lc, nil
}

// Login asks the login server for an interactive login URL (Status.LoginURL carries it).
func (n *Node) Login(ctx context.Context) error {
	lc, err := n.client()
	if err != nil {
		return err
	}
	return lc.StartLoginInteractive(ctx)
}

// LoginWithKey registers the node with an auth key (a node that is logged out, or never was).
func (n *Node) LoginWithKey(ctx context.Context, key string) error {
	lc, err := n.client()
	if err != nil {
		return err
	}
	return lc.Start(ctx, ipn.Options{AuthKey: key})
}

// Logout expires the node's key at the login server and forgets the login; the node stays up
// and asks for a login again.
func (n *Node) Logout(ctx context.Context) error {
	lc, err := n.client()
	if err != nil {
		return err
	}
	return lc.Logout(ctx)
}

// Forget stops the node and removes its state: the keys, the identity, the certificates. The
// caller logs out first when it can.
func (n *Node) Forget() error {
	n.Stop()
	entries, err := os.ReadDir(n.o.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(n.o.StateDir, e.Name())); err != nil {
			return err
		}
	}
	n.o.Log("the node's state is removed")
	return nil
}

// Stop ends the node; the identity stays in the state directory.
func (n *Node) Stop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.stopLocked()
}

func (n *Node) stopLocked() {
	if n.srv == nil {
		return
	}
	n.cancel()
	for _, l := range n.lns {
		l.Close()
	}
	n.lns = nil
	_ = n.srv.Close()
	n.srv, n.lc = nil, nil
	n.https.Store(false)
	n.state = "Stopped"
	n.loginURL = ""
	n.o.Log("node stopped")
}

// Running says whether the node is started.
func (n *Node) Running() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.srv != nil
}

// ClientVersion is the Tailscale module version the addon embeds ("1.104.4"), from the build info.
func ClientVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "tailscale.com" {
				return strings.TrimPrefix(d.Version, "v")
			}
		}
	}
	return ""
}

// Status reads the node.
func (n *Node) Status(ctx context.Context) Status {
	n.mu.Lock()
	st := Status{Running: n.srv != nil, State: n.state, LoginURL: n.loginURL, Error: n.lastErr, HTTPSError: n.httpsErr, Peers: []Peer{}, ControlURL: n.cfg.ControlURL, Version: ClientVersion()}
	lc := n.lc
	if n.srv != nil {
		st.Since = n.since.UnixMilli()
	}
	n.mu.Unlock()
	st.HTTPS = n.https.Load()
	st.Forwarded = n.forwarded.Load()
	st.Refused = n.refused.Load()
	st.SocketError, _ = n.sockErr.Load().(string)
	if lc == nil {
		st.HasIdentity = n.storedIdentity()
		return st
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s, err := lc.Status(ctx)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.State = s.BackendState
	st.HasIdentity = s.HaveNodeKey || s.BackendState == ipn.Running.String() || s.BackendState == ipn.Starting.String() || s.BackendState == ipn.NeedsMachineAuth.String()
	if s.AuthURL != "" && st.LoginURL == "" && s.BackendState == ipn.NeedsLogin.String() {
		st.LoginURL = s.AuthURL
	}
	if st.LoginURL != "" {
		if png, err := qrcode.Encode(st.LoginURL, qrcode.Medium, n.o.QRSize); err == nil {
			st.LoginQR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}
	st.Health = s.Health
	if s.Self != nil {
		st.Self = &Self{Name: s.Self.HostName, DNSName: strings.TrimSuffix(s.Self.DNSName, "."), Addresses: addrs(s.Self.TailscaleIPs), User: userOf(s, s.Self), Tags: tagsOf(s.Self), Expired: s.Self.Expired, MagicDNS: s.MagicDNSSuffix, Relay: s.Self.Relay, Online: s.Self.Online}
		if s.Self.KeyExpiry != nil {
			st.Self.KeyExpiry = s.Self.KeyExpiry.UnixMilli()
			if time.Now().After(*s.Self.KeyExpiry) {
				st.Self.Expired = true
			}
		}
		if s.CurrentTailnet != nil {
			st.Self.Tailnet = s.CurrentTailnet.Name
		}
		if len(s.CertDomains) > 0 {
			st.Self.CertDomain = s.CertDomains[0]
		}
	}
	allowed := Allowed(n.o.Config())
	for _, p := range s.Peer {
		pe := Peer{Name: p.HostName, DNSName: strings.TrimSuffix(p.DNSName, "."), OS: p.OS, User: userOf(s, p), Tags: tagsOf(p), Addresses: addrs(p.TailscaleIPs), Online: p.Online, RxBytes: p.RxBytes, TxBytes: p.TxBytes, Expired: p.Expired}
		if !p.LastSeen.IsZero() {
			pe.LastSeen = p.LastSeen.UnixMilli()
		}
		if !p.LastHandshake.IsZero() {
			pe.LastHandshake = p.LastHandshake.UnixMilli()
		}
		switch {
		case p.CurAddr != "":
			pe.Path = "direct " + p.CurAddr
		case p.Relay != "":
			pe.Path = "relay " + p.Relay
		}
		pe.Allowed = allowed(pe.User, pe.Tags)
		for _, a := range p.TailscaleIPs {
			pe.Connections += n.openFor(a)
		}
		st.Connections += pe.Connections
		st.Peers = append(st.Peers, pe)
	}
	sort.Slice(st.Peers, func(i, j int) bool {
		if st.Peers[i].Online != st.Peers[j].Online {
			return st.Peers[i].Online
		}
		return st.Peers[i].Name < st.Peers[j].Name
	})
	return st
}

// storedIdentity says whether the state directory holds a node state file (a stopped node that
// was logged in keeps its keys there).
func (n *Node) storedIdentity() bool {
	fi, err := os.Stat(filepath.Join(n.o.StateDir, "tailscaled.state"))
	return err == nil && fi.Size() > 2
}

func addrs(a []netip.Addr) []string {
	out := make([]string, 0, len(a))
	for _, x := range a {
		out = append(out, x.String())
	}
	return out
}

func userOf(s *ipnstate.Status, p *ipnstate.PeerStatus) string {
	if u, ok := s.User[p.UserID]; ok {
		return u.LoginName
	}
	return ""
}

func tagsOf(p *ipnstate.PeerStatus) []string {
	if p.Tags == nil {
		return nil
	}
	return p.Tags.AsSlice()
}

// Allowed is the who-may-connect rule of a configuration: every device, or only devices of the
// listed users and with the listed tags.
func Allowed(c config.Config) func(login string, tags []string) bool {
	return func(login string, tags []string) bool {
		if c.Who != config.WhoUsers {
			return true
		}
		for _, u := range c.AllowedUsers {
			if login != "" && strings.EqualFold(u, login) {
				return true
			}
			for _, t := range tags {
				if strings.EqualFold(u, t) {
					return true
				}
			}
		}
		return false
	}
}
