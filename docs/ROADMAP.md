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
- [x] First migration: a `servers` table with the local server's row, and a `server_id` column on every machine-bound table (schema convention only, see `DECISIONS.md`)
- [x] Installs Docker Engine if missing; the daemon verifies it at startup and reports the version
- [x] Self-signed HTTPS on first boot (Let's Encrypt for the panel's own domain arrives with the proxy in v0.3)
- [x] One-time login link printed by the installer
- [x] Self-update command with signature verification, stable and beta channels
- [x] Uninstall script that leaves Docker and apps running

**Auth and panel shell**
- [x] Admin account creation, Argon2id passwords, sessions, rate-limited login
- [x] TOTP 2FA with recovery codes; admins without it see a persistent banner until they enable it
- [x] React UI embedded in the binary, dark and light theme, responsive layout
- [x] Command palette (Cmd+K) skeleton and navigation
- [x] Audit log of every panel action, viewable in the UI

**Dashboard**
- [x] Live CPU, memory, swap, disk, network, load, uptime via SSE
- [x] 7-day metrics history in SQLite
- [x] Top processes and listening ports
- [x] Server info card (OS, kernel, hostname, timezone; Docker version and public IP once those features exist)

**Terminal**
- [x] Web terminal (xterm.js) with reconnect, copy and paste (tabs pending)

**Not in this phase:** Docker management UI, domains, anything that writes to the system beyond install.

**Done when:** a fresh Hetzner CX22 goes from creation to a logged-in dashboard in under two minutes, tested by the e2e job.

---

## Phase 1 — Containers and files `v0.2`
**Weeks 4–6. Goal: everything you would open Portainer or SSH for, done in Islet.**

After this phase a user can: manage every container, paste a Compose file and run it, browse and edit any file, and fix a config without leaving the browser.

**Docker**
- [x] Containers list with state, CPU, memory, ports, uptime; start, stop, restart, remove (recreate via stack update)
- [x] Live logs with search and follow (download pending)
- [x] Exec terminal into any container
- [x] Images: list, pull, remove, dangling detection; volumes and networks: list, inspect, remove
- [x] Compose stacks: create from pasted YAML, validate, up, down, pull and redeploy, edit in place
- [x] Registry logins (Docker Hub, GHCR, GitLab, private), stored encrypted and applied with docker login
- [x] Resource limits and restart policy editor
- [x] Prune with a disk-usage preview of what is reclaimable

**File explorer**
- [x] Breadcrumb navigation, hidden files toggle, filter (tree and bookmarks pending)
- [x] Create, rename, copy, move, delete to trash, restore from trash
- [x] Permissions by octal mode with recursive apply; owner and group shown (chown API exists, dialog pending)
- [x] Multi-file upload, download files and folders as zip (drag-and-drop and resumable pending)
- [x] Compress and extract archives, checksums
- [x] CodeMirror editor with syntax highlighting and Ctrl+S (diff on save and config validation pending); chosen over Monaco for bundle size
- [x] Image preview and tail view for large files (PDF, Markdown and CSV previews pending)
- [x] Search by name and by content
- [x] Docker volumes link into the file explorer at their mount point
- [x] Protected paths need typed confirmation, every write audited

**Disk Doctor**
- [x] Disk usage breakdown (Docker, journal, apt cache, logs, tmp, trash) with safe one-click cleanup; volumes are excluded on purpose

**Not in this phase:** domains and SSL for containers, app catalog.

**Done when:** a user can deploy Uptime Kuma by pasting its Compose file, edit its config file, and read its logs, all in the panel.

---

## Phase 2 — Domains, SSL and the app catalog `v0.3`
**Weeks 7–10. Goal: a container gets a domain with HTTPS in under a minute, and popular apps install in one click.**

After this phase a user can: point a domain at any container, get a certificate automatically, install Postgres or n8n from a catalog, and follow a recipe that ends with a working URL.

