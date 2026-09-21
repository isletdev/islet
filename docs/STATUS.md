# Status

**Head:** `fab7cb7`, tagged `v0.30.1`, 2026-09-21. 230 commits, 94 tags, CI green on `main`.

The installed daemon on the development server is running this release, updated
through the official GitHub channel rather than from the working tree — which is
the only way the update path gets tested at all.

## What this file is

The other documents in `docs/` each answer one question, and none of them answers
this one:

| Document | Answers |
|---|---|
| `ROADMAP.md` | what was planned, phase by phase |
| `DECISIONS.md` | why a non-obvious call was made, and what it cost |
| `STRUCTURE.md` | where code lives and the conventions it follows |
| `NEEDED_FROM_YOU.md` | what only the maintainer can unblock |
| `WORKSPACES.md`, `SQL_CLIENT.md` | one feature each, in depth |
| **this file** | **where the work actually stands, and what to pick up next** |

It exists so a fresh environment — a new machine, or a workspace running on the
server — can start without the history that produced the current state. Every
claim below was checked against the repository or a live endpoint on the date in
the heading; where something could not be checked, it says so.

## Where the project stands

Phases 0 through 6 of `ROADMAP.md` are substantially built: 191 of its items are
ticked and five remain open, all five waiting on something outside the code.

What the roadmap does not describe is the nineteen releases after it. From v0.6.0
onward the work stopped coming from the plan and started coming from a real
server — a live fleet migrated off Nginx Proxy Manager, and then the need to run
an agent next to the things it changes. That is the more interesting half of the
recent history, because it is the half that was found rather than designed.

## Shipped since the plan ran out — v0.6.0 to v0.30.1

| Tag | Commit | What it was |
|---|---|---|
| v0.6.0 | `f0aaa4e` | Apps and catalog: install, find and remove an app where it is listed; sortable tables that sort sizes as sizes; a DNS check that stops calling a CDN broken; the file editor follows the page and takes its colours from the theme tokens |
| v0.7.0 | `ffa9adb` | Custom locations per domain, and the DNS-01 challenge as a stored choice rather than a consequence of a hostname — so a plain host behind a CDN can be issued at all. The importer carries locations across |
| v0.7.1 | `526bbd9` | Certificates fail loudly: `acme.json` repaired when its mode is one Traefik refuses, an install with no email refused instead of quietly answering 200, and a Diagnose that reads Islet's own config and Traefik's log and says why there are no certificates |
| v0.7.2 | `c5d7ae6` | The cause under both of the above: the install handler decoded its body only `if r.ContentLength > 0`, and a chunked request — which is what arrives through a reverse proxy — reports −1. Every field arrived empty for exactly the users who had put the panel on a domain |
| v0.8.0 | `9afa4b5` | The visitor's hostname is passed to every target type, matching what nginx and NPM already did — the real reason WebSockets broke after an import. Plus a block-common-exploits router answering 403 above every other router on the host |
| v0.8.1 | `86a0bb6` | Traefik reports `wss` in `X-Forwarded-Proto` on an upgrade; Plug.SSL and its equivalents accept only `https`, so the app answered the handshake with a 301 to the URL it had just been asked for |
| v0.9.0 | `a2aa5e1` | **Workspaces.** A directory, a command and a tmux session, so work survives `islet update`, a closed tab and a dropped connection. Optional scoped MCP wiring for an agent. Two fixes in the shared PTY transport: a 25s keepalive, and a `TermView` split so reconnecting no longer wipes the scrollback |
| v0.9.1 | `698f342` | Open scrolls the terminal into view — it was below the cards and off the bottom of the screen |
| v0.9.2 | `44ac097` | Claude Code detected, installed from the panel, and launched by absolute path (the installer puts it in `~/.local/bin`, which a tmux shell often lacks). Copy and paste, and a terminal-shaped right-click menu, on every terminal in the panel |
| v0.9.3 | `4999a3c` | Users follow the selected server, so a fleet member's own accounts can be managed. And the login page says why a panel at one registrable domain can never authenticate a site at another, instead of silently landing on the dashboard |
| v0.9.4 | `95bd5a2` | Ctrl+V stopped pasting twice (xterm already pastes); the domains table reworked; a checkbox for `--dangerously-skip-permissions` |
| v0.9.5 | `cc33b70` | The `islet` CLI never existed on an installed server: goreleaser builds only `isletd`, and the installer symlinked `islet` to it with no argv[0] dispatch, so `islet update` started a second daemon and died on "address already in use". The symlink is true now |
| v0.10.0 | `19e5e1f` | Several agents per workspace, each a tmux window with a conversation of its own, resumed by session id rather than by `--continue`. And the reason `islet update` had been killing the sessions workspaces exist to protect: systemd's default KillMode takes the whole control group, and the tmux socket was inside the daemon's private `/tmp` |
| v0.10.1, v0.11.1 | `b840722` | `PrivateTmp=yes` gives the unit a `/tmp` that systemd destroys and rebuilds on every restart, so a session that survived the restart — the point of `KillMode=process` — was left holding a mount whose backing directory was gone, and anything in it touching `/tmp` failed with ENOENT on a path that plainly existed. Found by an agent losing its own tooling mid-release. Both tags point at this one commit |
| v0.11.0 | `a6d1e9a` | Two score checks measuring the wrong thing. `panel-2fa` counted `islet-controller`, the token-only account a fleet adoption creates, which no one can sign in to and therefore no one can enrol a second factor for: ten points, permanently, with no fix offered anywhere. `db-exposed` read `A \|\| B \|\| (C && D)` where `&&` binds tighter, so it asked whether `0.0.0.0:` appeared anywhere in a container's ports and `->5432/` appeared anywhere, never that the two were one mapping — reporting a loopback database as public, and a real exposure under its container port, the one number nobody can connect to |
| v0.11.2 | `4cfcf22` | Pressing Fix on "Docker cannot bypass the firewall" did nothing, three times, and reported success each time: the ufw-docker snippet was written to `/etc/ufw/after.rules` and then `ufw --force reset`, the first command in the list that followed, restored the file from the package. The evidence was three `after.rules.<timestamp>` backups each carrying the snippet beside a live file that did not |
| v0.11.3 | `9482368` | tmux starts a server as a child of whoever runs the first client command, so running every command from isletd put the server in isletd's control group and mount namespace — which is why the daemon's unit settings decided its fate at all. `systemd-run` hands it to PID 1 in a transient unit instead, making it a sibling rather than a child |
| v0.11.4 | `aa06328` | The above, serialised. `ensureServer` had no lock, four calls landed inside 121 ms, and two tmux processes racing to create a server on one socket segfaulted tmux 3.2a; the half-started unit then kept its name, so every retry failed with "already exists" and fell back to the placement being replaced. Correct and unreachable |

