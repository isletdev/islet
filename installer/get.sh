#!/bin/sh
# Islet installer. Usage:
#   curl -fsSL https://get.islet.dev | sh
#   curl -fsSL https://raw.githubusercontent.com/isletdev/islet/main/installer/get.sh | sh
#
# What it does, in order, and nothing else:
#   1. Checks it runs as root on a supported Linux (Ubuntu 22.04+, Debian 12+), x86_64 or arm64.
#   2. Installs curl and ca-certificates if missing.
#   3. Installs Docker Engine with Docker's official script if `docker` is missing.
#   4. Downloads the pinned isletd release, verifies its SHA-256, installs it to /usr/local/bin.
#   5. Creates /var/lib/islet, installs and starts the systemd service.
#   6. Prints the panel URL.
#
# Override the version with ISLET_VERSION=v0.1.0. Set ISLET_SKIP_DOCKER=1 to skip step 3.
set -eu

REPO="isletdev/islet"
BIN_DIR="/usr/local/bin"
DATA_DIR="/var/lib/islet"
VERSION="${ISLET_VERSION:-latest}"

say()  { printf '%s\n' "$*"; }
step() { printf '\n\033[1m%s\033[0m\n' "$*"; }
die()  { printf '\nislet: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root (sudo sh)"
[ "$(uname -s)" = "Linux" ] || die "Linux only"

case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

if [ -r /etc/os-release ]; then
  . /etc/os-release
else
  die "cannot read /etc/os-release"
fi
case "${ID:-}" in
  ubuntu|debian) ;;
  *) die "unsupported distribution: ${ID:-unknown} (Ubuntu 22.04+ and Debian 12+ for now)" ;;
esac
command -v systemctl >/dev/null 2>&1 || die "systemd is required"

step "1/5  Base packages"
if ! command -v curl >/dev/null 2>&1 || ! [ -d /etc/ssl/certs ]; then
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl ca-certificates >/dev/null
fi
say "curl and certificates present"

step "2/5  Docker"
if [ "${ISLET_SKIP_DOCKER:-0}" = "1" ]; then
  say "skipped (ISLET_SKIP_DOCKER=1)"
elif command -v docker >/dev/null 2>&1; then
  say "already installed: $(docker --version)"
else
  curl -fsSL https://get.docker.com | sh >/dev/null
  systemctl enable --now docker >/dev/null
  say "installed: $(docker --version)"
fi

step "3/5  isletd $VERSION"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
if [ -n "${ISLET_BINARY:-}" ]; then
  # A locally built binary (CI end-to-end runs, developers). No download, no checksum.
  [ -x "$ISLET_BINARY" ] || die "ISLET_BINARY is not an executable file: $ISLET_BINARY"
  say "using local binary $ISLET_BINARY"
  install -m 0755 "$ISLET_BINARY" "$BIN_DIR/isletd"
else
  if [ "$VERSION" = "latest" ]; then
    BASE="https://github.com/$REPO/releases/latest/download"
  else
    BASE="https://github.com/$REPO/releases/download/$VERSION"
  fi
  TARBALL="isletd_linux_${ARCH}.tar.gz"
  curl -fsSL "$BASE/$TARBALL" -o "$TMP/$TARBALL" || die "download failed: $BASE/$TARBALL"
  curl -fsSL "$BASE/checksums.txt" -o "$TMP/checksums.txt" || die "checksums download failed"
  ( cd "$TMP" && grep " $TARBALL\$" checksums.txt | sha256sum -c --quiet - ) || die "checksum mismatch, refusing to install"
  tar -xzf "$TMP/$TARBALL" -C "$TMP"
  install -m 0755 "$TMP/isletd" "$BIN_DIR/isletd"
fi
ln -sf "$BIN_DIR/isletd" "$BIN_DIR/islet"
say "installed $("$BIN_DIR/isletd" -version)"

step "4/5  Service"
mkdir -p "$DATA_DIR" && chmod 0750 "$DATA_DIR"
cat > /etc/systemd/system/isletd.service <<'UNIT'
[Unit]
Description=Islet server panel
Documentation=https://islet.dev
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/isletd
Environment=ISLET_DATA_DIR=/var/lib/islet
Environment=ISLET_LISTEN=0.0.0.0:9443
Restart=always
RestartSec=2
LimitNOFILE=65536
PrivateTmp=yes
UMask=0027

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now isletd >/dev/null
sleep 1
systemctl is-active --quiet isletd || die "isletd failed to start; see: journalctl -u isletd -n 50"
say "isletd is running"

step "5/5  Done"
IP="$(curl -fsS4 --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I 2>/dev/null | awk '{print $1}')"
TOKEN="$(cat "$DATA_DIR/setup-token" 2>/dev/null || true)"
HOST="${IP:-<server-ip>}"
case "$IP" in
  *.*.*.*) HOST="$(printf '%s' "$IP" | tr . -).sslip.io" ;;
esac
FP="$(openssl x509 -in "$DATA_DIR/tls/cert.pem" -noout -fingerprint -sha256 2>/dev/null | cut -d= -f2 || true)"
say ""
if [ -n "$TOKEN" ]; then
  say "  Create your admin account (this link works once):"
  say "  https://$HOST:9443/setup?token=$TOKEN"
else
  say "  Panel:  https://$HOST:9443"
fi
say ""
say "  The panel uses a certificate it issued itself. Your browser will warn once;"
say "  compare the fingerprint before accepting:"
say "  SHA-256 ${FP:-see: journalctl -u isletd | grep tls}"
say ""
say "  Logs:   journalctl -u isletd -f"
say "  CLI:    islet status"
