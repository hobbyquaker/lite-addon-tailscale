// Package headscale runs the node against a Headscale in docker (no Tailscale account, no internet
// beyond the image): the addon's node registers with a pre-auth key, a second tsnet node ("the
// phone") registers too, fetches a page from the addon's node through the tailnet, and the ingress
// stub shows the phone's tailnet address as the claimed client. Run with TS_DOCKER=1; needs docker.
package headscale

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tailscale.com/tsnet"

	"github.com/hobbyquaker/lite-addon-tailscale/internal/config"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/fakesys"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/node"
)

const hsConfig = `server_url: http://127.0.0.1:18085
listen_addr: 0.0.0.0:18085
metrics_listen_addr: 127.0.0.1:19090
grpc_listen_addr: 127.0.0.1:50443
grpc_allow_insecure: false
noise:
  private_key_path: /var/lib/headscale/noise_private.key
prefixes:
  v4: 100.64.0.0/10
  v6: fd7a:115c:a1e0::/48
  allocation: sequential
derp:
  server:
    enabled: true
    region_id: 999
    region_code: lab
    region_name: Lab
    stun_listen_addr: 0.0.0.0:13478
    private_key_path: /var/lib/headscale/derp_server_private.key
    automatically_add_embedded_derp_region: true
  urls: []
  paths: []
  auto_update_enabled: false
  update_frequency: 24h
disable_check_updates: true
ephemeral_node_inactivity_timeout: 30m
database:
  type: sqlite
  sqlite:
    path: /var/lib/headscale/db.sqlite
log:
  level: warn
policy:
  mode: database
dns:
  magic_dns: true
  base_domain: tail.test
  nameservers:
    global: []
unix_socket: /var/run/headscale/headscale.sock
`

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestHeadscale(t *testing.T) {
	if os.Getenv("TS_DOCKER") == "" {
		t.Skip("TS_DOCKER=1 runs the node against a Headscale in docker")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("no docker")
	}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte(hsConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("lite-addon-tailscale-hs-%d", os.Getpid())
	_ = exec.Command("docker", "rm", "-f", name).Run()
	docker(t, "run", "-d", "--name", name, "--network", "host", "-v", cfg+":/etc/headscale/config.yaml:ro", "headscale/headscale:0.29.7", "serve")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	// wait for the server, then a user and a reusable pre-auth key
	var key string
	for i := 0; i < 30; i++ {
		time.Sleep(time.Second)
		if out, err := exec.Command("docker", "exec", name, "headscale", "users", "create", "lab").CombinedOutput(); err == nil || strings.Contains(string(out), "already exists") {
			break
		}
		if i == 29 {
			t.Fatal("headscale did not come up")
		}
	}
	users := docker(t, "exec", name, "headscale", "users", "list", "-o", "json")
	uid := "1"
	if i := strings.Index(users, `"id":`); i >= 0 {
		rest := users[i+5:]
		rest = strings.TrimLeft(rest, " \"")
		j := strings.IndexAny(rest, `,}" `)
		if j > 0 {
			uid = rest[:j]
		}
	}
	key = strings.TrimSpace(docker(t, "exec", name, "headscale", "preauthkeys", "create", "--user", uid, "--reusable", "--expiration", "1h"))
	key = key[strings.LastIndex(key, "\n")+1:]
	if len(key) < 20 {
		t.Fatalf("no pre-auth key: %q", key)
	}
	t.Logf("headscale up, user %s, key %s…", uid, key[:8])

	stub, err := fakesys.ListenProxyStub(filepath.Join(dir, "proxy.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer stub.Close()
	c := config.Default
	c.ControlURL = "http://127.0.0.1:18085"
	c.Hostname = "ccu"
	c.Consented, c.Enabled = true, true
	c.Who = config.WhoUsers
	c.AllowedUsers = []string{"lab"}
	fixed := func(c config.Config) func() config.Config { return func() config.Config { return c } }
	n := node.New(node.Options{StateDir: filepath.Join(dir, "state"), Socket: stub.Path, Log: t.Logf, Config: fixed(c)})
	if err := n.Start(key); err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	ctx := context.Background()
	var st node.Status
	for i := 0; i < 60; i++ {
		st = n.Status(ctx)
		if st.State == "Running" && st.Self != nil && len(st.Self.Addresses) > 0 {
			break
		}
		time.Sleep(time.Second)
	}
	if st.State != "Running" || st.Self == nil || len(st.Self.Addresses) == 0 {
		t.Fatalf("the node did not come up: %+v", st)
	}
	t.Logf("node %s %v user %s", st.Self.DNSName, st.Self.Addresses, st.Self.User)
	if st.Self.DNSName != "ccu.tail.test" || st.Self.User != "lab" {
		t.Fatalf("self %+v", st.Self)
	}

	// the phone: a second tsnet node on the same Headscale
	phone := &tsnet.Server{Dir: filepath.Join(dir, "phone"), Hostname: "phone", ControlURL: c.ControlURL, AuthKey: key, Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
	defer phone.Close()
	pst, err := phone.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	phoneIP := pst.TailscaleIPs[0]
	cl := &http.Client{Transport: &http.Transport{DialContext: phone.Dial}, Timeout: 30 * time.Second}
	var res *http.Response
	for i := 0; i < 10; i++ {
		res, err = cl.Get("http://ccu.tail.test/api/ping")
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		t.Fatalf("through the tailnet: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("X-Claimed-Address") != phoneIP.String() || res.Header.Get("X-Forwarded-Proto") != "http" {
		t.Fatalf("the stub saw %q, want %s: %d %s", res.Header.Get("X-Claimed-Address"), phoneIP, res.StatusCode, body)
	}
	t.Logf("the phone %s reached the page; claimed %s", phoneIP, res.Header.Get("X-Claimed-Address"))
	st = n.Status(ctx)
	if len(st.Peers) != 1 || st.Peers[0].Name != "phone" || !st.Peers[0].Allowed || st.Forwarded != 1 {
		t.Fatalf("peers %+v forwarded %d", st.Peers, st.Forwarded)
	}
	// who may connect: a rule that excludes the phone's user refuses it
	c2 := c
	c2.Hostname = "ccu2"
	c2.AllowedUsers = []string{"tag:other"}
	n2 := node.New(node.Options{StateDir: filepath.Join(dir, "state2"), Socket: stub.Path, Log: t.Logf, Config: fixed(c2)})
	if err := n2.Start(key); err != nil {
		t.Fatal(err)
	}
	defer n2.Stop()
	for i := 0; i < 60 && n2.Status(ctx).State != "Running"; i++ {
		time.Sleep(time.Second)
	}
	short := &http.Client{Transport: &http.Transport{DialContext: phone.Dial}, Timeout: 8 * time.Second}
	if res, err := short.Get("http://ccu2.tail.test/"); err == nil {
		t.Fatalf("a peer outside the rule got through: %d", res.StatusCode)
	}
	if s2 := n2.Status(ctx); s2.Refused == 0 {
		t.Fatalf("the refusal is not counted: %+v", s2)
	}
	// the node's identity survives a stop and a start without a key
	n.Stop()
	n3 := node.New(node.Options{StateDir: filepath.Join(dir, "state"), Socket: stub.Path, Log: t.Logf, Config: fixed(c)})
	if err := n3.Start(""); err != nil {
		t.Fatal(err)
	}
	defer n3.Stop()
	for i := 0; i < 60; i++ {
		if s := n3.Status(ctx); s.State == "Running" && s.Self != nil && s.Self.DNSName == "ccu.tail.test" {
			break
		}
		time.Sleep(time.Second)
		if i == 59 {
			t.Fatalf("the identity did not survive: %+v", n3.Status(ctx))
		}
	}
	// logout: the node needs a login again (a URL from Headscale)
	if err := n3.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	var s node.Status
	for i := 0; i < 30; i++ {
		s = n3.Status(ctx)
		if s.State == "NeedsLogin" {
			break
		}
		time.Sleep(time.Second)
	}
	if s.State != "NeedsLogin" {
		t.Fatalf("after the logout: %+v", s)
	}
	if err := n3.Login(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		s = n3.Status(ctx)
		if s.LoginURL != "" {
			break
		}
		time.Sleep(time.Second)
	}
	if !strings.HasPrefix(s.LoginURL, "http://127.0.0.1:18085/register/") || !strings.HasPrefix(s.LoginQR, "data:image/png;base64,") {
		t.Fatalf("login url %q qr %d", s.LoginURL, len(s.LoginQR))
	}
	t.Logf("login URL %s", s.LoginURL)
	if s.HasIdentity {
		t.Fatalf("after the logout the node still claims an identity: %+v", s)
	}
	// a key registers the logged-out node again
	if err := n3.LoginWithKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		if s = n3.Status(ctx); s.State == "Running" && s.Self != nil && s.Self.DNSName == "ccu.tail.test" {
			break
		}
		time.Sleep(time.Second)
	}
	if s.State != "Running" || s.LoginURL != "" || !s.HasIdentity {
		t.Fatalf("after the key login: %+v", s)
	}
	// forget: the state directory is empty, a start asks for a login
	if err := n3.Forget(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "state")); len(entries) != 0 {
		t.Fatalf("state left: %v", entries)
	}
	n4 := node.New(node.Options{StateDir: filepath.Join(dir, "state"), Socket: stub.Path, Log: t.Logf, Config: fixed(c)})
	if err := n4.Start(""); err != nil {
		t.Fatal(err)
	}
	defer n4.Stop()
	for i := 0; i < 30; i++ {
		if s = n4.Status(ctx); s.LoginURL != "" {
			break
		}
		time.Sleep(time.Second)
	}
	if s.State != "NeedsLogin" || s.LoginURL == "" || s.HasIdentity {
		t.Fatalf("a fresh node: %+v", s)
	}
	_ = net.IPv4zero
}