| v0.12.0 | `c29603a` | **The assistant, and full MCP coverage under it.** A write scope per area so a token can be trusted with less than everything; one checked tool that reaches any endpoint the token allows; fifty-one curated tools on top of it, each recording what was refused and why; and a panel page that takes a question in words and acts on it with the asker's authority and nothing more |
| v0.13.0 | `996c728` | **The vault.** A secret stored once under a name, referred to as `@vault:NAME` everywhere a value is taken, revealed by nothing — not even a token with every scope |
| v0.14.0 | `9e41d9a` | An OpenID Connect provider in the catalog, verified by running the image rather than by reading its README. And two fixed paths that two daemons on one host were sharing: the socket under `/run` and the tmux unit name, which is how a development daemon took the installed one's socket |
| v0.14.1 | `2028fdc` | The assistant on a Claude subscription answered every question the same way, because Claude Code prompts before using an MCP tool and `--print` has no terminal to prompt on. `--allowed-tools`, naming the MCP servers in the configuration and nothing else — not Bash, not editing, not `bypassPermissions` |
| v0.14.2 | `1eada79` | Asking the assistant to deploy from GitHub answered **524**. The endpoint ran the whole tool loop and replied at the end; Cloudflare gives an origin 100 seconds. The answer streams now, one line per turn, with a twenty-second heartbeat — which is the part that mattered, since the subscription provider runs its whole loop inside one call and emits nothing until it is done. Measured at 159 s through Cloudflare, where it used to fail at 100 |
| v0.14.3 | `f02a002` | Four tools that described a field nothing reads: `diagnostics` offered `kind` for `tool`, `metrics_history` offered `hours` for `range` — so six hours quietly returned one — `list_files` offered a `hidden` that does not exist, and `create_domain` offered `https`/`wwwRedirect` for `tls`/`redirectWww`. And the expensive one: `Domain.Enabled` defaults false, so a domain created through the API was stored, listed, never served and never issued a certificate. Now defaulted at the handler, as cron, uptime and backups already did |
| v0.14.4 | `03d170c` | `create_domain` named six target types; the proxy takes three. An agent asked to put an app on a domain picks "app", which was refused every time |
| v0.14.5 | `735cafa` | What calling all seventy tools found that reading them did not. `diagnostics` answered "500 streaming unsupported" — its endpoint sends SSE and the recorder tools call through was not an `http.Flusher`; `search_files` offered `path`/`query` where the handler read `root`/`q`; and `content`, a boolean, was sent as "true" where every query flag here is compared against "1", so search-inside-files was a switch connected to nothing |
| v0.14.6 | `53cf249` | Making the recorder flushable let the diagnostics tool past the refusal and into a panic: `streamLines` drains the request body, and a request assembled in process has none. Fixed at both ends — the synthetic request carries `http.NoBody`, and the helper every streaming endpoint funnels through no longer takes the daemon's handler down on a nil |

