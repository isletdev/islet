# Repos, Paid Modules, and Gate Security

Companion to `VISION.md`. This document decides three things: how the code is organised, how Pro is built and sold, and how we keep the gate and the product itself from being abused.

---

## 1. The one fact that shapes everything

**You cannot stop a determined user from removing a license check in open-source code that runs on their own machine.** Go is easy to patch, forks are legal under AGPL, and any obfuscation you add costs you more than it costs them. GitLab, Plausible, Cal.com, Portainer, and Coolify all accept this.

So the strategy is not "make the check unbreakable". It is:

1. **Put the paid value where bypass is impossible**: on infrastructure we run (the Hub).
2. **Keep local Pro code thin** and treat its license check as a fence for honest people plus a legal line, not a wall.
3. **Make paying easier than bypassing**: $12, one click, no seat counting for individuals.
4. **Protect the name, not the bytes**: trademark policy means a fork cannot ship as "Islet Pro".

Everything below follows from this.

---

## 2. Repository layout

### 2.1 Decision: one public monorepo for the product, one private repo for the Hub

| Repo | Visibility | License | Contents |
|---|---|---|---|
| `isletdev/islet` | Public | AGPL-3.0 | Daemon, CLI, web UI, installer, docs, e2e tests, shared packages |
| `isletdev/hub` | Private | Proprietary | Control plane, billing, entitlements, push, AI, uptime probes, self-hosted Hub image build |
| `isletdev/mobile` | Private | Proprietary | Expo app, talks to Hub only |
| `isletdev/catalog` | Public | MIT | App templates, recipes, cron/script templates, fetched at runtime |

**Why a monorepo for the product.** The UI is embedded into the Go binary, so daemon and UI ship as one artefact and must be versioned together. API changes touch both sides in one PR. One CI, one release, one place for contributors. Splitting them buys nothing at this scale.

**Why the Hub is separate and private.** It contains billing, abuse controls, and the entitlement signing key. Nothing in it needs community contribution. Keeping it out of the public repo also keeps the AGPL boundary clean: the Hub is a separate program that talks to the daemon over a network API, so AGPL does not reach into it.

**Why the catalog is separate and MIT.** It changes weekly, it is where community PRs will land, and it is fetched at runtime so users get new templates without upgrading. MIT so nobody hesitates to copy a Compose file out of it.

**No `ee/` directory in the public repo.** GitLab keeps source-available enterprise code next to the open code; that works with a legal team and a CLA process. For a small team it invites "it's right there, why can't I use it" debates and forks that flip one flag. Pro code that must run on the server (see 3.3) lives in the Hub repo and is delivered as a separately built, license-checked plugin, or is simply orchestrated from the Hub with the daemon exposing free primitives.

### 2.2 Public monorepo structure

```
islet/
├─ cmd/
│  ├─ isletd/          # daemon entrypoint
│  └─ islet/             # CLI (same binary, different name via symlink)
├─ internal/             # not importable by other modules
│  ├─ api/               # HTTP handlers, OpenAPI, middleware (auth, entitlements)
│  ├─ auth/              # sessions, TOTP, passkeys, tokens
│  ├─ docker/            # Engine API wrapper
│  ├─ proxy/             # Traefik config reconciler
│  ├─ deploy/            # git, buildpacks, rollout
│  ├─ db/                # database provisioning
│  ├─ cron/              # scheduler, script store
│  ├─ files/             # file explorer backend
│  ├─ notify/            # event bus, channels, routing
│  ├─ security/          # hardening, firewall, scanners
│  ├─ backup/            # restic wrapper
│  ├─ metrics/           # collectors, ring buffer
│  ├─ reconcile/         # desired state engine
│  ├─ hublink/           # outbound connection to Hub, entitlement cache
│  ├─ plugins/           # plugin host (WASM), used for Pro local modules too
│  └─ store/             # SQLite, migrations
├─ pkg/                  # importable by the Hub and by third parties
│  ├─ api/               # request/response types, event schemas
│  ├─ entitlements/      # token format and verifier (public key only)
│  └─ client/            # Go client for the daemon API
├─ web/                  # React + Vite + TS, pnpm workspace
│  ├─ app/               # the panel
│  └─ packages/ui/       # shared components, published as @islet/ui for the Hub
├─ installer/            # get.sh, uninstall.sh
├─ deploy/               # systemd unit, goreleaser, apt/rpm packaging
├─ e2e/                  # tests that create a real VPS and run the installer
├─ docs/
└─ .github/workflows/
```

Tooling: single Go module, pnpm workspace for `web/`, Taskfile for the glue, goreleaser for releases, `embed.FS` for the built UI, golangci-lint, Playwright for UI tests, a Hetzner-backed e2e job that costs about one cent per run.

### 2.3 Contributor legal setup (do this before the first external PR)

