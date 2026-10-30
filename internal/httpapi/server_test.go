package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/lite-addon-tailscale/internal/config"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/demo"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/sysinfo"
)

type fakeFacts struct{}

func (fakeFacts) Facts(context.Context, bool) sysinfo.Facts {
	return sysinfo.Facts{Checked: true, TokenOK: true, Scopes: []string{"system:read"}, BackupUnencrypted: true, Addresses: []string{}}
}

func newServer(t *testing.T) (http.Handler, *config.Store, *demo.Node) {
	t.Helper()
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	www := filepath.Join(dir, "www")
	_ = os.MkdirAll(www, 0o755)
	_ = os.WriteFile(filepath.Join(www, "index.html"), []byte("<!doctype html><title>t</title>"), 0o644)
	n := &demo.Node{Config: store.Get}
	store.OnChange = func(c config.Config) { _ = n.Apply(c) }
	h := New(Options{BasePath: "/addons/tailscale", WWW: www, Store: store, Node: n, Facts: fakeFacts{}, Version: "test", Started: time.Now(), Ranges: []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"}})
	return h, store, n
}

func call(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, "/addons/tailscale"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(body))
	}
	if method != "GET" {
		req.Header.Set("X-CCU-Addon-Request", "1")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func TestFlow(t *testing.T) {
	h, store, _ := newServer(t)
	st, cfg := call(t, h, "GET", "/api/v1/config", "")
	if st != 200 || cfg["consented"] != false || cfg["hostname"] != "ccu" || cfg["https"] != true || cfg["who"] != "all" {
		t.Fatalf("%d %v", st, cfg)
	}
	if r := cfg["ranges"].([]any); len(r) != 2 || r[0] != "100.64.0.0/10" {
		t.Fatalf("ranges %v", cfg["ranges"])
	}
	// a write without the CSRF header is refused
	req := httptest.NewRequest("POST", "/addons/tailscale/api/v1/connect", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("without the header: %d", rr.Code)
	}
	// login before connect: 409
	if st, _ := call(t, h, "POST", "/api/v1/login", "{}"); st != 409 {
		t.Fatalf("login before connect: %d", st)
	}
	// connect with a Headscale URL: the consent is recorded, the node starts and asks for a login
	st, s := call(t, h, "POST", "/api/v1/connect", `{"control_url":"https://hs.example.org/"}`)
	if st != 200 || s["running"] != true || s["state"] != "NeedsLogin" || s["consented"] != true || s["enabled"] != true || !strings.HasPrefix(s["login_url"].(string), "https://login.tailscale.com/a/") || !strings.HasPrefix(s["login_qr"].(string), "data:image/png;base64,") {
		t.Fatalf("connect: %d %v", st, s)
	}
	if c := store.Get(); !c.Consented || !c.Enabled || c.ControlURL != "https://hs.example.org" {
		t.Fatalf("stored %+v", c)
	}
	// the health says ready
	if st, hl := call(t, h, "GET", "/api/v1/health?ready=1", ""); st != 200 || hl["ready"] != true {
		t.Fatalf("health %d %v", st, hl)
	}
	// a bad key is a 502 from the login server, a good one logs in
	if st, e := call(t, h, "POST", "/api/v1/login", `{"auth_key":"nope-nope-nope"}`); st != 502 || e["error"] != "login" {
		t.Fatalf("bad key: %d %v", st, e)
	}
	st, s = call(t, h, "POST", "/api/v1/login", `{"auth_key":"tskey-auth-k0123456789"}`)
	if st != 200 || s["state"] != "Running" || s["has_identity"] != true || s["login_url"] != nil {
		t.Fatalf("key login: %d %v", st, s)
	}
	self := s["self"].(map[string]any)
	if self["dns_name"] != "ccu.tail1234.ts.net" || len(s["peers"].([]any)) != 3 || s["https"] != true {
		t.Fatalf("self %v peers %v", self, s["peers"])
	}
	// the login server is locked while the identity exists
	if st, e := call(t, h, "PUT", "/api/v1/settings", `{"control_url":"","hostname":"ccu","https":true,"redirect":true,"who":"all","allowed_users":[]}`); st != 422 || !strings.Contains(e["message"].(string), "logged out") {
		t.Fatalf("control url change: %d %v", st, e)
	}
	// the settings: hostname, https off, the allow-list
	st, c := call(t, h, "PUT", "/api/v1/settings", `{"control_url":"https://hs.example.org","hostname":"ccu-keller","https":false,"redirect":true,"who":"users","allowed_users":[" alice@example.com ","tag:phone"]}`)
	if st != 200 || c["hostname"] != "ccu-keller" || c["https"] != false || c["who"] != "users" || len(c["allowed_users"].([]any)) != 2 {
		t.Fatalf("settings: %d %v", st, c)
	}
	st, s = call(t, h, "GET", "/api/v1/status", "")
	if st != 200 || s["https"] != false || s["self"].(map[string]any)["dns_name"] != "ccu-keller.tail1234.ts.net" {
		t.Fatalf("after settings: %v", s)
	}
	for _, p := range s["peers"].([]any) {
		pm := p.(map[string]any)
		if pm["allowed"] != false {
			t.Fatalf("peer %v should be refused by the list", pm["name"])
		}
	}
	// a refused value
	if st, _ := call(t, h, "PUT", "/api/v1/settings", `{"control_url":"https://hs.example.org","hostname":"ccu_1","https":true,"redirect":true,"who":"all","allowed_users":[]}`); st != 422 {
		t.Fatalf("bad hostname: %d", st)
	}
	if st, _ := call(t, h, "PUT", "/api/v1/settings", `{"control_url":"https://hs.example.org","hostname":"ccu","https":true,"redirect":true,"who":"users","allowed_users":[]}`); st != 422 {
		t.Fatalf("empty list: %d", st)
	}
	// disconnect and connect again
	if st, s = call(t, h, "POST", "/api/v1/disconnect", ""); st != 200 || s["running"] != false || s["enabled"] != false || s["has_identity"] != true {
		t.Fatalf("disconnect: %d %v", st, s)
	}
	if st, s = call(t, h, "POST", "/api/v1/connect", ""); st != 200 || s["running"] != true || s["state"] != "Running" {
		t.Fatalf("reconnect: %d %v", st, s)
	}
	// logout: a login is needed again
	if st, s = call(t, h, "POST", "/api/v1/logout", `{"forget":false}`); st != 200 || s["state"] != "NeedsLogin" || s["has_identity"] != false || s["enabled"] != true {
		t.Fatalf("logout: %d %v", st, s)
	}
	// now the login server may change
	if st, _ := call(t, h, "PUT", "/api/v1/settings", `{"control_url":"","hostname":"ccu","https":true,"redirect":true,"who":"all","allowed_users":[]}`); st != 200 {
		t.Fatalf("control url change while logged out: %d", st)
	}
	// forget: off, no identity, the consent stays
	if st, s = call(t, h, "POST", "/api/v1/logout", `{"forget":true}`); st != 200 || s["running"] != false || s["has_identity"] != false || s["enabled"] != false || s["consented"] != true {
		t.Fatalf("forget: %d %v", st, s)
	}
	// the facts
	if st, f := call(t, h, "GET", "/api/v1/system", ""); st != 200 || f["backup_unencrypted"] != true {
		t.Fatalf("system: %d %v", st, f)
	}
	// the page and an unknown API route
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/addons/tailscale/", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "<title>t</title>") {
		t.Fatalf("page: %d", rr.Code)
	}
	if st, e := call(t, h, "GET", "/api/v1/nothing", ""); st != 404 || e["error"] != "not-found" {
		t.Fatalf("unknown: %d %v", st, e)
	}
}

// Every route of the API is in docs/api.md.
func TestRoutesDocumented(t *testing.T) {
	newServer(t)
	doc, err := os.ReadFile("../../docs/api.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Routes {
		method, path, _ := strings.Cut(r, " ")
		want := "`" + method + " " + strings.TrimPrefix(path, "/") + "`"
		if !strings.Contains(string(doc), want) {
			t.Errorf("docs/api.md lacks %s", want)
		}
	}
}