| v0.14.7 | `6378beb` | Creating a Mongo database sends the new user's password inside a `--eval` script — one argument, so no redaction rule could see it, and it reached the audit table and the command drawer in full |
| v0.14.8 | `4babc79` | Streaming broke the tab that was already open: it reads the whole reply with `JSON.parse` and fails at the first newline. The shape now follows `Accept`, and a stale tab reloads itself when it asks for a chunk that no longer exists |
| v0.15.0 | `f15a49e` | **A run belongs to the daemon.** Asking for anything long from a phone failed silently — a locked screen closes the connection, and the loop was bound to the request. Runs now have an id, an event log and a sequence number to come back on; the panel reattaches on mount, on visibility and on reconnect. And they say what they are doing: each tool named as it starts and ticked when it returns, read out of Claude Code's own `stream-json` for the subscription provider and out of the loop for the rest |

| v0.15.1 | `6855b35` | Traefik's compressor holds a response's first kilobyte back to decide whether compressing is worth it, and for a stream that kilobyte is however long the work takes — so the browser saw nothing and then a gateway timeout, while curl, which sends no `Accept-Encoding`, saw it stream perfectly. Every streaming endpoint was affected: logs, Compose, SQL, ping |
| v0.16.0 | `defc6d7` | **Conversations.** Stored on the server, so one started on a laptop opens on a phone; titled from the first question; turns written as they complete. One run per conversation, no limit across them |
| v0.17.0 | `c3aef45` | **The assistant had a root shell.** `--allowed-tools` says which tools need no approval, not which exist, so Claude Code's own Bash was there — and classified as safe, so it ran without asking. Asked to `id -u`, the assistant answered 0. Closed with a deny list in `--settings`, `--permission-prompts none` and `--strict-mcp-config`. Found by getting a browser and looking at the panel, which also produced the Markdown rendering, the tool lines, and the record of what a turn did |
| v0.18.0 | `2ca0ec5` | **The panel worked when nobody was asking it anything.** Every page polled on a timer whether or not it was on screen, and a phone in a pocket kept a server busy. Polling now follows visibility |
| v0.18.1 | `ceb9456` | The backup end-to-end check passed and then failed: its cleanup trap could not remove root-owned restic files as a non-root runner, and the trap's exit status became the job's |
| v0.18.2 | `5ac4bef` | A "expires soon" badge read the clock while rendering, so it changed only when something unrelated re-rendered |
| v0.18.3 | `168c261` | Tests for the two places a mistake costs the most: the database credentials path and uptime's state machine |
| v0.18.4 | `ffa02db` | **"Protect with Islet login" sent signed-in people to the dashboard.** The gate was right; the panel renders `/login` only for an anonymous visitor, so a signed-in admin's `?next=` was thrown away by the catch-all. The decision now lives in one place and both paths ask it |
| v0.19.0 | `517375d` | **Protection per path and per person.** One switch per host became a grid: every route on the host down the side, every account across the top. A location is three-state — inherit, on, off — because /admin guarded on an open site and /hooks open on a guarded one are both real. An empty list still means any signed-in user, so nothing had to be migrated |
| v0.20.0 | `6c6b47c` | **The gateway audit.** Seven faults, five found only by putting a real Traefik in front of a real backend: an opened path also opened `/hooksecret` (PathPrefix is a string prefix); `X-Islet-User` was forgeable on any ungated route; an `Authorization` header broke the gate for everybody; the API let a deployer change who may reach a site; one refusal wrote one audit row per request; and a protected app was handed the panel's session cookie — now a separate gate cookie that opens the gate and nothing else |
| v0.20.1 | `c473211` | Every browser signed in before v0.20.0 still held a session cookie scoped to the parent domain, and went on handing it to protected sites until it expired. The wide cookie is deleted by name and the session re-issued host-only on the first panel load without a gate cookie |
| v0.20.2 | `0a7e1ad` | **Two tabs on one workspace took it from each other forever.** tmux attaches with `-d`, the panel reconnects on close, and together that is a loop. One seat per session now: the displaced tab is told (status 4001) and stops, and nothing reconnects while its tab is hidden. Also: runner containers could not resolve the address their own generated workflow tells them to call, and the catalog URL field hung off the side of a phone |
| v0.21.0 | `eed5c59` | **Blocklists and geo-blocking**, built into Islet rather than by installing a second firewall manager: three community lists and per-country zones in one ipset, dropped in INPUT and DOCKER-USER, refreshed daily, restored after a reboot — with every private and bogon range filtered out, because FireHOL level 1 contains `10.0.0.0/8` and loading it raw cuts a Docker host off from its own containers. Plus: the panel now says which GitHub App permission is missing instead of leaving "Resource not accessible by integration" in a log, Pocket ID's note no longer claims a first-user admin rule it never had, and Logto joins the catalog |
| v0.22.0 | `8b65f78` | **Models are things you set up, not a setting.** A provider is a named row — subscription, Anthropic key, OpenAI-compatible — configured under Settings → AI; the assistant picks one per conversation and a workspace agent picks one when it is created, and neither asks when only one is configured. The existing configuration migrates with its sealed key unchanged, so nobody sets Claude up twice and no open conversation changes model. An Anthropic key reaches an agent through tmux `-e`, never through a shell where it would sit in the scrollback |
| v0.22.1 | `6db4eed` | The workspaces page was four bars stacked over the terminal it exists for; it is one card, with the agent tabs inside the terminal's own control row and the rare actions behind a menu. A revealed vault secret is a dialog with Copy and Close rather than a banner that pushes the page down. And Logto leaves the catalog for Keycloak: Apache 2.0, no editions, no cloud tier, themes you replace outright |
| v0.23.0 | `f6d1764` | **The pre-freeze pass.** Two reviews were written first — a defect audit and a design review of Islet as a product — and this release is most of what they found. Authorization moves out of ninety-six hand-written checks into one table with a test that fails when a route is added without a line, which closed three routes that had no check at all: writing and deleting vault secrets, and emitting notifications. Scopes now mean the same thing through the fleet proxy as they do locally, and a workspace agent's token no longer reaches a root shell through cron or a Compose file. The SSE end event is JSON, so a build that fails in colour is readable instead of reported as a dropped connection. Commands have a deadline, a backup run has one too — with a bounded stderr and an unlock — and the uptime probe stopped recomputing two aggregates it discarded, which was a third of a core at a hundred monitors. A sealed value now records which key sealed it, which is the one thing rotation needed and could not be added later. Plus what testing found: the tmux server is no longer torn down on a slow socket dial, taking every workspace with it |
| v0.23.1 | `9881c96` | An agent's start/stop control moves inside that agent's own tab: at the end of the row it said nothing about which agent it acted on, and with more than one open the answer was written nowhere. The terminal's copy and paste stop being bordered boxes and become plain icons in the same style as Add. Both were asked for once and built wrong, so both were screenshotted and looked at this time |
| v0.23.2 | `b76748a` | Creating a workspace stopped the one you were working in, and the fix in v0.23.0 did not hold because a line above it removed the tmux socket first: `stale()` reads one ECONNREFUSED as a dead path, and a live socket returns that when its accept queue is full. The destructive path is now gated on the number of processes in the unit's cgroup — a signal a busy server cannot fake — and clearing the unit stops being the opening move, so `systemctl stop` is reached only when `systemd-run` actually refuses the name |
| v0.23.3 | `bd825f8` | The workspace that stopped when you created another one: tmux 3.2a segfaults its **server** when it expands a format for a target that does not exist, taking every session with it, and `running()` asked for `#{session_created}` of a session a new workspace does not have yet. `has-session` goes first, which takes no format and survives a missing target. "Server exited unexpectedly" was tmux's own words for its server dying, and two earlier releases treated the recovery from it rather than the crash |
| v0.24.0 | `9c764a8` | **A secret reaches every place an app is configured**, not just a Git deploy: cron resolves `@vault:NAME` into the process and no further, and a catalog install resolves it on the way to the `.env` Compose reads, which the page now says out loud. **Nineteen sidebar entries become thirteen** — Terminal is a header button, Logs and Uptime are one page with two tabs, the vault is Settings → Secrets, Notifications moves to the foot and the Assistant moves up. And **a fresh server can actually get a subscription working**: Settings → AI installs Claude Code and reports whether it has been signed in to, which is a separate question it never asked |
| v0.25.0 | `05baa56` | **The blocklist was blocking its own source.** A stateless DROP on a source address also drops the replies to connections this server opened, and ipdeny.com — where the country zone files come from — is in IPsum, so turning country blocks on made them impossible to download, silently, while the page reported the lists as loaded. The rule matches `--ctstate NEW` now, which also unbreaks any registry pull, ACME challenge or webhook to an address a list happens to name. **A second daemon can no longer take the machine**: the proxy container and the ipset are one per host, and a daemon from another data directory used to replace them without a word — ownership is recorded under `/run` and the second one is refused by name. Plus the country blocks offer all 249 countries behind a search rather than nine checkboxes and a box for typing codes by hand |
| v0.25.1 | `5046c13` | **Text could not be selected in a terminal that was running anything.** A full-screen program asks to be told about the mouse, and from then on a drag is the program's; the way out is a modifier, Shift everywhere and Option on a Mac, and xterm.js leaves the Mac one off unless asked — so on a Mac there was no way to select at all, and nowhere did the panel say which key. The toolbar now says it, and only while a program is actually holding the mouse. **Settings → Team put nothing in the same place twice**: each row measured its own cells, so a role name, "all projects" and an actions group that is shorter on your own row moved the columns about. The list is one grid the rows take their columns from, and delete is the panel's trash icon rather than a word beside "Reset password" |
| v0.26.0 | `af97300` | **The assistant can be handed files.** Images, video, documents, a zip of a brand kit: uploaded from the composer, checked for malware, and from then on an absolute path on this server — which is what every one of its seventy tools already takes, and what Claude Code opens itself, so a photograph and a forty-megabyte video cost the same to attach. Scanning is `clamdscan`, then `clamscan`, then nothing, and *nothing* is said out loud rather than implied: ClamAV wants a gigabyte of RAM for its signatures and this daemon is meant to run on a server with one. A scanner that is installed and cannot answer refuses. The composer shows a thumbnail of an image and an icon for everything else — which needed `blob:` on the panel's own `img-src`, without which the preview silently drew nothing — and the microphone beside it is the browser's own speech recognition or is not drawn at all. Plus: **starting an agent again was invisible until the page was reloaded**, because killing a tmux window moves every client attached to that session on to another one, so the panel was showing a sibling's window under the stopped agent's name — and a keystroke meant for one agent went to the other |
| v0.26.1 | `f3fedaa` | **A selection over Claude's output lasted until Claude wrote again.** Selecting in the shell worked and selecting in an agent did not, which is one behaviour, not two: an idle terminal and a busy one. A selection in xterm is a pair of buffer positions and tmux puts every session on the alternate screen, which has no scrollback — so when the program scrolls, the lines move and the positions do not. A selection reading `PICKME 15` read `PICKME 19` three scrolled lines later, and was gone a few lines after that, which means the Copy button would hand over text nobody chose. Output now waits while there is a selection on screen and goes in the moment the selection does; nothing is dropped, the toolbar says it is held, and past 2 MB the output wins and says so |
| v0.27.0 | `a2a27a7` | **The first Islet service an application calls rather than an operator.** Media: an app POSTs a file and gets a URL, with images resized, compressed and converted at named sizes, a PDF's first page and a video's frame thumbnailed, and the bytes stored on this disk or on R2, S3, MinIO, Backblaze, Wasabi or Hetzner — one hand-written SigV4 for all of them, because the AWS SDK alone is larger than this daemon. The two audiences never meet: a media key is refused by the panel API and a panel session by the service, in separate tables, middleware and muxes, with the service also answering at a hostname of its own. Converters live in a container Islet builds and owns, driven by `docker exec` so every conversion lands in the command drawer. Limits ship with it rather than after the first incident — named presets, one conversion at a time, per-key rates and quotas, a type allowlist, CORS per key, and the assistant's malware scanner moved to `internal/scan` so uploads from the open internet get it too. Plus: **a model added under Settings → AI no longer reports itself as no model at all** — the page asked the pre-row settings, and the daemon now decides by building the provider a question would use |
| v0.27.1 | `3e01cdc` | **Installing the converters worked and reported that it had failed.** The panel reads two stream shapes and nothing checked that a handler writes the one its caller reads: `postStream` parses server-sent events, this endpoint wrote newline-delimited JSON, so the parser saw a stream end without its end event and threw — with the converters visibly installed underneath the error. It emits SSE now, streams the build while it happens rather than summarising it afterwards, and a test reads the panel for which paths expect SSE and the daemon for which handlers write NDJSON and fails when one is the other |
| v0.27.2 | `594ffed` | **A media hostname that nothing routes now says so.** Naming one tells Islet to answer on it; it does not tell the proxy the host exists, so every request got Traefik's own 404 while the daemon answered perfectly well on the panel's host — found on a real server with the service enabled and a hostname set. The page now reports whether a domain for that host points at the panel, beside the link to where it is added |
| v0.28.0 | `dd20431` | **Naming a media hostname adds the domain for it.** It used to tell Islet to answer on the host and leave adding the domain — the half that makes the proxy aware the host exists — to be done by hand somewhere else, which produced a service reporting itself as answering while every request got the proxy's 404. It is created pointed at the panel with a certificate, saving twice changes nothing, and a hostname already serving an application is refused by name rather than taken over — before the settings are written, so a refused save keeps none of itself. The DNS record is the one part left to a person, which is the one part Islet cannot make |
| v0.28.1 | `ad8a3a2` | **The assistant stopped losing what it had already said.** Pressing Stop deleted the answer, because the provider returned its error and nothing else; it hands back what it had written now and the transcript keeps it. A crashed Claude Code wedged the conversation for thirty minutes — its MCP child inherits the pipe the answer is read from, so killing the parent left the read waiting for an end-of-file that never came, with every new question refused as "still working"; it runs in a process group now, and once the process is gone the read is unblocked from this side. And the answer used to arrive all at once: `--include-partial-messages` streams it a few tokens at a time, first words in three seconds rather than eight |
| v0.29.0 | `9c5250f` | **The assistant had no tools on any server, and now has them.** The subscription provider is Claude Code, which calls tools over HTTP for itself, so the only way to give it Islet's tools is an MCP configuration — and that was a field for an operator to fill in by hand, with no screen for it and no reason to know it existed. Worse than empty: without `--mcp-config` there is no `--strict-mcp-config`, so it fell back to whatever MCP servers the root account had. The daemon writes the file itself now, per run, with a token it mints and revokes, scoped to everything except shell, security, vault and cron. Asked what is running, it calls `list_containers` and answers with real names in eleven seconds. Also: a chat window that behaves like one — a box that grows, Enter to send, Stop that waits for the closing turn instead of hanging up on it, Continue and Ask again where an answer ended early, and copy on an answer |
| v0.29.1 | `a793f64` | **A backup repository ran off the side of a phone.** Sixty-six characters with nowhere to break, in a flex child that will not shrink below its longest word — so the row was a hundred pixels wider than its card, the card clipped it, and the line beneath lost its end mid-sentence. The address breaks anywhere now and takes the whole row below `sm`, with the buttons underneath. The layout audit gains the check that would have found it: it only ever looked for content passing the edge of the *window*, and a card narrower than the window clips instead — so nothing scrolls, nothing spills, and the text is just not there. Deliberate clipping is still allowed to be deliberate: an ellipsis, or a box that scrolls |
| v0.29.2 | `744baa9` | **iOS stopped zooming in every time a field was tapped** — Safari does that to any control whose text is under 16px and the panel's are 14, so tapping the assistant's box zoomed the page and left it zoomed. `maximum-scale=1`, with the cost accepted deliberately: it also suppresses pinch-zoom where Safari honours it. And **a bucket held on this server stopped accepting a public base URL**, which is an address for a bucket that is already public somewhere else — set on a local bucket it made every public object redirect to a host answering nothing, since Islet is the origin there and objects live at `/v1/objects/<id>` rather than at the key path |
| v0.29.3 | `96d0e89` | **No browser upload could get past its own preflight.** A CORS preflight deliberately carries no `Authorization` header, and the preflight was answered by looking up the key on the request — so it always came back empty, no `Access-Control-Allow-Origin` was sent, and the browser refused the upload it was asking permission for. On every server, whatever was configured; server-side callers never noticed because they send no Origin. It is answered from the keys as a whole now, with the real request still checked against its own key's origins. And a trailing slash on a stored origin — which the address bar has and an `Origin` header never does — stopped being a different origin |
| v0.30.0 | `8291b3a` | **Terminal scrolling and copying, actually checked this time.** Scrolling never worked because there was nothing to scroll: a tmux pane is on the alternate screen, which has no scrollback, so xterm turns a wheel — and the wheel events a finger drag becomes — into cursor keys and sends them to the program. `mouse on` hands tmux the wheel and it scrolls its own history, now 20000 lines rather than 2000. A plain drag stops selecting as a result, but Shift+drag selects *and copies*, verified by reading the clipboard rather than counting highlight rectangles. A phone has no Shift and no drag that is not a scroll, so the toolbar gained **Text**: the pane's last 2000 lines as ordinary selectable text with a copy button. And the page comes back to the workspace and agent you were last in |
| v0.30.1 | `fab7cb7` | **A conversation started in the Claude app can be continued in a workspace.** `claude --teleport <session-id>` pulls a cloud session down and runs it here, against the real files, while the app goes on showing it — the sync only goes that way, and a session started on the server cannot be pushed up. What stopped it working was Islet: an agent set to resume is handed `--session-id` or `--resume`, a teleported session already carries one, and Claude Code refuses two. The flag is left alone now |

