# Workspaces

A workspace is a directory and a session that keeps running when you close the
tab. Inside it are agents: one tmux window each, one conversation each, as many
as the work needs — a worker and a tester against the same checkout is the
ordinary case, not a trick.

It exists for one shape of work: running an agent — Claude Code, or anything
else — on the server it is changing, rather than on a laptop that has to stay
awake. Make a change, look at the live service, come back hours later and carry
on where it was.

## What keeps it alive

tmux. An agent runs as a child of tmux rather than of the Islet daemon, so it
survives:

- closing the browser, or navigating to another page
- a dropped connection, a sleeping laptop, a changed network
- `islet update`, which restarts the daemon underneath it

The third one was not true before v0.10.0, and the way it failed is worth
keeping. `islet update` ends at `systemctl restart isletd`, and a systemd unit
with no `KillMode` set kills **every process in its control group**, not just the
daemon — tmux included, because isletd started it. Daemonizing is no defence:
systemd kills by cgroup, not by process tree. The unit now sets
`KillMode=process`, and a daemon that finds an older unit on disk repairs it with
a drop-in, because an update replaces the binary and never the unit.

The tmux server also has a socket of its own at `<data dir>/tmux.sock` rather
than tmux's default under `/tmp`. The unit sets `PrivateTmp=yes`, so the daemon's
`/tmp` is a namespace of its own and a fresh one on every restart: sessions on
the default socket were unreachable after a restart, and were never reachable
from an SSH shell at all.

A reboot is different — nothing that lives only in memory survives one. Islet
recreates each workspace's session in the right directory and then starts the
agents that were **set to resume and had been started before**, back in the
conversations they were in. An agent that has never run is not started, and an
agent with resume switched off comes back to a prompt. v0.9.0 re-ran nothing at
all, on the reasoning that an agent resuming mid-task with nobody watching is not
something to do on your behalf; that reasoning survives as the switch, asked once
per agent instead of decided for everyone.

Because it is ordinary tmux, Islet is not the only way back in:

```
ssh you@server
tmux -S /var/lib/islet/tmux.sock ls
tmux -S /var/lib/islet/tmux.sock attach -t islet-ws-<id>
```

That matters. A panel that is the only route to your own work is a panel you
cannot afford to have go down.

## Resuming the same conversation

Each Claude Code agent owns a conversation, identified by a UUID stored with the
agent. The first start passes `--session-id <uuid>`, which names it; every start
afterwards passes `--resume <uuid>`, which reopens that exact one.

`--continue` is deliberately not used. It means "the most recent conversation in
this directory", and agents in one workspace share a directory — so two agents
resuming would both land in whichever was touched last, and the other would be
lost with nothing anywhere reporting it.

## Presets

| Preset | What it runs |
|---|---|
| Claude Code | `claude` in the directory. Sign in once inside the session; it stays signed in. |
| A shell | Nothing. A prompt that stays where you left it — good for a long build or a migration. |
| Something else | Any single-line command. |

The command is typed into the session with `send-keys`, not baked into it, so it
lands in the scrollback exactly as if you had typed it: visible to anyone who
attaches later, and repeatable with the up arrow.

## Letting the agent ask Islet about the server

With **Let it ask Islet about this server** on, the workspace gets its own API
token and a configuration file pointing at this panel's MCP endpoint. The agent
can then use Islet's own tools — read a container's logs, restart a container,
trigger a deploy, run a job, check uptime, send you a message — instead of
working it out as root.

The token is scoped to `read, logs, containers, cron, notify` and, in
particular, **not** `shell`. A token that could open a terminal would be a way
around every check on this page.

The configuration is written to `<data dir>/workspaces/<id>/mcp.json`, mode
`0600`, and the Claude preset is launched with `--mcp-config <that path>`.

> It is deliberately **not** written into your project. Claude Code also reads
> `.mcp.json` from a repository root, and a bearer token sitting in a repository
> root is one `git add .` away from being published.

Your own MCP servers keep working: Islet's is added alongside them, not instead
of them. Deleting a workspace revokes its token.

## What this gives an agent

A persistent shell on your server, as the user the daemon runs as. That is the
feature, and it is worth saying plainly rather than burying.

- Workspaces are **admin only**. A viewer, or any project-scoped account, cannot
  see or open one.
- Every attach is written to the audit log, with the address it came from.
- Every tmux command goes through the command runner, so it appears in the
  command log too.
- The MCP token is narrower than the shell the agent already has, so it is not
  the risk — it is an audited, revocable path for things the agent would
  otherwise do as root.

## Signing in to Claude Code

Press **Run claude** and sign in when it asks. Claude prints a link; open it on
any device, approve, and paste the code back. A subscription signs in exactly as
it does on a laptop, and stays signed in afterwards.

`claude setup-token` is the other route if you would rather hold a long-lived
token than a browser session.

## Requirements

tmux, and Claude Code for that preset. The page offers to install either if it
is missing — tmux through the system package manager, Claude Code through
Anthropic's installer with npm as a fallback.

Claude Code installs to `~/.local/bin`, which a non-login shell started by tmux
often does not have on its PATH. Islet records where it found the binary and
runs it by that path, so "command not found" cannot happen because of it.

## Copy and paste in the terminal

`Ctrl+C` copies **when there is a selection**, and interrupts when there is not
— taking interrupt away would leave no way to stop a running command, which
matters most in the sessions worth leaving open. The selection is cleared after
a copy, so the next `Ctrl+C` interrupts again. `Ctrl+V` pastes. On macOS `Cmd+C`
and `Cmd+V` do both, where nothing conflicts. `Ctrl+Shift+C` and `Ctrl+Shift+V`
work too.

Right-click gives Copy, Paste, Select all and Clear rather than the browser's
own menu, which offers Back and View Source and nothing a terminal can use.

Reading and writing the clipboard needs a secure context, so on a panel served
over plain HTTP the browser refuses and the terminal says so.

## Limits in this version

No split panes inside an agent's window. No session recording or playback, and no
sharing a session with another person. Agents run on the host, not in a
container. An agent's conversation is resumed by id, so moving a workspace to a
different directory leaves those conversations behind.
