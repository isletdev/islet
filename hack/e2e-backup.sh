#!/bin/sh
# A backup that restores, which is Phase 5's definition of done.
#
# internal/backup is the largest package in this repository with no tests, and
# the failure it is exposed to is the worst kind: silent until the day somebody
# needs the data. This proves the round trip against a live daemon, with a local
# restic repository, and compares the bytes.
#
#   ISLET_BASE=http://127.0.0.1:9444 hack/e2e-backup.sh "$SESSION_COOKIE"
#
# It needs Docker, because restic runs in a container — which is the point of
# the design and means there is nothing to install first.
set -eu

BASE="${ISLET_BASE:-http://127.0.0.1:9443}"
COOKIE="${1:?usage: ISLET_BASE=… hack/e2e-backup.sh <islet_session cookie>}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

api() {
  method="$1"; path="$2"; shift 2
  curl -sS -X "$method" "$BASE$path" -H "Cookie: islet_session=$COOKIE" \
       -H 'Content-Type: application/json' "$@"
}

fail() { echo "  FAIL  $1"; exit 1; }
ok()   { echo "  ok    $1"; }

mkdir -p "$WORK/repo" "$WORK/source"
echo "the canary, written $(date -Iseconds)" > "$WORK/source/canary.txt"
dd if=/dev/urandom of="$WORK/source/blob.bin" bs=1k count=200 2>/dev/null
before_blob=$(md5sum "$WORK/source/blob.bin" | cut -d' ' -f1)
before_text=$(cat "$WORK/source/canary.txt")

dest=$(api POST /api/v1/backups/destinations \
  -d "{\"name\":\"e2e\",\"type\":\"local\",\"config\":{\"path\":\"$WORK/repo\"},\"password\":\"e2e-pass\"}" \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
[ -n "$dest" ] || fail "could not create a destination"
ok "destination created, restic repository initialised"

plan=$(api POST /api/v1/backups/plans \
  -d "{\"name\":\"e2e-plan\",\"destinationId\":\"$dest\",\"sources\":[{\"type\":\"path\",\"value\":\"$WORK/source\"}],\"schedule\":\"0 3 * * *\",\"enabled\":true}" \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
[ -n "$plan" ] || fail "could not create a plan"

api POST "/api/v1/backups/plans/$plan/run" -d '{}' > "$WORK/run.log" 2>&1 || fail "the backup did not run"
grep -q "snapshot" "$WORK/run.log" || fail "the run produced no snapshot: $(tail -2 "$WORK/run.log")"
ok "backup ran and wrote a snapshot"

snap=$(api GET "/api/v1/backups/destinations/$dest/snapshots" | sed -n 's/.*"id":"\([0-9a-f]*\)".*/\1/p' | head -1)
[ -n "$snap" ] || fail "no snapshot is listed"
ok "snapshot $snap is listed"

# The host path, not the path as the container sees it: somebody restoring types
# what they backed up.
target=$(api POST "/api/v1/backups/destinations/$dest/restore" \
  -d "{\"Snapshot\":\"$snap\",\"Include\":\"$WORK/source\"}" \
  | sed -n 's/.*"target":"\([^"]*\)".*/\1/p')
[ -n "$target" ] || fail "restore returned no target"
ok "restored to $target"

restored_blob=$(find "$target" -name blob.bin -print -quit)
restored_text=$(find "$target" -name canary.txt -print -quit)
[ -n "$restored_blob" ] || fail "blob.bin is not in the restore"
[ -n "$restored_text" ] || fail "canary.txt is not in the restore"
[ "$(md5sum "$restored_blob" | cut -d' ' -f1)" = "$before_blob" ] || fail "the restored bytes differ from the original"
[ "$(cat "$restored_text")" = "$before_text" ] || fail "the restored text differs from the original"
ok "every byte came back identical"

api DELETE "/api/v1/backups/plans/$plan" > /dev/null 2>&1 || true
api DELETE "/api/v1/backups/destinations/$dest" > /dev/null 2>&1 || true
rm -rf "$target"
echo
echo "backup e2e: clean"
