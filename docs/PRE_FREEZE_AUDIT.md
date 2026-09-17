# Pre-freeze audit — 2026-09-17

Six audits run in parallel, each with its own remit: authentication and secrets;
infrastructure and data safety; MCP and the assistant; UI, UX and accessibility;
Go correctness and tests; install, update and documentation.

Every finding cites a file and line or a measurement. Findings marked **verified
here** were checked a second time by hand against this machine. One finding did
not survive that check and is recorded at the bottom under *Corrections*, because
an audit that only lists what it found is half an audit.

Order of work is at the end. Two rules decide it: anything that can damage
somebody else's running application outranks anything that damages Islet, and
anything already true on this server outranks anything needing a sequence of
events to become true.

---

## The short version

| | Count | What they are |
|---|---|---|
| **Critical** | 16 | Live holes, silent data loss, or an unrecoverable upgrade |
| **High** | 38 | Real failures needing a specific sequence, or defences that do not hold |
| **Medium** | 70+ | Wrong behaviour with a workaround, or a promise the product does not keep |
| **Low** | 40+ | Polish, inconsistency, documentation drift |

**True on this server right now**, not hypothetical:

- `FW-1` — a deployed application holds the firewall's "current admin" exception.
- `VAULT-1` — any signed-in user can write and delete vault secrets.
- `STACK-1` — three unmanaged Compose projects share a namespace Islet does not check.
- `ASSIST-3` — the assistant's authority is a hand-written file nothing validates.

**`go build`, `go vet`, `go test` and `go test -race` all pass.** The race
detector is clean because the concurrent code has no tests: `internal/terminal`
and `internal/watch` have no test files at all, `internal/api` is at 5.6%
statement coverage and `internal/backup` at 0.8%.

---

## Critical

### FW-1 · A deployed application holds the firewall's admin exception
**Verified here.** `ufw status` → `Anywhere ALLOW 172.24.0.7 # current admin`;
`172.24.0.7` is the container `islet-portfolio-r18`. `clientIP()` reads
`RemoteAddr` only (`internal/api/auth.go:205`), so a fix pressed through the
proxy records the proxy network's address and `internal/security/security.go:678`
writes an `Anywhere` allow for it.

With `DEFAULT_INPUT_POLICY=DROP` that container reaches every host port — SSH,
both Postgres instances, everything on `0.0.0.0` — and Docker hands the address
to whatever starts next. The same bug lets the panic button leave a bridge
address as the only permitted source, with the provider console as the only way
back.

**Task:** when `RemoteAddr` is inside the proxy subnet, take the left-most public
`X-Forwarded-For`; treat a private result as unknown and refuse to write the rule
(`firewall.go:89` already handles that case and is currently unreachable). Scope
the rule to SSH and the panel port. Re-derive the rule on this box.

### STACK-1 · An Islet stack can destroy an unrelated Compose project
**Verified here.** `loop-server`, `poolse` and `showcase` run from `/var/www` and
are not Islet's. `WriteStack` (`internal/docker/docker.go:707`) never checks
whether a project name is taken, and Compose scopes by label, not by file — so
`up -d --remove-orphans`, `down` and `down -v` act on the foreign project.
Installing a catalog app named `showcase` takes that site down; removing it with
volumes destroys its data.

**Task:** refuse a name for which
`docker ps -aq --filter label=com.docker.compose.project=<name>` returns a
container not already under `stacksDir`, in `WriteStack` and `catalog.Install`.

### VAULT-1 · Any signed-in user can write and delete secrets
**Verified here.** `internal/api/vault.go:12` (POST) and `:46` (DELETE) carry
`requireAuth` only; `handleVaultReveal:67` is the one with `adminOnly`. The panel
already hides *Show* behind `isAdmin` while leaving *Store* and *Delete* visible
to every role. A viewer overwrites a secret that `expandSecrets` injects into an
app's environment on its next deploy.

**Task:** add the role check to both handlers; gate the two panel controls.

### STREAM-1 · A long log line deadlocks the handler and orphans the process
**Verified here.** `streamLines` does `defer rc.Close()` — which fires *after*
`wait()` returns — then calls `err := wait()` with the reader still open
(`internal/api/docker.go:211,243`). `cmdrun.Stream` uses a synchronous
`io.Pipe`, so when the scanner stops early on one line over 1 MB, the producer
stays blocked and `wait()` never returns. Reproduced with a 2 MB line: still
blocked after 15 s. Reaches **11 endpoints** including container logs, the
journal, catalog install and workspaces. Each hit pins a goroutine and an
unreaped `docker logs -f`.

