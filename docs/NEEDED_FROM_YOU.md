# Needed from you

Things only the maintainer can do. Everything else keeps moving without them. Tick items as you complete them; the notes say what each unblocks.

## First: read what happened while you were away

Every roadmap item that can be built and checked without you is done: 95 commits on `main`, 35 signed releases, CI green on each. `docs/ROADMAP.md` has 191 items ticked and 5 open, and all five wait on you (the Hetzner token, GitHub Pages, the catalog repository, a decision on plugins, CrowdSec on a Linux box).

This list was written before any of it ran on a real server. `docs/STATUS.md` is the current state, checked against the repository and live endpoints; read that first, then the checklists below, then `docs/DECISIONS.md` for the calls I made, all reversible.

## Accounts and secrets

- [x] **Create the GitHub org `isletdev` and the repo `isletdev/islet`.** Done 2026-09-11; `main` is pushed and CI runs on every push. The website lives in the private `isletdev/website` repo.
- [ ] **Back up the release signing key** (the GitHub Actions secret is clearly set — 35 tags have published signature-verified releases — so what is left here is the offline copy) at `C:\Users\User1582\.islet\release-signing.key` somewhere offline (password manager or encrypted USB). If it is lost, no installed daemon can ever trust another update.
- [ ] **Register a GitHub App** (Settings → Developer settings → GitHub Apps → New) so users can pick repositories without pasting tokens and runners can register without a personal access token. Not blocking: deploys work today with any git URL plus a push webhook, runners with a PAT. Settings: name `Islet`, homepage `https://islet.dev`, webhook active with a placeholder URL, permissions Repository → Contents (read), Metadata (read), Webhooks (read and write), Administration (read and write); Organization → Self-hosted runners (read and write); events `push` and `workflow_job`; installable on any account. Then paste the App ID, client ID, slug, private key and webhook secret into Settings → GitHub App; set the app's webhook URL to `https://<panel>/api/v1/hooks/github`. The code is in place and verified up to the point where GitHub must answer.

## Domains

- [x] **Buy `islet.dev`** (and `islet.sh`, `islet.run` if you want them held). Done; `islet.dev` serves the site.
- [x] **Serve `get.islet.dev`** as the raw content of `installer/get.sh` from `main`. Simplest: a Cloudflare Worker that proxies `https://raw.githubusercontent.com/isletdev/islet/main/installer/get.sh`. Done; it answers.
- [ ] Set up `security@islet.dev` and `conduct@islet.dev` (both referenced in `SECURITY.md` and `CODE_OF_CONDUCT.md`), or change those files to addresses you own.

## A test server

Everything host-level was written for Ubuntu and Debian and originally could only be exercised on Windows against Docker Desktop. Real servers now run it, and the releases from v0.6.0 onward came out of that. What is below is the list of specific things that still have to be confirmed one at a time — please run through it and paste anything that fails:

