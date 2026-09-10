# Islet — Open-Source Roadmap

> Goal: ship a complete, free, single-server panel that people love before writing a single paid feature. Every phase ends with something a real user can install and use. Paid features start only after v1.0 and a real user base (see `VISION.md` section 4 for the eventual Pro list).

How to read this: each phase has a one-line goal, a list of what the user can do afterwards, the features as checkboxes, what is deliberately left out, and a definition of done. Tick boxes as you go.

---

## Phase 0 — Foundation `v0.1`
**Weeks 1–3. Goal: Islet installs on a clean Ubuntu VPS in under two minutes and shows the server.**

After this phase a user can: run one command, open a URL, log in with 2FA, and see live CPU, memory, disk and network for their box.

**Installer and daemon**
- [ ] `curl -fsSL https://get.islet.dev | sh` for Ubuntu 22.04+ and Debian 12+ on x86_64 and arm64
- [ ] Single static Go binary `isletd`, systemd unit, `islet` system user, state in `/var/lib/islet`
- [ ] First migration: a `servers` table with the local server's row, and a `server_id` column on every machine-bound table (schema convention only, see `DECISIONS.md`)
- [ ] Installs Docker Engine if missing, verifies it works
- [ ] Self-signed HTTPS on first boot, then Let's Encrypt on a `*.sslip.io` hostname automatically
- [ ] One-time login link printed by the installer
- [ ] Self-update command with signature verification, stable and beta channels
- [ ] Uninstall script that leaves Docker and apps running

**Auth and panel shell**
- [ ] Admin account creation, Argon2id passwords, sessions, rate-limited login
- [ ] TOTP 2FA with recovery codes, enforced for admins after first login
- [ ] React UI embedded in the binary, dark and light theme, responsive layout
- [ ] Command palette (Cmd+K) skeleton and navigation
- [ ] Audit log of every panel action, viewable in the UI

**Dashboard**
- [ ] Live CPU, memory, swap, disk, network, load, uptime via SSE
- [ ] 7-day metrics history in SQLite
- [ ] Top processes and listening ports
- [ ] Server info card (OS, kernel, Docker version, public IP, hostname, timezone)

**Terminal**
- [ ] Web terminal (xterm.js) with tabs, reconnect, copy and paste

**Not in this phase:** Docker management UI, domains, anything that writes to the system beyond install.

**Done when:** a fresh Hetzner CX22 goes from creation to a logged-in dashboard in under two minutes, tested by the e2e job.

---

## Phase 1 — Containers and files `v0.2`
**Weeks 4–6. Goal: everything you would open Portainer or SSH for, done in Islet.**

After this phase a user can: manage every container, paste a Compose file and run it, browse and edit any file, and fix a config without leaving the browser.

**Docker**
- [ ] Containers list with state, CPU, memory, ports, uptime; start, stop, restart, remove, recreate
- [ ] Live logs with search, follow, and download
- [ ] Exec terminal into any container
- [ ] Images: list, pull, remove, dangling detection; volumes and networks: list, inspect, remove
- [ ] Compose stacks: create from pasted YAML, validate, up, down, pull and redeploy, edit in place
- [ ] Registry logins (Docker Hub, GHCR, GitLab, private)
- [ ] Resource limits and restart policy editor
- [ ] Prune with dry-run preview

**File explorer**
- [ ] Tree and breadcrumb navigation, bookmarks, hidden files, sort and filter
- [ ] Create, rename, copy, move, delete to trash, restore from trash
- [ ] Permissions dialog (checkbox grid and octal), owner and group, recursive apply, make executable
- [ ] Upload with drag-and-drop and resumable large files, download files and folders as zip
- [ ] Compress and extract archives, checksums
- [ ] Monaco editor with syntax highlighting, diff on save, and validation for known configs
- [ ] Previews for images, PDF, Markdown, CSV, and streaming view for large logs
- [ ] Search by name and by content
- [ ] Docker volumes shown as browsable folders
- [ ] Protected paths need typed confirmation, every write audited

**Disk Doctor**
- [ ] Disk usage breakdown (images, volumes, logs, journal, apt cache) with safe one-click cleanup and preview

**Not in this phase:** domains and SSL for containers, app catalog.

**Done when:** a user can deploy Uptime Kuma by pasting its Compose file, edit its config file, and read its logs, all in the panel.

---

## Phase 2 — Domains, SSL and the app catalog `v0.3`
**Weeks 7–10. Goal: a container gets a domain with HTTPS in under a minute, and popular apps install in one click.**

After this phase a user can: point a domain at any container, get a certificate automatically, install Postgres or n8n from a catalog, and follow a recipe that ends with a working URL.

