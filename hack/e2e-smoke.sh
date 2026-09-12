#!/bin/sh
# End-to-end smoke test, run ON a freshly installed server as root:
# setup, login, catalog install, security report and a fix, firewall, a
# deploy from the sample repo, uptime check, backup plan run. Exits
# non-zero on the first failure and prints what it was.
#
#   sh hack/e2e-smoke.sh [https://127.0.0.1:9443]
set -eu
BASE="${1:-https://127.0.0.1:9443}"
JAR="$(mktemp)"; trap 'rm -f "$JAR"' EXIT
PASS="e2e password $(date +%s)"
say() { printf '\033[1m== %s\033[0m\n' "$*"; }
fail() { printf '\nFAIL: %s\n' "$*" >&2; exit 1; }
api() { # method path [json]
  if [ $# -ge 3 ]; then curl -sk -b "$JAR" -c "$JAR" -X "$1" -H 'Content-Type: application/json' -d "$3" "$BASE$2"; else curl -sk -b "$JAR" -c "$JAR" -X "$1" "$BASE$2"; fi
}
stream() { curl -sk -b "$JAR" -X POST -H 'Content-Type: application/json' -d "$3" "$BASE$2" | tr -d '\r'; }
has() { printf '%s' "$1" | grep -q "$2" || fail "$3: $(printf '%s' "$1" | head -c 300)"; }

say "waiting for isletd"
i=0; until curl -sk "$BASE/api/v1/health" >/dev/null 2>&1; do i=$((i+1)); [ "$i" -lt 60 ] || fail "panel did not answer on $BASE"; sleep 2; done
H="$(curl -sk "$BASE/api/v1/health")"; has "$H" '"version"' "health"

say "setup"
TOK="$(cat /var/lib/islet/setup-token 2>/dev/null || true)"
[ -n "$TOK" ] || fail "no setup token at /var/lib/islet/setup-token"
R="$(api POST /api/v1/setup "{\"token\":\"$TOK\",\"username\":\"admin\",\"password\":\"$PASS\"}")"; has "$R" '"admin"' "setup"
R="$(api POST /api/v1/auth/login "{\"username\":\"admin\",\"password\":\"$PASS\"}")"; has "$R" '"admin"' "login"

say "security report"
R="$(api GET /api/v1/security)"; has "$R" '"score"' "security state"; has "$R" '"linux":true' "linux detection"
printf 'score before: %s\n' "$(printf '%s' "$R" | sed -n 's/.*"score":\([0-9]*\).*/\1/p')"

say "firewall + fail2ban + auto-updates + swap + ntp"
R="$(api POST /api/v1/security/fix-all)"; printf '%s\n' "$R"; has "$R" '"fix"' "fix-all"
R="$(api GET /api/v1/security)"; has "$R" '"active":true' "ufw active after fix"
ufw status | head -n 12

say "catalog install (whoami)"
R="$(stream /api/v1/catalog/whoami/install '{"name":"whoami","fields":{},"domain":"","tls":""}')"; has "$R" 'done' "whoami install"
docker ps --format '{{.Names}} {{.Status}}' | grep whoami || fail "whoami container not running"

say "proxy"
R="$(api POST /api/v1/proxy/install '{"acmeEmail":""}')"; has "$R" '"installed":true' "proxy install"
IP="$(curl -fsS4 --max-time 5 https://api.ipify.org)"
R="$(api POST /api/v1/domains "{\"host\":\"whoami.$(printf '%s' "$IP" | tr . -).sslip.io\",\"targetType\":\"container\",\"target\":\"whoami-whoami-1\",\"port\":80,\"tls\":\"none\",\"enabled\":true}")"; has "$R" '"host"' "domain add"
sleep 4
curl -fsS -H "Host: whoami.$(printf '%s' "$IP" | tr . -).sslip.io" "http://127.0.0.1/" | head -n 2 || fail "route through the proxy"

say "deploy the Go sample from the Islet repository"
R="$(api POST /api/v1/apps '{"name":"sample","source":"git","repoUrl":"https://github.com/isletdev/islet","branch":"main","rootDir":"examples/go-service","tls":"none","strategy":"go","port":8080,"healthPath":"/health"}')"; has "$R" '"id"' "app create"
ID="$(printf '%s' "$R" | sed -n 's/.*"id":"\([a-f0-9]*\)".*/\1/p')"
R="$(stream "/api/v1/apps/$ID/deploy" '{}')"; printf '%s\n' "$R" | tail -n 5; has "$R" 'live in' "deploy"

say "uptime check + backup plan"
R="$(api POST /api/v1/uptime/checks '{"name":"panel","type":"http","target":"https://127.0.0.1:9443/api/v1/health","intervalSec":60,"timeoutSec":10,"enabled":true}')"; has "$R" '"id"' "uptime check"
R="$(api POST /api/v1/backups/destinations '{"name":"disk","type":"local","config":{"path":"/var/backups/islet-e2e"}}')"; has "$R" '"id"' "destination"
DID="$(printf '%s' "$R" | sed -n 's/.*"id":"\([a-f0-9]*\)".*/\1/p')"
R="$(api POST /api/v1/backups/plans "{\"name\":\"e2e\",\"destinationId\":\"$DID\",\"sources\":[{\"type\":\"islet\",\"value\":\"\"}],\"schedule\":\"0 3 * * *\",\"keepDaily\":2,\"keepWeekly\":0,\"keepMonthly\":0,\"keepYearly\":0,\"enabled\":true}")"; has "$R" '"id"' "plan"
PID="$(printf '%s' "$R" | sed -n 's/.*"id":"\([a-f0-9]*\)".*/\1/p')"
R="$(stream "/api/v1/backups/plans/$PID/run" '{}')"; has "$R" 'snapshot' "backup run"

say "host audit"
R="$(api POST /api/v1/security/host/audit '{"rkhunter":false}')"; has "$R" '"suid"' "host audit"

say "score after"
R="$(api GET /api/v1/security)"; printf 'score after: %s\n' "$(printf '%s' "$R" | sed -n 's/.*"score":\([0-9]*\).*/\1/p')"
say "all smoke checks passed"
