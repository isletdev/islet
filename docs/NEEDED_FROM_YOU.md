# Needed from you

Things only the maintainer can do. Everything else keeps moving without them. Tick items as you complete them; the notes say what each unblocks.

## First: read what happened while you were away

The roadmap is implemented through phase 6 with the exceptions listed at the bottom. 26 commits on `main`, pushed to `isletdev/islet`. Start with `docs/ROADMAP.md` (118 items ticked, 68 open, most of them polish) and `docs/DECISIONS.md` (the calls I made, all reversible).

## Accounts and secrets

- [x] **Create the GitHub org `isletdev` and the repo `isletdev/islet`.** Done 2026-09-11; `main` is pushed and CI runs on every push. The website lives in the private `isletdev/website` repo.
- [ ] **Back up the release signing key** at `C:\Users\User1582\.islet\release-signing.key` somewhere offline (password manager or encrypted USB). If it is lost, no installed daemon can ever trust another update. Then add the file's contents as the GitHub Actions secret `ISLET_SIGNING_KEY` on `isletdev/islet`. Unblocks: tagged releases.
- [ ] **Register a GitHub App** (Settings → Developer settings → GitHub Apps → New) so users can pick repositories without pasting tokens and runners can register without a personal access token. Not blocking: deploys work today with any git URL plus a push webhook, runners with a PAT. Settings: name `Islet`, homepage `https://islet.dev`, webhook active with a placeholder URL, permissions Repository → Contents (read), Metadata (read), Webhooks (read and write), Administration (read and write); Organization → Self-hosted runners (read and write); events `push` and `workflow_job`; installable on any account. Then paste the App ID, client ID, slug, private key and webhook secret into Settings → GitHub App; set the app's webhook URL to `https://<panel>/api/v1/hooks/github`. The code is in place and verified up to the point where GitHub must answer.

## Domains

- [ ] **Buy `islet.dev`** (and `islet.sh`, `islet.run` if you want them held). RDAP showed them unregistered on 2026-09-10.
- [ ] **Serve `get.islet.dev`** as the raw content of `installer/get.sh` from `main`. Simplest: a Cloudflare Worker that proxies `https://raw.githubusercontent.com/isletdev/islet/main/installer/get.sh`. Unblocks: the one-line install.
- [ ] Set up `security@islet.dev` and `conduct@islet.dev` (both referenced in `SECURITY.md` and `CODE_OF_CONDUCT.md`), or change those files to addresses you own.

## A test server

Everything host-level was written for Ubuntu and Debian but could only be exercised on Windows against Docker Desktop. On a real server, please run through this list and paste anything that fails:

- [ ] **One Ubuntu 24.04 VPS** (a Hetzner CX22 is enough). Give me root SSH access or run the installer and paste the output. Unblocks: installer, Docker install, systemd unit, TLS on a public IP.
- [ ] **Let's Encrypt:** point a domain at the box, add it on the Domains page with Let's Encrypt, confirm the certificate appears under Domains → Certificates.
- [ ] **Security page:** press each Fix (firewall, fail2ban, auto-updates, swap, NTP), then apply an SSH change and confirm the five-minute rollback works both ways (confirm, and let it expire). Check the Security Score reaches 90.
- [ ] **Deploy:** connect a real repository (a Vite site and a Next.js app are the best first tests), add the webhook from the app's Auto-deploy panel, push, watch it redeploy, roll back.
- [ ] **Runners:** create a GitHub personal access token (classic, `repo`) for a throwaway repository, add a pool on the Runners page, push a workflow with `runs-on: self-hosted`. I could only verify the pool logic, webhooks and the workflow generator without credentials. Same for GitLab and Gitea if you use them.
- [ ] **Backups off-site:** add an S3 (R2 or B2) or SFTP (Storage Box) destination and run a plan; local-path repositories are verified, remote ones are not.
- [ ] **Hetzner API token** (read/write, project-scoped) if you want the e2e job in CI to create and destroy throwaway servers automatically.

## Legal and billing (not urgent)

- [ ] Registration number, address, tax number and a contact address for the legal notice on the website (`../islet-website/index.html` has bracketed placeholders).
- [ ] Have a lawyer read the privacy policy and terms before the site goes public.
- [ ] Check whether Paddle or Lemon Squeezy accept a seller registered in North Macedonia. Only matters when paid work starts.

## Known gaps I left on purpose

Listed as open items in `docs/ROADMAP.md`. The ones most worth your opinion:

- Railpack/Nixpacks buildpacks (deploys use generated Dockerfiles for Node, Python, Go and static sites; PHP, Ruby, Java and .NET repos need their own Dockerfile today).
- Forward-auth "protect this app with Islet login" on routes (needs a cookie shared across subdomains; design question).
- Plugin host, i18n, PWA offline shell, docs site, email reports.
- Traefik's file watcher does not fire on Docker Desktop bind mounts on Windows, so local testing restarts the proxy after route changes. Linux inotify works; nothing to do on a real server.