- **CLA** (Contributor License Agreement) via a bot on PRs, naming Torsten Labs DOO as the licensee. Without it you cannot later dual-license, offer a commercial license to a customer who cannot accept AGPL, or move code into the Hub. DCO alone is not enough for open-core.
- **Trademark policy** file: the name and logo are not covered by AGPL; forks must rename. This is the enforcement mechanism that actually works.
- `SECURITY.md` with a private disclosure address, `LICENSE` (AGPL-3.0), `LICENSE-CATALOG` (MIT).

---

## 3. How Pro is built

### 3.1 Classify every Pro feature by where it runs

| Class | Where the value lives | Bypass possible? | Examples |
|---|---|---|---|
| **A. Hub-native** | Entirely on our infrastructure | No | Fleet dashboard, mobile push, external uptime probes, status page, included backup storage, AI Operator, provider billing dashboards, SSO, Team RBAC, audit export, on-call |
| **B. Hub-orchestrated** | Hub drives free daemon primitives | Only by rebuilding the Hub | PR previews (Hub listens to GitHub, tells daemon "create env"), Server as Code and drift detection (Hub diffs exported state), cross-server runners, clone server, golden templates, Swarm coordination |
| **C. Local plugin** | Code must run on the server | Yes, by a fork | Postgres PITR agent (WAL shipping), CVE continuous scan scheduler, secret-leak scanner, session recording |

Design rule: **prefer A, then B, and only accept C when the feature is impossible otherwise.** Most of the Pro list in `VISION.md` is already A or B. Class C is small, and it is the only place where a bypass is even possible.

### 3.2 The daemon exposes primitives, the Hub composes them

The free daemon has an API that can create an environment, apply a config bundle, export desired state, run a job, and stream events. None of that is gated. What is gated is the orchestration: watching GitHub for PRs, computing drift across ten servers, scheduling cross-server work. That orchestration is Hub code.

This also keeps the open-source product honest: nothing in the free binary is crippled or hidden, and a user with the API can build their own orchestration if they want to. They just will not get ours for free.

### 3.3 Class C modules are plugins, not compiled-in code

- The daemon has a plugin host (WASM via wazero, or a sidecar process over gRPC for things that need real system access). Plugins are also how the community extends the panel, so this is not Pro-only infrastructure.
- Pro plugins are built from the private Hub repo, signed with our release key, and downloaded by the daemon from the Hub **only after** the entitlement check passes.
- The daemon verifies the plugin signature before loading. A fork can remove that verification, but it then has to obtain the plugin binary somehow, and redistributing it is a plain license violation with no AGPL cover.
- Plugins declare the capabilities they need (`db:postgres:wal`, `files:read:/var/lib/islet`) and the user sees and approves that list. This matters for trust: "closed code running as root" is the main objection to Pro on a self-hosted box, and a capability manifest plus an audit log answers it.

### 3.4 Entitlements: how the daemon knows what you paid for

```
User pays (Paddle/Stripe) -> Hub updates subscription -> Hub signs entitlement token
                                                              |
Daemon <- pairing (one-time code from Hub UI) <- outbound WebSocket to Hub
Daemon caches token, re-fetches every 24h, keeps a 14-day offline grace
```

- **Token**: a compact signed payload (Ed25519, PASETO v4 or a minimal JWT) with account id, plan, feature list, server limit, seat count, `issued_at`, `expires_at` (7 days). The public key ships in `pkg/entitlements`. The private key lives only in the Hub, in a KMS or HSM-backed secret, never on a developer machine.
- **Revocation** is expiry: cancel a subscription and the daemon loses Pro within 7 days at most. For immediate cutoff the Hub pushes a "re-fetch now" message over the live connection.
- **Offline**: a self-hosted Hub (Team tier, later) issues the same tokens from its own key pair, which the daemon trusts if the user pinned it during pairing. Grace of 14 days covers air-gapped or flaky setups.
- **One check point.** Every Pro API route goes through an `entitlements.Require("feature")` middleware. The UI hides or upsells, but the UI is never the gate. No scattered `if isPro` checks in business logic.
- **Server and seat limits** are counted by the Hub, since servers must connect to it to be part of a fleet. This is enforced server-side and cannot be bypassed.

### 3.5 Billing

- Use a **merchant of record** (Paddle, or Lemon Squeezy) rather than raw Stripe: they handle VAT, sales tax, invoicing, and are available to sellers in more countries. The seller is Torsten Labs DOO in North Macedonia, so eligibility must be verified with each provider before choosing; this is a hard constraint.
- Plans: Pro Individual monthly and annual, Pro Team per seat. Trial of 14 days requires a card, which is the cheapest anti-abuse control there is.
- Webhooks from the billing provider update the subscription record; the Hub is idempotent on webhook replays and reconciles nightly against the provider API in case a webhook is missed.
- Entitlements are derived from the subscription record, never from the webhook payload directly.

