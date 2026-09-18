<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/logo/lockup-white.svg">
    <img src="brand/logo/lockup.svg" alt="Islet" width="200">
  </picture>
</p>

# Islet

> Own your infrastructure. One binary that hardens a clean VPS, puts a login in front of anything on it, and deploys from Git — with a panel you actually enjoy using.

**Status:** released and in use. Current version is v0.22.1; `curl -fsSL https://get.islet.dev | sh` installs it. v1.0 is close: what remains is a code freeze and a full QA pass, not missing features.

**What Islet has that the others do not.** Deploying from Git is table stakes —
Coolify, Dokploy, CapRover and Portainer all do it, and some of them do it with
more services than Islet's catalog has. Two things here have no equivalent in
that category:

- **A security score you can act on.** Every check names what is wrong, fixes it
  in one click, and can be rolled back — SSH hardening with a timer that reverts
  if you lock yourself out, Docker-aware firewall rules, fail2ban, unattended
  upgrades, an IP blocklist, image scanning. A fresh server reaches 90 in about
  ten minutes.
- **A login in front of anything.** Point a domain at any app and require an
  Islet account to reach it — per path, per person, with an allow-list matrix.
  The app needs no code, no plugin and no awareness that this is happening.

The rest of the panel exists so those two are useful on a server that actually
runs something.

## What it does

A single Go binary (`isletd`, about 32 MB) with an embedded React panel and a `islet` CLI. It manages one server:

| Area | What you get |
|---|---|
| **Overview** | Live CPU, memory, disk and network with 7-day history, top processes, listening ports, Disk Doctor, and an attention strip: Security Score, checks down, failed deploys and jobs, backup state |
| **Containers** | Every container with live stats, logs, shell, limits; Compose stacks with an editor; images, volumes, networks, prune; adopt existing Compose projects |
| **Files** | Explorer with a code editor, trash, permissions, archives, search, upload and download |
| **Terminal** | A shell on the server in the browser |
| **Domains** | Managed Traefik: route a domain to a container or the panel, Let's Encrypt, www redirect, basic auth, IP allowlist, rate limit, headers, maintenance page, DNS helper |
| **Apps** | Deploy from any git URL or a Docker image: framework detection (Vite, Next.js, Nuxt, SvelteKit, Astro, Remix, Angular, Node, FastAPI, Django, Flask, Go, Dockerfile, Compose), zero-downtime releases with health checks, instant rollback, push webhooks, encrypted env. Plus a one-click catalog of 27 apps |
| **Databases** | Postgres, MySQL, MariaDB, Redis and MongoDB instances with connection strings, databases and users, dumps and restores, extensions, slow queries, optional public port |
| **Cron** | Commands, scripts with versions, container exec, one-off containers, HTTP checks, chains and heartbeats; live output, history, retries, templates, crontab import and export |
| **Notifications** | Telegram, Discord, Slack, email, ntfy, Gotify, Pushover and signed webhooks, with routing by category and severity, quiet hours, retries and an event timeline |
| **Runners** | Ephemeral GitHub Actions runners scaled from the queue; GitLab and Gitea runners; a generated deploy workflow |
| **Uptime** | HTTP, keyword and TCP checks from the server with latency history and down/recovery alerts |
| **Backups** | restic in a container: volumes, paths, database dumps and Islet state to S3-compatible storage, SFTP, a REST server or local disk; snapshot browser, restores, weekly checks, recovery kit |
| **Security** | Security Score with one-click fixes (SSH hardening with rollback timer, ufw with Docker-aware rules, fail2ban, unattended upgrades, swap, NTP), Trivy image scans, blocked IPs, network diagnostics, panic button |
| **Workspaces** | A directory, a command and a tmux session that outlives the browser: run a coding agent on the server, close the tab, come back to it |
| **Assistant** | Ask a model about this server and let it act through Islet's own tools, with your authority and nothing more |
| **Vault** | Secrets stored sealed and referenced as `@vault:NAME` in an app's environment, so a credential is written once |
| **SQL** | A query editor for the databases above: schema browser, saved queries, history, read-only by default |
| **Servers** | Add another machine over SSH and manage it from here; every page works against whichever server is selected |
| **Logs** | System journal, log files and every container in one viewer |
| **Settings** | Users and roles, two-factor, sessions, scoped API tokens, MCP server toggle, signed self-update, command transparency, audit log |