Six of these — v0.7.2, v0.8.0, v0.8.1, v0.11.2, v0.11.3 and v0.11.4 — were each
the second or third attempt at one reported symptom. `DECISIONS.md` records what
the earlier attempts assumed and why measuring beat guessing; that pattern is the
most reusable thing in this period.

Worth naming, because it keeps recurring: the failures that survived longest were
the silent ones. The firewall fix reported success on every run. `panel-2fa` was
simply a red line nobody could act on. v0.11.3 fell back to the exact behaviour it
was replacing and said so only in a log nobody was reading. Each was found by
using the thing rather than reviewing it.

## Working on the repository

`CONTRIBUTING.md` has the build and dev-server commands. What it does not say:

```sh
# the full check before a commit. This is what ci.yml runs, in its order, and it
# is wider than the list in CLAUDE.md — gofmt covers the whole repository, and
# four steps after the tests have their own ways of failing.
cd web/app && pnpm install --frozen-lockfile && pnpm build   # build runs tsc -b
cd ../.. && rm -rf internal/web/dist && cp -r web/app/dist internal/web/dist
test -z "$(gofmt -l .)" && go vet ./... && go test ./...
node --experimental-strip-types hack/sql-split-agree.mjs
go run ./tools/docsite -out /tmp/site && test -f /tmp/site/index.html
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/isletd
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/isletd
for f in installer/*.sh hack/*.sh; do sh -n "$f" || exit 1; done
cd web/app && pnpm exec oxlint            # local only; CI does not run it
git checkout internal/web/dist/index.html # the embed step overwrote it
node hack/layout-audit.mjs <cookie> "/domains|1440|900" "/domains|390|844"
node hack/width-audit.mjs <cookie> /domains
```

