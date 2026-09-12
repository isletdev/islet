#!/bin/sh
# Islet full restore. Turns a fresh Ubuntu/Debian server into a copy of the
# one that wrote the recovery kit: installs Islet, restores its state, the
# Docker volumes and the database dumps from the latest snapshot, starts the
# managed stacks. Also the migration path between providers.
#
#   sh restore.sh recovery-kit.json [destination-name] [snapshot-id]
#
# Needs: root, curl, python3 (present on Ubuntu and Debian). Docker and
# isletd are installed if missing. Nothing is deleted; existing volumes with
# the same name are left alone and reported.
set -eu

KIT="${1:-}"; DEST="${2:-}"; SNAP="${3:-latest}"
[ -n "$KIT" ] && [ -r "$KIT" ] || { echo "usage: sh restore.sh recovery-kit.json [destination] [snapshot]" >&2; exit 2; }
[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 1; }

DATA_DIR="${ISLET_DATA_DIR:-/var/lib/islet}"
WORK="$(mktemp -d /tmp/islet-restore.XXXXXX)"
say()  { printf '%s\n' "$*"; }
step() { printf '\n\033[1m%s\033[0m\n' "$*"; }

# ---- 1. pick the destination and build the restic environment ----
step "Reading the recovery kit"
python3 - "$KIT" "$DEST" "$WORK" <<'PY'
import json, sys, os
kit = json.load(open(sys.argv[1]))
want, work = sys.argv[2], sys.argv[3]
dests = kit.get("destinations") or []
if not dests:
    sys.exit("the kit lists no destinations")
d = next((x for x in dests if x["name"] == want), None) if want else dests[0]
if d is None:
    sys.exit("no destination named %s; available: %s" % (want, ", ".join(x["name"] for x in dests)))
c = d.get("config") or {}
env = {"RESTIC_PASSWORD": d.get("password", "")}
t = d["type"]
if t == "s3":
    repo = "s3:https://%s/%s" % (c["endpoint"], c["bucket"])
    if c.get("prefix"): repo += "/" + c["prefix"].strip("/")
    env.update(RESTIC_REPOSITORY=repo, AWS_ACCESS_KEY_ID=c["accessKey"], AWS_SECRET_ACCESS_KEY=c["secretKey"])
    if c.get("region"): env["AWS_DEFAULT_REGION"] = c["region"]
elif t == "sftp":
    open(os.path.join(work, "sftp.key"), "w").write(c["privateKey"].strip() + "\n"); os.chmod(os.path.join(work, "sftp.key"), 0o600)
    port = c.get("port") or "22"
    env.update(RESTIC_REPOSITORY="sftp:%s@%s:%s" % (c["user"], c["host"], c["path"]),
               RESTIC_SFTP_COMMAND="ssh -p %s -i /work/sftp.key -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/work/known_hosts %s@%s -s sftp" % (port, c["user"], c["host"]))
elif t == "local":
    env.update(RESTIC_REPOSITORY="/repo"); open(os.path.join(work, "localpath"), "w").write(c["path"])
elif t == "rest":
    env.update(RESTIC_REPOSITORY="rest:" + c["url"].rstrip("/"))
    if c.get("user"): env.update(RESTIC_REST_USERNAME=c["user"], RESTIC_REST_PASSWORD=c.get("password", ""))
else:
    sys.exit("unknown destination type " + t)
with open(os.path.join(work, "restic.env"), "w") as f:
    for k, v in env.items(): f.write("%s=%s\n" % (k, v))
print("destination:", d["name"], "(%s)" % t, "->", d.get("repo", ""))
print("hostname in kit:", kit.get("hostname", "?"), "generated", kit.get("generatedAt", "?"))
PY

# ---- 2. Docker and Islet ----
if ! command -v docker >/dev/null 2>&1 || ! command -v isletd >/dev/null 2>&1; then
  step "Installing Islet (and Docker if missing)"
  curl -fsSL https://get.islet.dev | sh
fi
systemctl stop isletd 2>/dev/null || true

RESTIC="docker run --rm --env-file $WORK/restic.env -v $WORK:/work -v islet-restic-cache:/cache -e RESTIC_CACHE_DIR=/cache"
[ -f "$WORK/localpath" ] && RESTIC="$RESTIC -v $(cat "$WORK/localpath"):/repo"
RESTIC="$RESTIC -v $WORK/out:/restore restic/restic:0.17.3"

# ---- 3. restore the snapshot ----
step "Restoring snapshot $SNAP into $WORK/out"
mkdir -p "$WORK/out"
$RESTIC snapshots --latest 1 || { echo "cannot reach the repository; check the kit and network" >&2; exit 1; }
$RESTIC restore "$SNAP" --target /restore

# ---- 4. Islet state ----
if [ -d "$WORK/out/data/islet" ]; then
  step "Restoring Islet state into $DATA_DIR"
  mkdir -p "$DATA_DIR"
  cp -a "$WORK/out/data/islet/." "$DATA_DIR/"
  chmod 700 "$DATA_DIR"
else
  say "no Islet state in this snapshot (the plan did not include it); the panel starts fresh"
fi

# ---- 5. volumes ----
if [ -d "$WORK/out/data/volumes" ]; then
  step "Recreating Docker volumes"
  for dir in "$WORK/out/data/volumes"/*/; do
    [ -d "$dir" ] || continue
    name="$(basename "$dir")"
    if docker volume inspect "$name" >/dev/null 2>&1; then
      say "  $name exists already, left untouched"
      continue
    fi
    docker volume create "$name" >/dev/null
    docker run --rm -v "$name:/dst" -v "$dir:/src:ro" alpine:3 sh -c 'cp -a /src/. /dst/'
    say "  $name restored"
  done
fi

# ---- 6. start Islet and the stacks ----
step "Starting isletd"
systemctl start isletd
sleep 3
if [ -d "$DATA_DIR/stacks" ]; then
  step "Starting managed stacks"
  for dir in "$DATA_DIR/stacks"/*/; do
    [ -f "$dir/compose.yaml" ] || continue
    name="$(basename "$dir")"
    envf=""; [ -f "$dir/.env" ] && envf="--env-file $dir/.env"
    if docker compose -p "$name" -f "$dir/compose.yaml" $envf up -d >/dev/null 2>&1; then say "  $name up"; else say "  $name FAILED (see: docker compose -p $name -f $dir/compose.yaml up -d)"; fi
  done
fi

step "Done"
cat <<EOF
Database dumps are under $WORK/out/data/databases/<instance>/ and can be loaded from the Databases page (Restore into a new instance) or with the instance's Restore button once it runs.
Deployed apps keep their settings and releases; images were not in the backup, so press Deploy on each app.
Point your DNS records at this server's address, then open the panel on port 9443 with the same credentials as before.
Working files stay in $WORK until you remove them.
EOF
