<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/logo/lockup-white.svg">
    <img src="brand/logo/lockup.svg" alt="Islet" width="200">
  </picture>
</p>

# Islet

> Own your infrastructure. One binary that turns a clean VPS into a hardened, Docker-ready, deploy-from-Git server with a panel you actually enjoy using.

**Status:** pre-release. Every phase of the roadmap up to v1.0 has code; the daemon and panel run end to end on a workstation against Docker, and the host-level pieces (installer, firewall, SSH hardening, Let's Encrypt) wait for validation on a public Linux server. Not yet published or tagged.

## What it does

A single Go binary (`isletd`, about 25 MB) with an embedded React panel and a `islet` CLI. It manages one server:

| Area | What you get |
|---|---|
| **Overview** | Live CPU, memory, disk and network with 7-day history, top processes, listening ports, Disk Doctor, and an attention strip: Security Score, checks down, failed deploys and jobs, backup state |
| **Containers** | Every container with live stats, logs, shell, limits; Compose stacks with an editor; images, volumes, networks, prune; adopt existing Compose projects |
| **Files** | Explorer with a code editor, trash, permissions, archives, search, upload and download |
| **Terminal** | A shell on the server in the browser |
| **Domains** | Managed Traefik: route a domain to a container or the panel, Let's Encrypt, www redirect, basic auth, IP allowlist, rate limit, headers, maintenance page, DNS helper |
| **Apps** | Deploy from any git URL or a Docker image: framework detection (Vite, Next.js, Nuxt, SvelteKit, Astro, Remix, Angular, Node, FastAPI, Django, Flask, Go, Dockerfile, Compose), zero-downtime releases with health checks, instant rollback, push webhooks, encrypted env. Plus a one-click catalog of 24 apps |
| **Databases** | Postgres, MySQL, MariaDB, Redis and MongoDB instances with connection strings, databases and users, dumps and restores, extensions, slow queries, optional public port |
| **Cron** | Commands, scripts with versions, container exec, one-off containers, HTTP checks, chains and heartbeats; live output, history, retries, templates, crontab import and export |
| **Notifications** | Telegram, Discord, Slack, email, ntfy, Gotify, Pushover and signed webhooks, with routing by category and severity, quiet hours, retries and an event timeline |
| **Runners** | Ephemeral GitHub Actions runners scaled from the queue; GitLab and Gitea runners; a generated deploy workflow |
| **Uptime** | HTTP, keyword and TCP checks from the server with latency history and down/recovery alerts |
| **Backups** | restic in a container: volumes, paths, database dumps and Islet state to S3-compatible storage, SFTP, a REST server or local disk; snapshot browser, restores, weekly checks, recovery kit |
| **Security** | Security Score with one-click fixes (SSH hardening with rollback timer, ufw with Docker-aware rules, fail2ban, unattended upgrades, swap, NTP), Trivy image scans, blocked IPs, network diagnostics, panic button |
| **Logs** | System journal, log files and every container in one viewer |
| **Settings** | Users and roles, two-factor, sessions, scoped API tokens, MCP server toggle, signed self-update, command transparency, audit log |

Everything in the single-server panel is and stays free under AGPL-3.0.

## Try it

On a Linux server (Ubuntu 22.04+ or Debian 12+) as root:

```sh
curl -fsSL https://get.islet.dev | sh
```

It installs Docker if it is missing, downloads the release, verifies its signature and checksum and starts the daemon on port 9443. Nothing else on the server is touched: existing containers, volumes and web servers keep running, and the reverse proxy is only created when you add a domain. On a server where something already listens on 80 and 443, start with the panel's proxy on other ports by putting `Environment=ISLET_PROXY_PORTS=8880,8443` in a drop-in at `/etc/systemd/system/isletd.service.d/ports.conf` before the first domain; the installer owns the unit file itself and rewrites it on every run. `installer/uninstall.sh` removes the panel and leaves your apps running and serving.

From source, on any machine with Go 1.24+, Node 22 and Docker:

```sh
cd web/app && pnpm install && pnpm build && cd ../..
rm -rf internal/web/dist && cp -r web/app/dist internal/web/dist
go build -o isletd ./cmd/isletd && go build -o islet ./cmd/islet
ISLET_DATA_DIR=.data ISLET_LISTEN=127.0.0.1:9443 ./isletd
```

The daemon prints a one-time setup link. Open it, create the admin account, and turn on two-factor.

## CLI

```
islet login --url https://server:9443 --token islet_…   # token from Settings → API tokens
islet apps | deploy shop | logs shop -f | restart shop
islet cron list | cron run nightly-dump
islet db list | db shell pg
islet backup list | backup run nightly | backup snapshots hetzner
islet notify "deploy done" -m "v2.3 is live"
islet status | update
```

The REST API is described in [docs/openapi.yaml](docs/openapi.yaml). An MCP server for AI agents can be enabled in Settings.

## Documents

| File | What it is |
|---|---|
| [docs/ROADMAP.md](docs/ROADMAP.md) | The phased plan with checkboxes; what is done and what is left |
| [docs/VISION.md](docs/VISION.md) | Full product vision and feature inventory |
| [docs/DECISIONS.md](docs/DECISIONS.md) | Log of decisions that shape the code |
| [docs/NEEDED_FROM_YOU.md](docs/NEEDED_FROM_YOU.md) | What only the maintainer can unblock |
| [docs/STRUCTURE.md](docs/STRUCTURE.md) | What every folder is for |
| [docs/recipes/](docs/recipes/) | Step-by-step guides for common setups |
| [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md) | How to help, how to report |
| [brand/README.md](brand/README.md) | Brand guidelines and assets |

## Stack

Go daemon with SQLite, React + Vite + TypeScript panel embedded in the binary, Traefik for routing and certificates, restic for backups, Trivy for scanning. Everything the daemon runs on the host is recorded and visible in the panel.

## License

AGPL-3.0 for the core. Catalog templates will move to their own MIT repository.

Brand marks for catalog apps and recipe stacks come from [Simple Icons](https://simpleicons.org), released under CC0-1.0, and live in `web/app/src/components/brandIcons.ts`. The trademarks themselves belong to their owners; the marks identify the software an entry installs and imply no endorsement.

Copyright and trademark: Torsten Labs DOO, North Macedonia, https://torstenlabs.com