`go test -race ./...` needs cgo and therefore a C compiler. CI does not run it,
and it is worth running by hand for anything touching concurrency: the v0.11.3
crash was a data race in everything but name.

- **Migrations are sequential and never edited once pushed.** `internal/store/migrations/` is at `0029`; the next one is `0030`.
- **`internal/web/dist/index.html` is a tracked placeholder.** A UI build overwrites it. Restore it before staging, or a build artefact lands in the commit.
- **State is read from the system, not stored**, wherever the system already knows it — workspace state comes from tmux, runner state from Docker labels. Adding a status column is usually the wrong instinct.
- **One release per tag.** Push `main`, then the tag; the release workflow needs `test` to pass, so a red CI cannot publish. Afterwards, check the tag carries five assets and that `checksums.txt.sig` verifies against the key in `internal/update/pubkey.go` — the honest way is to run the daemon's own `update.VerifySignature`, not a reimplementation of it.

## What is left

### Open roadmap items

All five are blocked on something outside the code. Verified on the date above:

1. **CrowdSec and geo-blocking** (Phase 5). Needs a Linux box to develop the nftables bouncer against.
2. **WASM plugin host** (Phase 6). An open design question rather than an unwritten feature: plugins as sandboxed WASM modules, or as external services talking to the API and MCP, which the current scoped tokens already allow. `internal/plugins/` holds nothing but a `.gitkeep`, though `STRUCTURE.md` describes it as if it were built — worth correcting whichever way the decision goes.
3. **Catalog in its own repo.** `github.com/isletdev/catalog` returns 404. The daemon already fetches and overlays it when it exists (Settings → Catalog source); only the repository is missing.
4. **Docs site.** `isletdev.github.io/islet` returns 404 and the `docs` workflow's last two runs were skipped — it needs Pages enabled and the repository variable `DOCS_SITE=true`. The 15 recipes it would publish are written. Screencasts for the top five flows: not started.
5. **Load test on a 1 vCPU / 2 GB box.** `hack/loadtest.sh` exists, and on the dev box container and stack lists went from 2–3 s to under 200 ms with 38 containers. The small-box numbers are the ones that matter and have not been taken.

