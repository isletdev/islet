#!/bin/sh
# Islet uninstaller.
#
# Removes the panel. Your apps keep running and keep serving.
#
#   sh uninstall.sh            remove the daemon and its service, keep all state
#   sh uninstall.sh --purge    also forget Islet's own state (its database and
#                              keys). Your apps, their Compose files, your
#                              certificates, database dumps and trash are kept,
#                              and the proxy keeps serving your domains.
#
# Nothing here stops or removes a container, and nothing deletes a volume.
# Uninstalling the panel and destroying what it set up are different
# intentions, and only the first one has a flag.
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }

DATA_DIR=/var/lib/islet
PURGE=0
[ "${1:-}" = "--purge" ] && PURGE=1

if systemctl list-unit-files isletd.service >/dev/null 2>&1; then
  systemctl disable --now isletd >/dev/null 2>&1 || true
fi
rm -f /etc/systemd/system/isletd.service
rm -rf /etc/systemd/system/isletd.service.d
systemctl daemon-reload >/dev/null 2>&1 || true
rm -f /usr/local/bin/isletd /usr/local/bin/islet

# Domains marked "Protect with Islet login" ask the panel whether each visitor
# is signed in. With the panel gone there is nobody to ask, so say so plainly
# rather than letting the owner discover it as a 502.
PROTECTED=""
DYN="$DATA_DIR/proxy/dynamic/islet.yml"
if [ -f "$DYN" ] && grep -q "islet-forward-auth" "$DYN" 2>/dev/null; then
  PROTECTED=yes
fi

if [ "$PURGE" -eq 1 ]; then
  # Only Islet's own memory. Everything a person would be upset to lose lives
  # beside it and stays: stacks holds the Compose file for every app, proxy
  # holds the routing config and the Let's Encrypt certificates and account key,
  # dumps holds database exports, trash holds deleted files.
  rm -f "$DATA_DIR/islet.db" "$DATA_DIR/islet.db-wal" "$DATA_DIR/islet.db-shm"
  rm -f "$DATA_DIR/secret.key" "$DATA_DIR/setup-token" "$DATA_DIR/scans.json"
  rm -rf "$DATA_DIR/tls"
  echo "islet removed, and it has forgotten its own settings."
  echo "Kept in $DATA_DIR: stacks (your apps), proxy (routing and certificates), dumps, trash."
else
  echo "islet removed. Everything in $DATA_DIR was kept, so re-running the installer picks up where you left off."
fi

echo
echo "Still running and untouched: Docker, every container, every volume, and the proxy serving your domains."
echo "To manage an app without Islet: cd $DATA_DIR/stacks/<name> && docker compose ps"
if [ -n "$PROTECTED" ]; then
  echo
  echo "Note: some domains were set to require an Islet login. Those will stop"
  echo "     letting visitors through until Islet is installed again. Remove the"
  echo "     islet-forward-auth middleware from $DYN to open them."
fi
if [ "$PURGE" -eq 1 ]; then
  echo
  echo "To remove everything, including your apps' Compose files and your"
  echo "certificates, delete $DATA_DIR by hand once you are sure:"
  echo "     rm -rf $DATA_DIR"
fi
