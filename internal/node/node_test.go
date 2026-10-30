package node

import (
	"net/netip"
	"testing"

	"github.com/hobbyquaker/lite-addon-tailscale/internal/config"
)

func TestAllowed(t *testing.T) {
	all := Allowed(config.Config{Who: config.WhoAll})
	if !all("", nil) || !all("x@y", []string{"tag:a"}) {
		t.Fatal("all refuses")
	}
	list := Allowed(config.Config{Who: config.WhoUsers, AllowedUsers: []string{"Alice@example.com", "tag:phone"}})
	for _, tc := range []struct {
		login string
		tags  []string
		want  bool
	}{
		{"alice@example.com", nil, true},
		{"ALICE@example.com", nil, true},
		{"bob@example.com", nil, false},
		{"", []string{"tag:phone"}, true},
		{"", []string{"tag:server"}, false},
		{"", nil, false},
		{"bob@example.com", []string{"tag:phone", "tag:x"}, true},
	} {
		if got := list(tc.login, tc.tags); got != tc.want {
			t.Errorf("%q %v: %v", tc.login, tc.tags, got)
		}
	}
}

func TestInRanges(t *testing.T) {
	for a, want := range map[string]bool{"100.64.0.1": true, "100.127.255.254": true, "100.128.0.1": false, "fd7a:115c:a1e0::1": true, "fd7a:115c:a1e1::1": false, "::ffff:100.64.0.2": true, "10.77.0.2": false, "127.0.0.1": false} {
		if got := inRanges(netip.MustParseAddr(a)); got != want {
			t.Errorf("%s: %v", a, got)
		}
	}
}

// A node that is not running answers a status without touching the network.
func TestStatusStopped(t *testing.T) {
	n := New(Options{StateDir: t.TempDir()})
	st := n.Status(t.Context())
	if st.Running || st.State != "NoState" || st.HasIdentity || len(st.Peers) != 0 || st.Version == "" {
		t.Fatalf("%+v", st)
	}
	n.Stop() // a no-op
	if err := n.Forget(); err != nil {
		t.Fatal(err)
	}
}
