# Working on Islet

A single-server control panel: one Go module, one pnpm workspace, one static
binary. The daemon (`isletd`) serves a REST API and the embedded React panel;
the same binary is the `islet` CLI through a symlink. It manages Docker,
Traefik, certificates, databases, cron, backups and the host itself, so most
bugs here are about a real system's state, not about code in isolation.

## Read before changing anything

| File | What it answers |
|---|---|
| `../docs/STATUS.md` | where the work stands and what to pick up — **start here** |
| `../docs/ROADMAP.md` | the phased plan, with checkboxes |
| `../docs/DECISIONS.md` | why a non-obvious call was made. Long; the entries nearest the end explain the current code |
| `../docs/STRUCTURE.md` | what every folder is for |
| `../CONTRIBUTING.md` | build and dev-server commands |
| `../docs/AGENT_SETUP.md` | running an agent on a server Islet manages, and the machine rules that do not belong in this file |

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
  it. Uniqueness has to be scoped to it as well — `UNIQUE (server_id, name)`,
  not `name TEXT UNIQUE` — and five tables written before `0020` still have the
  global form: `apps.name`, `domains.host`, `backup_destinations.name`,
  `backup_plans.name`, `runner_pools.name`. That is latent rather than live,
  since a managed server runs its own daemon with its own database, and fixing
  it means a table rebuild each because SQLite cannot drop an implicit index.
  Write new tables the scoped way; do not copy those five.
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

## Skills

`.claude/skills/` travels with the repository, so the same skills are available
wherever it is cloned — a laptop, or a workspace on the server. Each is a folder
holding a `SKILL.md` whose frontmatter carries a `name` and a `description` of
when to use it; they load by matching that description, or by name with
`/<name>`.

`islet-commit` is the one specific to this project: the commit identity,
Conventional Commits, the clean-repo check, and the rule that no AI-attribution
trailer is ever added. The rest are general design and UI skills.

## This is a server with other things on it

When this repository is checked out on a machine that also serves live
applications — which is the normal case, since Islet manages exactly such
machines — everything on the box that is not this project is out of scope.

Look freely. `docker ps`, `docker compose ls`, `systemctl status`, reading a
config to understand how something is wired: all fine, on anything.

Change nothing outside this project. That means, against any container, unit,
file or database that is not Islet's own:

- no `docker stop / start / restart / kill / rm / exec / cp`
- no `docker compose up / down / restart / pull` on another project
- no `docker volume rm`, `docker network rm`, `docker image rm`
- **never** `docker system prune`, `docker volume prune` or `docker image
  prune` — they act on the whole daemon, cannot be scoped to one project, and
  read as routine housekeeping right up until an unrelated application is gone
- no `systemctl` stop, start, restart or disable on someone else's unit
- no edits under another application's directory, under `/etc`, or to shared
  configuration
- no `ufw` / `iptables` / `nftables` changes
- no connecting to, dumping or altering a database that is not this project's
- no binding a port already in use, and no killing a process you did not start
- no `git push`, no `git tag`, nothing else that publishes, without being asked

If a task appears to need one of these, stop and say so: what you want to
touch, why the task needs it, and what you expect to happen. Do not route
around it and do not do it "just to check". A blocked task is a normal
outcome; an application somebody depends on going down, so that a task could
finish faster, is not.

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
