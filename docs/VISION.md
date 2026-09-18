# Islet — Product Vision

> **Historical document.** This is the plan Islet was started from, kept because
> `ROADMAP.md` cites its section numbers for feature specs and because it
> records why several decisions were made. Two things in it are no longer true:
> the open-core business model below was abandoned — everything in the
> single-server panel is AGPL-3.0 and stays that way — and the feature inventory
> stops well short of what shipped. For what Islet is now, read the README; for
> an assessment of it, `DESIGN_REVIEW.md`.

> One-line install on a clean VPS. A beautiful panel that turns a bare Linux box into a hardened, Docker-ready, deploy-from-GitHub server in 10 minutes. Open-source core, paid Pro at $12/month.

---

## 1. The wedge (why this and not Coolify / Dokploy / Portainer / Forge)

| Product | What it is | Gap we exploit |
|---|---|---|
| Coolify, Dokploy | Self-hosted Heroku (deploy apps) | No security posture, no runner management, hardening is "not our job" |
| Portainer | Docker UI | Docker only, no domains/SSL/deploys/security, Business tier is expensive |
| Laravel Forge, Ploi, RunCloud | Paid SaaS server managers (~$12–20/mo) | Closed source, PHP-centric, no Docker-first story, no runners |
| Cockpit, Webmin, 1Panel, aaPanel | Sysadmin panels | Dated UX, no guided flows, weak deploy story |
| Nginx Proxy Manager | Proxy UI | Single-purpose; we absorb it |

**Positioning:** "The server panel for software engineers, not sysadmins."
Three things nobody bundles today, and they are our wedge:

1. **Security as a first-class feature** with a visible score and one-click fixes.
2. **GitHub Actions runners on your own box** so deploys need zero SSH keys or secrets in GitHub.
3. **Guided recipes**: "Next.js + Postgres + your domain" as a wizard, not a wiki page.

Docker, proxy, SSL, databases and app deploys are table stakes. We must do them well, but they are not the pitch.

---

## 2. Core architecture decisions

### 2.1 Shape: one daemon per server, optional Hub

```
+-------------------- VPS --------------------+
|  isletd (single Go binary, systemd)       |
|   +- embedded web UI (React, served :9443)  |
|   +- SQLite (state, metrics ring buffer)    |
|   +- Docker Engine API client               |
|   +- reconciler (desired state -> actual)   |
|   +- collectors (metrics, logs, auth.log)   |
|                                             |
|  Docker: traefik | apps | dbs | runners     |
+---------------------------------------------+
                     ^
                     | outbound WebSocket (no inbound port needed)
+--------------------+------------------------+
|  Islet Hub  (Pro: hosted, or self-hosted  |
|  with license) - fleet view, mobile push,   |
|  team RBAC, AI, external uptime probes      |
+---------------------------------------------+
```

- **Free tier = full-featured single-server panel.** Everything in section 3 ships in the open-source binary.
- **Pro = Hub.** Multi-server, teams, mobile, AI, offsite services. The daemon dials out to the Hub, so no extra firewall holes.

### 2.2 Stack

| Layer | Choice | Why |
|---|---|---|
| Daemon | **Go** | Single static binary (~25 MB), ~40 MB RAM idle, native Docker SDK, trivial `curl \| sh` install, ARM64 for free. Node/Bun would need a runtime and 5x the memory on a $4 VPS. |
| State | **SQLite (WAL)** | Zero ops. Metrics in a ring-buffer table; 30-day retention locally. |
| UI | **React + Vite + TypeScript, Tailwind, shadcn/ui** | Embedded into the binary via `embed.FS`. Same codebase becomes the PWA and later the Hub frontend. |
| Live data | SSE for metrics/logs, WebSocket for terminal | |
| Reverse proxy | **Traefik** (managed container) | Docker-label discovery means any Compose file from the internet "just works". Auto-SSL, HTTP-01 + DNS-01 wildcards. |
| Builds | **Railpack/Nixpacks + BuildKit** | Detects Next.js, Node, Go, Python, Rust, static without a Dockerfile. |
| Backups | **restic** | Dedup, encryption, S3/B2/R2/SFTP/local. |
| Security | UFW/nftables, fail2ban or CrowdSec, Lynis, Trivy, unattended-upgrades | Best-of-breed, all scriptable. |
| Terminal / editor | xterm.js, Monaco | |
| CLI | `islet` (same binary, `isletd` symlinked) | `islet deploy`, `islet logs`, `islet db shell` |
| API | REST + OpenAPI, plus **MCP server** | Claude Code / Cursor / any agent can operate the server. |

### 2.3 Non-negotiable principles

1. **Everything the UI does is a reconcilable record.** Domains, apps, firewall rules, cron jobs live in SQLite as desired state; the reconciler applies them. This is what makes export-as-YAML, GitOps, and "clone this server" possible later.
2. **Explain everything.** Every toggle has a "Why this matters" and "What this changes on disk". Guides are inline, not a docs site.
3. **Never be the single point of failure.** If `isletd` dies, Traefik and apps keep running. Uninstall leaves a working server.
4. **Secure by default.** Panel behind HTTPS on first boot (self-signed, then Let's Encrypt on sslip.io or the user's domain), 2FA nagged on first login, no root SSH after the wizard.
5. **Mobile-responsive from day one.** PWA installable; the native app comes later on top of the same API.

### 2.4 Install experience

```
curl -fsSL https://get.islet.dev | sh
```
- Detects Ubuntu/Debian first (RHEL family in phase 3), installs Docker if missing, creates an `islet` system user, systemd unit, prints a one-time login URL with token.
- First-run **Hardening Wizard** (skippable, but scored): create sudo user + paste SSH key, disable root login and password auth, move SSH port (optional), UFW allow 22/80/443 + panel, fail2ban, unattended-upgrades, swap + timezone + NTP, enable 2FA on the panel.
- Ends on a **Server Health Score** (0–100) with the remaining items as a checklist.

---

## 3. Open-source feature inventory (the free single-server panel)

### 3.1 Dashboard
- Live CPU / RAM / disk / network, load, uptime, container count, pending updates, reboot-required flag.
- **Server Health Score** widget: security + updates + backups + monitoring, each with one-click fixes.
- **Disk Doctor**: what is eating disk (Docker images, dangling volumes, journal, apt cache, logs) with safe one-click cleanup and a preview of what will be removed.
- Recent events feed: deploys, logins, blocked IPs, alerts.

### 3.2 Docker
- Containers, images, volumes, networks, Compose stacks; start/stop/restart/recreate; logs with search and follow; exec terminal; stats.
- Compose editor with validation; paste any `docker-compose.yml`, add a domain, done.
- Registry logins (Docker Hub, GHCR, GitLab, private).
- **Update radar**: shows newer image tags per container with digest diff; update individually or on a schedule (Watchtower without the foot-guns).
- Resource limits UI (CPU/memory), restart policies, healthchecks.
- Prune with dry-run.

### 3.3 Domains, proxy, SSL
- Add domain, pick container + port, HTTPS in seconds. HTTP-01 by default, DNS-01 for wildcards (Cloudflare, Route53, DigitalOcean, Hetzner DNS, deSEC, Porkbun, Namecheap).
- **DNS helper**: shows exactly which A/AAAA/CNAME record to create, polls propagation, turns green.
- Free preview hostnames on `*.sslip.io` so users see something working before they own a domain.
- Per-route: redirects (www, http to https), basic auth, IP allowlist, rate limit, custom headers, WebSocket, path prefix, maintenance page.
- Cert inventory with expiry alerts and manual renewal.
- Cloudflare Tunnel and Tailscale integrations for users who do not want ports open.
- App catalog also offers Nginx Proxy Manager as an installable app for people who insist on it.

