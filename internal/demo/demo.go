// Package demo is a stand-in for the node for the page's development and its Playwright tests
// (scripts/dev-serve.sh, the daemon's -demo flag): it behaves like a node on a small tailnet
// without contacting anything - connect brings it up in a moment with a few peers, logout asks
// for a login with a URL, a key logs it in again, forget removes the identity.
package demo

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/hobbyquaker/lite-addon-tailscale/internal/config"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/node"
)

// Node is the stand-in.
type Node struct {
	mu       sync.Mutex
	Config   func() config.Config
	running  bool
	identity bool
	loginURL string
	since    time.Time
	started  time.Time
	refused  int64
}

const loginURL = "https://login.tailscale.com/a/0123456789abcdef"

// Running says whether the node is started.
func (d *Node) Running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running
}

// Apply follows the configuration like the real node.
func (d *Node) Apply(c config.Config) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	want := c.Consented && c.Enabled
	if want && !d.running {
		d.running = true
		d.started = time.Now()
		if !d.identity {
			d.loginURL = loginURL
		}
	}
	if !want && d.running {
		d.running = false
	}
	return nil
}

// Stop ends the node.
func (d *Node) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running = false
}

// Login opens a login URL.
func (d *Node) Login(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.running {
		return errors.New("the node is not running")
	}
	d.loginURL = loginURL
	return nil
}

// LoginWithKey logs in at once; a key that does not start with tskey- is refused.
func (d *Node) LoginWithKey(ctx context.Context, key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.running {
		return errors.New("the node is not running")
	}
	if len(key) < 10 || key[:6] != "tskey-" {
		return errors.New("invalid key: the login server refused it")
	}
	d.identity = true
	d.loginURL = ""
	d.since = time.Now()
	return nil
}

// Logout forgets the login.
func (d *Node) Logout(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.running {
		return errors.New("the node is not running")
	}
	d.identity = false
	d.loginURL = ""
	return nil
}

// Forget stops the node and removes the identity.
func (d *Node) Forget() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running, d.identity, d.loginURL = false, false, ""
	return nil
}

// Status is the stand-in's state: a login after the first connect (a key logs it in), then a
// node on the tailnet "tail1234" with three peers.
func (d *Node) Status(ctx context.Context) node.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	// the first start logs in by itself after a few seconds, like a node that was approved
	if d.running && !d.identity && d.loginURL != "" && time.Since(d.started) > 12*time.Second && d.Config != nil && d.Config().ControlURL == "" {
		d.identity, d.loginURL, d.since = true, "", time.Now()
	}
	st := node.Status{Running: d.running, State: "NoState", Peers: []node.Peer{}, Version: "1.104.4", HasIdentity: d.identity, Refused: d.refused}
	if d.Config != nil {
		st.ControlURL = d.Config().ControlURL
	}
	if !d.running {
		if d.identity {
			st.State = "Stopped"
		}
		return st
	}
	st.Since = d.started.UnixMilli()
	if !d.identity {
		st.State = "NeedsLogin"
		if d.loginURL != "" {
			st.LoginURL = d.loginURL
			if png, err := qrcode.Encode(d.loginURL, qrcode.Medium, 240); err == nil {
				st.LoginQR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
			}
		}
		return st
	}
	cfg := config.Default
	if d.Config != nil {
		cfg = d.Config()
	}
	now := time.Now()
	expiry := now.Add(143 * 24 * time.Hour).UnixMilli()
	st.State = "Running"
	st.Self = &node.Self{Name: cfg.Hostname, DNSName: cfg.Hostname + ".tail1234.ts.net", Addresses: []string{"100.101.102.103", "fd7a:115c:a1e0::1"}, User: "sebastian@example.com", KeyExpiry: expiry, Tailnet: "tail1234.ts.net", MagicDNS: "tail1234.ts.net", Relay: "fra", Online: true}
	if cfg.HTTPS {
		st.HTTPS = true
		st.Self.CertDomain = cfg.Hostname + ".tail1234.ts.net"
	}
	st.Forwarded = 17
	st.Connections = 1
	allowed := node.Allowed(cfg)
	peers := []node.Peer{
		{Name: "pixel-7", DNSName: "pixel-7.tail1234.ts.net", OS: "android", User: "sebastian@example.com", Addresses: []string{"100.64.0.2"}, Online: true, LastHandshake: now.Add(-40 * time.Second).UnixMilli(), RxBytes: 1_234_567, TxBytes: 8_765_432, Path: "direct 203.0.113.5:41641", Connections: 1},
		{Name: "laptop", DNSName: "laptop.tail1234.ts.net", OS: "macOS", User: "sebastian@example.com", Addresses: []string{"100.64.0.3"}, Online: true, LastHandshake: now.Add(-5 * time.Minute).UnixMilli(), RxBytes: 45_000, TxBytes: 120_000, Path: "relay fra"},
		{Name: "nas", DNSName: "nas.tail1234.ts.net", OS: "linux", Tags: []string{"tag:server"}, Addresses: []string{"100.64.0.4"}, Online: false, LastSeen: now.Add(-3 * 24 * time.Hour).UnixMilli()},
	}
	for i := range peers {
		peers[i].Allowed = allowed(peers[i].User, peers[i].Tags)
	}
	st.Peers = peers
	return st
}
