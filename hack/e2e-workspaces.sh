#!/bin/sh
# The whole point of the feature, tested on Linux where it actually runs.
#
# Three promises, each of which has been broken at least once:
#   1. work survives the daemon being restarted underneath it
#   2. several agents in one workspace, each in a conversation of its own
#   3. an agent set to resume comes back into that same conversation
#
# isletd is built for linux/amd64 and run in a container with tmux. Claude Code
# is not installed here and does not need to be: a stub named `claude` records
# the argv it was given and then sleeps, which is what lets this assert the
# difference between --session-id and --resume without a network or an account.
# The flags are the contract; what Anthropic's binary does with them is not this
# test's business.
#
# On counting processes: `pgrep` matches the shell running it, and it also
# matches zombies — this container has no init to reap children, so a killed
# process lingers as a defunct entry and a naive check reads it as alive. Every
# check here asks ps for the state column and drops anything in Z.
set -u

IMG=islet-ws-test
SOCK=/srv/data/tmux.sock
docker rm -f $IMG >/dev/null 2>&1 || true

docker run -d --name $IMG -w /srv debian:12-slim tail -f /dev/null >/dev/null
docker exec $IMG sh -c 'apt-get update -qq && apt-get install -y -qq tmux procps curl >/dev/null 2>&1'
docker exec $IMG sh -c 'mkdir -p /srv/project /srv/data'
docker cp "$1" $IMG:/usr/local/bin/isletd
docker exec $IMG chmod +x /usr/local/bin/isletd

# The stub. It appends its whole command line to a log, then sleeps so the
# window stays busy and `running` is true the way a real agent's would be.
docker exec $IMG sh -c 'printf "#!/bin/sh\necho \"\$@\" >> /srv/claude-argv.log\nexec sleep 6000\n" > /usr/local/bin/claude && chmod +x /usr/local/bin/claude'

alive() { # alive <name> -> YES when a non-zombie process of that name exists
  docker exec $IMG sh -c "ps -C $1 -o stat= 2>/dev/null | grep -v Z | grep -q . && echo YES || echo NO"
}
count() { # count <name> -> how many non-zombie processes of that name
  docker exec $IMG sh -c "ps -C $1 -o stat= 2>/dev/null | grep -v Z | grep -c . || true"
}
tmuxls() { docker exec $IMG tmux -S $SOCK ls 2>&1; }

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

echo "== creating a workspace with two agents in it"
api POST /api/v1/workspaces \
  '{"name":"proj","directory":"/srv/project","preset":"shell","command":"","mcpEnabled":true}' >/dev/null
ID=$(api GET /api/v1/workspaces | sed 's/.*"id":"\([a-f0-9]*\)".*/\1/')
api POST "/api/v1/workspaces/$ID/agents" '{"name":"worker","preset":"claude","command":"claude","resume":true}' >/dev/null
api POST "/api/v1/workspaces/$ID/agents" '{"name":"tester","preset":"claude","command":"claude","resume":true}' >/dev/null
echo "   workspace=$ID"
api GET "/api/v1/workspaces/$ID/agents" | tr '}' '\n' | grep -o '"name":"[a-z]*"' | sed 's/^/   /'

