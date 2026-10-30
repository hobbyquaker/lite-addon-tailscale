#!/bin/sh
#
#   Builds the addon packages: dist/tailscale-<arch>-<version>.tar.gz and .sha256 for aarch64, x86_64
#   and armv7l (the asset names openccu-lite's catalogue resolves by uname -m).
#
#     VERSION=0.1.0 scripts/build.sh            # all three
#     ARCHES=x86_64 scripts/build.sh            # one
#     SKIP_UI=1 scripts/build.sh                # keep addon/tailscale/app as it is
#
#   The page is built with npm in ui/ and copied to addon/tailscale/app (the daemon serves it at
#   /addons/tailscale/); the manifest's icons, the 503 page and the proxy rule live in addon/tailscale/www
#   and addon/tailscale/etc, tracked, where the catalogue reads the icons from the repository at the
#   release tag; the daemon is a static Go binary (CGO_ENABLED=0) per architecture.
#
set -e
cd "$(dirname "$0")/.."
VERSION=${VERSION:-0.0.0-dev}
ARCHES=${ARCHES:-aarch64 x86_64 armv7l}
GO=${GO:-go}
# the Tailscale features the addon compiles out (log upload, port mapping, SSH, routes, …): one list
TAGS=$(cat scripts/tags)

if [ -z "$SKIP_UI" ]; then
    (cd ui && npm ci --no-audit --no-fund && npm run build)
    rm -rf addon/tailscale/app
    mkdir -p addon/tailscale/app
    cp -a ui/dist/. addon/tailscale/app/
fi
cp -a addon/static/. addon/tailscale/www/
printf 'VERSION_ADDON=%s\n' "$VERSION" > addon/tailscale/versions

mkdir -p dist
for arch in $ARCHES; do
    case $arch in
        aarch64) goarch=arm64; goarm= ;;
        x86_64) goarch=amd64; goarm= ;;
        armv7l) goarch=arm; goarm=7 ;;
        *) echo "unknown architecture $arch" >&2; exit 1 ;;
    esac
    mkdir -p addon/tailscale/bin
    rm -f addon/tailscale/bin/tailscale
    CGO_ENABLED=0 GOOS=linux GOARCH=$goarch GOARM=$goarm $GO build -trimpath -tags "$(echo $TAGS | tr ' ' ',')" -ldflags "-s -w -X main.version=$VERSION" -o addon/tailscale/bin/tailscale ./cmd/tailscale

    out=dist/tailscale-$arch-$VERSION.tar.gz
    stage=$(mktemp -d)
    cp -a addon/update_script addon/openccu-lite.json addon/tailscale "$stage/"
    rm -rf "$stage/tailscale/var"
    find "$stage" \( -name '._*' -o -name '.DS_Store' \) -exec rm -f {} +
    chmod 755 "$stage/update_script" "$stage/tailscale/rc.d" "$stage/tailscale/bin/tailscale"
    test -f "$stage/tailscale/etc/lighttpd.conf"
    COPYFILE_DISABLE=1 tar --owner=root --group=root --sort=name --mtime='2026-01-01 00:00Z' -czf "$out" -C "$stage" .
    rm -rf "$stage"
    (cd dist && sha256sum "$(basename "$out")" > "$(basename "$out").sha256")
    echo "built $out"
done