---

## 4. Keeping the gate honest without fighting your users

- **Accept the fork.** If someone forks, renames, and patches out the Class C check, they have a worse product with no Hub, no updates, and no name. That is the equilibrium. Do not build phone-home telemetry to "catch" them.
- **No dark patterns.** No expiring free features, no nag screens on the free tier beyond a single "Pro" item in the sidebar. Community goodwill is the growth engine for open-core.
- **Trademark enforcement** is the tool for anyone who redistributes Pro plugins or sells a rebranded Hub. Register the mark early in the EU and US.
- **Do not put the entitlement private key anywhere near the public repo.** Rotate it yearly; the daemon accepts two public keys during rotation.

---

## 5. Securing the product itself (this is where real risk lives)

The daemon runs as root on someone's production box and can self-update. A compromise of our release pipeline is far more dangerous than a bypassed paywall.

### 5.1 Supply chain
- Reproducible builds with goreleaser, signed with cosign (keyless via GitHub OIDC), SLSA provenance attached to every release.
- The daemon's self-update verifies the signature against a pinned public key before replacing the binary, and refuses downgrades.
- `get.sh` is served from a static host with a published checksum, and it downloads a specific version by checksum, never "latest" by name.
- Dependencies pinned, Dependabot on, `govulncheck` and Trivy in CI, no CGO where avoidable.
- Release keys and Hub signing keys in a KMS, two-person approval for releases once there is a second person.

### 5.2 Daemon hardening
- Runs as root but drops privileges per operation where possible; file explorer, cron runs, and container exec honour the run-as user and the panel role.
- Panel auth: Argon2id, 2FA enforced for admins after first login, session binding to IP prefix (optional), rate limiting and lockout on login, CSRF tokens on state-changing requests, strict CSP, no inline scripts.
- API tokens are scoped and hashed at rest; the MCP server uses a token with explicit scopes and is off by default.
- Local socket for runners and CLI uses Unix peer credentials, not a shared secret.
- Secrets at rest encrypted with a key derived from a machine secret stored `0600` under the panel's state directory; backups of state include it only when the user opts in.
- Everything the daemon executes is logged to the audit log and the command transparency drawer, including what the Hub asked it to do.

### 5.3 The Hub connection is the trust boundary users care about
- The daemon connects **outbound** only; the Hub cannot reach a server that has not paired.
- Pairing produces a per-server key pair; the Hub authenticates with mTLS over the WebSocket.
- The Hub sends **intents** ("deploy app X at commit Y"), not shell commands. The daemon validates each intent against its own policy and the user-chosen scope for that server: **view**, **deploy**, or **admin**.
- Users can see every Hub-initiated action in the audit log and can unpair with one click, which invalidates the server's key and Pro token together.

### 5.4 Abuse of the hosted Hub
- Trial requires a card; free accounts get no included storage, no external probes, and no AI.
- External uptime probes only target hostnames that resolve to a paired server's IP or a domain the account has verified via DNS TXT. This prevents using probes as a scanner or a DDoS amplifier.
- Included backup storage is encrypted client-side, quota-enforced, and rate-limited; terms forbid using it as general file hosting, and per-account egress is capped.
- AI Operator has per-account token budgets, only receives server context the user selects, and never executes commands without a confirmation step.
- Per-account and per-IP rate limits on every Hub API, plus an abuse contact and a fast disable switch per account.

### 5.5 Telemetry and privacy
- No telemetry in the free daemon by default. An opt-in, anonymous "ping with version and OS" helps plan support for distros, and the exact payload is shown before enabling.
- The Hub stores metrics and logs only for servers the user paired, with a documented retention, and offers export and deletion.

---

## 6. Decisions summary

| Question | Decision |
|---|---|
| Monorepo or multi-repo? | Monorepo for the open product; separate private repos for Hub and mobile; separate public repo for the catalog |
| Where does Pro code live? | In the Hub repo. Hub-native and Hub-orchestrated features by default; a small set of signed local plugins for what must run on the server |
| How is payment enforced? | Signed entitlement tokens issued by the Hub, checked in one API middleware, server and seat limits counted by the Hub |
| Can users bypass it? | Only Class C local plugins, by forking and patching. Accepted by design; trademark and the Hub make it not worth doing |
| Billing provider? | Merchant of record (Paddle or Lemon Squeezy), card-required trial, webhook plus nightly reconciliation |
| Biggest real security risk? | Our release pipeline, not the paywall. Signed reproducible builds, verified self-update, outbound-only Hub link with scoped intents |
| Legal prerequisites? | AGPL-3.0 core, MIT catalog, CLA bot, trademark registration, SECURITY.md |
