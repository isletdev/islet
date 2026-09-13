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
case "$VERSION" in
  latest|v[0-9]*) ;;
  *) printf '\nislet: ISLET_VERSION must be "latest" or a tag such as v0.1.0, got "%s"\n' "$VERSION" >&2; exit 1 ;;
esac

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

[ -r /etc/os-release ] || die "cannot read /etc/os-release"
# Read it in a subshell. /etc/os-release sets VERSION and NAME, and sourcing
# it here would overwrite this script's own VERSION with something like
# "24.04.4 LTS (Noble Numbat)".
DISTRO="$(. /etc/os-release && printf '%s' "${ID:-}")"
case "$DISTRO" in
  ubuntu|debian) ;;
  *) die "unsupported distribution: ${DISTRO:-unknown} (Ubuntu 22.04+ and Debian 12+ for now)" ;;
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


# verify_signature checks the Ed25519 signature the release publishes over
# checksums.txt. The key is inlined as PEM so the check needs nothing but
# openssl, which every distro we install on already has.
ISLET_RELEASE_PUBKEY_PEM="-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEALQocWNPSIb1cTZCmPu6Qgx8pXqqgEBA4+f1+7a1vIG8=
-----END PUBLIC KEY-----"

verify_signature() {
  file="$1"; sig="$2"
  if ! command -v openssl >/dev/null 2>&1; then
    printf 'islet: openssl is missing, so the release signature cannot be checked
' >&2
    return 1
  fi
  raw="$TMP/sig.raw"; pub="$TMP/relkey.pem"
  printf '%s
' "$ISLET_RELEASE_PUBKEY_PEM" > "$pub" || return 1
  tr -d '
' < "$sig" | base64 -d > "$raw" 2>/dev/null || return 1
  openssl pkeyutl -verify -pubin -inkey "$pub" -rawin -in "$file" -sigfile "$raw" >/dev/null 2>&1
}

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
  curl -fsSL "$BASE/checksums.txt.sig" -o "$TMP/checksums.txt.sig" || die "signature download failed"
  # The checksum file arrives from the same host as the tarball, so on its own
  # it proves nothing: whoever can serve one can serve the other. The release
  # signature is what ties this download to the project's key.
  verify_signature "$TMP/checksums.txt" "$TMP/checksums.txt.sig" || die "release signature is not valid, refusing to install"
  ( cd "$TMP" && grep " $TARBALL\$" checksums.txt | sha256sum -c --quiet - ) || die "checksum mismatch, refusing to install"
  tar -xzf "$TMP/$TARBALL" -C "$TMP"
  install -m 0755 "$TMP/isletd" "$BIN_DIR/isletd"
fi
ln -sf "$BIN_DIR/isletd" "$BIN_DIR/islet"
say "installed $("$BIN_DIR/isletd" -version)"

step "4/5  Service"
mkdir -p "$DATA_DIR" && chmod 0750 "$DATA_DIR"
# Settings people are told to change live in a drop-in, so re-running this
# script never reverts them. The unit itself is ours to own.
mkdir -p /etc/systemd/system/isletd.service.d
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
# Keep retrying. With the default start limit a port collision or a bad
# environment value burns five restarts in ten seconds and the unit then stays
# dead until someone runs "systemctl reset-failed" by hand.
StartLimitIntervalSec=0
LimitNOFILE=65536
PrivateTmp=yes
UMask=0027

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable isletd >/dev/null
# --now does nothing to a unit that is already active, so an upgrade used to
# leave the old process running while reporting the new version.
systemctl restart isletd
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
