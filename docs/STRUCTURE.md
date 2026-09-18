# Repository structure

One Go module, one pnpm workspace, one binary. A few folders (`reconcile/`, `recipes/`, `runners/`, `plugins/`, `pkg/client/`, `web/packages/ui/`, `e2e/`) are still empty placeholders; `runner/` (singular) holds the CI runner code, and `mcp/`, `uptime/`, `watch/`, `update/`, `tlsutil/`, `cmdrun/`, `version/` and `web/` exist beyond the list below.

```
islet/
├─ cmd/
│  ├─ isletd/            Daemon entrypoint: wires store, API, reconciler, collectors, embedded UI
│  └─ islet/             CLI entrypoint for development; the release ships isletd, which dispatches
│                        into internal/cli when invoked through the islet symlink
│
├─ internal/             Private packages, not importable from outside this module
│  ├─ api/               HTTP routes, OpenAPI spec, middleware (auth, roles, audit, rate limit), SSE and WebSocket
│  ├─ cli/               The islet command line: login, apps, deploy, logs, cron, db, backup, update
│  ├─ auth/              Users, roles, Argon2id, sessions, TOTP and passkeys, API tokens
│  ├─ store/             SQLite (WAL) access, migrations, desired-state tables, metrics ring buffer
│  ├─ reconcile/         Desired-state engine: diff records in store against the system, apply, report drift
│  ├─ docker/            Docker Engine API wrapper: containers, images, volumes, networks, Compose stacks, registries
│  ├─ proxy/             Traefik lifecycle and dynamic config generation from domain and route records
│  ├─ catalog/           Loads app templates and their forms, renders Compose, tracks installed apps and updates
│  ├─ recipes/           Multi-step wizard engine with progress and rollback
│  ├─ deploy/            Git sources, GitHub App, buildpacks, builds, zero-downtime rollout, rollbacks, env and secrets
│  ├─ runners/           Ephemeral GitHub, GitLab and Gitea runners, autoscaling, workflow generator
│  ├─ db/                Database provisioning, users and databases, extensions, dumps and restores
│  ├─ cron/              Scheduler, job types, script store with versions, run history, dead man's switch
│  ├─ files/             File explorer backend: listing, streaming read and write, trash, permissions, archives, search
│  ├─ terminal/          PTY sessions for the web terminal and container exec
│  ├─ notify/            Event bus, event catalog, channels (Telegram, Discord, Slack, email, ...), routing, outbox
│  ├─ security/          Hardening wizard, Security Score, firewall, fail2ban/CrowdSec, Lynis, Trivy, auth log parsing
│  ├─ backup/            restic wrapper, targets, schedules, restore browser, server export and import
│  ├─ uploads/           Files handed to the assistant: stored under the data directory, scanned, then an absolute path
│  ├─ metrics/           Collectors for host and container metrics, disk forecast, alert rules
│  └─ plugins/           WASM plugin host and capability manifests (phase 6)
│
├─ pkg/                  Public packages, importable by third parties and by the future Hub
│  ├─ api/               Request and response types, event schemas, shared with the web app via codegen
│  └─ client/            Go client for the daemon API, used by the CLI
│
├─ web/                  pnpm workspace
│  ├─ app/               The panel: React 19, Vite, TypeScript, Tailwind v4, react-router-dom, own components
│  └─ packages/ui/       Shared components and design tokens, reusable by a future Hub frontend
│
├─ catalog/              In-repo for now, moves to its own MIT repo in phase 6
│  ├─ apps/              One folder per app: compose.yml, islet.yaml (form, ports, volumes, notes), icon
│  ├─ recipes/           Wizard definitions
│  └─ scripts/           Cron and script templates
│
├─ brand/                Brand source of truth: README (guidelines), tokens/ (CSS, JSON, Tailwind preset),
│                        logo/ (SVG + PNG), icons/ (iOS, Android, Expo, web), fonts/ (OFL files + fonts.css), social/
│
├─ installer/            get.sh and uninstall.sh
├─ deploy/
│  ├─ systemd/           isletd.service and hardening drop-ins
│  └─ goreleaser/        Release config, cosign signing, apt and rpm packaging
├─ e2e/                  Tests that create a real VPS, run the installer, and drive the panel with Playwright
├─ docs/                 Planning documents and, later, the generated docs site source
└─ .github/workflows/    CI: lint, unit tests, UI build, e2e on demand, signed releases
```

## Conventions to adopt from the first commit

- Every feature is a record in `store` plus a reconciler, never a one-off shell command. This is what makes export, import and drift detection possible later.
- Every command the daemon executes goes through one runner that logs to the audit table, so the command transparency drawer works for free.
- `internal/api` is the only place that knows about HTTP. Business logic lives in the feature packages and is tested without a server.
- `pkg/api` types are mirrored by hand in `web/app/src/lib/api.ts`; both sides change together. Codegen from the OpenAPI spec was the intention and has not been built.
- No feature folder imports another feature folder directly. They communicate through `store`, `reconcile` and `notify` events.
- Every table that describes something running on a machine carries a `server_id` column from the first migration, always the local server for now. Queries filter on it from day one so scale-out never needs a schema rewrite.
