# Contributing to Islet

Thanks for helping. Here is what makes a contribution easy to merge.

## Before you start

- Open an issue for anything bigger than a bug fix, so the design can be discussed first. The roadmap in `docs/ROADMAP.md` shows what is planned.
- Translations are the other easy first contribution: copy `web/app/src/locales/en.json` to your language code, translate the values (keep `{placeholders}`), add one line in `web/app/src/lib/i18n.ts`.
- Catalog apps are the easiest first contribution: add `catalog/apps/<slug>/islet.yaml` and `compose.yaml`, pin the image tag, and test the install on a real server.

## Development

```sh
go build -o isletd ./cmd/isletd
ISLET_DATA_DIR=.data ISLET_LISTEN=127.0.0.1:9443 ISLET_TLS=off ./isletd
cd web/app && pnpm install && pnpm dev     # UI with hot reload, proxied to the daemon
go test ./...
```

The UI is embedded at build time: `pnpm build` in `web/app`, then copy `web/app/dist` to `internal/web/dist` (only the placeholder `index.html` is tracked).

## Pull requests

- One change per pull request, with a Conventional Commits title (`feat(cron): ...`, `fix(proxy): ...`).
- Include a short "how I tested this" section. For anything that touches the host (firewall, SSH, backups), say which distribution you tested on.
- Keep dependencies minimal. The daemon must stay a single static binary that runs on a 1 vCPU, 1 GB server.
- No attribution trailers in commits.

## Certifying your contribution

Islet uses the Developer Certificate of Origin (developercertificate.org) instead of a CLA: you keep your copyright and certify that you have the right to submit the change. Sign each commit with `git commit -s`, which adds a `Signed-off-by: Name <email>` line; the `dco` check on every pull request looks for it. Forgot? `git rebase --signoff main` and force-push your branch.

## Licence

Contributions are licensed under the repository licence (AGPL-3.0 for the core). By submitting a pull request you agree to that. The name and logo are covered by `TRADEMARK.md`.
