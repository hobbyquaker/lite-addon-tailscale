// Package config is the addon's configuration: whether the user consented to the node contacting
// its login server, whether the node should run, the login server (Tailscale, or a Headscale), the
// hostname on the tailnet, HTTPS and the redirect, and who may reach the system. One JSON file,
// tailscale.json, in the configuration directory the system gives the addon (0700); the node's own
// state (its keys) is tsnet's, in state/ beside it - both in the system's backup.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Who may connect.
const (
	WhoAll   = "all"   // every device the tailnet's policy lets through
	WhoUsers = "users" // only the listed users or tags
)

// Config is the whole file.
type Config struct {
	// Consented: the user read what the node contacts and agreed; nothing runs before.
	Consented bool `json:"consented"`
	// Enabled: the node runs (connects at start).
	Enabled bool `json:"enabled"`
	// ControlURL is the login server, "" for Tailscale's.
	ControlURL string `json:"control_url"`
	// Hostname on the tailnet, "ccu" by default.
	Hostname string `json:"hostname"`
	// HTTPS: serve 443 with the tailnet's certificate when the tailnet offers one.
	HTTPS bool `json:"https"`
	// Redirect: 80 → https while a certificate is there.
	Redirect bool `json:"redirect"`
	// Who is WhoAll or WhoUsers.
	Who string `json:"who"`
	// AllowedUsers are login names (user@example.com) or tags (tag:ccu) for WhoUsers.
	AllowedUsers []string `json:"allowed_users"`
}

// Defaults.
var Default = Config{Hostname: "ccu", HTTPS: true, Redirect: true, Who: WhoAll, AllowedUsers: []string{}}

// ValidationError is a refused change.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

var (
	hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	userRe = regexp.MustCompile(`^(tag:[a-z0-9-]{1,63}|[^\s@]{1,64}@[^\s@]{1,253}|[a-z0-9._-]{1,64})$`)
	urlRe  = regexp.MustCompile(`^https?://[A-Za-z0-9.\-]+(:[0-9]{1,5})?(/[^\s]*)?$`)
)

// Store holds the configuration and the file.
type Store struct {
	mu   sync.RWMutex
	path string
	c    Config
	// OnChange is called after every successful write.
	OnChange func(Config)
}

// Open reads dir/tailscale.json, or starts with the defaults.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "tailscale.json"), c: Default}
	b, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.c); err != nil {
			return nil, fmt.Errorf("%s: %w", s.path, err)
		}
		if s.c.Hostname == "" {
			s.c.Hostname = Default.Hostname
		}
		if s.c.Who == "" {
			s.c.Who = WhoAll
		}
		if s.c.AllowedUsers == nil {
			s.c.AllowedUsers = []string{}
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}
	if err := validate(s.c); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return s, nil
}

// Path is the file.
func (s *Store) Path() string { return s.path }

// Get is a copy.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.c
	c.AllowedUsers = append([]string{}, s.c.AllowedUsers...)
	return c
}

// Set stores a configuration.
func (s *Store) Set(in Config) error {
	in.ControlURL = strings.TrimRight(strings.TrimSpace(in.ControlURL), "/")
	in.Hostname = strings.ToLower(strings.TrimSpace(in.Hostname))
	users := make([]string, 0, len(in.AllowedUsers))
	for _, u := range in.AllowedUsers {
		if u = strings.TrimSpace(u); u != "" {
			users = append(users, u)
		}
	}
	in.AllowedUsers = users
	if err := validate(in); err != nil {
		return err
	}
	s.mu.Lock()
	prev := s.c
	s.c = in
	if err := s.write(); err != nil {
		s.c = prev
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	if s.OnChange != nil {
		s.OnChange(s.Get())
	}
	return nil
}

// Update changes the configuration under the lock.
func (s *Store) Update(f func(*Config)) error {
	c := s.Get()
	f(&c)
	return s.Set(c)
}

func (s *Store) write() error {
	b, err := json.MarshalIndent(s.c, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func validate(c Config) error {
	if !hostRe.MatchString(c.Hostname) {
		return invalid("the hostname is 1 to 63 lower-case letters, digits and hyphens")
	}
	if c.ControlURL != "" && !urlRe.MatchString(c.ControlURL) {
		return invalid("the login server is an http(s) URL")
	}
	if c.Who != WhoAll && c.Who != WhoUsers {
		return invalid("who is all or users")
	}
	if c.Who == WhoUsers && len(c.AllowedUsers) == 0 {
		return invalid("name at least one user or tag, or allow every device")
	}
	for _, u := range c.AllowedUsers {
		if !userRe.MatchString(u) {
			return invalid("%q is neither a login name nor a tag", u)
		}
	}
	if len(c.AllowedUsers) > 100 {
		return invalid("no more than 100 users or tags")
	}
	return nil
}