### Closed: secrets reaching the audit log

This section used to describe a live defect — `mysql -phunter2`, `mongosh -p
hunter2` and `gitlab-runner --token abc` all passing through `cmdrun.Display`
untouched and into the audit table and the command-transparency drawer. It is
closed, in two parts, and worth recording how because the second part was still
open when the first was declared done.

The call sites were converted to long forms (`--password=`, `--password X`,
`--token X`), which `Redact` and the flag rule already cover. Short flags are
deliberately *not* matched, and the comment above `secretFlag` says why: `-p` is
a password to mysql, a published port to `docker run`, a project to `docker
compose` and a property to `timedatectl`. The live command log bears that out —
133 of the last 400 commands carry a short `-p`, and every one of them is a
`tmux display-message -p` or a `timedatectl show -p`. A blanket rule would have
emptied the audit trail of the detail it exists for.

The part that was still open: creating a Mongo database sends

    mongosh … --eval 'db.getSiblingDB("shop").createUser({user: "shop", pwd: "…"})'

and the whole script is one argument, so neither rule could see the password in
it. `Redact` now blanks a quoted value under a secret-looking key inside an
argument — whole word only, quoted values only, so `passthrough` and prose are
left alone. `internal/cmdrun` has a test file now, and these shapes are in it.

### Packages with no tests

