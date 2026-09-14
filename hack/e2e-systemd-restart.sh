#!/bin/sh
# The deployed shape, which is the one nothing else tests.
#
# hack/e2e-workspaces.sh runs isletd as a bare process and kills it with pkill.
# That is not how it runs on a server: it is a systemd unit, and `islet update`
# ends at `systemctl restart isletd`. Two things in the unit decide whether a
# workspace survives that, and both have been wrong at least once.
#
#   where the socket lives
#       tmux's default is /tmp/tmux-<uid>/default. With PrivateTmp=yes the unit
#       gets a /tmp of its own, and systemd destroys it and builds another on
#       every restart — so the socket disappears, the restarted daemon finds
#       nothing, and a fresh empty session is created in place of the one that
#       was running. This is what lost the session before v0.10.0. Note that the
#       old process may well still be alive: the work is not killed so much as
#       made unreachable, which looks identical from the panel.
#
#   PrivateTmp
#       Moving the socket out of /tmp fixes reachability and exposes the next
#       layer. A session that now survives the restart is still holding the
#       destroyed /tmp, so anything inside it that touches /tmp fails with
#       ENOENT on a path that plainly exists — `claude` does, on startup.
#
# KillMode matters too (the default, control-group, SIGTERMs and then SIGKILLs
# every process in the unit's cgroup), but it is not what this probe pins down:
# a tmux server does not always go down inside the stop timeout, so the outcome
# is timing-dependent in a way the two above are not. KillMode=process removes
# the question rather than winning the race.
#
# No isletd here on purpose: the question is about systemd and tmux, and adding
# the daemon would only add ways to be wrong.
#
#   sh hack/e2e-systemd-restart.sh
set -u

IMG=islet-systemd-probe
docker rm -f $IMG >/dev/null 2>&1 || true

docker run -d --name $IMG --privileged --cgroupns=host \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw jrei/systemd-debian:12 >/dev/null 2>&1 || {
  echo "could not start a systemd container (needs --privileged)"; exit 1; }
sleep 6
docker exec $IMG sh -c 'apt-get update -qq && apt-get install -y -qq tmux procps >/dev/null 2>&1'

# probe <socket> <private-tmp> <kill-mode>
#
# Starts a session, notes when it was created, restarts the unit, and asks the
# restarted unit — in its own namespace, as a restarted isletd would — whether
# the session it finds is the same one, and whether /tmp still works inside it.
probe() {
  SOCK=$1 PT=$2 KM=$3
  # A tmux server is called "tmux: server", so pkill -x tmux misses it and the
  # next probe reconnects to the previous one on the same path — which reports
  # the previous probe.s namespace and quietly invents a result.
  docker exec $IMG sh -c "systemctl stop probe 2>/dev/null; tmux -S $SOCK kill-server 2>/dev/null; pkill -f 'tmux.*$SOCK' 2>/dev/null; rm -f $SOCK; true"
  docker exec $IMG sh -c "cat > /etc/systemd/system/probe.service <<EOF
[Service]
Type=simple
PrivateTmp=$PT
KillMode=$KM
ExecStart=/bin/sh -c \"tmux -S $SOCK new-session -d -s s; sleep infinity\"
EOF
systemctl daemon-reload && systemctl start probe"
  sleep 3
  BEFORE=$(docker exec $IMG sh -c "PID=\$(systemctl show probe -p MainPID --value); nsenter -t \$PID -m tmux -S $SOCK display-message -p '#{session_created}' 2>/dev/null")

  docker exec $IMG systemctl restart probe
  sleep 5

  AFTER=$(docker exec $IMG sh -c "PID=\$(systemctl show probe -p MainPID --value); nsenter -t \$PID -m tmux -S $SOCK display-message -p '#{session_created}' 2>/dev/null")
  if [ -z "$AFTER" ]; then
    echo "   socket=$SOCK PrivateTmp=$PT KillMode=$KM -> NO SESSION at all"
    return
  fi
  if [ "$BEFORE" != "$AFTER" ]; then
    echo "   socket=$SOCK PrivateTmp=$PT KillMode=$KM -> a DIFFERENT session (the work was lost)"
    return
  fi
  docker exec $IMG sh -c "PID=\$(systemctl show probe -p MainPID --value); nsenter -t \$PID -m tmux -S $SOCK send-keys -t s 'mkdir -p /tmp/probe && echo TMP-OK || echo TMP-GONE' Enter"
  sleep 2
  R=$(docker exec $IMG sh -c "PID=\$(systemctl show probe -p MainPID --value); nsenter -t \$PID -m tmux -S $SOCK capture-pane -p -t s" | grep -oE 'TMP-(OK|GONE)' | tail -1)
  echo "   socket=$SOCK PrivateTmp=$PT KillMode=$KM -> the SAME session, /tmp $R"
}

echo "== before v0.10.0: the socket lived in the unit's private /tmp"
probe /tmp/ws.sock yes control-group   # want: a DIFFERENT session

echo
echo "== v0.10.0 moved the socket out, and kept PrivateTmp"
probe /run/ws2.sock yes process         # want: the SAME session, TMP-GONE

echo
echo "== what the unit ships now"
probe /run/ws3.sock no process          # want: the SAME session, TMP-OK

docker rm -f $IMG >/dev/null 2>&1 || true
