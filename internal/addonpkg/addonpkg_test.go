// Package addonpkg tests the package files the addon ships.
package addonpkg

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit/fragment"
	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit/manifest"
)

// The manifest as the system validates it at install, through the kit's strict load.
func TestManifest(t *testing.T) {
	m, err := manifest.Load("../../addon/openccu-lite.json")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "tailscale" || m.Undeclared() || m.Runtime == nil || !m.Runtime.Daemon {
		t.Fatalf("manifest: %+v", m)
	}
	// no capability, no root, no group, no port: tsnet runs in userspace and connects outwards only
	if m.Runtime.Root || len(m.Runtime.Capabilities) != 0 || len(m.Runtime.Groups) != 0 || len(m.Runtime.Ports) != 0 {
		t.Errorf("the addon needs no privilege and no port: %+v", m.Runtime)
	}
	if len(m.Runtime.APIScopes) != 1 || m.Runtime.APIScopes[0] != "system:read" {
		t.Errorf("scopes %v", m.Runtime.APIScopes)
	}
	// the web ingress: the tailnet's two ranges, nothing else
	if m.Runtime.WebIngress == nil || len(m.Runtime.WebIngress.Ranges) != 2 || m.Runtime.WebIngress.Ranges[0] != "100.64.0.0/10" || m.Runtime.WebIngress.Ranges[1] != "fd7a:115c:a1e0::/48" {
		t.Errorf("web_ingress %+v", m.Runtime.WebIngress)
	}
	if _, err := m.Runtime.WebIngress.Prefixes(); err != nil {
		t.Errorf("the ranges by the system's rules: %v", err)
	}
	if m.Description["de"] == "" || m.Description["en"] == "" || m.Runtime.Note["de"] == "" || m.Runtime.Note["en"] == "" {
		t.Error("the description and the note in both languages")
	}
	for _, want := range []string{"Headscale", "100.64.0.0/10", "No logs", "Connect"} {
		if !strings.Contains(m.Runtime.Note["en"], want) {
			t.Errorf("the note does not say %q", want)
		}
	}
	if m.Requires.Lite != "1.1.0-dev.6" {
		t.Errorf("requires.lite %q: the ingress needs 1.1.0-dev.6 or newer (the first build with it)", m.Requires.Lite)
	}
}

// The manifest's version is the changelog's newest.
func TestVersionInChangelog(t *testing.T) {
	var m struct {
		Version string `json:"version"`
	}
	b, _ := os.ReadFile("../../addon/openccu-lite.json")
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	cl, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cl), "## ["+m.Version+"]") {
		t.Errorf("CHANGELOG.md has no entry for %s", m.Version)
	}
}

// The manifest's images are tracked at the path the catalogue reads them from.
func TestImagesTracked(t *testing.T) {
	for _, p := range []string{"addon/tailscale/www/icon.svg", "addon/tailscale/www/icon-dark.svg", "addon/tailscale/etc/lighttpd.conf", "addon/tailscale/www/lighttpd-error-503.html"} {
		if out, err := exec.Command("git", "-C", "../..", "ls-files", "--error-unmatch", "--", p).CombinedOutput(); err != nil {
			t.Errorf("%s not tracked: %v %s", p, err, out)
		}
	}
}

// The proxy rule is what the kit's renderer writes (apart from the comment lines).
func TestFragment(t *testing.T) {
	want, err := fragment.Render(fragment.Fragment{ID: "tailscale", Port: 8299, ErrorfilePrefix: "/usr/local/addons/tailscale/www/lighttpd-error-"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile("../../addon/tailscale/etc/lighttpd.conf")
	strip := func(s string) string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			if !strings.HasPrefix(l, "#") && strings.TrimSpace(l) != "" {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	if strip(string(got)) != strip(want) {
		t.Errorf("etc/lighttpd.conf:\n%s\nwant:\n%s", got, want)
	}
}

// The 503 page names the addon, in the tracked copy and the one the build copies.
func TestErrorPage(t *testing.T) {
	for _, p := range []string{"../../addon/static/lighttpd-error-503.html", "../../addon/tailscale/www/lighttpd-error-503.html"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "<title>Fernzugriff (Tailscale)</title>") {
			t.Errorf("%s does not name the addon", p)
		}
	}
}

// The rc.d script and the update_script name no other addon and are sh scripts.
func TestScripts(t *testing.T) {
	for _, p := range []string{"../../addon/tailscale/rc.d", "../../addon/update_script"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, other := range []string{"snmp", "forwarder", "nut", "UPS", "upsd", "wireguard", "WireGuard", "51820"} {
			if strings.Contains(string(b), other) {
				t.Errorf("%s names another addon: %s", p, other)
			}
		}
		if !strings.HasPrefix(string(b), "#!/bin/sh\n") {
			t.Errorf("%s is not a sh script", p)
		}
	}
	rc, _ := os.ReadFile("../../addon/tailscale/rc.d")
	if !strings.Contains(string(rc), "ADDON=tailscale\n") || !strings.Contains(string(rc), `DAEMON_ARGS="-addon-dir $ADDON_DIR"`) {
		t.Error("rc.d: the addon name or the daemon's arguments")
	}
	if !strings.Contains(string(rc), "TS_NO_LOGS_NO_SUPPORT=1") {
		t.Error("rc.d: the unit must carry TS_NO_LOGS_NO_SUPPORT=1 as well")
	}
}

// The build compiles the unwanted Tailscale features out; the list is one place, scripts/tags.
func TestOmitTags(t *testing.T) {
	b, err := os.ReadFile("../../scripts/tags")
	if err != nil {
		t.Fatal(err)
	}
	tags := strings.Fields(string(b))
	have := map[string]bool{}
	for _, tg := range tags {
		if !strings.HasPrefix(tg, "ts_omit_") {
			t.Errorf("tag %q is no ts_omit_ tag", tg)
		}
		have[tg] = true
	}
	for _, must := range []string{"ts_omit_logtail", "ts_omit_portmapper", "ts_omit_ssh", "ts_omit_debug", "ts_omit_clientupdate", "ts_omit_webclient", "ts_omit_taildrop", "ts_omit_drive", "ts_omit_advertiseroutes", "ts_omit_advertiseexitnode", "ts_omit_useexitnode"} {
		if !have[must] {
			t.Errorf("tag %s missing", must)
		}
	}
	for _, never := range []string{"ts_omit_acme", "ts_omit_serve", "ts_omit_netstack"} {
		if have[never] {
			t.Errorf("tag %s would break the addon", never)
		}
	}
}