The correct idiom is four files away: `api/backup.go:197` closes the pipe inside
the wait closure.

**Task:** `_ = rc.Close()` before `err := wait()`.

### BLOCK-1 · The daily refresh re-adds the admin's own address
Found independently by two audits. `EnableBlocklist` excludes `clientIP(r)`;
`RefreshBlocklist` hardcodes `""` (`internal/security/blocklist.go:180`) and
`ReapplyBlocklist` excludes nothing (`:202`). The exclusion therefore applies
once and is undone within 24 hours.

It matters precisely when the admin's address *is* on a chosen list — IPsum lists
reassigned residential IPs routinely. The DROP is inserted at `-I INPUT 1`, ahead
of ufw's chain jump, so no `ufw allow` overrides it: provider console only.

**Task:** persist the excluded address (`security.blocklist.exclude`) and apply it
on every path; let the panel add more.

### UPD-1 · A bad release is unrecoverable
**Verified here.** `internal/update/update.go:257` says the previous binary is
kept because `islet update --rollback` relies on it. That command does not exist
and nothing reads `.prev`. `internal/store/migrate.go:91` refuses to start when
the database is ahead of the binary, so after v0.22.0's migration the older
binary cannot start either. Nothing copies the database before migrating.

A release that starts and then misbehaves leaves no rollback, no older binary and
no snapshot, while `Restart=always` crash-loops every two seconds and the panel —
the only recovery UI — is gone.

**Task:** `VACUUM INTO islet.db.pre-<version>` before applying migrations;
implement `islet update --rollback` restoring binary and database together; make
the refusal message name the file it needs.

### DB-1 · The next table rebuild will silently delete every custom location
`store.go:29` enables foreign keys; `migrate.go:98` runs each migration in a
transaction where the pragma cannot change. With FK on, `DROP TABLE` fires
`ON DELETE CASCADE`, and `domain_locations` gained one in `0024`. Reproduced:
**1 location before, 0 after, transaction committed, no error.**
`PRAGMA defer_foreign_keys` does not help — it defers the check, not the action.
Only `foreign_keys = OFF` on the connection outside the transaction does.

**Task:** in `migrate.go`, take a dedicated connection, turn foreign keys off, run
the migration, run `foreign_key_check`, restore.

### BK-1 · Islet's database is backed up live, in WAL mode, with no checkpoint
`internal/backup/backup.go:822` mounts the data directory read-only and restic
walks it while the daemon writes. No `VACUUM INTO`, no `wal_checkpoint`, nowhere.
The monthly restore test checks only that the file came back, never that it opens.

**Task:** `VACUUM INTO` a staging copy, back that up, exclude `islet.db*` from the
live mount, and make the restore test open the database.

### BK-2 · A backup can leave third-party containers stopped and report success
`pause` stops every container mounting a selected volume; the restart discards its
error (`backup.go:766`) and the run stores `success`. The paused set lives only in
a local slice and scheduled runs use `context.Background()`, so a SIGTERM at 3 a.m.
leaves them down — and `restart: unless-stopped` will not bring them back, because
an explicit stop is what that policy honours.

**Task:** persist the paused set before stopping, restart from it at daemon start,
and downgrade the run to `partial` when a restart fails.

### DUMP-1 · A truncated database dump is published as good
`internal/db/db.go:651` discards `gz.Close()` and `f.Close()` — where the gzip
footer is written and where delayed-allocation ENOSPC surfaces — then renames
`.part` to its final name. It lists at a plausible size, restic backs it up, and
it fails at restore months later with `unexpected EOF`.

**Task:** check both closes, remove the `.part` on error, and only then rename.

### DEP-1 · A failed deploy routes the domain to a container it then deletes
`internal/deploy/deploy.go:1001` routes to the new container; if a later process
container fails, `fail` removes it (`:791`) without re-routing and sets the app
`live`. Traefik points at a container that no longer exists: the site 502s while
the panel says the app is live on the previous release.

**Task:** in `fail`, re-route to the previous container before removing the new one.

