#!/bin/sh
# Build the web app, embed it, and compile the daemon and CLI.
# Usage: scripts/build.sh [version]   (default: git describe)
set -eu
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PKG="github.com/isletdev/islet/internal/version"
LDFLAGS="-s -w -X $PKG.Version=$VERSION -X $PKG.Commit=$COMMIT -X $PKG.Date=$DATE"

echo "web: building"
( cd web/app && pnpm install --frozen-lockfile --silent && pnpm build --silent )
rm -rf internal/web/dist && cp -r web/app/dist internal/web/dist

mkdir -p bin
EXT=""; [ "$(go env GOOS)" = "windows" ] && EXT=".exe"
echo "go: building isletd $VERSION"
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "bin/isletd$EXT" ./cmd/isletd
echo "go: building islet"
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "bin/islet$EXT" ./cmd/islet
ls -la bin
