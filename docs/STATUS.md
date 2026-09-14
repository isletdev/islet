# Status

**Head:** `95bd5a2`, tagged `v0.9.4`, 2026-09-14. 95 commits, 35 tags, CI green on `main`.

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

What the roadmap does not describe is the eleven releases after it. From v0.6.0
onward the work stopped coming from the plan and started coming from a real
server — a live fleet migrated off Nginx Proxy Manager, and then the need to run
an agent next to the things it changes. That is the more interesting half of the
recent history, because it is the half that was found rather than designed.

## Shipped since the plan ran out — v0.6.0 to v0.9.4

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

Three of these — v0.7.2, v0.8.0 and v0.8.1 — were each the second or third attempt
at one reported symptom. `DECISIONS.md` records what the earlier attempts assumed
and why measuring beat guessing; that pattern is the most reusable thing in this
period.

## Working on the repository

`CONTRIBUTING.md` has the build and dev-server commands. What it does not say:

```sh
# the full check before a commit
gofmt -l ./cmd ./internal          # must print nothing
go build ./... && go test ./...
cd web/app && pnpm exec tsc -b && pnpm exec oxlint
node hack/layout-audit.mjs <url>   # at 1440x900 and 390x844
node hack/width-audit.mjs <url>
```

- **Migrations are sequential and never edited once pushed.** `internal/store/migrations/` is at `0027`; the next one is `0028`.
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

### Tests written but not wired into CI

`e2e.yml` runs `hack/e2e-vps.sh`, which runs `hack/e2e-smoke.sh` on a fresh
server. Three other suites are run by hand only, and nothing invokes them:

- `hack/e2e-workspaces.sh` — the tmux survival test, including that a workspace outlives `pkill isletd`
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
key is in place and 35 tags have published signed releases. Phase 0's first two
boxes had been left unticked from before any of that was true and are now
corrected, as is the summary at the top of `NEEDED_FROM_YOU.md`, which still
described a repository of 48 commits.

### Not verified by anyone yet

Two things in the newest feature have never been exercised end to end by a person:

- signing in to Claude Code with a subscription account inside a workspace
- an MCP tool call made by a real agent, landing in the audit log

Both are listed as done because the code paths are tested. Neither has been used
in anger.

### Smaller inconsistencies

- **No commit on `main` carries a `Signed-off-by` line**, though `CONTRIBUTING.md`
  asks every contributor for one and `dco.yml` enforces it. That workflow runs on
  pull requests only, so direct pushes were never checked. The first outside
  contributor would be held to a rule the history does not follow — either start
  using `git commit -s`, or say plainly that the rule applies to pull requests.
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

Working on a server that Islet manages has one advantage worth using: features can
be exercised against the thing they manage rather than against Docker Desktop on a
laptop. Several of the bugs in the table above existed only in the deployed shape
— behind a reverse proxy, on a real certificate, with a real DNS record — and none
of them could have been reproduced anywhere else.