### SSH-1 · The lockout safety net writes unchecked
`internal/security/security.go:940` — `_ = os.WriteFile(s.sshdPath, prev, 0o644)`
is the only thing between a bad SSH config and a locked-out admin. At `:886` the
caller reports *"sshd rejected the configuration, nothing changed"*, untrue if the
restore failed. At `:910` the five-minute rollback reloads sshd **unconditionally**
and announces "the previous configuration is back" — on a read-only `/etc` that
applies the unconfirmed config while claiming safety.

**Task:** return the error, include it at both call sites, and emit a Critical
event instead of reloading when the restore failed.

### ASSIST-1 · The subscription provider lends its authority to every asker
Claude Code calls back into `/mcp` with the token from the MCP config file, so
authority comes from *that token's owner*, not from whoever asked
(`internal/assistant/subscription.go:29`, `internal/api/assistant.go:305`,
`internal/auth/tokens.go:311`). Latent here only because every account is an
admin — it becomes live with the first viewer, which is the feature's purpose.

**Task:** mint a per-run token from the asker's scopes and write a temporary MCP
config for that run, or refuse the subscription provider for a narrower caller.

### ASSIST-2 · Prompt injection reaches `islet_request`, and a session is `*`
`internal/api/assistant.go:94` gives a browser session `scopes = "*"`, and
`ScopeAllows` short-circuits on `*` before every rule (`tokens.go:245`). So
`islet_request` reaches what the tool table deliberately withholds: vault reveal,
token minting, user creation. Tool output returns to the model with nothing
marking it untrusted.

**Task:** deny `…/reveal`, `/api/v1/auth/` and `/api/v1/users` at the tool layer
regardless of scope; derive a session's scopes from its role; state in the system
prompt that tool results are data, not instructions.

### ASSIST-3 · Nothing writes or validates the assistant's MCP config
`internal/api/aiproviders.go:119` takes a free-text path; nothing in the daemon
creates or inspects `/var/lib/islet/assistant/mcp.json`. An admin pasting a `*`
token there makes every conversation, for every user, unbounded.

**Task:** generate it the way `wireMCP` does for workspaces — minted token, known
scopes, 0600, revoked on change — and refuse a hand-written path.

### INST-1 · `get.islet.dev` serves whatever is on `main`, and `main` is unprotected
**Verified here.** The redirect resolves to
`raw.githubusercontent.com/isletdev/islet/main/installer/get.sh`, and the branch
protection API returns `404 Branch not protected`. CI checks the installer with
`sh -n` only, and the end-to-end suite has never actually executed. One
unreviewed push is instantly the installer for every new user; `restore.sh` is
fetched the same way.

**Task:** serve from the latest tag, protect `main`, and add a CI job that runs
the real download path against the previous release in a container.

---

## High

### Authority and secrets

| ID | Claim | Where |
|---|---|---|
| SEC-1 | A `*` token can reveal every vault secret, defeating the "admin-and-session only" rule the code states twice. The guard test skips `*`. | `auth/tokens.go:245`, `api/vault.go:67` |
| SEC-2 | The fleet proxy forwards with a `role=admin, scopes=*, never-expires` token after checking only the caller's role, so a narrow `system` token reaches a root shell on any fleet member. | `api/fleet.go:199`, `cmd/isletd/fleettoken.go:87` |
| SEC-3 | Sixteen hand-written MCP tools call services directly, skipping project-scope middleware **and** the audit row — including `run_job`, which executes as root. | `api/mcp.go:106-189` |
| SEC-4 | The Claude Code deny list misses `Agent`, the current subagent launcher. **Verified:** `Agent`, `TaskStop`, `EnterWorktree` are all present in the installed 2.1.271. The same class of miss was fixed once already for `Monitor`. | `assistant/subscription.go:314` |
| SEC-5 | `islet_request` with no body leaves `req.Body` nil and panics; the assistant's goroutine has no recover, so the daemon restarts. Fixed already in `viaRouter` two files away. | `api/mcp.go:290` |
| SEC-6 | A deployer can `docker rm -f` any container on the host, including the proxy. | `api/docker.go:78` |
| SEC-7 | Domain create/edit/delete skips the project-scope check the listing applies. | `api/proxy.go:325,372` |
| SEC-8 | `handleAudit` has no role check: a viewer reads the whole server's audit log. | `api/audit.go:12` |
| SEC-9 | `CapScopes` lets a viewer mint a token with write scopes; only in-handler role checks stop it landing. | `auth/tokens.go:152` |
| SEC-10 | `scopeAdminOnly` does not mean admin-only — a viewer with no project list passes. No live hole; the name asserts what it does not provide. | `api/scope.go:128` |
| SEC-11 | Command stderr is stored and returned unredacted. | `cmdrun/cmdrun.go:153` |
| SEC-12 | `files.IsProtected` is a textual deny-list and the read path follows symlinks. | `files/files.go:61,173` |
| GATE-1 | **The gate cookie can go permanently stale.** `EnsureGate` mints a fresh token on every call and `handleMe` heals only when the cookie is *absent*, so two concurrent `/auth/me` requests leave the browser holding a token the database no longer has. Every protected site then bounces to a login already completed, until sign-out. Also returns success when its `UPDATE` matched zero rows. | `auth/service.go:448`, `api/auth.go:405` |

