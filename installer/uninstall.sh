#!/bin/sh
# Islet uninstaller. Removes the daemon and its service. Leaves Docker, every
# container, every volume and every app exactly as they are.
#
#   sh uninstall.sh            keep /var/lib/islet (state, certificate, secrets)
#   sh uninstall.sh --purge    also delete /var/lib/islet
set -eu

[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }

PURGE=0
[ "${1:-}" = "--purge" ] && PURGE=1

if systemctl list-unit-files isletd.service >/dev/null 2>&1; then
  systemctl disable --now isletd >/dev/null 2>&1 || true
fi
rm -f /etc/systemd/system/isletd.service
systemctl daemon-reload >/dev/null 2>&1 || true
rm -f /usr/local/bin/isletd /usr/local/bin/islet

if [ "$PURGE" -eq 1 ]; then
  rm -rf /var/lib/islet
  echo "islet removed, including its state."
else
  echo "islet removed. State kept in /var/lib/islet; re-run the installer to pick it up, or delete it with --purge."
fi
echo "Docker, containers, volumes and apps were not touched."