### 3.4 App deploys
- Sources: GitHub (via GitHub App, no personal tokens), GitLab, Gitea/Forgejo, generic git URL with managed deploy keys, Docker image, Compose, static upload.
- Build strategies: Dockerfile, Compose, Railpack/Nixpacks auto-detect, static site.
- Env vars and secrets (encrypted at rest, masked in UI, `.env` import), build-time vs runtime split.
- Zero-downtime deploys (start new, healthcheck, swap Traefik, stop old), rollbacks to any previous build, deploy history with logs.
- Auto-deploy on push via webhook; branch and tag rules.
- Cron jobs / scheduled commands inside the app's image.
- Pre/post deploy hooks (migrations), persistent volumes UI.
- Deploy notifications.

### 3.5 Databases
- One-click Postgres, MySQL, MariaDB, Redis, Valkey, MongoDB, ClickHouse, MinIO (S3).
- Create databases and users, connection strings with copy button (internal Docker network vs public with TLS).
- Public exposure gated behind an explicit warning + IP allowlist; SSH-tunnel instructions generated.
- Extension toggles for Postgres (pgvector, postgis, pg_stat_statements), PgBouncer toggle.
- Embedded admin: Adminer / pgweb / RedisInsight as a modal, session-scoped.
- Backups: scheduled dumps to any restic target, retention policy, **restore into a new instance** (never overwrite silently), backup verification job.
- Major-version upgrade assistant with pre-checks and a snapshot first.

### 3.6 CI runners (the killer free feature)
- Register **GitHub Actions self-hosted runners** for a repo or org via the GitHub App. Ephemeral runners in Docker, auto-scaled from webhook queue events (a lightweight actions-runner-controller).
- GitLab Runner and Gitea act_runner too.
- **Workflow generator**: "Deploy this app from GitHub" produces a ready `.github/workflows/deploy.yml` that runs on the box and calls the local daemon over a unix socket. No SSH keys, no secrets in GitHub, no open ports.
- Runner dashboard: queue, job history, per-job logs, cache volumes, cleanup.
- Label-based routing (e.g. `self-hosted, gpu, arm64`).

### 3.7 Security and auditing
- **Security Score** with explanations and one-click remediation.
- SSH: keys manager, disable password auth, port, allowed users, login banner, login notifications.
- Firewall UI over UFW/nftables with presets, per-app port awareness ("Postgres is exposed publicly" warning), Docker-bypasses-UFW protection (the classic foot-gun, handled for the user).
- Intrusion prevention: fail2ban jails or CrowdSec with community blocklists; live "blocked IPs" map.
- Lynis audit on schedule with score trend; Trivy image and filesystem CVE scanning with severity filters; rkhunter.
- Updates: pending apt packages, unattended-upgrades config, reboot scheduling, kernel livepatch status.
- Open ports and listening sockets view, correlated to containers.
- Auth log viewer (who logged in, from where), sudo log.
- Panel: 2FA (TOTP + passkeys), session management, API tokens with scopes, full audit log of every panel action.
- **Panic button**: block all inbound except your current IP/VPN, rotate panel sessions and tokens, snapshot state.

### 3.8 Backups (server-level)
- restic-based: volumes, bind mounts, database dumps, `/etc`, panel state. Targets: S3, B2, R2, Wasabi, Hetzner Storage Box (SFTP), local disk, another Islet server.
- Schedules, retention, encryption key escrow reminder, restore browser (pick files or whole volume), monthly restore-test job.
- **Full server export**: a tarball of desired state + restic snapshot IDs so a server can be rebuilt elsewhere.

### 3.9 Monitoring, logs, alerts
- 30-day metrics history, per-container graphs, disk growth forecast ("full in ~12 days").
- Unified log viewer: journald, docker, Traefik access logs, auth.log; filters, tail, saved searches.
- Uptime checks (HTTP, TCP, ping, keyword) from the server itself.
- Alert rules to email, Telegram, Discord, Slack, ntfy, Pushover, generic webhook. Sensible defaults pre-armed: disk > 85%, RAM pressure, container crash-loop, cert expiring, failed backup, SSH login from a new country.

### 3.10 Cron and task runner (full spec)

Goal: a user who has never written a crontab line can schedule a backup script in 60 seconds, and a power user never has to leave the panel to write, version, test and monitor a script.

**Job types**
- Simple command (`docker system prune -f`, `certbot renew`).
- Inline script: bash, sh, python, node, php, ruby, perl or any interpreter via shebang. Stored on disk at `/var/lib/islet/scripts/<slug>` with `chmod 750`, owned by the run-as user. The panel handles executable bits and line endings automatically, and shows the resulting path so the script is usable from the terminal too.
- Existing script on disk: browse and pick a file, optionally adopt it into the managed library.
- Run inside a container: `docker exec` into a chosen container (e.g. `php artisan schedule:run`, `rails runner`).
- One-off container: image + command, with volumes and env (e.g. run `rclone` or `pg_dump` from an official image without installing anything on the host).
- HTTP request: GET/POST a URL with headers and body (ping a health endpoint, trigger a webhook).
- Chain: run jobs in sequence with stop-on-failure.

**Schedule builder**
- Presets: every N minutes/hours, daily at HH:MM, weekdays, weekly on day, monthly on date, first/last day of month, on reboot, one-shot at date/time, manual only.
- Visual builder with minute/hour/day/month/weekday pickers.
- Raw cron expression field with live English translation ("At 03:00 on Monday and Thursday") and the next 5 run times, so both forms stay in sync.
- Per-job timezone (server default, UTC, or any IANA zone).
- Random jitter (0–N minutes) to avoid thundering herds across servers.

**Script editor**
- Monaco with syntax highlighting per interpreter, shebang selector, tab/space and CRLF handling.
- ShellCheck linting inline for shell scripts; syntax check for python/node before save.
- Snippets: "safe bash header" (`set -euo pipefail`), lock with `flock`, log to file, retry loop, send notification.
- Version history with diff and one-click revert; every save is a version.
- Secrets and env vars injected at runtime from the vault, never written into the script; masked in logs.
- Arguments field with per-run override on "Run now".
- Test run in a sandbox mode: same script, `DRY_RUN=1` exported, output shown live.

**Execution controls**
- Run as user (root, a system user, or the `islet` user), working directory, umask.
- Timeout with kill, overlap policy (skip, queue, or kill previous), max concurrency.
- Retries with backoff, and "only alert after N consecutive failures".
- `nice`/`ionice` presets (background, normal, urgent), optional CPU/memory cgroup limit.
- Enable/disable, pause until date, tags, description, and a "runbook" note next to the job.

**Observability**
- "Run now" with live streaming output.
- Per-job history: start time, duration, exit code, stdout/stderr, who triggered it, with retention settings.
- Duration and failure-rate charts, "took 3x longer than usual" anomaly alert.
- Dead man's switch: alert if a job that should have run did not (healthchecks.io style), including for jobs running elsewhere that just ping a URL.
- Notifications per job on failure, success, timeout or missed run, through any configured channel.

**Under the hood**
- The daemon runs its own scheduler so it can lock, capture output, retry and alert. It also **imports** existing crontabs (`/etc/crontab`, `/etc/cron.d`, per-user crontabs) and systemd timers, shows them read-only with an "adopt" button, and can **export** any managed job as a plain crontab line or systemd timer for people who want the system to own it.
- Every managed job is visible in the command transparency drawer, so nothing is hidden.

**Templates library** (one click, then edit): Postgres/MySQL dump to S3, Docker prune, log cleanup, cert expiry check, disk usage report to Telegram, git pull and redeploy, rclone sync to Drive/Backblaze, "curl this URL every minute", weekly apt update, restart container nightly, rotate a log file, warm a cache, run Laravel/Django/Rails scheduler, compress old backups.