### Infrastructure and data

| ID | Claim | Where |
|---|---|---|
| INF-1 | No pre-migration database copy: a failed upgrade is unrecoverable in both directions. | `store/store.go:45` |
| INF-2 | Enabling the firewall resets ufw to package defaults, discarding every database allowlist — and `FixAll` does this on a live host. | `security/security.go:651,372` |
| INF-3 | `ufw --force reset` disables the firewall first, so the whole apply window runs unprotected — on the HTTP request's context, which that change is likely to cut. | `security/security.go:651` |
| INF-4 | A blocklist that installed no rules still reports "protected"; with Docker down at enable time, every published container port stays unfiltered. | `security/blocklist.go:356` |
| INF-5 | Shutdown closes the database while deploys and backups are still writing; neither is parented to the root context. | `main.go:297`, `deploy.go:634` |
| INF-6 | `releases.log` and `assistant_messages` grow forever. Measured: 36 assistant rows hold 1.05 MB, one of them 188 KB. | `store/housekeep.go:41` |
| INF-7 | A wildcard domain's exploit-blocker outranks every router of every exact host in the zone, so a normal URL ending `.sql` on another site gets Islet's 403. | `proxy/proxy.go:1416` |
| INF-8 | Priority bands overlap: a wildcard location can tie with an exact host's root, losing that host's forward-auth, basic auth and allowlist. | `proxy/proxy.go:1232,1342` |
| INF-9 | A new `http.Transport` per fleet request, never closed — one leaked SSH channel each. Reproduced: 25 requests, 25 connections still open after two GCs. | `fleet/fleet.go:335` |
| INF-10 | `fleet.Conn.Run` races a `strings.Builder` on every cancelled command. | `fleet/ssh.go:173` |
| INF-11 | The SQL client's idle sweep closes a *busy* session's connection mid-statement. | `sqlclient/manager.go:82` |
| INF-12 | Same early-stop deadlock as STREAM-1 in the deploy pipeline, with the default 64 KB cap and no deadline: one long line hangs a deploy forever. | `deploy/deploy.go:1068` |
| INF-13 | `docker compose up` failure during a catalog update is swallowed and the stream still says "done". | `api/catalog.go:233` |
| INF-14 | A failed catalog install leaves `.env` on disk and blocks retry permanently. | `catalog/catalog.go:211` |
| INF-15 | SSH hardening can silently no-op: if the include line cannot be written, `sshd -t` still passes and the page reports hardened. | `security/security.go:880` |
| INF-16 | ~25 background goroutines sit outside the recover middleware; a panic in any takes the daemon down. | `api/server.go:506` |

### Install, update, documentation

| ID | Claim | Where |
|---|---|---|
| INST-2 | The installer writes a different systemd unit than the repo's — `PrivateTmp=yes`, no `KillMode` — re-introducing the stranded-workspace bug already fixed. The correct unit ships in the tarball and is discarded. | `installer/get.sh:125` |
| INST-3 | `StartLimitIntervalSec=0` sits in `[Service]`, where systemd ignores it. **Verified**: directive at line 142, last section header `[Service]` at 132. | `installer/get.sh:142` |
| INST-4 | The release workflow never verifies its own signature against the embedded key, nor that asset names match what the updater expects. | `.github/workflows/release.yml:64` |
| INST-5 | `SECURITY.md` promises reproducible builds; the binary bakes the build run's timestamp. Measured against published v0.22.1. | `SECURITY.md:42` |
| INST-6 | The self-signed certificate regenerates whenever Docker adds a bridge, invalidating the fingerprint the installer told the user to trust. Twelve bridge addresses are in this box's certificate. | `tlsutil/selfsigned.go:41` |
| INST-7 | No non-browser way to create the first admin, and the installer prints one URL that is wrong on AWS/GCP/Azure and malformed on IPv6-only. | `installer/get.sh:160` |
| INST-8 | `restore.sh` merges over a live data directory with no confirmation and no copy aside; the recovery kit claims it restores dumps, which it does not; it has never been run. | `installer/restore.sh:63,83` |
| INST-9 | `README.md:12` opens with "pre-release … Not yet published or tagged", with 74 tags published. It is the first paragraph of the docs site. **Verified.** | `README.md:12` |
| INST-10 | `security@islet.dev` does not exist, so a vulnerability report bounces. | `SECURITY.md:7` |
| INST-11 | CI does not run oxlint, contradicting the contributor documentation. | `.github/workflows/ci.yml` |
| INST-12 | Three recipe steps name things that do not exist: a container `islet-db-shop`, a "Sessions" settings tab, an "Add service" button. | `docs/recipes/*.md` |