Everything in the single-server panel is and stays free under AGPL-3.0.

**Not every row above is equally finished.** Domains, Security, Notifications,
Containers, Files, Workspaces and SQL are done and used daily. Backups work and
are the one feature whose failure is unrecoverable, so verify a restore rather
than trusting the green tick — the restore test on the Backups page does exactly
that. Runners, the Assistant and the embedded panel are newer and thinner: they
do what they say, and they have had less weather than the rest.

## What Islet is not

Worth knowing before you install it, because none of this is a defect list — it
is the shape of the thing.

**Islet is single-tenant, and every account on it is an account on the server.**
An admin is root. That is deliberate: the panel holds the Docker socket, offers
a host terminal and a file browser rooted at `/`, and runs cron jobs as root, so
pretending otherwise would be theatre. The compensating control is that
everything it does is written down — every command it runs, redacted and
recorded, visible in the drawer and the audit log.

**Deployer and viewer are convenience roles, not a boundary against the people
who hold them.** They are the right thing for colleagues you already trust with
the server. They are not a sandbox: a deployer can run an existing job, which
runs as root.

**It does not isolate the apps it deploys from each other or from the host.**
Shared Docker daemon, no user namespaces, no capability dropping. Islet is for
running your own software on your own server, not for running other people's
code for customers you do not control.

**A copy of the data directory is a copy of every credential on the server**,
including every two-factor seed — the panel holds the key that opens them. Treat
a backup of Islet's own state the way you would treat the server's private keys.

**Turning on the assistant or a workspace agent gives a model your authority.**
It acts through the same API you do, with your role and no more, and there is no
confirmation step. A workspace runs as root in a tmux session, not a sandbox.
Use it for work you would have done yourself as root, and not otherwise.

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
| [docs/STATUS.md](docs/STATUS.md) | Where the work stands today, and what to pick up next |
| [docs/ROADMAP.md](docs/ROADMAP.md) | The phased plan with checkboxes; what is done and what is left |
| [docs/DESIGN_REVIEW.md](docs/DESIGN_REVIEW.md) | An in-depth review of Islet as a product: what it is, what it claims, and where those drifted apart |
| [docs/DECISIONS.md](docs/DECISIONS.md) | Log of decisions that shape the code |
| [docs/NEEDED_FROM_YOU.md](docs/NEEDED_FROM_YOU.md) | What only the maintainer can unblock |
| [docs/STRUCTURE.md](docs/STRUCTURE.md) | What every folder is for |
| [.claude/CLAUDE.md](.claude/CLAUDE.md) | Rules and skills for an AI agent working in this repo |
| [docs/AGENT_SETUP.md](docs/AGENT_SETUP.md) | Running an AI agent on a server Islet manages |
| [docs/recipes/](docs/recipes/) | Step-by-step guides for common setups |
| [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md) | How to help, how to report |
| [brand/README.md](brand/README.md) | Brand guidelines and assets |

## Stack

Go daemon with SQLite, React + Vite + TypeScript panel embedded in the binary, Traefik for routing and certificates, restic for backups, Trivy for scanning. Everything the daemon runs on the host is recorded and visible in the panel.

## License

AGPL-3.0 for the core. Catalog templates will move to their own MIT repository.

Brand marks for catalog apps and recipe stacks come from [Simple Icons](https://simpleicons.org), released under CC0-1.0, and live in `web/app/src/components/brandIcons.ts`. The trademarks themselves belong to their owners; the marks identify the software an entry installs and imply no endorsement.

Copyright and trademark: Torsten Labs DOO, North Macedonia, https://torstenlabs.com