echo
echo "== each agent gets its own conversation"
api GET "/api/v1/workspaces/$ID/agents" | tr '}' '\n' | grep -o '"sessionUuid":"[^"]*"' | sort -u | sed 's/^/   /'
echo "   distinct: $(api GET "/api/v1/workspaces/$ID/agents" | tr '}' '\n' | grep -o '"sessionUuid":"[^"]*"' | sort -u | grep -c .)  (want 2)"

echo
echo "== starting both"
for A in $(api GET "/api/v1/workspaces/$ID/agents" | tr '}' '\n' | grep -o '"id":"[a-f0-9]*"' | cut -d'"' -f4); do
  api POST "/api/v1/workspaces/$ID/agents/$A/start" '{}' >/dev/null
done
sleep 3
echo "   claude processes: $(count sleep)   (want 2, one per agent)"
tmuxls | sed 's/^/   /'
docker exec $IMG tmux -S $SOCK list-windows -t "islet-ws-$ID" -F '   window #{window_index}: #{window_name}' 2>&1

echo
echo "== the first run names the conversation, it does not pick one"
docker exec $IMG sed 's/^/   /' /srv/claude-argv.log
echo "   --session-id used: $(docker exec $IMG grep -c -- --session-id /srv/claude-argv.log)  (want 2)"
echo "   --continue  used: $(docker exec $IMG grep -c -- --continue /srv/claude-argv.log)  (want 0 — it cannot tell two agents in one directory apart)"

echo
echo "== the socket is not in the daemon's private /tmp"
docker exec $IMG sh -c "test -S $SOCK && echo '   socket at $SOCK' || echo '   *** NO SOCKET AT $SOCK ***'"
docker exec $IMG sh -c "tmux ls 2>&1 | head -1 | sed 's/^/   default socket: /'"

echo
echo "== killing the daemon. This is the test."
docker exec $IMG pkill -x isletd
sleep 3
echo "   isletd alive: $(alive isletd)   (want NO)"
echo "   agents alive: $(count sleep)   (want 2, the work outlived the daemon)"
tmuxls | sed 's/^/   /'

echo
echo "== starting the daemon again: it finds the sessions on the same socket"
start_daemon
api POST /api/v1/auth/login '{"username":"lev","password":"a strong password 123"}' >/dev/null
api GET "/api/v1/workspaces/$ID/agents" | tr '}' '\n' | grep -oE '"name":"[a-z]*"|"running":(true|false)' | sed 's/^/   /'

echo
echo "== reboot: tmux is gone, and the agents set to resume come back"
docker exec $IMG tmux -S $SOCK kill-server 2>/dev/null
docker exec $IMG pkill -x isletd
sleep 3
echo "   agents alive before restart: $(count sleep)   (want 0, tmux took them with it)"
docker exec $IMG sh -c ': > /srv/claude-argv.log'
start_daemon
sleep 5
echo "   agents alive after restart:  $(count sleep)   (want 2, resumed)"
echo "   what they were started with:"
docker exec $IMG sed 's/^/     /' /srv/claude-argv.log
echo "   --resume used: $(docker exec $IMG grep -c -- --resume /srv/claude-argv.log)  (want 2 — the same conversations, not new ones)"
echo "   --session-id used: $(docker exec $IMG grep -c -- --session-id /srv/claude-argv.log)  (want 0 — those conversations already exist)"

echo
echo "== an agent that was never started is not started by a reboot"
api POST /api/v1/auth/login '{"username":"lev","password":"a strong password 123"}' >/dev/null
api POST "/api/v1/workspaces/$ID/agents" '{"name":"idle","preset":"claude","command":"claude","resume":true}' >/dev/null
docker exec $IMG tmux -S $SOCK kill-server 2>/dev/null
docker exec $IMG pkill -x isletd
sleep 2
docker exec $IMG sh -c ': > /srv/claude-argv.log'
start_daemon
sleep 5
echo "   agents alive: $(count sleep)   (want 2, not 3 — 'idle' has no conversation to resume)"

echo
echo "== scopes on an agent's attach socket"
docker exec $IMG sh -c "curl -s -b /srv/c -c /srv/c -X POST -H 'Content-Type: application/json' -d '{\"name\":\"ro\",\"scopes\":\"read\"}' http://127.0.0.1:9443/api/v1/auth/tokens > /srv/ro.json"
docker exec $IMG sh -c "curl -s -b /srv/c -c /srv/c -X POST -H 'Content-Type: application/json' -d '{\"name\":\"sh\",\"scopes\":\"read,shell\"}' http://127.0.0.1:9443/api/v1/auth/tokens > /srv/sh.json"
AG=$(api GET "/api/v1/workspaces/$ID/agents" | tr '}' '\n' | grep -o '"id":"[a-f0-9]*"' | head -1 | cut -d'"' -f4)
docker exec $IMG sh -c "RO=\$(sed 's/.*\"token\":\"\([^\"]*\)\".*/\1/' /srv/ro.json); SH=\$(sed 's/.*\"token\":\"\([^\"]*\)\".*/\1/' /srv/sh.json); \
  curl -s -o /dev/null -w '   read scope  on agent attach -> %{http_code}  (must not be 426)\n' -H \"Authorization: Bearer \$RO\" http://127.0.0.1:9443/api/v1/workspaces/$ID/agents/$AG/attach; \
  curl -s -o /dev/null -w '   shell scope on agent attach -> %{http_code}  (426 = reached the upgrade)\n' -H \"Authorization: Bearer \$SH\" http://127.0.0.1:9443/api/v1/workspaces/$ID/agents/$AG/attach"

echo
echo "== the MCP credential is not in the project"
docker exec $IMG sh -c "stat -c '   mode=%a owner=%U path=%n' /srv/data/workspaces/$ID/mcp.json"
docker exec $IMG sh -c "test -e /srv/project/.mcp.json && echo '   *** A TOKEN WAS WRITTEN INTO THE PROJECT ***' || echo '   nothing written into the project'"

docker rm -f $IMG >/dev/null 2>&1 || true
