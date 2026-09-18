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

## What `/api/v1` promises

The prefix had no stated meaning, which is the worst of both worlds: clients
could not tell what was safe to rely on, and nobody could tell what would
require a `v2`. It means this.

Within `v1`:

- **Fields may be added to a response.** A client must ignore ones it does not
  know.
- **A field is never removed or retyped.** If it has to change, the new one is
  added beside it and the old one keeps working.
- **A status code for a given condition does not change.** A conflict stays
  409, a failed command stays 502.
- **Unknown fields in a request body are ignored**, so a newer client can talk
  to an older daemon. A typo is therefore silent — that is the price, and it is
  smaller than the alternative, which was that every client had to be upgraded
  in lockstep with the daemon.
- **A list is `[]` or absent, never `null`**, and the TypeScript mirror in
  `web/app/src/lib/api.ts` must agree about which. There is a test.

Removing a field, retyping one, or changing a status code means `v2`.

One thing `v1` does not yet promise: `GET /apps/{id}` returns nine fields —
`status`, `currentRelease`, `url`, `container` and the rest — that `PUT` accepts
and silently discards, because they are derived rather than stored. A client
cannot tell which half of what it read is writable. They are documented as
read-only here rather than removed, because removing them from the response
would break the panel, and refusing them on input would break every client that
round-trips an object it just read.

## Pull requests

- One change per pull request, with a Conventional Commits title (`feat(cron): ...`, `fix(proxy): ...`).
- Include a short "how I tested this" section. For anything that touches the host (firewall, SSH, backups), say which distribution you tested on.
- Keep dependencies minimal. The daemon must stay a single static binary that runs on a 1 vCPU, 1 GB server.
- No attribution trailers in commits.

## Certifying your contribution

Islet uses the Developer Certificate of Origin (developercertificate.org) instead of a CLA: you keep your copyright and certify that you have the right to submit the change. Sign each commit with `git commit -s`, which adds a `Signed-off-by: Name <email>` line; the `dco` check on every pull request looks for it. Forgot? `git rebase --signoff main` and force-push your branch.

## Licence

Contributions are licensed under the repository licence (AGPL-3.0 for the core). By submitting a pull request you agree to that. The name and logo are covered by `TRADEMARK.md`.
