package cron

// Template is a starting point for a script job.
type Template struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Schedule    string `json:"schedule"`
	Script      string `json:"script"`
}

// Templates is the built-in library shown in the job editor.
var Templates = []Template{
	{
		ID: "pg-dump-s3", Name: "Postgres dump to S3", Description: "Dumps a Postgres container with pg_dump, gzips it, uploads with rclone and keeps 14 days.", Schedule: "0 3 * * *",
		Script: `#!/usr/bin/env bash
set -euo pipefail

CONTAINER="postgres"          # docker container name
DB="app"                      # database name
USER="postgres"
REMOTE="s3:my-bucket/backups" # rclone remote:path
KEEP_DAYS=14

STAMP=$(date +%Y%m%d-%H%M%S)
OUT="/tmp/${DB}-${STAMP}.sql.gz"

docker exec "$CONTAINER" pg_dump -U "$USER" "$DB" | gzip -6 > "$OUT"
rclone copy "$OUT" "$REMOTE/$DB/"
rm -f "$OUT"
rclone delete --min-age "${KEEP_DAYS}d" "$REMOTE/$DB/"
echo "backup ${STAMP} uploaded"
`,
	},
	{
		ID: "mysql-dump", Name: "MySQL dump to disk", Description: "Dumps every database from a MySQL or MariaDB container to a local folder, keeps 7 days.", Schedule: "30 3 * * *",
		Script: `#!/usr/bin/env bash
set -euo pipefail

CONTAINER="mysql"
DIR="/var/backups/mysql"
KEEP_DAYS=7

mkdir -p "$DIR"
docker exec "$CONTAINER" sh -c 'exec mysqldump --all-databases -uroot -p"$MYSQL_ROOT_PASSWORD"' | gzip -6 > "$DIR/all-$(date +%Y%m%d-%H%M%S).sql.gz"
find "$DIR" -name '*.sql.gz' -mtime +$KEEP_DAYS -delete
echo "dump written to $DIR"
`,
	},
	{
		ID: "docker-prune", Name: "Docker cleanup", Description: "Removes stopped containers, dangling images and unused build cache. Volumes are never touched.", Schedule: "0 4 * * 0",
		Script: `#!/usr/bin/env bash
set -euo pipefail
docker container prune -f
docker image prune -f
docker builder prune -f --keep-storage 2GB
docker system df
`,
	},
	{
		ID: "log-cleanup", Name: "Log cleanup", Description: "Vacuums the systemd journal and deletes rotated logs older than 14 days.", Schedule: "15 4 * * *",
		Script: `#!/usr/bin/env bash
set -euo pipefail
journalctl --vacuum-time=14d || true
find /var/log -type f \( -name '*.gz' -o -name '*.[0-9]' \) -mtime +14 -delete
df -h / | tail -1
`,
	},
	{
		ID: "cert-check", Name: "Certificate check", Description: "Fails when a public certificate expires within 10 days, so you get a notification.", Schedule: "0 8 * * *",
		Script: `#!/usr/bin/env bash
set -euo pipefail
HOSTS="example.com api.example.com"   # space separated
MIN_DAYS=10

status=0
for h in $HOSTS; do
  end=$(echo | openssl s_client -servername "$h" -connect "$h:443" 2>/dev/null | openssl x509 -noout -enddate | cut -d= -f2)
  left=$(( ( $(date -d "$end" +%s) - $(date +%s) ) / 86400 ))
  echo "$h expires in $left days"
  if [ "$left" -lt "$MIN_DAYS" ]; then status=1; fi
done
exit $status
`,
	},
	{
		ID: "rclone-sync", Name: "Folder sync with rclone", Description: "Mirrors a folder to any rclone remote (S3, B2, Google Drive, SFTP).", Schedule: "0 */6 * * *",
		Script: `#!/usr/bin/env bash
set -euo pipefail
SRC="/srv/data"
DEST="remote:bucket/data"
rclone sync "$SRC" "$DEST" --fast-list --transfers 8 --stats-one-line -v
`,
	},
	{
		ID: "apt-security", Name: "Security updates", Description: "Installs pending security updates on Debian and Ubuntu without rebooting.", Schedule: "0 5 * * *",
		Script: `#!/usr/bin/env bash
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get -y -o Dpkg::Options::="--force-confdef" -o Dpkg::Options::="--force-confold" upgrade
[ -f /var/run/reboot-required ] && echo "reboot required" || echo "no reboot needed"
`,
	},
}
