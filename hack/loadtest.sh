#!/bin/sh
# Load test: does the panel stay responsive with many containers?
#
# Starts N throwaway containers, then times the endpoints the UI polls and
# reports p50/p95 latencies. Meant for a small VPS (1 vCPU, 2 GB); it also
# runs anywhere with Docker. Cleans up its own containers.
#
# Usage: hack/loadtest.sh [-n 30] [-u https://127.0.0.1:9443] [-k] USER PASSWORD
set -eu

N=30; BASE="https://127.0.0.1:9443"; INSECURE=""
while getopts "n:u:k" opt; do
  case "$opt" in
    n) N="$OPTARG" ;;
    u) BASE="$OPTARG" ;;
    k) INSECURE="-k" ;;
    *) exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[ $# -eq 2 ] || { echo "usage: $0 [-n N] [-u BASE] [-k] USER PASSWORD" >&2; exit 2; }
USER="$1"; PASS="$2"
JAR="$(mktemp)"; trap 'rm -f "$JAR"; cleanup' EXIT

cleanup() {
  ids="$(docker ps -aq --filter label=islet.loadtest 2>/dev/null || true)"
  [ -n "$ids" ] && docker rm -f $ids >/dev/null 2>&1 || true
}

echo "== login"
curl -s $INSECURE -c "$JAR" -H 'Content-Type: application/json' -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" "$BASE/api/v1/auth/login" >/dev/null

echo "== starting $N containers"
i=0
while [ "$i" -lt "$N" ]; do
  docker run -d --rm --label islet.loadtest=1 --name "islet-lt-$i" --memory 32m traefik/whoami:v1.10 >/dev/null
  i=$((i + 1))
done
sleep 5

stats() { # name path [n]
  name="$1"; path="$2"; n="${3:-20}"
  times=""
  j=0; bad=0
  while [ "$j" -lt "$n" ]; do
    out="$(curl -s $INSECURE -b "$JAR" -o /dev/null -w '%{http_code} %{time_total}' "$BASE$path")"
    code="${out%% *}"; t="${out#* }"
    [ "$code" = "200" ] || bad=$((bad + 1))
    times="$times $t"
    j=$((j + 1))
  done
  [ "$bad" = 0 ] || printf '%-28s %d of %d requests did not return 200\n' "$name" "$bad" "$n"
  sorted="$(printf '%s\n' $times | sort -n)"
  p50="$(printf '%s\n' "$sorted" | awk -v n="$n" 'NR==int(n*0.5)+1')"
  p95="$(printf '%s\n' "$sorted" | awk -v n="$n" 'NR==int(n*0.95)')"
  printf '%-28s p50 %6.0f ms   p95 %6.0f ms\n' "$name" "$(awk -v v="$p50" 'BEGIN{print v*1000}')" "$(awk -v v="$p95" 'BEGIN{print v*1000}')"
}

echo "== latencies with $N extra containers (${N}x whoami)"
stats "health"            "/api/v1/health"
stats "metrics latest"    "/api/v1/metrics/latest"
stats "containers list"   "/api/v1/docker/containers"
stats "stacks"            "/api/v1/docker/stacks" 10
stats "images"            "/api/v1/docker/images" 10
stats "apps"              "/api/v1/apps"
stats "domains"           "/api/v1/domains"
stats "events"            "/api/v1/notify/events?limit=50"
stats "attention"         "/api/v1/attention"

echo "== daemon"
curl -s $INSECURE -b "$JAR" "$BASE/api/v1/health" | tr ',' '\n' | grep -E 'version|goroutines|rss' || true
echo "Target: every p95 under 500 ms on a 1 vCPU / 2 GB box."