### UI, UX and accessibility

| ID | Claim | Where |
|---|---|---|
| UI-1 | **The focus trap leaks on every type-to-confirm dialog.** `Dialog.tsx` does not filter disabled elements, so while the confirm word is untyped the submit button is disabled and Tab escapes into the sidebar. `Modal.tsx:42` filters and holds. **Verified** — and it affects exactly the dialogs guarding the worst actions: delete user, drop database, prune volumes, lock the server down. | `components/Dialog.tsx:54` |
| UI-2 | **"Remove the proxy" has no confirmation and no error handling** — `onClick={() => api.proxyRemove().then(load)}` tears down Traefik and every routed domain. **Verified.** Every other destructive action on that page confirms. | `pages/Domains.tsx:413` |
| UI-3 | The catalog install overlay is not a dialog: no `role="dialog"`, no `aria-modal`, no accessible name, and focus is left on `<body>` when it opens — ten Tab presses walk the sidebar before reaching the first field. | `pages/Apps.tsx:230` |
| UI-4 | Three SQL overlays have the same problem, and close on a plain backdrop `onClick`, so a text-selection drag that ends outside discards the form — password included. | `pages/Sql.tsx:877`, `components/sql/*` |
| UI-5 | No skip link anywhere: a keyboard user crosses ~20 navigation links before reaching content on every page. | `components/Shell.tsx` |
| UI-6 | Six destructive actions with no confirmation, each beside a sibling that does confirm: image remove, network delete, dump delete, disable 2FA, revoke session, revoke API token. | `Containers.tsx:434,528`, `Databases.tsx:380`, `Settings.tsx:129,215,263` |

---

## Medium and low

Recorded in full in the audit transcripts. The themes worth carrying into the
freeze:

**Scope and authority.** `GET /api/v1/ai/providers` is not admin-gated and returns
the MCP config path. `get_database` returns connection strings to a `read` token.
The default workspace MCP token carries `cron`, which is root execution. A
password change revokes sessions but not tokens. `maxSteps` is caller-controlled
and unbounded. Revoking a token does not stop an in-flight run.

**Silent failure.** Fifteen `rows.Err()` sites unchecked. Fourteen places detect a
UNIQUE violation by matching the driver's English error text. `limitedWriter`
turns a successful 16 MB command into a failure. Unchecked writes that report
success with the file unwritten: DNS provider clearing, `acme-dns.json`, fail2ban
and unattended-upgrades config, the static-site nginx config, SFTP key material,
and the blocklist cache that the whole reboot-persistence story depends on.

**Proxy correctness.** The IP-allowlist validator accepts `1.2.3.4/999` and the
routers then stop serving while the panel reports success. `validPath` permits NUL
and ANSI escapes. Root `-http` and both `www` routers set no priority and fall back
to rule length. Redirect-www always targets `https://`, even on an HTTP-only domain.

**Data lifecycle.** App deletion never removes the volumes it created. Retention
tags snapshots by plan name, so renaming orphans the lineage. A `check` that times
out is reported as corruption. restic credentials are passed as `-e` and visible in
the process table. Decompression bombs have no expanded-size budget.

**UI consistency.** Seven pages use literal `bg-[#0A0A0A]` for the log surface
where nine others use the `bg-code-bg` token — a one-pass mechanical fix. Four
tables lack `min-w-[…]` plus an overflow wrapper. Four pages carry status in colour
alone. Twelve create forms; only one uses a real modal. Files and SQL have no
heading at all. `Files.tsx:228` reads `window.innerWidth` during render with no
resize listener.

