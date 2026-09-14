# Workspaces

A workspace is a directory, a command, and a session that keeps running when you
close the tab.

It exists for one shape of work: running an agent — Claude Code, or anything
else — on the server it is changing, rather than on a laptop that has to stay
awake. Make a change, look at the live service, come back hours later and carry
on where it was.

## What keeps it alive

tmux. The command runs as a child of tmux rather than of the Islet daemon, so it
survives:

- closing the browser, or navigating to another page
- a dropped connection, a sleeping laptop, a changed network
- `islet update`, which restarts the daemon underneath it

It does not survive a reboot, because nothing that lives only in memory does.
After a reboot Islet recreates each workspace's session, in the right directory,
**at a shell prompt** — and deliberately does not re-run the command. An agent
resuming by itself, mid-task, with nobody watching, is not something to start on
your behalf. Open the workspace and press Run.

Because it is ordinary tmux, Islet is not the only way back in:

```
ssh you@server
tmux ls
tmux attach -t islet-ws-<id>
```

That matters. A panel that is the only route to your own work is a panel you
cannot afford to have go down.

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

One window per workspace: no split panes. No session recording or playback, and
no sharing a session with another person. Agents run on the host, not in a
container.
