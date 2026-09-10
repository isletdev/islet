# Decisions log

Short record of decisions that shape the code. Newest at the bottom. Each entry: what was decided, why, and what it rules out.

## 2026-09-10 — Product name: Islet
Chosen after four naming rounds. Registry lookups on that date showed `islet.dev`, `islet.sh` and `islet.run` unregistered. GitHub org will be `isletdev`. Binaries: `isletd` (daemon), `islet` (CLI).

## 2026-09-10 — Open source first, paid later
Ship the complete single-server panel under AGPL-3.0 through v1.0 and build a user base before any paid work. Pro design is captured in `VISION.md` section 4 and `REPOS_AND_MONETIZATION.md` but nothing paid is scheduled.

## 2026-09-10 — Daemon language: Go
Single static binary, low memory on cheap VPSes, native Docker SDK, easy `curl | sh` install, arm64 for free. Rules out a Node or Bun runtime on the server.

## 2026-09-10 — Frontend: React + Vite + TypeScript
Static single-page app embedded in the Go binary via `embed.FS`. Tailwind and shadcn/ui for components. Chosen over Next.js because the panel needs no server-side rendering and must add zero runtime cost on the box. Rules out any Node process on the server for the panel.

## 2026-09-10 — Resource budget
The panel must stay usable on a 1 vCPU, 2 GB VPS with 30 containers. Targets: daemon under 60 MB RSS idle, UI bundle under 1 MB gzipped, no background CPU above 1% when idle. These numbers are goals for phase 0 and gates for v1.0.