**Proxy and domains**
- [x] Traefik managed as a container, config reconciled from Islet's state
- [x] Add domain, pick container and port, HTTPS issued via HTTP-01 (issuance itself needs a public server to verify)
- [x] DNS helper: shows the exact record to create and rechecks on demand
- [x] DNS-01 wildcards for Cloudflare, Hetzner DNS, DigitalOcean, Route53, deSEC, Porkbun, Gandi, OVH, Namecheap, Linode, Vultr, Scaleway (credentials encrypted; issuance unverified until a real domain exists)
- [x] Per-route options: www and https redirects, basic auth, IP allowlist, rate limit, custom headers, path prefix, maintenance page
- [x] Certificate inventory with expiry (manual renewal pending)
- [x] Any Compose stack with Traefik labels works unchanged (Docker provider enabled, exposedByDefault off)
- [x] Panel itself moves to the user's own domain (target type "panel")

**App catalog**
- [x] Catalog format (Compose plus metadata, form fields, post-install notes) and an in-repo `catalog/` folder, embedded in the binary
- [x] Launch set of 21 templates: Postgres, MySQL, MariaDB, Redis, MongoDB, MinIO, Uptime Kuma, n8n, Plausible, Umami, Gitea, Vaultwarden, Nginx Proxy Manager, Grafana, Metabase, WordPress, Ghost, code-server, Adminer, Nextcloud, whoami
- [x] Install form with generated secrets and domain picker (volume paths and resource limits pending)
- [x] Update checks for installed catalog apps with digest diff and a one-click update

**Recipes**
- [ ] Recipe engine: multi-step wizards with progress and rollback
- [x] First recipes as guides in `docs/recipes/`: static site, Docker image, WordPress
- [ ] Recipes as in-panel wizards

**Not in this phase:** git-based deploys, databases as first-class objects.

**Done when:** a new user installs Islet, installs Uptime Kuma from the catalog, and reaches it at `status.example.com` over HTTPS within ten minutes without reading docs.

---

## Phase 3 — Databases, cron and notifications `v0.4`
**Weeks 11–14. Goal: the boring operational work is a form, not a shell session, and the server tells you when something is wrong.**

After this phase a user can: create a Postgres instance with a backup schedule, write and schedule a script from the browser, and get a Telegram message when it fails.

**Databases**
- [x] First-class database objects for Postgres, MySQL, MariaDB, Redis, MongoDB (catalog installs appear on the Databases page)
- [x] Create databases and users, copyable connection strings for internal and public access
- [x] Public exposure gated behind a warning, on a chosen host port, with SSH tunnel instructions
- [ ] IP allowlist for published database ports (needs the firewall, v0.6)
- [x] Postgres extension toggles (pg_stat_statements, pg_trgm, pgcrypto, uuid-ossp, hstore, citext; pgvector/postgis when the image has them)
- [ ] PgBouncer toggle
- [ ] Embedded Adminer, pgweb and RedisInsight sessions (Adminer is in the catalog; deep links pending)
- [x] Dump now, scheduled dumps to local disk as an editable cron job, retention, restore into any database, download
- [x] Dumps reach S3-compatible storage through backup plans (database sources are dumped into the restic repository)
- [x] Connection count, size, uptime and slow query view (pg_stat_statements)

**Cron and task runner**
- [x] Job types: command, inline script, existing file, container exec, one-off container, HTTP request, chain, heartbeat
- [x] Schedule builder with presets, raw cron field, English translation, next five runs, timezone, jitter
- [x] Visual schedule builder (every N minutes, hourly, daily, chosen weekdays, monthly)
- [x] Script editor with ShellCheck (when installed), shebang selector, templates, version history and revert
- [x] Managed script storage with correct ownership and executable bit
- [x] Run-as user, working directory, timeout, overlap policy, retries, nice level
- [x] Run now with live output, per-run history, duration chart, dead man's switch (heartbeat jobs with a ping URL)
- [x] Import existing crontabs (root crontab and /etc/cron.d), export any job as a crontab line
- [x] Import systemd timers (OnCalendar converted to cron, jobs start paused)
- [x] Template library (DB dump to S3, MySQL dump, Docker prune, log cleanup, cert check, rclone sync, security updates)