- [x] **One Ubuntu 24.04 VPS** (a Hetzner CX22 is enough). Done — several, in fact, and the installer, Docker install, systemd unit and TLS have all run on them. Most of what follows has since been exercised there; the boxes below are still worth ticking one at a time, because each names a specific thing that has to work.
- [ ] **Let's Encrypt:** point a domain at the box, add it on the Domains page with Let's Encrypt, confirm the certificate appears under Domains → Certificates.
- [ ] **Wildcard certificate:** on the proxy card pick your DNS provider (Cloudflare token is the easiest), reinstall the proxy, add `*.yourdomain` with Let's Encrypt, confirm the certificate lists the wildcard.
- [ ] **Security page:** press each Fix (firewall, fail2ban, auto-updates, swap, NTP), then apply an SSH change and confirm the five-minute rollback works both ways (confirm, and let it expire). Check the Security Score reaches 90.
- [ ] **Server setup and host audit** (Security page): create a sudo user with your key, set the timezone, set the /etc baseline, run the audit with rkhunter. Then publish a database port with an allowlist and confirm `ufw status` shows the rule and a connection from another address is refused.
- [ ] **Outbound mail** (Notifications): set up the relay for a domain you own, publish the four records, send a test to a Gmail address and check it lands in the inbox with `dkim=pass`.
- [ ] **Deploy:** connect a real repository (a Vite site and a Next.js app are the best first tests), add the webhook from the app's Auto-deploy panel, push, watch it redeploy, roll back.
- [ ] **Runners:** create a GitHub personal access token (classic, `repo`) for a throwaway repository, add a pool on the Runners page, push a workflow with `runs-on: self-hosted`. I could only verify the pool logic, webhooks and the workflow generator without credentials. Same for GitLab and Gitea if you use them.
- [ ] **Backups off-site:** add an S3 (R2 or B2) or SFTP (Storage Box) destination and run a plan; local-path repositories are verified, remote ones are not.
- [ ] **Full restore:** on a second throwaway VPS run `installer/restore.sh` with the recovery kit and check the stacks come back. This script could only be written, not run.
- [ ] **Hetzner snapshot hook:** Settings → Hosting provider, paste a read/write token, press "Take a snapshot now", confirm it appears in the console; then change an SSH setting and check a second snapshot was taken first.
- [ ] **Hetzner API token** (read/write, project-scoped) as the GitHub Actions secret `HCLOUD_TOKEN` on `isletdev/islet`. Still missing, and it matters more than it looks: the `e2e` workflow has exactly one run in its history, it reported success, and both its `build` and `vps` jobs were skipped — so the four-distribution suite has never actually executed. With the secret it runs weekly and on demand: four throwaway servers (Ubuntu 22.04, 24.04, Debian 12, arm64), installer, firewall fixes, catalog install, proxy route, sample deploy, backup run, host audit, deleted afterwards. Roughly EUR 0.05 per run. Without the secret the workflow skips itself.
- [ ] **Load test on the small box:** `hack/loadtest.sh -n 30 -k admin '<password>'` on a 1 vCPU / 2 GB VPS and paste the table. Target: every p95 under 500 ms.
- [x] **First tagged release.** Done, and 34 more since; every tag publishes two binaries, `checksums.txt`, an Ed25519 signature that verifies against the key embedded in the daemon, and `checksums.txt.cosign.bundle`.

## Legal and billing (not urgent)

- [ ] Registration number, address, tax number and a contact address for the legal notice on the website (`../islet-website/index.html` has bracketed placeholders).
- [ ] Have a lawyer read the privacy policy and terms before the site goes public.
- [ ] Check whether Paddle or Lemon Squeezy accept a seller registered in North Macedonia. Only matters when paid work starts.

## Known gaps I left on purpose

Listed as open items in `docs/ROADMAP.md`. The ones most worth your opinion:

- Railpack/Nixpacks buildpacks: deploys use generated Dockerfiles for Node, Python, Go, PHP/Laravel, Ruby/Rails, Rust, Java and .NET, plus static sites. Anything else needs its own Dockerfile. I would keep it that way.
- The WASM plugin host is the one big piece left. Say whether you want plugins as WASM modules (sandboxed, harder to write) or as external services talking to the API and MCP (what the current API tokens already allow); I would start with the latter and document it as the plugin story.
- CrowdSec: needs a Linux box to develop against (bouncer at the nftables level); waiting for the test VPS.
- **Catalog in its own repo:** create `isletdev/catalog` (public, MIT) and copy `catalog/apps`, `catalog/recipes` and `catalog/README.md` into it. The daemon already fetches `https://github.com/isletdev/catalog/archive/refs/heads/main.tar.gz` when you press Fetch in Settings → Catalog source (daily afterwards), overlaying the embedded copy.
- **Docs site:** enable GitHub Pages on `isletdev/islet` (Settings → Pages → Source: GitHub Actions) and add the repository variable `DOCS_SITE=true` (Settings → Secrets and variables → Actions → Variables). The `docs` workflow then publishes the guides at `https://isletdev.github.io/islet/`; point `docs.islet.dev` at it later.
- Traefik's file watcher does not fire on Docker Desktop bind mounts on Windows, so local testing restarts the proxy after route changes. Linux inotify works; nothing to do on a real server.
