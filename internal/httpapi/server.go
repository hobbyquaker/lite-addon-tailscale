// Package httpapi is the addon's own HTTP server: the Svelte page's files and its JSON API under
// one base path (/addons/tailscale/), which lighttpd proxies to the loopback port. Every request
// carries the shell's session, confirmed through the kit (administrators only); writes carry the
// kit's CSRF header. docs/api.md documents the routes.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit"

	"github.com/hobbyquaker/lite-addon-tailscale/internal/config"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/node"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/sysinfo"
)

// FactsReader answers the system's facts (sysinfo.Client; a fake in the tests).
type FactsReader interface {
	Facts(ctx context.Context, force bool) sysinfo.Facts
}

// NodeAPI is the node as the API drives it (node.Node; the demo stand-in in development).
type NodeAPI interface {
	Running() bool
	Status(ctx context.Context) node.Status
	Apply(c config.Config) error
	Stop()
	Login(ctx context.Context) error
	LoginWithKey(ctx context.Context, key string) error
	Logout(ctx context.Context) error
	Forget() error
}

// Options configure the server.
type Options struct {
	// BasePath is where the page is served, /addons/tailscale (no trailing slash).
	BasePath string
	// WWW is the directory with the built page.
	WWW   string
	Store *config.Store
	Node  NodeAPI
	Facts FactsReader
	// Wrap is the session check; nil: none (development only).
	Wrap    func(http.Handler) http.Handler
	Version string
	Started time.Time
	// Ranges are the manifest's ingress ranges, shown on the page.
	Ranges []string
}

// Routes lists every "METHOD /path" the API serves; the readme's table is held to it by a test.
var Routes []string

type server struct {
	o Options
}

func handle(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	Routes = append(Routes, pattern)
	mux.HandleFunc(pattern, h)
}

// New builds the handler.
func New(o Options) http.Handler {
	o.BasePath = strings.TrimRight(o.BasePath, "/")
	s := &server{o: o}
	Routes = nil
	api := http.NewServeMux()
	handle(api, "GET /api/v1/health", s.health)
	handle(api, "GET /api/v1/config", s.config)
	handle(api, "PUT /api/v1/settings", s.putSettings)
	handle(api, "POST /api/v1/connect", s.connect)
	handle(api, "POST /api/v1/disconnect", s.disconnect)
	handle(api, "POST /api/v1/login", s.login)
	handle(api, "POST /api/v1/logout", s.logout)
	handle(api, "GET /api/v1/status", s.status)
	handle(api, "GET /api/v1/system", s.system)
	api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		addonkit.WriteError(w, http.StatusNotFound, "not-found", r.URL.Path)
	})

	root := http.NewServeMux()
	root.Handle("/api/", addonkit.CSRF(api))
	root.Handle("/", addonkit.AppFiles(o.WWW))

	var h http.Handler = http.StripPrefix(o.BasePath, root)
	if o.Wrap != nil {
		h = o.Wrap(h)
	}
	return guard(h)
}

func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; p != "/" && (path.Clean(p) != strings.TrimSuffix(p, "/") && path.Clean(p) != p) {
			http.NotFound(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		addonkit.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, err error) {
	var ve *config.ValidationError
	switch {
	case errors.As(err, &ve):
		addonkit.WriteError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
	default:
		addonkit.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// ConfigView is GET /api/v1/config: the configuration (nothing secret is in it) and the constants
// the page shows.
type ConfigView struct {
	Consented    bool     `json:"consented"`
	Enabled      bool     `json:"enabled"`
	ControlURL   string   `json:"control_url"`
	Hostname     string   `json:"hostname"`
	HTTPS        bool     `json:"https"`
	Redirect     bool     `json:"redirect"`
	Who          string   `json:"who"`
	AllowedUsers []string `json:"allowed_users"`
	// Ranges are the tailnet address ranges the manifest declares for the ingress.
	Ranges []string `json:"ranges"`
	// ClientVersion is the embedded Tailscale client's version.
	ClientVersion string `json:"client_version"`
	Version       string `json:"version"`
}

func (s *server) view() ConfigView {
	c := s.o.Store.Get()
	return ConfigView{Consented: c.Consented, Enabled: c.Enabled, ControlURL: c.ControlURL, Hostname: c.Hostname, HTTPS: c.HTTPS, Redirect: c.Redirect, Who: c.Who, AllowedUsers: c.AllowedUsers, Ranges: append([]string{}, s.o.Ranges...), ClientVersion: node.ClientVersion(), Version: s.o.Version}
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	c := s.o.Store.Get()
	ready := !(c.Consented && c.Enabled) || s.o.Node.Running()
	if r.URL.Query().Get("ready") == "1" && !ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "ready": false, "version": s.o.Version})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "version": s.o.Version, "uptime_s": int(time.Since(s.o.Started).Seconds()), "ready": ready})
}

func (s *server) config(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.view())
}

// Settings is PUT /api/v1/settings.
type Settings struct {
	ControlURL   string   `json:"control_url"`
	Hostname     string   `json:"hostname"`
	HTTPS        bool     `json:"https"`
	Redirect     bool     `json:"redirect"`
	Who          string   `json:"who"`
	AllowedUsers []string `json:"allowed_users"`
}