**Notifications and events**
- [x] Event bus with a catalog of typed events and severities
- [x] Channels: email (SMTP relay), Telegram with chat detection wizard, Discord webhook, Slack webhook, ntfy, Gotify, Pushover, generic signed webhook
- [x] Routing matrix by category and severity, quiet hours, cooldowns, recovery messages
- [x] Hourly and daily digests per channel (criticals still go out at once)
- [ ] Per-app overrides
- [x] Message template with deep links, send-test button, delivery log
- [x] In-app notification centre and event timeline
- [ ] Outbound SMTP relay setup wizard with DKIM, SPF and DMARC helpers

**Alerts and uptime**
- [x] Default alert rules: disk over 85%, memory pressure, CPU saturation, container exit/OOM/crash loop, cert expiring, failed logins, update available
- [x] Alert rules for cron failed and heartbeat missed, with recovery
- [x] Alert rules for backup failed and backup stale
- [x] HTTP, TCP and keyword uptime checks from the server, with latency history and down/recovery events

**Not in this phase:** git deploys, runners, security suite.

**Done when:** a nightly Postgres backup written in the script editor runs on schedule, and a deliberately broken version of it reports the failure to Telegram within a minute.

---

## Phase 4 — Deploys and runners `v0.5`
**Weeks 15–18. Goal: push to main and it is live, with no SSH keys or secrets stored in GitHub.**

After this phase a user can: connect a GitHub repo, get a build on every push, roll back in one click, and run GitHub Actions on their own server.