**Proxy and domains**
- [ ] Traefik managed as a container, config reconciled from Islet's state
- [ ] Add domain, pick container and port, HTTPS issued via HTTP-01
- [ ] DNS helper: shows the exact record to create and polls until it resolves
- [ ] DNS-01 wildcards for Cloudflare, Hetzner DNS, DigitalOcean, Route53, deSEC, Porkbun
- [ ] Per-route options: www and https redirects, basic auth, IP allowlist, rate limit, custom headers, path prefix, maintenance page
- [ ] Certificate inventory with expiry and manual renewal
- [ ] Any Compose stack with Traefik labels works unchanged
- [ ] Panel itself moves to the user's own domain

**App catalog**
- [ ] Catalog format (Compose plus metadata, form fields, post-install notes) and an in-repo `catalog/` folder
- [ ] Launch set of 20 templates: Postgres, MySQL, MariaDB, Redis, MongoDB, MinIO, Uptime Kuma, n8n, Plausible, Umami, Gitea, Vaultwarden, Nginx Proxy Manager, Grafana, Metabase, WordPress, Ghost, code-server, Adminer, Nextcloud
- [ ] Install form with generated secrets, domain picker, volume paths, and resource limits
- [ ] Update checks for installed catalog apps with digest diff

**Recipes**
- [ ] Recipe engine: multi-step wizards with progress and rollback
- [ ] First recipes: "Static site with a domain", "Docker image with a domain", "WordPress with a domain"

**Not in this phase:** git-based deploys, databases as first-class objects.

**Done when:** a new user installs Islet, installs Uptime Kuma from the catalog, and reaches it at `status.example.com` over HTTPS within ten minutes without reading docs.

---

## Phase 3 — Databases, cron and notifications `v0.4`
**Weeks 11–14. Goal: the boring operational work is a form, not a shell session, and the server tells you when something is wrong.**

After this phase a user can: create a Postgres instance with a backup schedule, write and schedule a script from the browser, and get a Telegram message when it fails.

**Databases**
- [ ] First-class database objects for Postgres, MySQL, MariaDB, Redis, Valkey, MongoDB
- [ ] Create databases and users, copyable connection strings for internal and public access
- [ ] Public exposure gated behind a warning and an IP allowlist, with SSH tunnel instructions
- [ ] Postgres extension toggles (pgvector, postgis, pg_stat_statements) and a PgBouncer toggle
- [ ] Embedded Adminer, pgweb and RedisInsight sessions
- [ ] Scheduled dumps to local disk or S3-compatible storage, retention, restore into a new instance
- [ ] Connection count, size and slow query views

**Cron and task runner**
- [ ] Job types: command, inline script, existing file, container exec, one-off container, HTTP request, chain
- [ ] Schedule builder with presets, visual picker, raw cron field, English translation, next five runs, timezone, jitter
- [ ] Script editor with ShellCheck, shebang selector, snippets, version history and revert
- [ ] Managed script storage with correct ownership and executable bit
- [ ] Run-as user, working directory, timeout, overlap policy, retries, nice level
- [ ] Run now with live output, per-run history, duration charts, dead man's switch
- [ ] Import existing crontabs and systemd timers, export any job as a crontab line
- [ ] Template library (DB dump to S3, Docker prune, log cleanup, cert check, rclone sync)

**Notifications and events**
- [ ] Event bus with a catalog of typed events and severities
- [ ] Channels: email (SMTP relay), Telegram with chat detection wizard, Discord webhook, Slack webhook, ntfy, Gotify, Pushover, generic signed webhook
- [ ] Routing matrix by category and severity, per-app overrides, quiet hours, digests, cooldowns, recovery messages
- [ ] Message template with deep links, send-test button, delivery log
- [ ] In-app notification centre and event timeline
- [ ] Outbound SMTP relay setup wizard with DKIM, SPF and DMARC helpers

**Alerts and uptime**
- [ ] Default alert rules: disk over 85%, memory pressure, container crash loop, cert expiring, backup failed, cron failed
- [ ] HTTP, TCP and keyword uptime checks from the server

**Not in this phase:** git deploys, runners, security suite.

**Done when:** a nightly Postgres backup written in the script editor runs on schedule, and a deliberately broken version of it reports the failure to Telegram within a minute.

---

## Phase 4 — Deploys and runners `v0.5`
**Weeks 15–18. Goal: push to main and it is live, with no SSH keys or secrets stored in GitHub.**

After this phase a user can: connect a GitHub repo, get a build on every push, roll back in one click, and run GitHub Actions on their own server.

