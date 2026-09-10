<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/logo/lockup-white.svg">
    <img src="brand/logo/lockup.svg" alt="Islet" width="200">
  </picture>
</p>

# Islet

> Own your infrastructure. One-line install that turns a clean VPS into a hardened, Docker-ready, deploy-from-GitHub server with a panel you actually enjoy using.

**Status:** pre-alpha, planning complete, code starting. Nothing here runs yet.

## What Islet will be

A single Go binary (`isletd`) with an embedded web UI that manages one server:

- Docker containers, Compose stacks, images and volumes
- Domains and automatic HTTPS through a managed Traefik
- One-click app catalog and guided recipes
- Databases with backups and restores
- Cron and task runner with a real script editor
- Git deploys and self-hosted GitHub Actions runners, no SSH keys in GitHub
- Security Score, hardening wizard, firewall, intrusion prevention, scanning
- Notifications to Telegram, Discord, Slack, email and more
- File explorer, web terminal, and a REST API, CLI and MCP server

Everything in the single-server panel is and stays free under AGPL-3.0.

## Documents

| File | What it is |
|---|---|
| [docs/ROADMAP.md](docs/ROADMAP.md) | The open-source phased plan, v0.1 to v1.0, with checkboxes |
| [docs/VISION.md](docs/VISION.md) | Full product vision, feature inventory A to Z, and the eventual Pro plan |
| [docs/REPOS_AND_MONETIZATION.md](docs/REPOS_AND_MONETIZATION.md) | Repo layout, how paid modules will be built later, and gate security |
| [docs/STRUCTURE.md](docs/STRUCTURE.md) | What every folder in this repo is for |
| [docs/DECISIONS.md](docs/DECISIONS.md) | Log of decisions that shape the code |
| [brand/README.md](brand/README.md) | Brand guidelines: voice, colour, type, logo, app icons |

## Names and places

| Thing | Name |
|---|---|
| Product | Islet |
| Daemon binary | `isletd` |
| CLI | `islet` |
| System user and state dir | `islet`, `/var/lib/islet` |
| Domains to register | `islet.dev` (primary), `islet.sh`, `islet.run` |
| GitHub org | `isletdev` (the bare `islet` handle is held by a user) |

## Stack

Go daemon, SQLite, React + Vite + TypeScript UI embedded in the binary, Traefik, restic, Railpack. See `docs/VISION.md` section 2 for the reasoning.

## License

AGPL-3.0 for the core (to be added as `LICENSE`). Catalog templates will be MIT in their own repo.

Copyright and trademark: Torsten Labs DOO, North Macedonia, https://torstenlabs.com

## Development

Requirements: Go 1.27+, Node 22+, pnpm 11+, Docker (for the features that need it).

```sh
# API and daemon with the placeholder page (plain HTTP for local development)
go run ./cmd/isletd -data-dir .data/state -tls off

# Web app with hot reload, proxied to the daemon on :9443
cd web/app && pnpm install && pnpm dev

# Full build: web app embedded into bin/isletd and bin/islet
sh scripts/build.sh
```

`internal/web/dist` holds a committed placeholder. The build script replaces it with the Vite output before compiling; do not commit the generated assets.