**App deployments** (full spec in `VISION.md` section 3.17)
- [x] "New app" flow: Source, Detect, Configure, Deploy, with a preview domain pre-filled
- [x] "Services" step: add Postgres, MySQL or Redis from the app page
- [x] Sources: any git URL (public, or with a token stored encrypted), local path, Docker image
- [x] GitHub App: repository picker, installation tokens for private clones, one webhook for pushes and CI jobs (code complete; needs the app registered to exercise)
- [ ] Drag-and-drop folder or zip
- [x] Framework detection with editable install, build, start, output directory and port: Vite, CRA, Next.js, Nuxt, SvelteKit, Astro, Remix, Angular, Gatsby, plain HTML, Node and Bun APIs, Python (FastAPI, Django, Flask, uv, Poetry), Go, Dockerfile, Compose
- [x] Detection for Rust, PHP and Laravel, Rails, Spring Boot, .NET
- [x] Build strategies chosen automatically: Static, generated Dockerfile (Node, Python, Go), Dockerfile, Compose, Image
- [ ] Railpack/Nixpacks as the buildpack for languages without a generated Dockerfile
- [x] Static apps: SPA fallback, `_redirects` file, hashed-asset caching, gzip, custom 404, password via the domain's basic auth
- [x] Static apps: base path (host/prefix domains); brotli comes from the proxy compress middleware
- [x] Build-time (NEXT_PUBLIC_*, VITE_*, PUBLIC_*) vs runtime env split; persistent paths for caches
- [x] Next.js standalone output detection
- [x] Environment variables stored encrypted, `.env` paste, changes apply on the next deploy
- [x] Shared env groups (@name lines in an app's environment)
- [x] "Add Postgres / MySQL / Redis" buttons that install the instance, create a database and inject DATABASE_URL or REDIS_URL
- [x] Pipeline: clone, layer-cached build, pre-deploy command, health check, zero-downtime switch, drain old
- [x] Deploy history with commit, author, duration and log; instant rollback without rebuild
- [x] Triggers: push webhook (GitHub, GitLab, Gitea signatures), manual deploy, redeploy, API; one deploy at a time per app, cancellable
- [x] Trigger: CLI (`islet deploy`)
- [x] Trigger: tag rules (branch field tag:v* deploys matching tag pushes)
- [x] Trigger: CI passed (GitHub workflow_run through the GitHub App, or a call from any CI job)
- [x] Processes: web, worker, scheduler from the same build with instance counts and per-process logs
- [x] Root directory per app for monorepos; environments (production, staging) with promote-without-rebuild
- [x] App page: status, releases, live deploy log, link to container logs and shell, crash-loop alert via container events, maintenance page via the domain
- [x] Multiple domains per app (comma separated, first is primary)
- [x] Deploy events wired into notifications (started, deployed, failed with the last 30 log lines)
- [ ] Sample repos for each recipe so users can try a deploy before connecting their own code

**Runners**
- [x] Ephemeral GitHub Actions runners in Docker, registered per repo or org through the GitHub App or a personal access token (unverified against GitHub until credentials exist)
- [x] Autoscale from workflow_job webhook events with an idle floor and a maximum, labels, a work cache volume, exited-container sweep
- [x] Workflow generator that writes a `deploy.yml` calling the daemon API with a scoped token
- [x] Root-only Unix socket: the local CLI needs no token on the server; runners can mount it
- [x] GitLab Runner and Gitea act_runner registration (long-lived, one container per pool; unverified)
- [x] Runner dashboard: pools, idle/busy runners, job history from webhooks, container logs

**CLI and API**
- [x] `islet` CLI: login, whoami, apps, deploy (with rollback), logs, restart, cron list/run, notify, db list/shell
- [x] Scoped API tokens (Settings → API tokens) with bearer auth
- [x] REST API described in `docs/openapi.yaml`
- [x] Recipes as guides: Next.js + Postgres + domain, Node API + Redis, Django, Go
- [x] Recipes: Rails, Laravel

**Not in this phase:** security suite, backups beyond database dumps.

**Done when:** a Next.js repo goes from "Connect GitHub" to a live HTTPS URL, a push redeploys it with zero downtime, and a rollback restores the previous build, with no secrets added to GitHub.

---

## Phase 5 — Security and backups `v0.6`
**Weeks 19–23. Goal: a Security Score that a beginner can drive to 90 in ten minutes, and backups that restore.**

After this phase a user can: harden SSH and the firewall with one click each, see who tried to log in, scan images for CVEs, and back up the whole server to S3 with tested restores.

**Hardening wizard and Security Score**
- [x] One-click fixes: disable root and password login, UFW rules, fail2ban, unattended-upgrades, security upgrades, swap, NTP
- [x] "Fix everything safe" runs updates, fail2ban, swap, NTP and the firewall in order
- [ ] Guided sudo user, SSH key and timezone steps
- [x] Security Score (0–100) with explanations and one-click fixes
- [x] Security Score card on the dashboard
- [x] SSH settings UI with validation (sshd -t, authorized_keys present) and a five-minute rollback timer

**Firewall and intrusion prevention**
- [x] UFW UI with the standard preset, allow and remove rules, Docker-aware DOCKER-USER rules so published ports honour the firewall
- [x] Published database ports and Docker socket mounts flagged in the score
- [x] fail2ban with sshd and recidive jails, live blocked-IP list with unban
- [ ] CrowdSec community blocklists, geo-blocking
- [x] Listening ports correlated to the container publishing them

**Audit and scanning**
- [x] Lynis audit on demand with the hardening index in the Security Score and a stored history
- [x] Lynis weekly once it has been run by hand
- [x] Trivy image scanning (in a container), stored results with critical and high findings, events on criticals
- [ ] Filesystem scans, findings on the container page
- [ ] rkhunter, file integrity monitoring on `/etc`, SUID and world-writable audits
- [x] Auth log viewer (Logs → SSH logins)
- [x] Login-from-new-address alert (first sign-in from an IP raises a warning)
- [ ] Country lookup for that alert
- [x] Pending security updates and reboot-required in the score
- [x] Kernel livepatch status (when canonical-livepatch is installed)
- [x] Panic button: block all inbound except the current IP, revoke every other session and all API tokens

**Backups** (full spec in `VISION.md` section 3.16)
- [x] restic-based backup plans: sources (volumes, host paths, database dumps, Islet state), destinations, schedule, retention; restic runs in a container
- [x] Destinations: S3-compatible (AWS, R2, B2, Wasabi, Hetzner Object Storage, MinIO), SFTP and Hetzner Storage Box, local disk, restic REST server
- [ ] "Another Islet server" as a one-click destination (rest-server hosted by Islet)
- [x] Presets ("Everything nightly", "Databases hourly", "Config weekly") and a nudge while no plan exists
- [x] Recovery kit download (repositories, keys, credentials, restore instructions)
- [x] Recovery kit banner until downloaded, daily reminder event while missing, quarterly reminder after
- [x] Retention in plain English with the maximum snapshot count
- [x] Repository size after every run with a rough monthly cost
- [x] Consistency: automatic pre-snapshot database dumps
- [ ] Per-app pre and post hooks, pause-during-snapshot toggle
- [x] Progress and dedup stats (bytes added vs processed), per-run logs, resumable uploads (restic)
- [ ] Bandwidth and IO limits
- [x] Backup health summary on the Backups page, stale-backup and failure events through notifications, backup plan counted in the Security Score
- [x] Backup health in the dashboard's attention strip
- [x] Restore browser: single file or folder to disk, volume into a new volume
- [ ] Database into a new instance, whole app, dry run
- [ ] Full server restore from a fresh install using the recovery kit, also the migration path between providers
- [x] Weekly integrity check (restic check with a data sample) and a "last verified" date
- [x] Monthly automated restore test (pulls Islet state or a dump out of the latest snapshot into a scratch folder), with a manual button
- [ ] Write-only credentials and object lock or append-only mode set up automatically for B2, R2 and S3
- [ ] Provider snapshot before risky operations where the provider API is configured
- [x] `islet backup list|run|snapshots|verify|restore` in the CLI

**Networking and access**
- [x] WireGuard (wg-easy) in the catalog with QR onboarding; "panel only via VPN" via a firewall rule (documented in the app notes)
- [x] One-click "panel only via VPN": restrict the panel port to a CIDR, keep the admin's IP as fallback, undo button
- [x] Cloudflare Tunnel and Tailscale in the catalog
- [x] Diagnostics: ping, traceroute, dig, port check (Security page)
- [ ] Bandwidth per interface and container
- [x] "Protect this app with Islet login" forward-auth toggle on any route

**Not in this phase:** nothing paid; everything here stays free forever.

**Done when:** a fresh server reaches Security Score 90 through the wizard alone, and a full restore onto a second server brings every app back with its data.

---

## Phase 6 — Launch polish `v1.0`
**Weeks 24–28. Goal: the product feels finished, imports existing servers, and is ready for Show HN and r/selfhosted.**

After this phase a user can: adopt a server that already runs things, extend the panel, use it from a phone, and let an AI agent operate it through MCP.

**Adoption and import**
- [x] Adopt existing Compose projects into managed stacks; crontab import
- [x] Import nginx sites as domains (proxied server blocks) and systemd timers as jobs
- [x] Compose projects created by Coolify, Dokploy or Portainer show up as adoptable stacks
- [ ] Import their app metadata (domains, env) rather than only the Compose file

**Platform**
- [x] Multi-user with Admin, Deployer and Viewer roles (Settings → Users)
- [ ] Roles scoped to projects
- [x] MCP server (Streamable HTTP) with scoped tokens, off by default, 16 tools
- [ ] Plugin host (WASM) with the first two community-style plugins as examples
- [ ] "Add to sidebar" embedding of any app UI behind Islet login
- [ ] Catalog moved to its own public MIT repo, fetched at runtime, with a contribution guide
- [x] PWA install with a service worker for the app shell
- [ ] Dedicated mobile layouts for logs, deploys, notifications and the file explorer
- [ ] i18n framework with English complete and two community languages
- [x] Command transparency: every command Islet ran, copyable (Settings)

**Guides and content**
- [ ] Inline "Why this matters" on every security and infra toggle
- [ ] Teaching empty states across the app
- [ ] 15 recipes total, docs site generated from the repo, screencasts for the top five flows
- [x] Weekly "your server this week" email report

**Quality**
- [ ] e2e suite on real VPSes for Ubuntu 22.04, 24.04, Debian 12, arm64
- [ ] Signed releases with cosign and SLSA provenance, reproducible builds
- [x] `SECURITY.md`, `CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`, issue templates
- [ ] CLA bot, trademark policy
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
