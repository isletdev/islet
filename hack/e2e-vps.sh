#!/bin/sh
# Creates a throwaway Hetzner Cloud server, installs the given isletd
# binary with the real installer, runs hack/e2e-smoke.sh on it and deletes
# the server again (also on failure). Used by .github/workflows/e2e.yml
# and usable by hand.
#
#   HCLOUD_TOKEN=… hack/e2e-vps.sh <image> <type> <path/to/isletd> [ssh-key-name]
#   e.g. hack/e2e-vps.sh ubuntu-24.04 cx22 dist/isletd_linux_amd64
set -eu
IMAGE="${1:?image, e.g. ubuntu-24.04}"; TYPE="${2:?server type, e.g. cx22}"; BIN="${3:?path to a linux isletd binary}"; KEY="${4:-islet-e2e}"
command -v hcloud >/dev/null 2>&1 || { echo "hcloud CLI is required (https://github.com/hetznercloud/cli)" >&2; exit 2; }
[ -n "${HCLOUD_TOKEN:-}" ] || { echo "HCLOUD_TOKEN is required" >&2; exit 2; }
NAME="islet-e2e-$(date +%s)-$(printf '%s' "$IMAGE" | tr -c 'a-z0-9' '-')"
SSH="ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 -o BatchMode=yes"
cleanup() { echo "== deleting $NAME"; hcloud server delete "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== creating $NAME ($IMAGE, $TYPE)"
hcloud server create --name "$NAME" --image "$IMAGE" --type "$TYPE" --ssh-key "$KEY" --label islet-e2e=1 >/dev/null
IP="$(hcloud server ip "$NAME")"
echo "== $IP: waiting for SSH"
i=0; until $SSH "root@$IP" true 2>/dev/null; do i=$((i+1)); [ "$i" -lt 40 ] || { echo "SSH never came up" >&2; exit 1; }; sleep 5; done

echo "== copying binary, installer and smoke test"
scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -q "$BIN" "root@$IP:/tmp/isletd"
scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -q installer/get.sh hack/e2e-smoke.sh "root@$IP:/tmp/"

echo "== installing"
$SSH "root@$IP" 'chmod +x /tmp/isletd && ISLET_BINARY=/tmp/isletd sh /tmp/get.sh'

echo "== smoke test"
$SSH "root@$IP" 'sh /tmp/e2e-smoke.sh https://127.0.0.1:9443'

echo "== journal tail"
$SSH "root@$IP" 'journalctl -u isletd -n 30 --no-pager' || true
echo "== $IMAGE on $TYPE: passed"