Thirteen of thirty-two, and the list is not the comfortable half:

    internal/backup (1369 lines)   internal/db (951)    internal/runner (596)
    internal/metrics               internal/uptime      internal/mcp
    internal/terminal              internal/watch
    cmd/isletd  cmd/islet  internal/web  internal/version  pkg/api

`internal/backup` is the one to start with: "backups that restore" is Phase 5's
definition of done and nothing exercises it.

### Tests written but not wired into CI

`e2e.yml` runs `hack/e2e-vps.sh`, which runs `hack/e2e-smoke.sh` on a fresh
server. Three other suites are run by hand only, and nothing invokes them:

- `hack/e2e-workspaces.sh` — several agents in one workspace, each with its own conversation; survival across the daemon being killed; and resume-on-reboot asserted on the actual flags, through a stub `claude` that records its argv
- `hack/e2e-privileges.py` — proves a viewer and a read-scoped token are refused on every privileged route
- `hack/e2e-sql.py` — the SQL client against a real Postgres

Wiring these into `e2e.yml` is the highest-value quality task left, and it is the
only one that depends on nobody else.

### The VPS end-to-end suite has never actually run

`e2e.yml` has one run in its history (2026-09-14) and it reported success — but
its `build` and `vps` jobs were both **skipped**. The workflow is written to skip
itself when `HCLOUD_TOKEN` is absent, and it is absent. So "e2e on four
distributions" is at present a workflow that has been syntax-checked, not a
result. Setting that secret turns it on; a run costs roughly EUR 0.05.

### What is done that the plan still showed as open

`get.islet.dev` answers (302) and `islet.dev` serves the site (200); the signing
key is in place and 43 tags have published signed releases. Phase 0's first two
boxes had been left unticked from before any of that was true and are now
corrected, as is the summary at the top of `NEEDED_FROM_YOU.md`, which still
described a repository of 48 commits.