func (s *server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in Settings
	if !decode(w, r, &in) {
		return
	}
	cur := s.o.Store.Get()
	in.ControlURL = strings.TrimSpace(strings.TrimRight(in.ControlURL, "/"))
	if in.ControlURL != cur.ControlURL && s.o.Node.Status(r.Context()).HasIdentity {
		addonkit.WriteError(w, http.StatusUnprocessableEntity, "invalid", "the login server can only change while the node is logged out: log out first (the identity belongs to the current login server)")
		return
	}
	err := s.o.Store.Update(func(c *config.Config) {
		c.ControlURL, c.Hostname, c.HTTPS, c.Redirect, c.Who, c.AllowedUsers = in.ControlURL, in.Hostname, in.HTTPS, in.Redirect, in.Who, in.AllowedUsers
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, s.view())
}

// Connect is POST /api/v1/connect: the first time it records the consent (and takes the login
// server from the consent card); an auth key, when given, registers the node without the
// interactive login. The key is used once and never stored.
type Connect struct {
	ControlURL string `json:"control_url"`
	AuthKey    string `json:"auth_key"`
}

func (s *server) connect(w http.ResponseWriter, r *http.Request) {
	var in Connect
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	cur := s.o.Store.Get()
	in.ControlURL = strings.TrimSpace(strings.TrimRight(in.ControlURL, "/"))
	if !cur.Consented || !s.o.Node.Status(r.Context()).HasIdentity {
		// the login server may be set with the first connect, or while there is no identity
		if in.ControlURL != "" || !cur.Consented {
			cur.ControlURL = in.ControlURL
		}
	} else if in.ControlURL != "" && in.ControlURL != cur.ControlURL {
		addonkit.WriteError(w, http.StatusUnprocessableEntity, "invalid", "the login server can only change while the node is logged out")
		return
	}
	key := strings.TrimSpace(in.AuthKey)
	if len(key) > 200 || strings.ContainsAny(key, " \t\n") {
		addonkit.WriteError(w, http.StatusUnprocessableEntity, "invalid", "that is no auth key")
		return
	}
	// the node is started by hand here so the key can go with the start; the store's OnChange
	// then finds it running
	wasRunning := s.o.Node.Running()
	if !wasRunning {
		cur.Consented, cur.Enabled = true, true
		if err := s.o.Store.Set(cur); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.o.Node.Apply(cur); err != nil {
			writeErr(w, err)
			return
		}
	}
	if key != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := s.o.Node.LoginWithKey(ctx, key); err != nil {
			addonkit.WriteError(w, http.StatusBadGateway, "login", err.Error())
			return
		}
	}
	s.status(w, r)
}

func (s *server) disconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.o.Store.Update(func(c *config.Config) { c.Enabled = false }); err != nil {
		writeErr(w, err)
		return
	}
	s.o.Node.Stop()
	s.status(w, r)
}

// Login is POST /api/v1/login: a new interactive login URL, or a registration with an auth key.
type Login struct {
	AuthKey string `json:"auth_key"`
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in Login
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	if !s.o.Node.Running() {
		addonkit.WriteError(w, http.StatusConflict, "not-running", "the node is not running: connect first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	key := strings.TrimSpace(in.AuthKey)
	var err error
	if key != "" {
		err = s.o.Node.LoginWithKey(ctx, key)
	} else {
		err = s.o.Node.Login(ctx)
	}
	if err != nil {
		addonkit.WriteError(w, http.StatusBadGateway, "login", err.Error())
		return
	}
	s.status(w, r)
}

// Logout is POST /api/v1/logout: {forget} - plain: the login server expires the node's key and
// the node asks for a new login; forget: the node is stopped as well, its state removed and the
// addon switched off (the consent stays).
type Logout struct {
	Forget bool `json:"forget"`
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	var in Logout
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if s.o.Node.Running() {
		if err := s.o.Node.Logout(ctx); err != nil && !in.Forget {
			addonkit.WriteError(w, http.StatusBadGateway, "logout", err.Error())
			return
		}
	}
	if in.Forget {
		if err := s.o.Store.Update(func(c *config.Config) { c.Enabled = false }); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.o.Node.Forget(); err != nil {
			writeErr(w, err)
			return
		}
	}
	s.status(w, r)
}

// StatusView is GET /api/v1/status: the node, with the page's clock.
type StatusView struct {
	node.Status
	Enabled   bool  `json:"enabled"`
	Consented bool  `json:"consented"`
	Now       int64 `json:"now"`
}

func (s *server) status(w http.ResponseWriter, r *http.Request) {
	c := s.o.Store.Get()
	writeJSON(w, 200, StatusView{Status: s.o.Node.Status(r.Context()), Enabled: c.Enabled, Consented: c.Consented, Now: time.Now().UnixMilli()})
}

func (s *server) system(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.o.Facts.Facts(r.Context(), r.URL.Query().Get("refresh") == "1"))
}