### 3.11 Server tooling
- Web terminal (xterm.js) with tabs, file browser + Monaco editor, upload/download, permissions.
- Script library shared with the cron manager ("Run on server" with saved scripts and history).
- System users, sudoers, SFTP-only users.
- WireGuard VPN one-click (with QR for phone) and a "panel only reachable via VPN" toggle.
- Packages: apt search/install.
- Swap, timezone, hostname, kernel params presets (e.g. sysctl hardening set).

### 3.12 Guides and helpers (the UX layer)
- **Recipes**: multi-step wizards with real progress. Launch set: Next.js + Postgres + domain; Static site; Node API + Redis; Python/Django; Rails; Go; WordPress; n8n; Uptime Kuma; Plausible; Gitea; Vaultwarden; Minecraft.
- Inline "Why" panels, "What this does on disk" diffs, and a **command transparency** drawer that shows the shell commands the panel ran (educational and trust-building).
- Empty states that teach ("No domains yet: here's how DNS works").
- Keyboard-driven command palette (Cmd+K) for everything.

### 3.13 Platform
- Multi-user with roles (Admin, Deployer, Viewer), API tokens, OpenAPI docs, CLI, MCP server.
- Self-update with stable/beta channels and changelog in-app.
- Themeable, dark mode, i18n-ready.
- Import from existing servers: detect running containers, Compose files, Nginx sites, crontabs and adopt them.

### 3.14 Notifications and events (full spec)

Goal: the user never has to open the panel to know their server is fine, and when something breaks they learn it on the channel they already look at, with enough context to act from their phone.

