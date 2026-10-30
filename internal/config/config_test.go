package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDefaults(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Get()
	if c.Consented || c.Enabled || c.Hostname != "ccu" || !c.HTTPS || !c.Redirect || c.Who != WhoAll || c.ControlURL != "" {
		t.Fatalf("defaults %+v", c)
	}
	if _, err := os.Stat(filepath.Join(dir, "tailscale.json")); !os.IsNotExist(err) {
		t.Fatal("nothing is written before a change")
	}
	var seen []Config
	s.OnChange = func(c Config) { seen = append(seen, c) }
	if err := s.Update(func(c *Config) { c.Consented, c.Enabled, c.ControlURL = true, true, "https://hs.example.org/" }); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "tailscale.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file: %v %v", err, fi)
	}
	if len(seen) != 1 || seen[0].ControlURL != "https://hs.example.org" {
		t.Fatalf("OnChange %+v", seen)
	}
	// reopen
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c := s2.Get(); !c.Consented || !c.Enabled || c.ControlURL != "https://hs.example.org" {
		t.Fatalf("reopened %+v", c)
	}
}

func TestValidate(t *testing.T) {
	ok := func(f func(*Config)) Config {
		c := Default
		c.AllowedUsers = []string{}
		f(&c)
		return c
	}
	for _, tc := range []struct {
		name string
		c    Config
		bad  bool
	}{
		{"default", ok(func(*Config) {}), false},
		{"hostname upper", ok(func(c *Config) { c.Hostname = "CCU" }), true},
		{"hostname hyphen end", ok(func(c *Config) { c.Hostname = "ccu-" }), true},
		{"hostname long", ok(func(c *Config) { c.Hostname = string(make([]byte, 64)) }), true},
		{"hostname ok", ok(func(c *Config) { c.Hostname = "ccu-keller2" }), false},
		{"url ok", ok(func(c *Config) { c.ControlURL = "https://headscale.example.org:8443" }), false},
		{"url path", ok(func(c *Config) { c.ControlURL = "http://10.0.0.5:8080/hs" }), false},
		{"url no scheme", ok(func(c *Config) { c.ControlURL = "headscale.example.org" }), true},
		{"url space", ok(func(c *Config) { c.ControlURL = "https://a b" }), true},
		{"who bad", ok(func(c *Config) { c.Who = "nobody" }), true},
		{"users empty", ok(func(c *Config) { c.Who = WhoUsers }), true},
		{"users ok", ok(func(c *Config) { c.Who = WhoUsers; c.AllowedUsers = []string{"alice@example.com", "tag:phone", "lab"} }), false},
		{"users bad", ok(func(c *Config) { c.Who = WhoUsers; c.AllowedUsers = []string{"a b"} }), true},
		{"tag bad", ok(func(c *Config) { c.Who = WhoUsers; c.AllowedUsers = []string{"tag:"} }), true},
	} {
		err := validate(tc.c)
		if (err != nil) != tc.bad {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestSetCleans(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := s.Get()
	c.Hostname = " CCU "
	c.ControlURL = " https://hs.example.org// "
	c.Who = WhoUsers
	c.AllowedUsers = []string{" alice@example.com ", "", "tag:x"}
	if err := s.Set(c); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.Hostname != "ccu" || got.ControlURL != "https://hs.example.org" || len(got.AllowedUsers) != 2 || got.AllowedUsers[0] != "alice@example.com" {
		t.Fatalf("%+v", got)
	}
	// a refused change leaves the stored one
	c.Hostname = "-x"
	if err := s.Set(c); err == nil {
		t.Fatal("accepted a bad hostname")
	}
	if s.Get().Hostname != "ccu" {
		t.Fatal("the stored hostname changed")
	}
}
