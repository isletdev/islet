#!/bin/sh
# The whole point of the feature, tested on Linux where it actually runs:
# does the work survive the daemon being restarted underneath it?
#
# isletd is built for linux/amd64 and run inside a container with tmux. A
# workspace is created through the API, a long-running process is started in it,
# isletd is killed and started again, and the process has to still be there.
#
# On counting processes: `pgrep` matches the shell running it, and it also
# matches zombies — this container has no init to reap children, so a killed
# `sleep` lingers as a defunct entry and a naive check reads it as alive. Every
# check here asks ps for the state column and drops anything in Z.
set -u

IMG=islet-ws-test
docker rm -f $IMG >/dev/null 2>&1 || true

docker run -d --name $IMG -w /srv debian:12-slim tail -f /dev/null >/dev/null
docker exec $IMG sh -c 'apt-get update -qq && apt-get install -y -qq tmux procps curl >/dev/null 2>&1'
docker exec $IMG sh -c 'mkdir -p /srv/project /srv/data'
docker cp "$1" $IMG:/usr/local/bin/isletd
docker exec $IMG chmod +x /usr/local/bin/isletd

# alive <name> -> YES when a non-zombie process of that name exists.
alive() {
  docker exec $IMG sh -c "ps -C $1 -o stat= 2>/dev/null | grep -v Z | grep -q . && echo YES || echo NO"
}

start_daemon() {
  docker exec -d $IMG sh -c \
    'ISLET_DATA_DIR=/srv/data ISLET_LISTEN=127.0.0.1:9443 ISLET_TLS=off exec /usr/local/bin/isletd >>/srv/daemon.log 2>&1'
  sleep 4
}

api() { # method path [body]
  if [ $# -ge 3 ]; then
    docker exec $IMG curl -s -b /srv/c -c /srv/c -X "$1" -H 'Content-Type: application/json' -d "$3" "http://127.0.0.1:9443$2"
  else
    docker exec $IMG curl -s -b /srv/c -c /srv/c -X "$1" "http://127.0.0.1:9443$2"
  fi
}

echo "== starting the daemon"
start_daemon
TOKEN=$(docker exec $IMG cat /srv/data/setup-token)
api POST /api/v1/setup "{\"token\":\"$TOKEN\",\"username\":\"lev\",\"password\":\"a strong password 123\"}" >/dev/null

echo "== tmux detected:"
api GET /api/v1/workspaces | head -c 120; echo

echo "== creating a workspace"
api POST /api/v1/workspaces \
  '{"name":"proj","directory":"/srv/project","preset":"custom","command":"sleep 6000","mcpEnabled":true}' >/dev/null
ID=$(api GET /api/v1/workspaces | sed 's/.*"id":"\([a-f0-9]*\)".*/\1/')
echo "   id=$ID"

echo "== starting the command in it"
api POST "/api/v1/workspaces/$ID/start" '{}' >/dev/null
sleep 2
echo "   sleep alive: $(alive sleep)   (want YES)"

echo "== the MCP credential"
docker exec $IMG sh -c "stat -c '   mode=%a owner=%U path=%n' /srv/data/workspaces/$ID/mcp.json"
docker exec $IMG sh -c "grep -o '\"url\":\"[^\"]*\"' /srv/data/workspaces/$ID/mcp.json | sed 's/^/   /'"
docker exec $IMG sh -c "test -e /srv/project/.mcp.json && echo '   *** A TOKEN WAS WRITTEN INTO THE PROJECT ***' || echo '   nothing written into the project'"

echo
echo "== killing the daemon. This is the test."
docker exec $IMG pkill -x isletd
sleep 3
echo "   isletd alive: $(alive isletd)   (want NO)"
docker exec $IMG tmux ls 2>&1 | sed 's/^/   /'
echo "   sleep alive:  $(alive sleep)   (want YES, the work outlived the daemon)"

echo
echo "== starting the daemon again"
start_daemon
api POST /api/v1/auth/login '{"username":"lev","password":"a strong password 123"}' >/dev/null
api GET /api/v1/workspaces | tr ',' '\n' | grep -E '"name"|"running"' | sed 's/^/   /'

echo
echo "== the history the session kept while nobody was watching:"
api GET "/api/v1/workspaces/$ID/history" | head -c 160 | sed 's/^/   /'; echo

echo
echo "== scopes on the attach socket"
docker exec $IMG sh -c "curl -s -b /srv/c -c /srv/c -X POST -H 'Content-Type: application/json' -d '{\"name\":\"ro\",\"scopes\":\"read\"}' http://127.0.0.1:9443/api/v1/auth/tokens > /srv/ro.json"
docker exec $IMG sh -c "curl -s -b /srv/c -c /srv/c -X POST -H 'Content-Type: application/json' -d '{\"name\":\"sh\",\"scopes\":\"read,shell\"}' http://127.0.0.1:9443/api/v1/auth/tokens > /srv/sh.json"
docker exec $IMG sh -c "RO=\$(sed 's/.*\"token\":\"\([^\"]*\)\".*/\1/' /srv/ro.json); SH=\$(sed 's/.*\"token\":\"\([^\"]*\)\".*/\1/' /srv/sh.json); \
  curl -s -o /dev/null -w '   read scope  on attach -> %{http_code}  (must not be 426)\n' -H \"Authorization: Bearer \$RO\" http://127.0.0.1:9443/api/v1/workspaces/$ID/attach; \
  curl -s -o /dev/null -w '   shell scope on attach -> %{http_code}  (426 = reached the upgrade)\n' -H \"Authorization: Bearer \$SH\" http://127.0.0.1:9443/api/v1/workspaces/$ID/attach"

echo
echo "== reboot: the session is gone, the daemon restarts"
docker exec $IMG tmux kill-server 2>/dev/null
docker exec $IMG pkill -x isletd
sleep 3
echo "   sleep alive before restart: $(alive sleep)   (want NO, tmux took it with it)"
start_daemon
sleep 3
docker exec $IMG tmux ls 2>&1 | sed 's/^/   /'
echo "   sleep alive after restart:  $(alive sleep)   (want NO, the command must not be re-run)"

docker rm -f $IMG >/dev/null 2>&1 || true
