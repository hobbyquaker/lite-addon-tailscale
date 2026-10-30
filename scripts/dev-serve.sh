#!/bin/sh
#
#   Runs the daemon against the fake system and a stand-in node, for the page's development and
#   the Playwright tests: the fake system on 127.0.0.1:18080, the page on
#   http://127.0.0.1:18299/addons/tailscale/ (no session check). The node is the demo stand-in
#   (-demo): it contacts nothing and behaves like a node on a small tailnet. A fresh
#   configuration directory each run unless TS_CONF_DIR names one. The page is served from
#   ui/dist (npm run build first).
#
set -e
cd "$(dirname "$0")/.."
DIR=${TS_CONF_DIR:-$(mktemp -d)}
mkdir -p build "$DIR/addon"
go build -o build/fakesystem ./cmd/fakesystem
go build -o build/tailscale-dev ./cmd/tailscale
echo dev > "$DIR/token"
build/fakesystem -addr 127.0.0.1:18080 -socket "$DIR/proxy.sock" &
FAKE=$!
sleep 0.5
build/tailscale-dev -system http://127.0.0.1:18080 -no-session-check -port 18299 -conf-dir "$DIR" -addon-dir "$DIR/addon" -www ui/dist -token-file "$DIR/token" -ingress-socket "$DIR/proxy.sock" -demo &
DAEMON=$!
trap 'kill $DAEMON $FAKE 2>/dev/null' EXIT INT TERM
wait $DAEMON