**App deployments** (full spec in `VISION.md` section 3.17)
- [ ] "New app" flow: Source, Detect, Configure, Services, Deploy, with a preview domain pre-filled
- [ ] Sources: GitHub App (no personal tokens), GitLab, Gitea, public git URL, Docker image, drag-and-drop folder or zip
- [ ] Framework detection with editable install, build, start, output directory and port: Vite, CRA, Next.js, Nuxt, SvelteKit, Astro, Remix, Angular, plain HTML, Node and Bun APIs, Python (FastAPI, Django, Flask), Go, Rust, PHP and Laravel, Rails, Spring Boot, .NET
- [ ] Build strategies chosen automatically: Static, Buildpack (Railpack, Nixpacks fallback), Dockerfile, Compose, Image
- [ ] Static apps: SPA fallback, base path, redirects file, asset caching, brotli, optional password
- [ ] Next.js: standalone output, build-time vs runtime env split, ISR cache volume
- [ ] Environment variables and secrets, `.env` paste, shared env groups, "redeploy to apply" prompt
- [ ] "Add Postgres / MySQL / Redis" buttons that create the service and inject its URL
- [ ] Pipeline: clone, cached install and build, package, pre-deploy command, health check, zero-downtime switch, drain old
- [ ] Deploy history with commit, author, duration and log; instant rollback without rebuild
- [ ] Triggers: push webhook, manual deploy, redeploy, tag rules, CLI, API, runner job; one deploy at a time per app, cancellable
- [ ] Processes: web, worker, scheduler from the same build with instance counts and per-process logs
- [ ] Root directory per app for monorepos; environments (production, staging) with promote-without-rebuild
- [ ] App page: status, logs, restart, shell, metrics, health, crash-loop alert, multiple domains, maintenance page
- [ ] Deploy events wired into notifications
- [ ] Sample repos for each recipe so users can try a deploy before connecting their own code

**Runners**
- [ ] Ephemeral GitHub Actions runners in Docker, registered per repo or org through the GitHub App
- [ ] Autoscale from webhook queue events, label-based routing, cache volumes, cleanup
- [ ] Workflow generator that writes a `deploy.yml` calling the local daemon over a Unix socket
- [ ] GitLab Runner and Gitea act_runner registration
- [ ] Runner dashboard: queue, job history, per-job logs

**CLI and API**
- [ ] `islet` CLI: login, deploy, logs, restart, db shell, cron run, notify
- [ ] REST API with OpenAPI docs and scoped API tokens
- [ ] Recipes: "Next.js + Postgres + domain", "Node API + Redis", "Django", "Rails", "Go", "Laravel"

**Not in this phase:** security suite, backups beyond database dumps.

**Done when:** a Next.js repo goes from "Connect GitHub" to a live HTTPS URL, a push redeploys it with zero downtime, and a rollback restores the previous build, with no secrets added to GitHub.

---

## Phase 5 — Security and backups `v0.6`
**Weeks 19–23. Goal: a Security Score that a beginner can drive to 90 in ten minutes, and backups that restore.**

After this phase a user can: harden SSH and the firewall with one click each, see who tried to log in, scan images for CVEs, and back up the whole server to S3 with tested restores.

**Hardening wizard and Security Score**
- [ ] First-run wizard: sudo user with SSH key, disable root and password login, optional SSH port, UFW rules, fail2ban, unattended-upgrades, swap, timezone, NTP
- [ ] Security Score (0–100) with explanations and one-click fixes, shown on the dashboard
- [ ] SSH settings UI with validation before applying and a rollback timer if you lock yourself out

**Firewall and intrusion prevention**
- [ ] UFW/nftables UI with presets, Docker-aware rules so published ports cannot bypass the firewall
- [ ] Per-app port awareness with warnings for publicly exposed databases
- [ ] fail2ban or CrowdSec with community blocklists, live blocked-IP list, geo-blocking
- [ ] Open ports and listening sockets correlated to containers

**Audit and scanning**
- [ ] Lynis audit on a schedule with score trend
- [ ] Trivy scanning of images and the filesystem, severity filters, findings on the container page
- [ ] rkhunter, file integrity monitoring on `/etc`, SUID and world-writable audits
- [ ] Auth log and sudo log viewers, login-from-new-country alert
- [ ] Pending updates, reboot-required tracking, kernel livepatch status
- [ ] Panic button: block all inbound except the current IP, rotate panel sessions and tokens