**Event catalog** (every event has a severity: info, warning, critical)
- System: disk over threshold, disk forecast under N days, RAM/swap pressure, load high, CPU sustained, reboot required, reboot happened, updates available, OS EOL approaching, time drift.
- Security: SSH login success and failure, login from a new IP or country, sudo usage, fail2ban/CrowdSec ban, firewall rule changed, new listening port, Security Score dropped, Lynis score dropped, CVE found in an image, file integrity change, panel login, 2FA change, API token created or used from a new IP.
- Deploys: started, succeeded, failed (with the last 30 lines of build log), rolled back, preview created or destroyed, webhook received but ignored (branch mismatch).
- Containers: crashed, restart loop, OOM killed, healthcheck failing, image update available, container stopped by someone.
- Databases: backup succeeded or failed, restore completed, disk growth, connection spike, replication lag (Pro).
- Domains and SSL: DNS verified, certificate issued, expiring in 14/7/1 days, renewal failed, domain pointing elsewhere.
- Cron: failed, missed (dead man's switch), timed out, ran longer than usual, first success after failures.
- Backups: completed, failed, restore test passed or failed, repository unreachable, retention pruned.
- Runners: runner offline, job failed, queue waiting longer than N minutes.
- Uptime: check down, check recovered, slow response.
- Custom: `islet notify "text"` from any script, an inbound webhook event, or a manual "send test".

**Channels**
- Email via the configured SMTP relay or a Resend/Postmark API key.
- **Telegram**: setup wizard walks through BotFather, the user sends `/start` to the bot, the panel detects the chat id automatically. Supports private chats, groups, and forum topics per severity. Markdown messages with inline "Open in panel" button.
- **Discord**: incoming webhook (30-second setup) or a bot for two-way use. Rich embeds with colour by severity, fields for server, event, details, and a link.
- **Slack**: incoming webhook or Slack app with Block Kit messages, per-channel routing, threads for follow-ups to the same incident.
- Also: Matrix, Microsoft Teams, ntfy, Gotify, Pushover, generic webhook with HMAC signature and JSON payload, and Apprise for a hundred more.
- Each channel has a **Send test** button, a delivery log, and a fallback channel.
- [Pro] Mobile push, SMS and phone call for critical events (Twilio), and two-way bots: `/status`, `/deploy app`, `/restart container`, `/rollback`, `/ack`, with a 2FA confirmation for destructive commands.

**Routing rules**
- Matrix of event category x severity x channel, with sensible defaults after setup ("critical to Telegram and email, warnings to Discord, info to digest only").
- Per-project and per-app overrides (production deploys to `#deploys-prod`, staging to `#deploys-staging`).
- Quiet hours per channel with critical override.
- Digests: batch info and warning events into hourly or daily summaries; a weekly "your server this week" report with score, deploys, blocked IPs, disk trend.
- Deduplication and cooldowns: the same alert does not fire every minute, recovery messages close the loop ("disk back under 85%").
- Maintenance windows suppress expected noise.
- [Pro] Escalation: if not acknowledged within N minutes, notify the next channel or person; on-call rotation.

**Message design**
- Consistent shape: severity emoji, server name, one-line what happened, why it matters, suggested action, deep link into the exact panel page.
- Editable templates per channel (Go templates with documented variables) for people who want their own format.
- Deep links work with the mobile PWA so a tap lands on the failing container's logs.

**Delivery engine**
- Outbox table in SQLite, worker with retries and backoff, per-channel rate limiting, delivery status per event visible in the panel, alert when a channel itself is failing.
- In-app notification centre (bell, unread count, filters) and an event feed that doubles as the server timeline.

### 3.15 File explorer (full spec)

Goal: a user should be able to do anything they would normally SSH in for, including fixing a config file, moving a backup, or extracting an upload, from a familiar two-pane explorer, on desktop or phone.

**Navigation**
- Tree sidebar plus breadcrumb, optional dual pane for copy/move between locations.
- Bookmarks with smart defaults (`/etc`, `/var/log`, `/home`, app directories, Docker volumes, backup mounts) and recent locations.
- Hidden files toggle, sort and filter, list or grid view, pagination for huge directories.
- Search by name (fd) and by content (ripgrep) with regex, scoped to the current folder, results open in the editor at the matching line.
- Virtual folders: **Docker volumes** appear as folders, **container filesystems** can be browsed (via `docker cp`-style streaming), and **backup snapshots** can be mounted and browsed for point-and-click restore.

**Actions** (multi-select and bulk aware)
- New file or folder, rename (F2), duplicate, symlink, copy and move with drag-and-drop or the dual pane, delete to a **trash** with 7-day recovery, permanent delete with typed confirmation on protected paths.
- Permissions dialog: checkbox grid plus octal field, owner and group pickers, recursive apply with a preview of affected count, one-click "make executable".
- Compress to zip/tar.gz and extract archives in place, checksum (sha256/md5), touch timestamps.
- Upload: drag-and-drop, multiple files and whole folders, resumable for large files, progress per file. Download single files or folders as a zip.
- "Download from URL into this folder" (wget/curl) for tarballs and installers.
- "Open terminal here", "Run this script" (with output panel), "Adopt as cron script".
- Edit in Monaco with syntax by extension, diff on save, optional `.bak` copy, and validation for known files (`nginx -t`, `sshd -t`, `docker compose config`, YAML/JSON parse) before writing.
- Preview images, PDF, Markdown, CSV as a table, video and audio, and safe streaming view for large logs (head/tail/follow) instead of loading the whole file.
- File info panel: size, owner, permissions, mtime, mime type, which package owns it (`dpkg -S`), which container mounts it, and disk usage of a folder with a treemap (ncdu-style).
- Temporary share links with expiry and password, plus SFTP credential card and WebDAV mount instructions for desktop access.

**Safety and permissions**
- Operations run through the daemon as root only for Admin users; Deployers see only their project directories; Viewers are read-only.
- Protected paths (`/boot`, `/etc/passwd`, `/var/lib/docker`, the panel's own state) show a warning and require typed confirmation.
- Every write, delete, permission change, and download is in the audit log.
- Path validation prevents traversal, and symlink targets outside allowed roots are shown but not followed for non-admins.

**Keyboard and mobile**
- Desktop shortcuts: F2 rename, Delete, Ctrl+C/X/V, Ctrl+A, Enter to open, Backspace to go up, Ctrl+F to search.
- Mobile: long-press for the action sheet, swipe for rename/delete, touch-friendly upload from the camera roll.

---

### 3.16 Backups (full spec)

Goal: a beginner sets up a complete, encrypted, off-server backup in two minutes on day one, and a restore is something you have already watched succeed before you ever need it. Expands section 3.8.

**What gets backed up (sources)**
- Docker volumes, per app, with the app's own consistency hooks (see below).
- Bind mounts and any folder on disk, with include and exclude patterns.
- Databases through their native tools (`pg_dump`, `mysqldump`, Redis RDB, `mongodump`) taken right before the snapshot. Never a raw copy of a running database's files.
- Server config: `/etc`, crontabs, systemd units, Traefik certificates, firewall rules.
- Islet state: the SQLite database, encrypted secrets, managed scripts, catalog installs, domain records. This is what makes a whole-server rebuild possible.
- Not backed up: app images and build artefacts. Their digests are recorded so the rebuild pulls the exact same versions.

**Where it goes (destinations)**
- S3-compatible: AWS, Cloudflare R2, Backblaze B2, Wasabi, Hetzner Object Storage, MinIO on another server.
- SFTP, including Hetzner Storage Box, the cheapest option for most people.
- Local disk or an attached block volume, for fast restores.
- Another Islet server.
- rclone remotes: Google Drive, Dropbox, OneDrive, for hobby projects.
- A plan can write to more than one destination. The UI explains the 3-2-1 rule (three copies, two media, one off-site) and shows which of the three you have.

**Engine**
- restic underneath: client-side encryption, deduplication, incremental snapshots, retention pruning, resumable uploads.
- One repository per destination, encryption key generated by Islet and kept in the secrets vault. The key never appears in logs.
- **Recovery kit**: on first setup the user must download a small encrypted file with the repository locations, credentials and key, plus a one-page instruction sheet. Islet reminds them quarterly to confirm they still have it. Without this kit, an encrypted backup is useless after the server is gone.

**Backup plans**
- A plan is sources plus destinations plus a schedule plus a retention policy.
- Presets offered on first run: "Everything nightly" (recommended default), "Databases hourly", "Config weekly".
- Schedules use the same builder as the cron manager, with jitter and a run window.
- Retention in plain English: keep the last 7 daily, 4 weekly, 6 monthly, 1 yearly, with a preview of how many snapshots that is and the estimated storage size and monthly cost at the chosen provider.

**Consistency**
- Database dumps run pre-snapshot automatically for any database Islet manages.
- Per-app pre and post hooks ("enable maintenance mode", "flush cache"), and an optional pause-container-during-snapshot toggle for apps that write constantly.
- Filesystem snapshots (btrfs, ZFS, LVM) used automatically when the disk supports them, so the copy is atomic.

**Running**
- Bandwidth limit, low CPU and IO priority, resume after interruption, progress with size and ETA, per-run logs.
- Deduplication stats so users see that nightly backups of 20 GB cost megabytes, not gigabytes.
- Failures, missed runs and "backup older than expected" all raise events through the notification system.
- A backup health card on the dashboard: last success, next run, total size, destinations, and last verified date. It goes red when stale.

**Restore**
- Browse snapshots by date, drill into files, and restore a single file or folder to its original path, a new path, or as a download.
- Restore a volume into a **new** volume and container by default, so the running app is never overwritten silently. Swap when satisfied.
- Restore a database into a new instance, verify, then swap connection strings with one click.
- Restore a whole app: volumes, database, env, domains, in one action.
- **Full server restore**: on a fresh box, `islet restore` with the recovery kit rebuilds config first, then apps, then data, in dependency order. This is also the migration path between providers.
- Dry run for every restore, showing exactly what will be written.
- Restores require an admin with a fresh 2FA confirmation and are written to the audit log.

**Verification**
- Weekly repository integrity check.
- Monthly automated restore test: pull a random database dump and a sample of files into a scratch container, verify checksums and that the database loads, then report. The health card shows "last verified".
- Notification if a destination becomes unreachable or credentials expire.

**Protection against deletion and ransomware**
- Where the provider supports it, Islet sets up write-only credentials and object lock or append-only mode, so a compromised server cannot delete its own backups. The setup wizard does this automatically for B2, R2 and S3, and explains it for the rest.
- Retention pruning uses a separate credential that only runs from the panel.

**Provider snapshots (separate from backups)**
- Hetzner, DigitalOcean and Vultr snapshot APIs: take a whole-disk snapshot before risky actions like OS upgrades, database major-version upgrades, or a full restore. Fast to roll back, but same-provider only, so never a substitute for off-site backups. The UI makes the difference clear.

**Multi-server note**
- Each server backs up its own volumes to the shared repository with a host tag. Databases are dumped only where they live. A full restore of a main server also restores the list of attached servers so they can be re-joined.

**CLI**
- `islet backup run|list|restore|verify`, usable from cron and from scripts.

---

### 3.17 App deployments (full spec)

Goal: Forge-level ease for any stack. A user picks a repo, Islet detects what it is, and a working HTTPS URL appears a few minutes later. No Dockerfile, no YAML, no SSH. Expands section 3.4.

**The Docker question, answered once.** Under the hood every app runs in a container, including a plain React build. The user never sees or writes Docker unless they already have a Dockerfile, in which case Islet respects it. Containers are what make rollbacks, zero-downtime swaps, per-app resource limits, and clean removal possible. A "native mode" that runs Node or Python directly under systemd for people who insist is listed as a free post-v1.0 item, but it is not the default and not needed to deploy a React or Next.js app.

**The "New app" flow (five screens)**

1. **Source.** Pick a GitHub repo from a list (GitHub App, no tokens to paste), or GitLab, Gitea, any public git URL, a Docker image, or drag a folder or zip into the browser. Choose a branch.
2. **Detect.** Islet scans the repo and says what it found, in one sentence, with every value editable:
   - "Vite + React app. Install: `npm ci`. Build: `npm run build`. Output: `dist/`. Served as a static site."
   - "Next.js 15 app. Build: `next build`. Start: `next start`. Port 3000."
   - "Python app with a Dockerfile. We will build your Dockerfile as-is. Port 8000 from `EXPOSE`."
   - Detection covers: Vite, Create React App, Next.js, Nuxt, SvelteKit, Astro, Remix, Angular, Vue, plain HTML; Node and Bun APIs (Express, NestJS, Fastify, Hono); Python (FastAPI, Django, Flask, uv, Poetry, requirements.txt); Go; Rust; PHP and Laravel; Rails; Spring Boot; .NET; Dockerfile; docker-compose.yml.
3. **Configure.** Name, domain (a free `app.<server>.sslip.io` preview domain is pre-filled, or type your own and the DNS helper appears), environment variables (paste a whole `.env`), root directory for monorepos, Node or Python version, port, health check path, pre-deploy command (migrations), resources (CPU and memory caps with sensible defaults).
4. **Services.** "Add Postgres", "Add Redis", "Add MySQL" buttons. Islet creates the database, injects `DATABASE_URL` or `REDIS_URL` into the app's environment, and puts the database on the app's backup plan.
5. **Deploy.** Live log with named steps (clone, install, build, package, health check, switch). Ends with a clickable URL and a "Set up auto-deploy on push" toggle that is on by default.

**Three concrete walkthroughs**

*React app, no Docker knowledge.* Pick repo, Islet detects Vite, keeps `npm run build` and `dist/`. User types `app.example.com`, the DNS helper shows the A record, turns green when it resolves. Deploy runs the build in a throwaway builder, copies `dist/` into a tiny static-server image, serves it with SPA fallback to `index.html`, long cache headers on hashed assets, gzip and brotli, custom 404 support. Two minutes, no Docker vocabulary anywhere in the UI.

*Next.js app.* Detected as Next.js. Islet sets `output: 'standalone'` if not already set (or explains why), builds with Railpack, runs `node server.js` on port 3000, injects `NEXT_PUBLIC_*` variables at build time and the rest at runtime, and mounts a persistent volume for the ISR cache. Add Postgres with one click; `DATABASE_URL` appears in the env list. Pre-deploy command `npx prisma migrate deploy` runs once before the new version takes traffic. Zero-downtime swap, rollback button on every previous build.

*Python API with a Dockerfile.* Detected from the Dockerfile; Islet uses it unchanged, reads the port from `EXPOSE` or asks. Build args and secrets supported. Health check on `/health`. Add Redis, set worker count, done. If there were no Dockerfile, Railpack would detect FastAPI or Django and pick uvicorn or gunicorn with a reasonable worker count.

**Build strategies (chosen automatically, overridable)**
| Strategy | When | What happens |
|---|---|---|
| Static | Vite, CRA, Astro, Hugo, plain HTML, any framework with a static export | Build in a builder image, serve `dist/` from a static-server container |
| Buildpack | Node, Bun, Python, Go, Rust, PHP, Ruby, Java, .NET without a Dockerfile | Railpack (Nixpacks as fallback) produces an image, no Dockerfile written by the user |
| Dockerfile | A Dockerfile exists | Built as-is with BuildKit, cached layers, build args and secrets |
| Compose | A `docker-compose.yml` exists | Whole stack deployed, the web service gets the domain |
| Image | User provides an image reference | Pulled and run, with update checks by digest |

**Deploy pipeline**
- Steps: clone at commit, restore build cache, install, build, package image, run pre-deploy command in a one-off container, start new version, wait for health check, switch Traefik, drain and stop the old version, prune.
- Build cache per app (node_modules, pip, Go modules) so the second deploy is fast.
- Deploy history with commit message, author, duration, log, and a **Rollback** button that re-points to the previous image instantly, no rebuild.
- Triggers: push to branch (webhook), manual "Deploy" button, "Redeploy" without a new commit, tag rules, the CLI, the API, a self-hosted runner job.
- Concurrency: one deploy at a time per app, queued, cancellable.
- Env var changes prompt "Redeploy to apply" rather than silently restarting.
- Every deploy emits events (started, succeeded, failed with the last 30 log lines, rolled back) through the notification system.

**Processes and workers**
- An app can define extra processes from the same build: `web`, `worker`, `scheduler`, each with its own start command, instance count, and logs. Laravel queue workers and the scheduler, Django with Celery, Rails with Sidekiq, all get first-class UI without extra containers to manage.
- Scheduled commands inside the app image (`php artisan schedule:run`, `python manage.py cleanup`) via the cron manager, using the app's environment.

**Environments and monorepos**
- Environments (production, staging) on the same repo with different branches, domains and env vars. "Promote staging build to production" reuses the image without rebuilding.
- Root directory per app, so one repo can produce a Next.js frontend and a FastAPI backend as two apps.
- Shared env groups (the same `SENTRY_DSN` across apps) and secrets marked as such, masked everywhere.

**Runtime**
- App page: status, URL, current commit, resource use, live logs per process, restart, shell into the container, metrics, recent events, health check status, crash-loop detection with an alert.
- Multiple domains per app, www redirect, custom headers, basic auth for staging, maintenance page toggle.

**Static site extras**
- SPA fallback, base path support, custom 404 and redirects file (Netlify-style `_redirects` honoured), asset caching, brotli, optional password protection.

**CLI parity**
- `islet deploy` from a local folder without git, `islet logs <app> -f`, `islet env set KEY=value`, `islet rollback <app>`, `islet run <app> -- <command>` for one-off commands like migrations.

**Recipes that use this**
- "Next.js + Postgres + domain", "React SPA + FastAPI + Redis", "Laravel + MySQL + queue worker", "Django + Postgres + Celery", "Static site with a domain", with sample repos to try before connecting your own.

**How this maps to Laravel Forge, for orientation**
| Forge | Islet |
|---|---|
| Site | App |
| Deploy script | Detected install, build and start commands, editable |
| Quick deploy | Auto-deploy on push |
| Environment editor | Env vars and secrets, with `.env` paste |
| Daemons | Processes (worker, scheduler) |
| Scheduler | Cron manager, app-aware |
| SSL | Automatic on every domain |
| Database tab | Services: add Postgres, MySQL, Redis with injected URLs |
| Server-level PHP versions | Per-app runtime version, isolated |

---

### 3.18 Scale-out: attaching extra servers (design only, not scheduled)

Status: **design captured, low priority, not on the roadmap.** Nothing here is planned for any phase. It is written down so the single-server design does not accidentally rule it out.

**What it is.** A user with one Islet server adds a second VPS to it. The main server keeps the domains, Traefik, and the databases. The added server runs extra copies of apps. Traffic still enters through the main server and is spread across all copies.

```
                users
                  |
        [ main server: Traefik ]      domains, certificates, databases live here
           /        |        \
     app copy 1   app copy 2   app copy 3
     (main)       (server 2)   (server 2)
                    |
           private tunnel between servers
```

**User experience**
- "Add server" shows a one-line install command. Running it on the new box joins it as a worker; the private tunnel and overlay network are created automatically.
- Each app gets a "copies" count and a "run on" picker (main, workers, or any).
- Traefik discovers every copy and health-checks it. A copy that dies stops receiving traffic.
- The dashboard shows the added server's CPU, memory and its copies next to the main one. Workers can also host runners and cron jobs so they never slow the main site.

**Under the hood**
- Docker Swarm mode: the main server is the manager, added servers are workers. Swarm provides the overlay network, replica placement, restarts on node failure, and Traefik has a native Swarm provider. Coolify uses the same approach. No Kubernetes.
- Backups per server, tagged by host, into the same repository (see 3.16).

**Honest limits, shown in the UI**
- The app must be stateless: sessions in Redis or the database, uploads in S3 or MinIO. Most Next.js, Node, Django and Rails apps qualify once uploads are moved.
- The database stays on one server. Read replicas are a separate feature.
- The main server remains a single point of failure and its network port the bottleneck. Surviving the loss of the main server needs a provider load balancer, Cloudflare, or a floating IP in front, which is out of scope here.
- Most apps never need this. One well-sized VPS serves thousands of requests per second. The UI should say so and suggest resizing first.

**The one cheap preparation worth doing early**
- Records that describe something running on a machine (containers, app copies, metrics, cron runs) should carry a server id column from the first migration, even while there is only ever one server. This costs nothing now and avoids rewriting every table and query if scale-out is ever built. Decided on 2026-09-10: adopted as a schema convention in phase 0 (see `DECISIONS.md`). Scale-out itself stays unscheduled.

**Where it would sit commercially, if ever built**
- Attached workers for a small number of servers could stay free, since it is what makes people trust Islet for production. The Pro Hub sells a different thing: managing many independent servers with separate apps, teams and mobile.

---

## 4. Pro — what people pay $12/month for

Rule for the split: **Free covers one person, one server, forever, with no crippling. Pro sells scale, convenience, and things that cost us money to run.**

### 4.1 Pro Individual — $12/mo or $99/yr
| Feature | Why it's worth paying for |
|---|---|
| **Hub: fleet dashboard** for unlimited servers, single login, cross-server search | The moment you have 2 servers you want one screen. |
| **Mobile app** (iOS/Android) with push: deploy failed, disk 92%, new SSH login, approve/rollback from phone | Peace of mind is the easiest sale. |
| **AI Operator**: "why did this deploy fail?" reads logs and suggests a fix; natural-language ops ("add a cron that backs up postgres nightly"); explains Lynis findings | Real cost per token; clear value; on top of the free MCP server. |
| **Preview environments**: every PR gets `pr-123.app.example.com`, auto-cleaned, status posted back to GitHub | Team-adjacent feature solo devs also love. |
| **External uptime probes** from 5 regions + public **status page** | Needs our infrastructure. |
| **Included offsite backup storage** (50 GB, encrypted, we run it) + Postgres **point-in-time recovery** (WAL archiving) | Zero-config safety net. |
| **Security Pro**: continuous CVE monitoring with alerts, weekly PDF security report, CrowdSec premium blocklists, secret-leak scanning in env vars/images | Compliance-lite for freelancers with clients. |
| **Server as Code**: export/import full config as YAML, GitOps sync to a repo, drift detection, **clone server** and **golden templates** | Reproducibility is a pro workflow. |
| **Provider integration**: create Hetzner/DigitalOcean/Vultr/Linode/OVH servers from inside the app, pre-installed, with cost dashboard | Onboarding funnel and stickiness. |
| **Cross-server runners**: one runner pool across servers, autoscaling policies, job cost/time analytics | |
| Long metrics retention (1 year) and log archiving | |
| Priority support and Discord channel | |

### 4.2 Pro Team — $20/user/mo
- Everything in Individual, plus: SSO (Google, GitHub, OIDC/SAML), fine-grained RBAC per server/app/environment, deploy approvals, audit log export and SIEM webhooks, shared secrets vault with rotation, per-client workspaces (agencies), invoiced billing.

### 4.3 Deliberately NOT paywalled
SSL, backups to your own storage, Docker features, runners, security hardening, multi-user basics, API, CLI, MCP. Paywalling any of these would kill the community and the wedge.

### 4.4 Licensing and enforcement
- Core: **AGPL-3.0** (stops cloud providers from hosting it without contributing). `ee/` directory under a commercial license (GitLab/Cal.com pattern).
- License key: Ed25519-signed token, verified offline, 14-day grace when offline, so a self-hosted Hub works in air-gapped setups.
- Hosted Hub at the same price includes the license. Most users pick hosted; self-hosted Hub keeps the open-source crowd happy.

---

## 5. Signature features (the ones people tweet about)

1. **Deploy from GitHub with zero secrets**: runner lives on the box, workflow talks to the daemon over a socket.
2. **Security Score with one-click fixes** and plain-English explanations.
3. **Disk Doctor** and the **"full in 12 days"** forecast.
4. **Command transparency drawer**: see and copy every shell command the panel runs.
5. **Panic button**.
6. **DNS helper that turns green** when your record propagates.
7. **Recipes** that finish with a working URL, not a wall of text.
8. **MCP server**: "Claude, deploy main to staging and tail the logs."
9. **Restore into a new instance** so you never overwrite good data.
10. **Import existing server**: adopt what is already running.

---

## 6. Build order

Ship something usable at every phase. Each phase is a public release.

| Phase | Weeks | Scope | Exit criterion |
|---|---|---|---|
| **0. Skeleton** | 1–3 | Go daemon, installer script, systemd, HTTPS bootstrap, auth + 2FA, embedded React UI, dashboard metrics, Docker containers/images/logs/exec, web terminal, file explorer with editor | Install on a fresh Hetzner box in under 2 min, see and manage containers, edit a config file from the browser |
| **1. Proxy + apps** | 4–7 | Traefik, domains + SSL + DNS helper, Compose stacks, app catalog (10 templates), databases with backups, env/secrets, cron and task runner with script editor, notification channels (email, Telegram, Discord, Slack) with the event catalog | "Next.js + Postgres + domain" recipe works end to end, nightly DB backup scheduled from the UI, failed deploy lands in Telegram |
| **2. Deploys + runners** | 8–11 | GitHub App, git deploys with Railpack, zero-downtime, rollbacks, ephemeral GitHub runners, workflow generator, CLI | Push to main goes live with no SSH keys anywhere |
| **3. Security + ops** | 12–15 | Hardening wizard, Security Score, firewall, fail2ban/CrowdSec, Lynis, Trivy, updates, alerts, restic backups, log viewer, Disk Doctor, MCP server | Public launch (Show HN, r/selfhosted) |
| **4. Hub (Pro)** | 16–22 | Hosted Hub, fleet view, mobile PWA then native (Expo), push, licensing, billing (Stripe), preview envs, external uptime | First paying customers |
| **5. Pro depth** | 23+ | AI Operator, Server as Code, provider integration, PITR, Team tier | |

Phases 0–3 are about 4 months for one focused engineer with AI assistance. Keep the surface area honest: every feature in section 3 has a "v1 = the 80% case" scope.

---

## 7. Risks and how we handle them

- **Scope explosion.** Section 3 is the roadmap, not the MVP. Phases 0–2 are the MVP.
- **Coolify/Dokploy add security features.** Our moat is the guided UX plus runners plus fleet/mobile. Move fast on phases 3 and 4.
- **Docker vs UFW foot-gun, Traefik edge cases, distro differences.** Budget time; write integration tests that spin up real VPSes (Hetzner API, about one cent per test run).
- **Support load from free users.** In-app guides and command transparency reduce tickets; a Discord community handles the rest; Pro gets priority.
- **Pricing pressure.** $12 undercuts Forge ($12–19) with more features and open source. Annual at $99 improves cash flow.

---

## 8. A to Z tool inventory (the all-in-one checklist)

Everything a software engineer might ever need to do on a server, grouped by domain. Tags: **[Free]** ships in the open-source daemon, **[Pro]** is Hub or licensed, **[App]** is a one-click catalog template (free to install, we just package it). Phase numbers refer to section 6; "later" means after phase 5.

### A. Access, identity and auth
- [Free] Panel users, roles, 2FA (TOTP, passkeys), sessions, API tokens with scopes, audit log.
- [Free] Linux users and groups, per-user SSH keys, sudo rules, SFTP-only users, password policy, account expiry.
- [Free] **"Protect this app with login"**: Traefik forward-auth using the panel's own login, so any self-hosted app (Grafana, Adminer, a staging site) gets SSO and 2FA with one toggle.
- [App] Authentik, Authelia, Keycloak, Zitadel for full IdP needs.
- [Free] SSH hardening: keys only, port, allowed users, banner, idle timeout, PAM 2FA for SSH, fail2ban jail.
- [Pro] SSO into the Hub (Google, GitHub, OIDC, SAML), SSH certificate authority with short-lived certs, session recording for shared servers.

### B. Backups and disaster recovery
- [Free] restic backups of volumes, bind mounts, DB dumps, `/etc`, panel state, to S3/B2/R2/Wasabi/SFTP/local/another Islet server. Schedules, retention, restore browser, monthly restore test, full server export.
- [Free] Provider snapshots via API (Hetzner, DO, Vultr) before risky operations ("Time Machine" button).
- [Free] Backup encryption key escrow reminder, "backup of the backup config" printed as a recovery sheet.
- [Pro] Included offsite storage, Postgres PITR, cross-server replication, one-click restore onto a fresh server, DR drill runbook with a scored rehearsal.

### C. Containers and orchestration
- [Free] Full Docker management (section 3.2), Compose stacks, registries, update radar, resource limits, healthchecks, prune.
- [Free] Container hardening defaults: `no-new-privileges`, read-only root FS toggle, seccomp/AppArmor profiles, docker-socket-proxy for apps that need the socket, rootless Docker option.
- [Free] Replicas and scaling UI for stateless services and worker queues.
- [Pro] Docker Swarm across Hub servers (multi-node services, overlay networks, rolling updates). Later: k3s node join, Podman, Incus/LXC system containers, KVM VMs via libvirt.

### D. Databases and data stores
- [Free] Postgres, MySQL, MariaDB, Redis, Valkey, MongoDB, ClickHouse, MinIO (section 3.5). Users, DBs, extensions, PgBouncer, embedded admin, dump backups, restore into a new instance, major-version upgrade assistant.
- [App] Meilisearch, Typesense, OpenSearch/Elasticsearch, Qdrant, Weaviate, Milvus, InfluxDB, TimescaleDB, CouchDB, Neo4j, SurrealDB, Dragonfly, Memcached.
- [App] Backend-as-a-service: Supabase, PocketBase, Appwrite, Nhost, Directus, Hasura.
- [Free] Slow query log viewer, connection count and size graphs, "who is connected" list.
- [Pro] PITR, read replicas across servers, query analytics with recommendations, scheduled data anonymised copies to staging.

### E. Deploys and CI/CD
- [Free] Git deploys, buildpacks, Dockerfile, Compose, static, zero-downtime, rollbacks, webhooks, env/secrets, hooks (section 3.4).
- [Free] GitHub/GitLab/Gitea self-hosted runners, workflow generator, runner dashboard (section 3.6).
- [Free] Environments per project (dev/staging/prod) with promoted builds, per-env domains and secrets.
- [Free] Official GitHub Action `isletdev/deploy`, CLI, Terraform provider (later), Ansible playbook runner, Renovate/Dependabot-style image bump PRs.
- [Free] Import from Heroku (`app.json`, Procfile), Render blueprints, Railway, Coolify, Dokploy, Portainer stacks, a plain directory of Compose files, cPanel/Plesk sites (best effort).
- [Pro] PR preview environments, deploy approvals, canary and blue/green across servers, deploy windows and freeze periods, DORA-style metrics.

### F. Files and storage
- [Free] File browser and editor, upload/download, permissions, archive/extract, search, trash.
- [Free] Disk and partition view, mount external block volumes (Hetzner Volume, DO Volume), NFS/SMB mounts, fstab editor with validation, SMART health, LVM/RAID status, btrfs/ZFS snapshot view.
- [Free] rclone remotes: mount or sync Google Drive, Dropbox, OneDrive, S3, B2 from the UI.
- [Free] SFTP/FTPS server, WebDAV share, quotas, temporary share links for files.
- [App] Nextcloud, Seafile, Syncthing, FileBrowser, Immich, PhotoPrism, MinIO, Garage, SeaweedFS.

### G. Guides, docs and learning
- [Free] Recipes, inline "Why" panels, command transparency drawer, teaching empty states (section 3.12).
- [Free] Per-server notes and runbooks in Markdown, timeline annotations ("deployed v2.3", "resized to 8 GB"), auto-generated change log of everything the panel did.
- [Free] "Explain this" on any log line, error or config file (rule-based in free, model-backed in Pro).
- [Free] Custom MOTD: a server summary card on every SSH login.
- [Pro] AI Operator: chat with the server, root-cause analysis, guided fixes, natural-language cron and firewall rules.

### H. Hosting (non-Docker and classic web)
- [Free] Static sites with instant deploy and cache headers.
- [Free] PHP hosting with php-fpm pools per site, multiple PHP versions, Composer; WordPress recipe with hardening.
- [Free] Node/Python/Go processes as managed systemd units (PM2-style: restart, logs, env, clustering) for people who do not want Docker.
- [Free] Nginx/Caddy vhosts for non-Docker sites, with the same domain/SSL flow.
- [App] WordPress, Ghost, Strapi, Payload, Directus, Statamic, Grav, Hugo/Astro build pipelines.

### I. Infrastructure and provider lifecycle
- [Free] Reboot, shutdown, rescue mode hint, kernel and OS upgrade assistant (`do-release-upgrade` with pre-checks), distro EOL warnings, time sync, hostname, timezone, swap, locale.
- [Free] Provider API link (Hetzner, DO, Vultr, Linode, OVH, Scaleway, AWS Lightsail): snapshots, resize awareness, floating IPs, reverse DNS, firewall sync, console link.
- [Pro] Create and destroy servers from the app, cost dashboard across providers, right-sizing suggestions, spot/auction watcher for Hetzner.

### J. Jobs, automation and workflows
- [Free] Cron and task runner with script editor (section 3.10), systemd timers view, script library.
- [Free] Webhook receiver: inbound URL with signature verification (Stripe, GitHub, generic) that triggers a script, a deploy, or a container restart.
- [Free] Event automations: "when disk > 90% run cleanup", "when container crashes 3x, restart and notify", "when cert renews, reload service".
- [App] n8n, Windmill, Activepieces, Huginn, Node-RED, Temporal.
- [Pro] Cross-server job orchestration and runbook automation with approvals.

### K. Keys, secrets and certificates
- [Free] Secrets vault encrypted at rest (age), env injection into apps, containers, scripts and cron; masked in logs; per-secret audit trail.
- [Free] SSL: Let's Encrypt and ZeroSSL, HTTP-01/DNS-01/TLS-ALPN, wildcards, cert inventory, custom certs, custom CA, HSTS, TLS version presets, mTLS client certs for admin routes, OCSP stapling.
- [Free] SSH key manager, GPG keys, deploy keys, age keys, key rotation reminders.
- [App] Vaultwarden, Infisical, HashiCorp Vault (OpenBao).
- [Pro] Shared team vault with rotation policies, secret-leak scanning in repos, images and env vars.

### L. Logs and observability
- [Free] Unified log viewer (journald, Docker, Traefik, auth, app files), saved searches, tail, export, logrotate UI, retention policies.
- [Free] 30-day metrics, per-container graphs, disk forecast, Traefik access analytics (top paths, status codes, latency, countries, bots).
- [Free] Ship logs and metrics to Loki, Prometheus remote-write, OpenTelemetry, Better Stack, Datadog, Axiom.
- [App] Grafana + Prometheus + Loki stack, Netdata, Beszel, GlitchTip, Sentry, SigNoz, HyperDX, Uptime Kuma, Gatus, GoAccess.
- [Pro] 1-year retention, log archiving to your S3, anomaly alerts, external uptime from 5 regions, public status page, on-call rotation and escalation.

### M. Mail and messaging
- [Free] Outbound SMTP relay setup (Postfix or msmtp to SES, Resend, Postmark, Mailgun, Brevo) so cron, apps and the panel can send mail; DKIM/SPF/DMARC record generator and checker; deliverability test.
- [Free] Notification channels: email, Telegram, Discord, Slack, Matrix, ntfy, Gotify, Pushover, webhook, Apprise.
- [App] Stalwart, Mailcow, Mailu, Postal (full mail servers), Listmonk, Mautic (marketing), Novu (notifications).
- [Pro] Mobile push, digest emails ("your servers this week"), Slack/Discord bot with slash commands.

### N. Networking
- [Free] Firewall UI (UFW/nftables) with Docker-aware rules, presets, geo-blocking, per-app port awareness (section 3.7).
- [Free] WireGuard server with QR onboarding, Tailscale, Netbird, Cloudflare Tunnel, ngrok-style tunnels from your laptop through your server (frp/rathole) for demos and webhooks.
- [Free] IPv6 enablement checklist, reverse DNS, hosts file editor, DNS resolver settings, DNS-over-TLS.
- [Free] Diagnostics from the UI: ping, traceroute/mtr, dig, whois, curl, port check, speed test, per-interface and per-container bandwidth (vnstat), connection table.
- [App] Pi-hole, AdGuard Home, Technitium DNS, Headscale, Traefik/Caddy/NPM/HAProxy as alternatives.
- [Pro] Fleet-wide private mesh, cross-server load balancing, private networks between Hub servers.

### O. OS packages, processes and services
- [Free] apt/dnf package search, install, remove, hold; pending updates; unattended-upgrades config; reboot scheduling; kernel livepatch status.
- [Free] Process manager (htop-like: tree, CPU/RAM, kill, nice), open files, zombie detection.
- [Free] systemd services: start/stop/enable, logs, edit units with validation, drop-ins, timers; dmesg and kernel log viewer.
- [Free] Language runtimes installer: Node (fnm), Python (uv), Go, Rust, PHP, Java, .NET, Ruby, Bun, Deno, with version switching.
- [Free] sysctl editor with hardening presets, limits.conf, environment and profile editor, shell and alias manager.

### P. Proxy, domains and edge
- [Free] Everything in section 3.3: Traefik, domains, SSL, DNS helper, redirects, auth, rate limits, headers, maintenance page.
- [Free] Manage DNS records at the provider from the panel (Cloudflare, Route53, Hetzner, DO, deSEC, Porkbun), dynamic DNS for home boxes.
- [Free] WAF toggle per route (Coraza with OWASP CRS), CrowdSec bouncer on Traefik, bot and scraper blocking, IP reputation, Cloudflare proxy toggle and real-IP handling.
- [Free] Custom error pages, compression, caching headers, HTTP/3, CORS presets, path-based routing to multiple containers, TCP/UDP routing (game servers, databases).

### Q. Quotas, multi-tenancy and projects
- [Free] Projects and environments grouping apps, DBs, domains and cron jobs; per-project resource usage.
- [Free] Deployer and Viewer roles scoped to a project.
- [Pro] Per-client workspaces for agencies with quotas, separate billing views, white-label panel (logo, colours, custom domain) for reselling to clients.

### R. Runtime platforms for specific stacks (recipes and catalog)
- Web apps: Next.js, Nuxt, SvelteKit, Remix, Astro, Django, Flask, FastAPI, Rails, Laravel, Phoenix, Spring Boot, .NET, Go.
- Business tools: Cal.com, Documenso, Twenty CRM, Odoo, ERPNext, Invoice Ninja, Kimai, Formbricks, Fider, Rallly, Plane, Vikunja, Focalboard.
- Knowledge: Outline, Wiki.js, BookStack, Docmost, HedgeDoc, Paperless-ngx.
- Comms: Matrix (Synapse/Conduit), Mattermost, Rocket.Chat, Jitsi, Mumble.
- Media and home: Jellyfin, Plex, Navidrome, Immich, Home Assistant, Audiobookshelf, the *arr suite.
- Game servers: Pterodactyl/Pelican panel, Minecraft, Valheim, Palworld, Satisfactory, with TCP/UDP routing and backups pre-wired.
- Analytics and product: Plausible, Umami, Matomo, PostHog, Unleash, Flagsmith.
- Dev: Gitea/Forgejo, GitLab, Drone/Woodpecker, code-server, Jupyter, SonarQube, Verdaccio, Harbor, Nexus.
- URLs and misc: Shlink, Linkwarden, Wallabag, FreshRSS, Miniflux, Stirling-PDF, IT-Tools, Excalidraw.

### S. Security and compliance
- [Free] Everything in section 3.7: Security Score, hardening wizard, firewall, fail2ban/CrowdSec, Lynis, Trivy, rkhunter, auth logs, panic button.
- [Free] File integrity monitoring (AIDE) on `/etc` and binaries, sudo and login alerting, world-writable and SUID audits, open-port drift alerts.
- [Free] Supply chain: SBOM per image (syft), image signature verification (cosign), pinned digests, base image EOL warnings.
- [Free] Docker bench for security, kernel hardening presets, auditd rules presets.
- [Pro] Continuous CVE monitoring with alerts, weekly PDF report, compliance checklists (CIS benchmark, SOC 2-lite evidence export), secret scanning, premium blocklists.

### T. Terminal and remote access
- [Free] Web terminal with tabs, split panes, session persistence (tmux-backed), file drag-and-drop, copy/paste, themes.
- [Free] Container exec, database shells (`psql`, `mysql`, `redis-cli`) one click from the DB page.
- [Free] SSH config generator for VS Code Remote / JetBrains Gateway, and a "connect from your laptop" card with the exact command.
- [App] code-server, Coder, Jupyter for a full browser IDE.
- [Pro] Shared terminal sessions with a teammate, session recording and playback, break-glass access with approval.

### U. Updates and maintenance
- [Free] OS updates, Docker image updates with digest diff and changelog links, panel self-update with channels, database major-version upgrades, Traefik upgrades, runtime upgrades, with a snapshot before each.
- [Free] Maintenance windows, scheduled reboots, maintenance page toggle per route, "reboot required" tracking.
- [Free] Health checks of dependencies before updates (disk free, backup fresh, no deploy running).

### V. Virtualization, AI and GPUs
- [Free] NVIDIA driver and container toolkit installer, GPU utilisation graphs, per-container GPU assignment.
- [App] Ollama, vLLM, LocalAI, Open WebUI, LiteLLM proxy, ComfyUI, Stable Diffusion WebUI, Whisper, Qdrant/Weaviate for vectors.
- [Free] Model manager: pull, list, remove models; expose an OpenAI-compatible endpoint behind auth and rate limits with one toggle.
- Later: KVM virtual machines, Incus containers, Windows VM for that one legacy tool.

### W. Webhooks, APIs and extensibility
- [Free] REST API with OpenAPI, CLI, MCP server, outbound webhooks for every event, inbound webhook receiver.
- [Free] Plugin system (WASM or sandboxed JS) for custom pages, dashboard widgets and job types; "add to sidebar" embeds any app UI inside the panel with SSO.
- [Free] Community catalog repo for app templates, recipes and scripts, with ratings and update PRs (like Coolify's services repo, but also for scripts and cron templates).
- [Free] Terraform provider and GitHub Action (later), Slack/Discord bot (Pro).

### X. Cross-server and fleet (Hub)
- [Pro] Fleet dashboard, cross-server search, grouped alerts, one-click actions across servers ("update Traefik everywhere"), server groups and tags, golden templates, clone server, config drift detection, GitOps sync, cross-server runners and backups, private mesh, Swarm.

### Y. Your team and collaboration
- [Free] Multi-user, roles, audit log, shared notes and runbooks, activity feed.
- [Pro] SSO, granular RBAC, approvals, comments on deploys and incidents, incident timeline, on-call, shared terminals, agency workspaces, invoiced billing.

### Z. Zero-downtime, resilience and scaling
- [Free] Health-checked rolling deploys, rollbacks, replicas, restart policies, crash-loop detection, OOM detection with suggestions, swap and memory pressure alerts, graceful shutdown handling, connection draining in Traefik.
- [Pro] Multi-server load balancing, failover of routes between servers, database replicas, automated capacity suggestions.

---

## 9. Open questions for you

1. Name and domain. "Islet" is descriptive but generic; check availability early.
2. Go for the daemon is the recommendation. If you strongly prefer one language, Bun's single-file compile is the fallback, at a memory and install-size cost.
3. Hosted Hub first or self-hosted Hub first? Recommendation: hosted first (faster billing, and mobile push needs a server anyway), self-hosted Hub in phase 5.
4. Which cloud provider integration first? Recommendation: Hetzner (dominant in the indie/self-hosted crowd, cheap ARM boxes).