### The assistant is finished

Everything asked of it is built and was exercised against this server rather
than reasoned about:

- both provider kinds — an API key, and the person's own Claude subscription
- seventy MCP tools, every one of them called; five were broken and only calling
  them found out
- answers that stream, tool by tool, with what each one did and how long it took
- runs that belong to the daemon, so a phone locking its screen mid-deploy does
  not stop the deploy, and reattach from a sequence number when it comes back
- conversations stored per person, openable from another device, several running
  at once, one run each
- a boundary that holds: no shell, no unscoped file access, every call through
  the same gate a direct API request passes, and written to the audit log

Two things are deliberately left, and neither blocks the feature:

1. **The claude process runs as root.** The deny list is what stops its built-in
   tools, and a deny list is a floor: a tool added upstream and classified as
   safe would not be on it. Running it as another user is the durable answer and
   needs the subscription's credentials to live somewhere other than root's
   home. Until then, the assistant's real boundary is the scopes on the token in
   its MCP configuration — and on this server that token carries `*`, so
   narrowing it is a decision worth taking deliberately.
2. **A run's events die with the daemon.** The conversation survives, because
   turns are written as they complete; the live event log does not, since a
   model call cannot be resumed across a restart anyway.

### Not verified by anyone yet

One thing in the workspace feature has never been exercised end to end by a
person: **signing in to Claude Code with a subscription account inside a
workspace.** It is listed as done because the code paths are tested.

The other entry here — an MCP tool call made by a real agent, landing in the
audit log — is settled. All seventy tools were called against the live panel on
2026-09-15: the twenty-three that need no arguments, and the rest with real ids
taken off the lists. Every one of them left an `mcp.request` row carrying its
method, path and status. Five tools were broken, and only being called found
them; see v0.14.3 to v0.14.6 above.

A third, from v0.11.3: **the transient `islet-tmux` unit has never run on the
development server.** `ensureServer` only acts when no server is listening, and
that server has been up since before the release, so the live one is still in
`isletd.service`'s control group — working, but only because `KillMode=process`
tells systemd to ignore it. The mechanism is proven in isolation (its own cgroup,
the host mount namespace, six concurrent starts yielding one server and no
segfault) and not yet end to end. A reboot, or one `tmux -S <sock> kill-server`,
settles it.

### Smaller inconsistencies

- **Most commits on `main` carry no `Signed-off-by` line**, though
  `CONTRIBUTING.md` asks every contributor for one and `dco.yml` enforces it on
  pull requests only, so direct pushes were never checked. Commits from v0.14.2
  onward are signed off; the history before that is not, and the first outside
  contributor would still be held to a rule most of the log does not follow.
- **`v0.10.1` and `v0.11.1` are the same commit**, `b840722`, tagged twice. No
  harm done, but "one release per tag" reads oddly against a tag list where two
  names point at one change.
- **CI logs a deprecation on every run.** `actions/checkout@v4`, `setup-go@v5`,
  `setup-node@v4` and `pnpm/action-setup@v4` all target Node 20 and are being
  forced onto Node 24. Nothing is broken; it will need a bump.
- Several roadmap lines carry an inline "(… pending)" for a sub-feature that was
  deliberately deferred: terminal tabs, a file tree, drag-and-drop upload, diff on
  save, manual certificate renewal. They are real and small; none blocks 1.0.

## Picking this up somewhere else

The repository is the handoff. Clone it, then read in this order: `ROADMAP.md` for
the shape, this file for the state, `DECISIONS.md` for the reasoning behind
anything that looks odd — it is long, and the entries nearest the end explain the
current code.

To run the panel:

```sh
go build -o isletd ./cmd/isletd
ISLET_DATA_DIR=.data ISLET_LISTEN=127.0.0.1:9443 ISLET_TLS=off ./isletd
cd web/app && pnpm install && pnpm dev
```

On a server Islet already manages, the installed daemon owns `0.0.0.0:9443`, so a
development one needs another port — and `hack/layout-audit.mjs` and
`hack/width-audit.mjs` both hardcode `127.0.0.1:9443`, which means they would
drive the *installed* panel rather than the build under test. Worth knowing before
either is run there. Both take `ISLET_BASE`, so they can drive a build under test
rather than the installed daemon. They need a browser speaking the DevTools
protocol, which this server has no package for — a Chrome-for-Testing build
unpacked into a scratch directory works, is not an install, and is how
`/assistant` was measured at 390, 768 and 1440.

Working on a server that Islet manages has one advantage worth using: features can
be exercised against the thing they manage rather than against Docker Desktop on a
laptop. Several of the bugs in the table above existed only in the deployed shape
— behind a reverse proxy, on a real certificate, with a real DNS record — and none
of them could have been reproduced anywhere else. The two most expensive entries
above were found by an agent working on the box: one by losing its own `/tmp` when
the daemon it had just updated restarted underneath it, and one by reading three
`ufw` backups that each contained rules the live file did not.