**Backups** (full spec in `VISION.md` section 3.16)
- [ ] restic-based backup plans: sources (volumes, bind mounts, database dumps, `/etc`, Islet state), destinations, schedule, retention
- [ ] Destinations: S3-compatible (AWS, R2, B2, Wasabi, Hetzner Object Storage, MinIO), SFTP and Hetzner Storage Box, local disk, another Islet server
- [ ] Presets on first run ("Everything nightly", "Databases hourly", "Config weekly") and a day-one nudge if no plan exists
- [ ] Recovery kit download forced on first setup, with quarterly reminders
- [ ] Retention in plain English with snapshot count, size and monthly cost estimate
- [ ] Consistency: automatic pre-snapshot database dumps, per-app pre and post hooks, pause-during-snapshot toggle
- [ ] Bandwidth and IO limits, resume after interruption, progress and dedup stats, per-run logs
- [ ] Backup health card on the dashboard, stale-backup and failure events through notifications
- [ ] Restore browser: single file or folder, volume into a new volume, database into a new instance, whole app, with dry run
- [ ] Full server restore from a fresh install using the recovery kit, also the migration path between providers
- [ ] Weekly integrity check and monthly automated restore test with a "last verified" badge
- [ ] Write-only credentials and object lock or append-only mode set up automatically for B2, R2 and S3
- [ ] Provider snapshot before risky operations where the provider API is configured
- [ ] `islet backup run|list|restore|verify` in the CLI

**Networking and access**
- [ ] WireGuard server with QR onboarding and a "panel only via VPN" toggle
- [ ] Cloudflare Tunnel and Tailscale integrations
- [ ] Diagnostics: ping, traceroute, dig, port check, bandwidth per interface and container
- [ ] "Protect this app with Islet login" forward-auth toggle on any route

**Not in this phase:** nothing paid; everything here stays free forever.

**Done when:** a fresh server reaches Security Score 90 through the wizard alone, and a full restore onto a second server brings every app back with its data.

---

## Phase 6 — Launch polish `v1.0`
**Weeks 24–28. Goal: the product feels finished, imports existing servers, and is ready for Show HN and r/selfhosted.**

After this phase a user can: adopt a server that already runs things, extend the panel, use it from a phone, and let an AI agent operate it through MCP.

**Adoption and import**
- [ ] Import existing containers, Compose files, Nginx sites, crontabs and systemd units into managed objects
- [ ] Import from Coolify, Dokploy and Portainer stacks

**Platform**
- [ ] Multi-user with Admin, Deployer and Viewer roles scoped to projects
- [ ] MCP server with scoped tokens, off by default
- [ ] Plugin host (WASM) with the first two community-style plugins as examples
- [ ] "Add to sidebar" embedding of any app UI behind Islet login
- [ ] Catalog moved to its own public MIT repo, fetched at runtime, with a contribution guide
- [ ] PWA install, mobile layouts for dashboard, logs, deploys, notifications and file explorer
- [ ] i18n framework with English complete and two community languages
- [ ] Command transparency drawer: every command Islet ran, copyable

**Guides and content**
- [ ] Inline "Why this matters" on every security and infra toggle
- [ ] Teaching empty states across the app
- [ ] 15 recipes total, docs site generated from the repo, screencasts for the top five flows
- [ ] Weekly "your server this week" email report

**Quality**
- [ ] e2e suite on real VPSes for Ubuntu 22.04, 24.04, Debian 12, arm64
- [ ] Signed releases with cosign and SLSA provenance, reproducible builds
- [ ] `SECURITY.md`, CLA bot, trademark policy, code of conduct, issue templates
- [ ] Load test: panel stays responsive on a 1 vCPU, 2 GB box with 30 containers

**Done when:** three outside users install Islet on servers they already run, adopt what is there, and report nothing broke. Then launch.

---

## After v1.0

Grow the user base and the catalog for at least a release cycle before starting paid work. When the time comes, the Pro plan in `VISION.md` section 4 and the gating design in `REPOS_AND_MONETIZATION.md` are ready. The first paid feature should be the multi-server Hub, because that is what users with two servers ask for first.

Ideas that stay free and can be picked up any time after launch, roughly in order of demand:
- RHEL family support (AlmaLinux, Rocky, Fedora)
- Language runtime installer and PM2-style systemd process manager for non-Docker apps
- PHP-FPM hosting with per-site PHP versions
- rclone remotes and external volume mounts in the file explorer
- Server-side uptime status page
- GPU driver installer and Ollama, vLLM and Open WebUI templates
- WAF toggle per route (Coraza with OWASP CRS)
- SBOM per image and image signature verification
- More import sources: Heroku, Render, Railway, cPanel
- Podman support, Docker Swarm mode