---

## Verified correct

A freeze needs the other half of the record.

- **Migrations.** All 34 apply cleanly to a fresh database and stepwise with data;
  `foreign_key_check` and `integrity_check` clean; no row lost in the `0024`
  rebuild. **No migration has ever been edited after shipping** — every file has
  exactly one commit. Each runs in its own transaction, and a database from a
  newer build is refused with an actionable message.
- **Crypto and sessions.** Argon2id at OWASP minimums, constant-time comparison,
  equal-time path for unknown usernames. TOTP is RFC 6238 with a replay guard and
  single-use hashed recovery codes. Session tokens are 256-bit, stored as SHA-256
  only. No gate token exists while MFA is pending.
- **Injection.** No YAML injection is possible in the proxy — everything is
  marshalled from maps, never templated. No shell injection in the firewall or
  blocklist paths: argv through `cmdrun`, no `sh -c`, ipset program on stdin. No
  `innerHTML`, `dangerouslySetInnerHTML` or `eval` in the panel. CSP is
  `default-src 'self'` with `frame-ancestors 'none'`.
- **The signature chain**, verified end to end against published v0.22.1: key in
  the installer byte-identical to the embedded one, signature verifies, one-byte
  tamper rejected, checksum matches, provenance attestation passes.
- **The blocklist's entry filter**, against real FireHOL level 1 content: every
  private, loopback, link-local, multicast and CGNAT range discarded — which is
  what stops the list severing a Docker host from its own containers. The ipset
  swap is atomic.
- **Ownership.** Runs and conversations are keyed to their owner and re-checked
  inside the transaction. Restore never overwrites a live Docker volume. No sweep
  of unknown containers or volumes exists anywhere — that was looked for.
- **Identity headers.** `islet-strip-identity` is in every base chain ahead of any
  gate, so `X-Islet-User` cannot be spoofed on an open route.
- **The protection matrix**, including the `relaxes`/`%2f` interaction and the
  middleware-slice copy. **The seat registry**, the rate limiter and the cached
  stats map are bounded and race-free. **`proxy.Install`** keeps a previous
  container and restores it on failure — the model the other rollback paths should
  be written against.
- **Best-in-class pages**, worth copying: the Domains protection grid (sticky
  column, labelled ticks, measured 20px hit areas), Uptime (status in text beside
  every dot), Vault's reveal flow, Workspaces' modals, the SSH card's five-minute
  rollback, and Files' icon-button labelling.

---

## Corrections

Findings that did not survive a second check, recorded so the list stays
trustworthy:

- **"The panic button fires on a single click" — false.** `Security.tsx:70` calls
  `panic()`, and `panic()` opens a confirm dialog with `typeToConfirm: "PANIC"`
  and a body naming exactly what happens (`Security.tsx:43-49`). The real issue on
  that path is UI-1: the dialog exists, but its focus trap leaks.

One process note: the UI audit deleted this document's first draft, mistaking an
untracked file for a stray write by one of its own helpers. It was rewritten from
the six reports. Nothing in the repository was otherwise modified.

---

## Order of work

1. **FW-1**, **STACK-1** — the two that can damage something else running on this
   machine. Both small.
2. **VAULT-1**, **SEC-1**, **SEC-5**, **UI-1**, **UI-2** — a missing role check, a
   missing session check, a one-line nil body, a one-line focus filter, a missing
   confirmation. All small, all guarding something expensive.
3. **STREAM-1**, **INF-12** — the deadlock, on the most-used endpoint in the panel.
4. **DB-1**, **UPD-1**, **INF-1** — the migration foreign-key trap and the absence
   of any recovery path. Neither is visible until it happens.
5. **BK-1**, **BK-2**, **DUMP-1**, **DEP-1**, **SSH-1** — the paths whose failure
   is discovered when you need them.
6. **GATE-1**, **BLOCK-1** — two lockouts with no in-band recovery.
7. **ASSIST-1/2/3** — the assistant's authority model, latent only while every
   account is an admin.
8. **INST-1**, **INST-2**, **INST-9** — what a stranger downloads and reads.
9. Everything else, in severity order.

Before the freeze is lifted, the three untested paths that would cost the most to
get wrong deserve tests rather than review: `internal/backup`'s restore, the
`streamLines` early-stop case, and `internal/security`'s blocklist apply and SSH
rollback. Each is where a finding above already lives.
