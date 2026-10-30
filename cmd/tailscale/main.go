// tailscale is the lite-addon-tailscale daemon: a Tailscale node in userspace (tsnet, no TUN
// device, no capability, no root) inside this process, which makes exactly one thing reachable
// from the tailnet - the system's web server - by handing every connection to the system's
// PROXY-protocol ingress socket with the peer's tailnet address in front. It serves its page and
// API on the loopback behind lighttpd's proxy of /addons/tailscale/. Nothing is contacted before
// the user consented on the page. openccu-lite only.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit"
	"tailscale.com/envknob"

	"github.com/hobbyquaker/lite-addon-tailscale/internal/config"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/demo"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/httpapi"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/node"
	"github.com/hobbyquaker/lite-addon-tailscale/internal/sysinfo"
)

// version is set by the build (-ldflags "-X main.version=…").
var version = "dev"

// ranges are the manifest's web_ingress ranges: the tailnet's addresses.
var ranges = []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"}

func main() {
	log.SetFlags(0) // the journal adds the time
	addonDir := flag.String("addon-dir", "/usr/local/addons/tailscale", "the addon's directory (app/)")
	confDir := flag.String("conf-dir", "/usr/local/etc/config/addons/tailscale", "the configuration's directory (tailscale.json, state/; mode 0700)")
	id := flag.String("id", "tailscale", "the addon id (token file, base path)")
	port := flag.Int("port", 8299, "the loopback port lighttpd proxies to")
	base := flag.String("system", "http://127.0.0.1", "openccu-lite's URL")
	basePath := flag.String("base-path", "", "the path the page is served at (/addons/<id> by default)")
	www := flag.String("www", "", "the built page (<addon-dir>/app by default)")
	socket := flag.String("ingress-socket", node.DefaultSocket, "the web server's PROXY-protocol ingress socket")
	tokenFile := flag.String("token-file", "", "a token file for the system's API (the addon's own token by default)")
	noSession := flag.Bool("no-session-check", false, "serve the page without the session check (development only)")
	tsLog := flag.Bool("ts-log", false, "write Tailscale's own verbose log to the journal (debugging)")
	demoNode := flag.Bool("demo", false, "a stand-in node that contacts nothing (the page's development only)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("%s (tailscale %s)\n", version, node.ClientVersion())
		return
	}
	if *basePath == "" {
		*basePath = "/addons/" + *id
	}
	if *www == "" {
		*www = filepath.Join(*addonDir, "app")
	}
	if err := run(*confDir, *id, *port, *base, *basePath, *www, *socket, *tokenFile, *noSession, *tsLog, *demoNode); err != nil {
		log.Fatalf("tailscale: %v", err)
	}
}

func run(confDir, id string, port int, base, basePath, www, socket, tokenFile string, noSession, tsLog, demoNode bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logf := func(format string, args ...any) { log.Printf("tailscale: "+format, args...) }

	// no log upload to Tailscale, ever: logtail is compiled out (ts_omit_logtail) and the knob
	// tells the control server so (Hostinfo.NoLogsNoSupport)
	envknob.SetNoLogsNoSupport()

	store, err := config.Open(confDir)
	if err != nil {
		return err
	}
	opts := node.Options{StateDir: filepath.Join(confDir, "state"), Socket: socket, Log: logf, Config: store.Get}
	if tsLog {
		opts.TSLog = func(format string, args ...any) { log.Printf("tsnet: "+format, args...) }
	}
	var n httpapi.NodeAPI = node.New(opts)
	if demoNode {
		logf("DEMO: a stand-in node, nothing is contacted")
		n = &demo.Node{Config: store.Get}
	}
	defer n.Stop()
	store.OnChange = func(c config.Config) {
		if err := n.Apply(c); err != nil {
			logf("the node could not follow the configuration: %v", err)
		}
	}
	if err := n.Apply(store.Get()); err != nil {
		logf("the node is not up: %v", err)
	}
	// a node that could not come up is tried again every minute
	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				c := store.Get()
				if c.Consented && c.Enabled && !n.Running() {
					if err := n.Apply(c); err == nil {
						logf("the node is up after a retry")
					}
				}
			}
		}
	}()

	var token addonkit.TokenSource
	if tokenFile != "" {
		token = &addonkit.FileToken{Path: tokenFile}
	} else {
		token = addonkit.AddonToken(id)
	}
	facts := &sysinfo.Client{Base: base, HTTP: &http.Client{Transport: &addonkit.AuthTransport{Source: token}, Timeout: 30 * time.Second}, AddonID: id, Log: logf}
	go func() {
		// wait for the system in the background: at boot its web server answers 503 until the
		// system service is up, and the token may not exist yet; the node runs regardless
		if f, err := facts.Await(ctx); err == nil {
			logf("the system accepts the token (%v)", f.Scopes)
		}
	}()

	wrap := func(h http.Handler) http.Handler { return h }
	if !noSession {
		c := addonkit.NewChecker(addonkit.SessionOptions{Base: base, Need: addonkit.LevelAdmin, AddonID: id, OnRefused: func(r *http.Request, reason string) {
			logf("refused %s %s: %s", r.Method, r.URL.Path, reason)
		}})
		wrap = c.Handler
	}
	h := httpapi.New(httpapi.Options{BasePath: basePath, WWW: www, Store: store, Node: n, Facts: facts, Wrap: wrap, Version: version, Started: time.Now(), Ranges: ranges})
	l, err := addonkit.Listen(port, "")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	c := store.Get()
	logf("%s (tailscale %s): serving %s on %s; consented %v, enabled %v, hostname %s, login server %s, the web server through %s", version, node.ClientVersion(), basePath, l.Addr(), c.Consented, c.Enabled, c.Hostname, orTailscale(c.ControlURL), socket)
	if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
		return err
	}
	logf("stopping")
	os.Stdout.Sync()
	return nil
}

func orTailscale(u string) string {
	if u == "" {
		return "Tailscale"
	}
	return u
}