## 2026-09-10 — Legal owner: Torsten Labs DOO
Torsten Labs DOO, North Macedonia (https://torstenlabs.com) owns the copyright, trademark and domains. The CLA names Torsten Labs DOO as the licensee. Billing provider must accept sellers in North Macedonia; verify Paddle or Lemon Squeezy eligibility before any paid work.

## 2026-09-10 — Scale-out not scheduled
Design captured in `VISION.md` section 3.18. Not on the roadmap.

## 2026-09-10 — Server id on machine-bound records from the first migration
Every table that describes something running on a machine (containers, app copies, processes, metrics, cron runs, logs, backups runs) carries a `server_id` column from the first migration, always the local server's id for now. Cheap today, avoids rewriting every table and query if scale-out (`VISION.md` section 3.18) is ever built. This is a schema convention only; no multi-server behaviour is scheduled.

## 2026-09-10 — Brand: Islet visual identity v2 (v1 rejected)
v1 (split-disc mark, lowercase wordmark, teal and sand palette, "Your own little island") was rejected as soft and generic. v2: abstract geometric mark, a square with its corner piece set apart; wordmark "Islet" in Geist 600; black and white palette with one blue accent for links and focus only; Geist and Geist Mono, both SIL OFL, self-hosted; tagline "Own your infrastructure." References: Vercel, Linear, Raycast for finish, Tailscale and Cloudflare for trust. Guidelines in `brand/README.md`, tokens in `brand/tokens/`.

## 2026-09-10 — Website: static, separate repo, Astro
The marketing site and legal pages are static content, so no Next.js: static HTML is the best SEO and adds no runtime. Plan: Astro in a separate public repo (`isletdev/website`), deployed to Cloudflare Pages, legal pages as Markdown routes, fonts self-hosted. A single-file sample in the v2 brand lives outside the core repo at `../islet-website/index.html`; its legal texts are drafts and must be reviewed by a lawyer and completed with the company's registration details before publishing.

## 2026-09-10 — Panel TLS: self-signed on first boot, ACME with the proxy
The daemon serves HTTPS from the first request with a certificate it issues itself (ECDSA P-256, ten years, SANs for hostname, local IPs and the public IP's sslip.io name). The installer prints the SHA-256 fingerprint. Publicly trusted certificates for the panel come with Traefik in v0.3, because ACME needs ports 80 or 443 and those belong to the proxy; running a second ACME client in the daemon would fight it. Plain HTTP exists only behind `-tls off` for development.

## 2026-09-10 — Release signing: Ed25519 key held by the maintainer
Releases are built by goreleaser on tag push. `checksums.txt` is signed with an Ed25519 key (`tools/sign`); the public key is embedded in `internal/update`, and the daemon refuses any update whose checksums do not verify, then refuses any archive whose SHA-256 does not match. The private key lives only on the maintainer's machine at `~/.islet/release-signing.key` and in the GitHub Actions secret `ISLET_SIGNING_KEY`. Losing it means shipping a key rotation release signed by nothing, so it is backed up offline. Sigstore keyless signing was considered and deferred: verifying it in the daemon pulls in a large dependency tree for no gain over a pinned key at this stage.

## 2026-09-10 — Notifications: one bus, delivery outbox, criticals always pass
Every feature emits typed events (category, severity, title, message, deep link) on one bus. Channels filter by category, minimum severity and quiet hours; criticals ignore quiet hours. Deliveries are rows in an outbox retried with backoff, so a channel outage never loses an alert. Warnings with the same title are suppressed for ten minutes and recoveries are info events with the same title, which closes the loop without a separate "resolved" concept.

## 2026-09-10 — Cron: robfig/cron for parsing and scheduling, heartbeats for external jobs
Jobs are scheduled in-process (no system crontab), scripts are files under the data directory with the executable bit, every run is recorded with its output. Jobs that run elsewhere register as heartbeats with a ping URL, which gives a real dead man's switch instead of only "did our own scheduler fire".

## 2026-09-10 — Databases: everything through docker exec
Database work (listing, users, dumps, restores, extensions) runs inside the instance's own container, so the host needs no client tools and versions always match the server. Dumps land under the data directory; scheduled dumps are ordinary cron jobs so they inherit history and alerts. Publishing a port edits the stack's Compose file rather than tweaking the container, so the change survives recreation.

## 2026-09-10 — Deploys: generated Dockerfiles instead of Railpack for now
Node, Python, Go and static sites build from a Dockerfile Islet writes into `.islet/` in the checkout and shows in the deploy log. It keeps the build transparent and needs no extra binary. Railpack or Nixpacks stay planned for languages without a generated Dockerfile. Zero downtime comes from starting the new container, health-checking it over the proxy network, re-pointing the Traefik route and draining the old one; rollbacks re-point to a kept image.

## 2026-09-10 — Proxy: per-host HTTP to HTTPS redirect, not a global one
Traefik's entrypoint-level redirect made HTTP-only routes unreachable. The redirect is now a router per host on the `web` entrypoint, and the proxy container carries an `islet.proxy.args` label so the daemon recreates it when the arguments change.

## 2026-09-10 — HTTP server: no read timeout on the panel listener
A read timeout on the listener cancels handler contexts for long streams (deploys, logs, backups) after it elapses. Header reads stay bounded at ten seconds; bodies are drained before streaming so closing a connection never resets the client.

## 2026-09-10 — API tokens: scoped bearer tokens, tokens cannot mint tokens
Tokens act with their owner's role narrowed by scopes matched against method and path. They never create other tokens, so a leaked CI token cannot escalate. The MCP endpoint accepts only tokens, never browser sessions, and is off by default.

## 2026-09-10 — Runners: personal access token until the GitHub App exists
Ephemeral GitHub runners need a registration token per job, which needs either a GitHub App or a PAT. The PAT is stored encrypted and only used to mint short-lived registration tokens; the GitHub App replaces it once registered. GitLab and Gitea runners are long-lived containers because their tokens are per runner.

## 2026-09-10 — Security fixes run as root through the daemon
One-click fixes (ufw, fail2ban, unattended upgrades, sshd drop-in) are shell commands recorded in the command log, not a separate agent. SSH changes are validated with `sshd -t` and roll back after five minutes unless confirmed, so a mistake cannot lock the admin out permanently.

## 2026-09-10 — Backups: restic in a container, one repository per destination
restic runs from the pinned `restic/restic` image with sources mounted read-only, so the host installs nothing and volumes are backed up by mount rather than by path. Each destination has its own repository and generated key, kept encrypted and exported in the recovery kit. Retention always keeps the last three snapshots on top of the daily/weekly/monthly/yearly policy.

## 2026-09-11 — MCP: minimal JSON-RPC over HTTP, tools mirror the API scopes
The MCP server is a stateless Streamable HTTP endpoint implemented in-house (a few hundred lines) rather than a dependency, exposing sixteen tools that call the same service methods as the HTTP API and are gated by the same scope table.
