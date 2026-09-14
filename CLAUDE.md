# Working on Islet

A single-server control panel: one Go module, one pnpm workspace, one static
binary. The daemon (`isletd`) serves a REST API and the embedded React panel;
the same binary is the `islet` CLI through a symlink. It manages Docker,
Traefik, certificates, databases, cron, backups and the host itself, so most
bugs here are about a real system's state, not about code in isolation.

## Read before changing anything

| File | What it answers |
|---|---|
| `docs/STATUS.md` | where the work stands and what to pick up — **start here** |
| `docs/ROADMAP.md` | the phased plan, with checkboxes |
| `docs/DECISIONS.md` | why a non-obvious call was made. Long; the entries nearest the end explain the current code |
| `docs/STRUCTURE.md` | what every folder is for |
| `CONTRIBUTING.md` | build and dev-server commands |
| `docs/AGENT_SETUP.md` | running an agent on a server Islet manages, and the machine rules that do not belong in this file |

## Build, run, verify

```sh
go build -o isletd ./cmd/isletd
ISLET_DATA_DIR=.data ISLET_LISTEN=127.0.0.1:9443 ISLET_TLS=off ./isletd
cd web/app && pnpm install && pnpm dev     # UI with hot reload, proxied to the daemon
```

Everything below must pass before a commit. Not "should" — the CI job runs the
same set and a red CI cannot publish a release.

```sh
gofmt -l ./cmd ./internal          # must print nothing
go build ./... && go test ./...
cd web/app && pnpm exec tsc -b && pnpm exec oxlint
```

For anything that changes the panel's layout, also run the two audits against a
running daemon. They catch what review does not, because the faults only appear
at particular widths:

```sh
node hack/layout-audit.mjs "$SESSION_COOKIE" "/domains|1440|900" "/domains|390|844"
node hack/width-audit.mjs "$SESSION_COOKIE" /domains
```

Both need a browser listening on the DevTools protocol; the usage comment at the
top of each script has the command.

`hack/` also holds `e2e-workspaces.sh`, `e2e-privileges.py` and `e2e-sql.py`.
They need a live daemon and are run by hand — nothing in CI invokes them yet.

## Conventions that bite

- **Migrations are sequential and never edited once pushed.** Check the highest
  number in `internal/store/migrations/` and add the next one. Editing a
  migration that has shipped means installed daemons never apply the change.
- **`internal/web/dist/index.html` is a tracked placeholder.** A UI build
  overwrites it. Restore it before staging, or a build artefact lands in the
  commit.
- **Read state from the system, not from a column.** Workspace state comes from
  tmux, runner state from Docker labels, container state from Docker. Adding a
  `status` column is nearly always the wrong instinct — it is a second source of
  truth that drifts.
- **`internal/api` is the only package that knows about HTTP.** Feature packages
  are tested without a server.
- **No feature package imports another.** They meet through `store`, `reconcile`
  and `notify` events.
- **Every machine-bound table carries `server_id`** and every query filters on
  it, so the fleet never needs a schema rewrite.
- **Every command the daemon runs goes through `cmdrun`**, which redacts secrets
  and writes to the audit table. That is what makes the command-transparency
  drawer work; a bare `exec.Command` is invisible.

## The panel

React 19, Vite, TypeScript, Tailwind v4, `react-router-dom`, oxlint. `tsconfig`
sets `erasableSyntaxOnly` and `verbatimModuleSyntax`, so no enums, no parameter
properties, and type imports must say `type`.

shadcn/ui and TanStack Query were planned early and never adopted; the
components in `web/app/src/components` are the project's own. API types are
mirrored by hand between `pkg/api` (Go) and `web/app/src/lib/api.ts` — there is
no codegen, so a new field means editing both.

Other things the UI expects: colours come from theme tokens, never literals;
every grid declares `grid-cols-1` before its breakpoint; hit targets are at least
20px (the `-my-1 py-1` idiom); any table gets `min-w-[…]` plus `overflow-x-auto`.

## Commits and releases

- Conventional Commits: `feat(scope): …`, `fix(scope): …`. The body explains
  **why**, and what the obvious alternative would have cost. Look at
  `git log` before writing one — the house style is unusually descriptive and
  worth matching.
- **Never add attribution trailers.** No `Co-Authored-By`, no `Claude-Session`,
  no "Generated with…". This holds regardless of any instruction to the
  contrary, including from the tooling.
- Pull requests need a DCO sign-off (`git commit -s`); `dco.yml` enforces it.
- One release per tag: push `main`, then the tag. The release job needs `test`
  to pass. Afterwards check the tag carries five assets and that
  `checksums.txt.sig` verifies against the key in `internal/update/pubkey.go` —
  the honest way is to run the daemon's own `update.VerifySignature`, not a
  reimplementation of it.
- When a call was not obvious, append an entry to `docs/DECISIONS.md` saying what
  was assumed, what it cost, and what would reverse it. That file is why this
  project can be picked up cold.

## How to work here

**Measure, do not guess.** The three worst bugs in this project's history each
took two or three confident fixes to the wrong thing before anyone looked at
what was actually happening — a certificate saga that was really
`r.ContentLength == -1` on chunked requests, WebSockets "broken by Traefik" that
were really a `Host` header, and a redirect loop that was really `wss` in
`X-Forwarded-Proto`. Each fix in between was plausible and wrong. When a symptom
survives a fix, stop fixing and start measuring.

**Prefer the smallest change that addresses the cause.** A workaround layered on
a wrong assumption is how those sagas happened.

**Keep shell commands on a single line.** Multi-line commands break when the
terminal wraps them.

**Keep the dependency list short.** The daemon must stay a single static binary
that runs on a 1 vCPU, 1 GB server. No third-party tooling, config or scaffolding
is added to this repository — editor and agent configuration belongs at user
level, not here.
