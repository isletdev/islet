# Needed from you

Things only the maintainer can do. Everything else keeps moving without them. Tick items as you complete them; the notes say what each unblocks.

## Accounts and secrets

- [ ] **Create the GitHub org `isletdev` and the repo `isletdev/islet`.** Then push `main` from this folder. Unblocks: CI, releases, the update check, the installer's download URL.
- [ ] **Back up the release signing key** at `C:\Users\User1582\.islet\release-signing.key` somewhere offline (password manager or encrypted USB). If it is lost, no installed daemon can ever trust another update. Then add the file's contents as the GitHub Actions secret `ISLET_SIGNING_KEY` on `isletdev/islet`. Unblocks: tagged releases.
- [ ] **Register a GitHub App** (Settings → Developer settings → GitHub Apps → New) so users can pick repositories without pasting tokens. Deploys already work today with any git URL plus a push webhook, so this is not blocking. Settings to use: name `Islet`, homepage `https://islet.dev`, callback URL left empty for now, webhook active with URL `https://<panel>/api/v1/hooks/github` (per install; leave a placeholder), permissions: Repository → Contents (read), Metadata (read), Webhooks (read and write), Administration (read and write, needed for self-hosted runner registration tokens); Organization → Self-hosted runners (read and write); subscribe to events `push` and `workflow_job`. Where can it be installed: any account. After creating it, put the App ID, the client ID and the private key (.pem) somewhere safe; the daemon will take them as settings once the GitHub App code lands.

## Domains

- [ ] **Buy `islet.dev`** (and `islet.sh`, `islet.run` if you want them held). RDAP showed them unregistered on 2026-09-10.
- [ ] **Serve `get.islet.dev`** as the raw content of `installer/get.sh` from `main`. Simplest: Cloudflare Pages project on the repo with a redirect rule, or a Cloudflare Worker that proxies `https://raw.githubusercontent.com/isletdev/islet/main/installer/get.sh`. Unblocks: the one-line install.
- [ ] **Serve `update.islet.dev`** only if you want a first-party update endpoint later; today updates go straight to GitHub Releases, so this is optional.

## A test server

The proxy is built and tested locally with self-signed certificates. Let's Encrypt issuance can only be verified on a server with a public IP and a real domain pointing at it, so that stays unticked until the VPS exists.


- [ ] **One Ubuntu 24.04 VPS** (a Hetzner CX22 is enough, about 4 EUR/month). Give me root SSH access or run the installer yourself and paste the output. Unblocks: verifying the installer, Docker install, systemd unit, TLS on a public IP, and later the Traefik and certificate work. Without it, those roadmap items stay unticked no matter how much code exists.
- [ ] **Hetzner API token** (read/write, project-scoped) if you want the e2e job in CI to create and destroy throwaway servers automatically.

## Legal and billing (not urgent)

- [ ] Registration number, address, tax number and a contact address for the legal notice on the website (`islet-website/index.html` has bracketed placeholders).
- [ ] Have a lawyer read the privacy policy and terms before the site goes public.
- [ ] Check whether Paddle or Lemon Squeezy accept a seller registered in North Macedonia. Only matters when paid work starts, but it is a hard constraint.

## Decisions I made while you were away

Logged in `docs/DECISIONS.md` as they happen. Anything you disagree with can be reversed; nothing has been pushed anywhere.
- [ ] **Try the runners page with a real token.** Create a GitHub personal access token (classic, `repo` scope) for a throwaway repository, add a pool on the Runners page, push a workflow with `runs-on: self-hosted`, and tell me what happened. Same for GitLab (runner authentication token) or Gitea if you use them. I could only verify the pool logic, webhooks and the workflow generator without credentials.
