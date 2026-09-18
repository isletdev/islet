# Islet — design review

A review of Islet as a product: what it is, what it claims to be, where the
design contradicts itself, and what is worth changing while changing it is
still cheap. It is **not** a review of the server this repository happens to be
checked out on, and it contains no findings about that machine's state.

Six reviews were run in parallel — architecture, product coherence, API and data
model, security design, UX, operational fitness — and every load-bearing claim
below was verified by hand against the source at `74406ca` (v0.22.1) before it
was written down. Claims that did not survive that check are recorded in
[§9](#9-corrections).

The companion document, `docs/PRE_FREEZE_AUDIT.md`, is the defect list. This one
is about design. Where they overlap, this file states the design reason and the
audit states the bug.

---

## 1. The summary

Islet is a very good single-operator control panel wearing the documentation of
an earlier, smaller product, and carrying a third one in its sidebar. The build
is substantially ahead of the pitch.

Three things are true at once:

- **The engineering is better than the category norm.** A 33 MB static binary
  that idles at 20–28 MB RSS and 0.1% of a core. Ed25519 + cosign + SLSA +
  reproducible builds. `cmdrun` as a single argv-only exec door, with a
  command-injection sweep that found no site where request-derived text reaches
  a shell unvalidated. Refusal copy that explains mechanisms rather than naming
  error codes.
- **The documentation describes roughly v0.8.** The README's feature table omits
  Workspaces, the Assistant, the Vault, the SQL client and multi-server. The SQL
  client alone is ~5,800 lines plus a 959-line page and appears nowhere.
  `VISION.md` is still linked as "Full product vision" while describing an
  abandoned open-core plan the roadmap contradicts.
- **Several stated boundaries are not boundaries.** A workspace agent's token is
  documented as bounded by withholding `shell`; it is root-equivalent. Project
  scopes are documented as a wall; they are applied to 17 routes and ignored by
  every other write path. `CLAUDE.md` says every machine-bound table is
  `server_id`-scoped; five tables' unique constraints are global.

The through-line: **Islet is honest in its code and optimistic in its prose.**
Almost every finding in this document is a place where the prose got ahead of
the code, and the fix is usually to change one of them to match the other.

---

## 2. What Islet is, and what it should say it is

### 2.1 The positioning is inverted

The README leads with deploys. Deploys are table stakes: Coolify has 57k stars,
280+ services and native multi-server; Dokploy, CapRover and Portainer all do
this. Islet has 27 catalog entries and a server switcher. **On the deploy axis
Islet is behind, and it should stop competing there.**

What nobody else in the category ships:

- **A scored, fixable, reversible hardening flow.** A security score with
  one-click fixes, each with a rollback, each explaining what it changes.
- **The protection gateway.** Per-path, per-person SSO in front of any app, with
  a user×location allow-list matrix, delivered through Traefik's forwardAuth
  with measured header stripping.

That is the reason to exist. The pitch should say so first.

**Task P-1.** Rewrite the README's opening and feature table around the security
surface and the protection gateway, with deploys as the thing that makes them
useful rather than the headline. **P-2.** Bring the feature table to the actual
v0.22.1 surface — Workspaces, Assistant, Vault, SQL client, multi-server, AI
providers. **P-3.** Unlink `VISION.md` from the README or rewrite it; a document
describing an abandoned business model, linked as the vision, is worse than no
vision document.

### 2.2 What Islet should say it is *not* for

The strongest thing the security review produced is not a finding, it is a
paragraph Islet should publish:

> Islet is a single-tenant administration tool. Every account on it is an
> account on the server. An admin is root — deliberately. "Deployer" and
> "viewer" are convenience roles for colleagues you already trust, not a
> security boundary against them: they are enforced by hand at each endpoint,
> they do not extend to the forward-auth gateway or the MCP tool surface, and
> several of them reach root today. Islet does not isolate the applications it
> deploys from each other or from the host — no capability dropping, no user
> namespaces, a shared Docker daemon — so it is not a platform for running code
> you did not write, for customers you do not control. A copy of the data
> directory, or of one Islet-state backup snapshot, is a copy of every
> credential on the server including every TOTP seed, and the key that protects
> them cannot currently be rotated. Enabling the assistant or a workspace agent
> grants an LLM the asker's full authority with no confirmation step, and a
> workspace agent is root on the host, not a sandbox. The fleet feature makes
> the controller the permanent root of every server it enrols.

None of that makes Islet a worse product. It makes it the thing it actually is,
and "honest about running as root" is its best security property — it should be
extended to the places where the docs currently claim otherwise.

**Task P-4.** Publish that paragraph, in the README and in `docs/SECURITY.md`.

### 2.3 Feature maturity is uneven and unstated

| Finished | Usable but unproven | Demo-quality |
|---|---|---|
| Domains, Security, Notifications, Containers, Files, Workspaces, SQL | Backups — the only feature whose failure is unrecoverable, and the one with 0.8% test coverage | Runners, Assistant, Embed, i18n |

**Task P-5.** Decide per feature: finish, mark experimental in the UI, or remove
before the freeze. Shipping four demo-quality features unlabelled in a sidebar
that reads as a finished product is the single largest gap between what a
stranger expects and what they get.

---

## 3. Architecture

### 3.1 Four documented packages are empty directories

`internal/reconcile`, `internal/plugins`, `internal/runners` and `pkg/client`
contain no Go files. `docs/STRUCTURE.md` describes all four as built, and
`CLAUDE.md`'s rule — "no feature package imports another; they meet through
`store`, `reconcile` and `notify` events" — names one of them as the mechanism.

**Task A-1.** Delete the directories and the doc entries, or build them. The
rule they support is real and mostly observed; it just does not work the way the
document says.

### 3.2 `notify.Bus` has 40 emitters and no subscribers

There is no `Subscribe` method. Every one of the 40 `Emit` call sites writes an
`events` row and queues deliveries. That is a fine design — it is a durable
outbox, not a bus — but it is documented as the decoupling mechanism between
feature packages, and it cannot decouple anything without a read side.

**Task A-2.** Either add `Subscribe` and use it for the one case that wants it
(reconcile-on-change), or rename the concept and correct the doc. The current
state invites the next contributor to build on a seam that is not there.

### 3.3 `internal/api` is 13,541 lines and owns policy

`pkg/api` is 139 lines. `internal/api` holds 290 registered routes, 65
`adminOnly` call sites, 96 hand-written role checks across 25 files, and every
authorization decision in the product. `CLAUDE.md` says `internal/api` is the
only package that knows about HTTP; in practice it is also the only package that
knows about *authorization*, and that is a different and much larger claim.

**Task A-3.** See [§6.1](#61-there-is-no-authorization-middleware) — the
declarative role table is the fix for this too. It moves 96 scattered decisions
into one reviewable artefact.

### 3.4 Four background loops outlive shutdown

`api.New` starts four loops on `context.Background()`, so the SQL client's
janitor can never close users' database connections at shutdown. This is the
same root shape as [§7.2](#72-an-unreachable-backup-target-hangs-a-plan-forever)
and [§7.5](#75-a-restart-marks-work-dead-while-the-work-keeps-running): work
derived from `Background()` rather than the daemon's context.

**Task A-4.** Audit every `context.Background()` in long-lived code and decide,
per site, whether it is deliberate (a run that must outlive a closed tab — of
which there are several, correctly) or accidental.

---

## 4. Product coherence

### 4.1 Documentation drift

Beyond the README: `docs/openapi.yaml` declares `version: "0.5"` against a
v0.22.1 product, documents **94 of 238 paths (39%)**, and lists two paths —
`/api/v1/provider` and `/api/v1/provider/snapshot` — that do not exist anywhere
in `internal/`.

**Task C-1.** Bring the spec to 100% of paths and delete the phantom entries, or
delete the file. At 39% with a stale version and dead routes it is actively
harmful, because it is the artefact a third-party integrator will trust.

### 4.2 Editing any domain silently deletes its basic-auth users

`Domains.tsx:467` opens the editor with `setEditing({ ...d, basicAuth: "" })` —
correct, the panel should not echo hashes. But `proxy.Save` writes `d.BasicAuth`
straight into the UPDATE with no keep-if-empty branch. Open a domain, change the
port, save: the credentials are gone, silently. `SetDNS` in the same file *does*
have exactly that logic, so the pattern exists and was not applied here.

**Task C-2.** Apply the `SetDNS` keep-if-empty pattern to `BasicAuth`. Four
lines. This is a data-loss bug on a routine action.

### 4.3 `@vault:NAME` only expands in app environments

Expansion is implemented in `internal/deploy` and nowhere else. `internal/cron`,
`backup`, `notify`, `uptime`, `runner`, `db` and `sqlclient` contain no
reference to it. `Vault.tsx:68` tells the user the reference works in "a cron
command". It does not; they get the literal string.

**Task C-3.** Either wire expansion into cron (the case the copy promises) or fix
the copy to say where it works. A secret store that silently fails to expand is
worse than not shipping one.

---

## 5. API and data model

### 5.1 The SSE terminal event is not JSON — a verified crash

`docker.go:248` and `fleet.go:147`:

```go
fmt.Fprintf(w, "event: end\ndata: %q\n\n", msg)
```

`%q` is Go quoting, not JSON. Measured:

```
Go:   "error: docker build: exit 1: \x1b[31mfailed\x1b[0m"
Node: JSON.parse → Bad escaped character in JSON at position 31
```

The `line` events use `json.Marshal` and are safe; only the terminal event does
not. Its payload is `cmdrun`'s error — the tail of stderr — and docker, build
tools and package managers emit ANSI colour by default. `stream.ts:44` parses it
with no `try`, inside the read loop. **A failed coloured build therefore throws,
and the user is told "the connection closed before the command finished" for a
command that finished with a real error nobody ever sees.** Compounding it, the
failure signal is `text.startsWith("error")` — a prefix match on English prose.

**Task D-1 (do not defer).** Make the end event a JSON object,
`{ok, error?, message?}`. Four lines. Before the freeze it is a bug fix; after
it, a protocol break.

### 5.2 Twelve error mappers ending in `default: 400 + err.Error()`

Measured across the package: **400×170, 502×39, 409×10, 422×0**, and **217
sites** put a raw `err.Error()` into the client-visible message.

- **Status.** Thirteen distinct "already exists" errors return 400. `deployErr`
  already maps `ErrBusy` to 409, so the machinery exists and is used ten times
  in the whole API.
- **Meaning.** The same arm catches restic, rclone, `psql`, `docker exec`,
  `ufw`, `sshd -t`, `useradd` and GitHub calls, and reports all of them as "your
  request was bad". `dockerErr` gets this right with 502. Two opposing
  conventions in one package.
- **Leak.** `cmdrun.go:45` formats errors as `"%s: exit %d: %s"` where the first
  `%s` is the full argv. Secrets are redacted; paths, unit names, container
  names and 400 bytes of stderr are not. `security.go:42`, `host.go:21` and
  `security.go:180` append stdout on top. All 76 `bad_json` sites pass
  `encoding/json`'s message through, naming internal Go struct fields.

`authError` does it correctly — a `500 {"internal","internal error"}` default
plus an explicit allow-list of prefixes it will echo. Nothing else copies it,
including `users.go` in the same package.

**Task D-2.** One shared `serviceErr(w, err)`: sentinels first (`ErrNotFound`
404, `ErrBusy`/`ErrExists` 409), default 502 for anything wrapping a `cmdrun`
error and 500 `"internal error"` otherwise, detail to the log. Keep the ~128
hand-written literal messages; they are good. The problem is the 217 that were
never written for a human.

### 5.3 Four `null`-slice crashes, and the shape that produces them

59 Go response slice fields have no `omitempty`, so a nil slice marshals to
`null`; the TS mirror declares them required. Four sites deref today:

| Site | Cause |
|---|---|
| `db.go:88` | `if out.Databases, err = …; err != nil` assigns **before** the error check; `db.go:300` returns `nil, err` → `Databases.tsx:365` `.map` throws |
| `db.go:98` | `out.Dumps, _ = s.db.Dumps(inst)` — error discarded, same shape |
| `runner.go:219` | returns `nil` whenever `docker ps` fails → `Runners.tsx:68` |
| `attention.go:68,80,92,102` | four fields written only on success; `Attention.tsx` iterates unguarded → a notify query error blanks the dashboard |

The signature is already visible: `backup.go:44` coerces `plans` to `[]` while
`destinations` in the same literal was not — somebody was bitten once and fixed
one field.

**Task D-3.** A ~40-line reflection test over a registry of response types that
fails on any slice field without `omitempty`, plus hand-fixing the two
assignment-before-error-check bugs. **Not** an OpenAPI generator: 96 interfaces
against 253 shapes is past the point where hand-mirroring works, but the repo's
rule is no third-party tooling, and `null` → absent is a widening TS already
models correctly.

**Task D-4.** Promote the ten `map[string]any` responses the panel actually
types — `Attention`, `SecurityState`, `BackupOverview`, `DBDetail`,
`ProxyStatus`, `DockerStatus`, `MailRelay`, `AssistantConfig`, `Registry`,
`LogSource` — into named `pkg/api` structs. That makes `pkg/api`'s doc comment
("the source for the TypeScript types") true for the first time; today 372 of
~390 usages of the package are `api.Error`.

### 5.4 Naming inconsistencies that are cheapest to fix now

- `POST /ai/providers/{id}` is the lone update-by-POST among a dozen `PUT`s.
- `/databases/{name}/databases/{db}` — "database" means an instance and a
  database one segment apart.
- `POST /docker/containers/{id}/{action}` puts the verb in a wildcard, so
  `limits` (a literal sibling route) is a permanently reserved container name,
  and a client cannot learn the vocabulary from the route table.
- `/catalog/{slug}` is shadowed by literal `source` and `installed` siblings in
  a **user-controlled** key space — a catalog app named `source` is unreachable.
- Four names for cancel (`cancel` on apps, plans, runs, SQL; `kill` on cron), at
  three nesting levels.
- `POST /workspaces/tmux` and `/workspaces/claude` install host packages and are
  not workspaces.
- `GET /api/v1/ping/{token}` **mutates** — a prefetcher marks a heartbeat alive.
  The POST already exists on the same path.

**Task D-5.** Fix all seven. Every one is a rename, all are breaking, and they
will never be cheaper than before a freeze.

### 5.5 Three tables with no unique name, and no idempotency anywhere

`checks`, `channels` and `jobs` have **no unique constraint at all**; nine other
collections have `UNIQUE (server_id, name)`. A retried `POST /cron/jobs` creates
a second job on the same schedule, which then runs the command twice forever.

There is no optimistic concurrency in the product: zero `If-Match`, `ETag` or
`Idempotency` occurrences. Eight tables carry `updated_at` and nothing reads it.
Two admins editing one app is a silent last-writer-wins over 24 columns; two
admins editing env groups, registries or sidebar links is a read-modify-write of
one encrypted JSON blob and loses one of them entirely.

**Task D-6.** `UNIQUE (server_id, name)` on the three tables. **D-7.**
`If-Unmodified-Since` on the five full-body PUTs that matter (apps, domains,
jobs, plans, checks) — the column is already there. **D-8.** Move
`deploy.env_groups`, `docker.registries` and `sidebar.links` out of `settings`
into real tables, which removes the read-modify-write rather than papering over
it. `0034_ai_providers.sql` is the model migration for all three: it moved five
settings keys into a table, carried the sealed credential without decrypting it,
and made the no-op case a no-op.

### 5.6 The `server_id` convention is half-applied

`CLAUDE.md` says every machine-bound table carries `server_id` "so the fleet
never needs a schema rewrite". Every table carries the column. But the
**uniqueness constraints** do not, on the five tables written before `0020`:
`apps.name`, `domains.host`, `backup_destinations.name`, `backup_plans.name`,
`runner_pools.name` are all globally unique. Separately, `fleet_servers`,
`vault_secrets`, `assistant_chats` and `assistant_messages` have no
`REFERENCES servers(id)` while every other table does, under
`PRAGMA foreign_keys(ON)`.

**Task D-9.** Server-scope the five indexes and add the four missing foreign
keys. One migration; latent today, a data migration later.

### 5.7 Five missing indexes, with query plans

```
events prune       SCAN deliveries              ← FK cascade, full scan per deleted event
deliveries/event   SCAN deliveries              ← GET /notify/events/{id}/deliveries
audit list         USE TEMP B-TREE FOR ORDER BY ← sorts up to 200k rows per page load
commands list      USE TEMP B-TREE FOR ORDER BY ← same
checkres prune     SCAN check_results           ← structurally the largest table
```

`deliveries` is indexed only on `(status, next_try_at)`. `audit_log` and
`commands` are indexed on `(server_id, created_at)` — which correctly serves the
prune — but every list query orders by `id DESC`, so two of the most-visited
pages sort in a temp b-tree on a 1 vCPU box.

**Task D-10.** One migration, five indexes: `deliveries(event_id)`,
`deliveries(channel_id, status)`, `audit_log(server_id, id DESC)`,
`commands(server_id, id DESC)`, `check_results(check_id, at)`. Additive, no
behaviour change, and the last one is load-bearing for [§7.1](#71-uptime-monitoring-is-quadratically-self-defeating).

### 5.8 `/api/v1` has no stated meaning

Nothing in `DECISIONS.md`, `CONTRIBUTING.md`, `README.md` or `STATUS.md` says
what `v1` guarantees. Meanwhile `dec.DisallowUnknownFields()` applies to all 76
decode sites, so the **request** contract is strictly non-forward-compatible: a
newer client sending an unknown field gets a 400 on every save. Invisible while
the panel ships in the same binary; fatal for the CLI, MCP and API tokens.
Conversely `GET /apps/{id}` returns nine derived fields that `PUT` accepts and
silently discards, so a client cannot tell which half of what it read is
writable.

**Task D-11.** Write the eight-line policy (fields may be added; never removed
or retyped; status codes for a condition do not change; unknown request fields
are ignored), relax `DisallowUnknownFields` on requests, and mark derived fields
`json:"-"` on input. Or drop the prefix. A frozen `v1` with no stated meaning is
the worst of the three options.

---

## 6. Security design

The threat model the code implies is coherent and worth stating plainly: the
machine, the Docker daemon and any admin are trusted absolutely; the internet,
the browser and deployed git repos are not. **Admin is root, by design**, and
the compensating control is legibility — `cmdrun`, the transparency drawer, the
audit log. That is the right call for a single-server panel.

The incoherence is entirely in the middle tier: deployer and viewer are treated
as semi-trusted colleagues by a design that documents them as a boundary.

### 6.1 There is no authorization middleware

The chain is `recover → logRequests → securityHeaders → withSession`. Nothing in
it checks a role. All 290 routes are `requireAuth` only; role is decided by 96
hand-written `if u.Role` lines across 25 files plus 65 `adminOnly` sites.
Adding a route and forgetting the check yields a viewer-writable endpoint,
silently — which is how [§6.3](#63-any-signed-in-user-can-overwrite-every-vault-secret),
[§6.4](#64-a-deployer-can-strip-protection-and-publish-the-panel) and the
`POST /notify/emit` gap all happened.

`routescope_test.go` already proves every route is covered by some **token
scope**. There is no mirror test for **roles**.

**Task S-1 (highest value per line in the document).** A declarative
`(method, pattern) → minimum role` table applied in `New()`, defaulting to admin
for anything unlisted, plus the mirror test. One reviewable table replaces 96
decisions and turns "forgot a check" into a test failure.

### 6.2 The workspace agent's MCP token is root-equivalent and never expires

```go
s.auth.CreateToken(r.Context(), u.ID, "workspace "+name, "read,logs,containers,cron,notify", 0)
```

Owner is the creating **admin**; TTL `0` means never. The comment directly above
it and `docs/WORKSPACES.md:92-94` both claim withholding `shell` bounds it.

- `cron` → `POST /cron/jobs` is `adminOnly` and *satisfied by the owner* → a
  `type:"command"` job runs `/bin/sh -c` as root.
- `containers` → `handleStackWrite` → a Compose file with `privileged: true` or
  `- /:/host`, brought up.
- `read` + `/api/v1/servers/{id}/proxy/…` → any file on any fleet member, because
  the `files` scope carve-out matches the *local* path.

The token is one line in `<data>/workspaces/<id>/.mcp.json`, read by an agent
that also reads untrusted READMEs. **Prompt injection there is host root plus
fleet-wide file read, permanently, with no `shell` in the audit trail.** The
workspace is a tmux session started by `systemd-run` with no `--uid` — root,
unconfined, not a container.

**Task S-2.** Drop `cron` and `containers`; add a TTL tied to workspace
lifetime; correct `docs/WORKSPACES.md`. If the agent genuinely needs those
scopes, the documentation must say a workspace agent is root.

### 6.3 Any signed-in user can overwrite every vault secret

`POST /api/v1/vault` and `DELETE /api/v1/vault/{name}` are wrapped in
`requireAuth` only, and neither handler contains a role check. Only `/reveal` is
gated. `vault` is not in `writeScopes`, so a viewer may hold the scope too.
Vault values are expanded into app environments at deploy time, so this is
credential **substitution**, not only destruction.

**Task S-3.** `adminOnly` on both. Two lines.

### 6.4 A deployer can strip protection and publish the panel

`handleDomainSave` explicitly refuses a non-admin who changes protection, with a
comment saying that was the fix for this exact bug class. But
`handleDomainDelete` checks only `Role == "viewer"`, and on *create* `before` is
nil, so `ProtectionEquals(nil)` compares a zero `Domain` and `protect:false`
passes. **Delete + recreate removes the gate**, both steps deployer-permitted.

Separately, `validateTarget`'s entire `url` case is a `http(s)://` prefix check.
`{targetType:"url", target:"https://127.0.0.1:9443"}` publishes the panel,
routing around both the admin-only `panel` gate and the panel IP restriction.
The same primitive reaches `169.254.169.254`.

**Task S-4.** Make domain delete admin-only when the domain is protected.
**S-5.** Reject loopback, private-range and link-local URL targets.

### 6.5 The fleet proxy collapses every scope carve-out

`ScopeAllows` matches the **local** path, so prefixing
`/api/v1/servers/{id}/proxy/` defeats each deliberate exception: `terminal/ws`
needs `shell` → `system` suffices; `/reveal` is denied to every scope → `system`
suffices; no scope may mint a token → `system` suffices. `…/exec` and
`…/attach` *are* caught by suffix rules, which marks the rest as an oversight
rather than a decision.

**Task S-6.** Re-run `ScopeAllows` against the remote path in
`handleServerProxy`; `rest` is already in hand. One call.

### 6.6 Five authorization systems that do not compose

| System | Axis | State |
|---|---|---|
| Roles | what you may do | source of truth, scattered across 96 sites |
| Project globs | which apps | applied to 17 routes; **ignored** by domains, cron run, catalog install, vault, images, audit |
| Token scopes | which API areas | the best layer — deny-by-default, capped at mint, cannot self-widen. Defeated by §6.5 |
| MCP tool scopes | same, per tool | the whole role model is `if method != "GET" && role == "viewer"` — no admin tier, no project scope |
| Forward-auth lists | which sites | knows nothing of role or project; empty list = any signed-in user |

A deployer scoped to `shop-*` is admitted by the gate to **every** protected site
on the box. Nine MCP tools call service packages directly instead of through
`viaRouter`, skipping `scopeContainer`, `scopeApp` and `allowsInstance`.

**Task S-7.** Make project scope a filter inside the role check rather than a
parallel wrapper. **S-8.** Route all 53 MCP tools through `viaRouter` so MCP has
no role logic of its own. **S-9.** Default an empty forward-auth list to the
creating admin; "anyone signed in" becomes an explicit opt-in.

Is "deployer" coherent? Mostly — five walls are well-placed and consistent (no
exec, no terminal, no workspaces, no SQL, no file writes, no `shell`). The
incoherence is concentrated in three places: `POST /cron/jobs/{id}/run` (root,
by running an admin's job), domains (§6.4) and the vault (§6.3). Fix those and
the role becomes defensible as *deploy and operate, do not configure*. Leave
them and it is admin with extra steps.

### 6.7 The assistant has every write tool and no confirmation gate

The model is handed the full MCP tool set — `create_job`, `file_operation`,
`create_stack`, `apply_security_fix`, `add_firewall_rule` — plus `islet_request`,
which reaches any `/api/v1/` route. A panel session acts with `scopes = "*"`.
The loop runs 12 steps by default over tool output that includes container logs,
deploy logs, repo contents and file reads — all attacker-influenceable.

The containment design is genuinely good: the assistant borrows the asker's
authority through the same gate an HTTP call passes, and has none of its own.
The gap is that an admin's authority *is* root, and nothing asks first.

**Task S-10.** Confirm-before-write by default, with a setting to turn it off.

### 6.8 Secrets: no rotation story at all

The cipher layer is correct and boring — AES-256-GCM, random per-message nonces,
0600 key. Management is not:

- **No rotation.** No `key_version` column across all 34 migrations, no
  re-encrypt routine, no CLI, no `ISLET_SECRET_KEY`. The `islet.enc.v1:` label
  anticipated versioning; nothing reads it.
- **Islet's own backup contains `secret.key`.** The plan mounts the whole data
  dir; the exclude list is `trash`, `apps/*/src`, `bin` and caches. The restic
  repo password is itself encrypted with that key. One snapshot is every vault
  value, every app env, every repo URL with its embedded PAT, all notification
  tokens, S3/SFTP credentials, the GitHub App private key, all AI keys, the
  fleet token, and every TOTP seed.
- **No AAD**, so an attacker with DB write access but no key can transplant
  ciphertexts between rows — swap two apps' env blobs, move a TOTP seed onto
  another user.
- **Recovery codes** are ~49.5 bits behind one *unsalted* SHA-256, and they
  bypass 2FA.
- **Plaintext exceptions** beside sealed columns: `apps.webhook_secret`,
  `runner_pools.webhook_secret`, and the SMTP relay password written unsealed to
  `stacks/<name>/.env`.

**Task S-11 (before the freeze).** Add the `key_version` column now — it is a
migration you cannot add later without a second migration. **S-12.** Exclude
`secret.key` from the `islet` backup source and put the repo password in the
recovery kit. **S-13.** Salt the recovery-code hash. `islet rekey` can follow
the freeze; the column cannot.

### 6.9 Defaults worth changing

| Default | Now | Change to |
|---|---|---|
| Panel exposure | `0.0.0.0:9443`, installer never touches the firewall | `127.0.0.1:9443` + a printed SSH-tunnel line, or a ufw rule for the installing IP. The safe code default is dead in practice |
| Setup token | never expires, endpoint unrate-limited | 60-minute TTL, reissued from the console; rate-limit `POST /setup` |
| Login rate limit | keyed on `r.RemoteAddr` | honour `X-Forwarded-For` from the trusted proxy. Through Islet's own Traefik — the documented setup — **every internet client shares one bucket and every audited login IP is the proxy's** |
| 2FA | no enforcement, report-only score check | `security.require_2fa` for admins |
| Session | 7-day absolute, no idle timeout | keep 7d absolute, add 24h idle |
| Deployed containers | no hardening flags anywhere | `--security-opt no-new-privileges` always; `--cap-drop ALL` opt-out |
| Self-signed cert | 10 years | 1 year, auto-renewed |
| CSP / HSTS | no `base-uri`, `form-action`, `object-src`; HSTS only when `r.TLS != nil` | add all three; honour `X-Forwarded-Proto` as `isSecure` already does |

Correct as-is and worth protecting: MCP off, assistant off, blocklist off,
`skip_permissions` off, no auto-update, no open signup, no default credentials,
TLS on. Also: argon2id with a dummy verify on unknown usernames, role re-read
live per request so a demotion is immediate, the two-cookie split, measured
Traefik header stripping, MCP refusing session cookies, MCP refusals audited,
`islet_request` dispatching through the real router.

### 6.10 What a security-conscious buyer will ask and Islet cannot answer

No rate limiting beyond login and MFA (`/mcp`, webhooks and setup are
unlimited). No account lockout. No session pinning. No append-only or off-box
audit trail — the log is a SQLite table the root daemon writes, so anyone who
compromises the panel can rewrite the record of it. No IPv6 parity in the
blocklist. No `govulncheck`, CodeQL, Dependabot or SBOM. No secrets-in-memory
handling — credentials pass as argv and are visible in `/proc/*/cmdline`.

**Task S-14.** Add `govulncheck` to CI. It is one job and it is the cheapest
item in this section.

### 6.11 `IsProtected` hardcodes the default data directory

`files.go:61` lists `/var/lib/islet` literally while the data dir is
configurable. Under any non-default `ISLET_DATA_DIR`, a **viewer** can read
`secret.key`, `islet.db` and every workspace `.mcp.json` — which chains directly
into §6.2. `Clean` also does not resolve symlinks, so the blocklist is textual.

**Task S-15.** Derive the prefix from the configured data dir; resolve symlinks
before checking.

---

## 7. Operational fitness

The daemon itself is excellent and the 1 GB claim holds for it: **20–28 MB RSS,
~0.1% of a core, 10 threads, 70 MB/day of writes, flat over 12 minutes**; 80
concurrent expensive requests moved it to 36 MB and it came back down. What is
no longer true is the claim about the *system* the daemon sits in.

### 7.1 Uptime monitoring is quadratically self-defeating

`Get()` unconditionally calls `uptime()`, which runs two aggregate scans;
`probe()` calls `Get()` and uses only `Enabled`, `Status`, `Failures`,
`DownSince` — **both percentages are computed and discarded on every probe of
every monitor.** `List()` repeats it per row, and `/api/v1/attention` calls
`List()` from **every page in the panel, every 60 seconds.**

The index is `(check_id, id DESC)`; `at` is not in it, so each aggregate reads
every retained row for that check. `purge()` is an unfiltered full scan.

| Monitors @60s | `check_results` | Probe load | + Uptime tab | + any tab |
|---|---|---|---|---|
| 10 | 37 MB | 0.8% | 3.5% | 0.9% |
| 20 | 74 MB | 2.2% | 8.8% | 2.2% |
| 50 | 182 MB | 14.7% | 46% | 11.5% |
| 100 | 366 MB | 31.4% | **120%** | 30% |

Percentages are of one core, sustained, on a 2-core box with a warm cache — a
floor, not a ceiling.

**Task O-1.** A `checkOnly` variant of `Get` for `probe` — one line, removes 31%
of a core at 100 monitors. **O-2.** `check_results(check_id, at)`
([§5.7](#57-five-missing-indexes-with-query-plans)), which makes both the
aggregate and the purge index-ranged. **O-3.** Cache the two percentages on the
`checks` row, refreshed by the probe that already read the data.

### 7.2 An unreachable backup target hangs a plan forever

The scheduler launches `RunPlan(context.Background(), …)` — not the daemon ctx —
and `RunPlan` derives `context.WithCancel(ctx)`, **never `WithTimeout`**. restic's
retry backend has no `MaxElapsedTime` and relies entirely on caller context.
There is none.

The cascade is worse than the hang: `s.active[id]` is cleared only by a `defer`
that never runs, and `tick` skips running plans, so **that plan is excluded from
every future tick for the life of the process.** The only symptom is a generic
"Backup stale" warning that cannot appear for ≥26 hours. `Verify` (2h) and
`RestoreTest` (30m) already have timeouts; the unattended path does not. Nothing
anywhere runs `restic unlock`, so a killed run leaves the repo locked with no
code path to clear it.

**Task O-4.** Derive from the daemon ctx with a generous `WithTimeout`, cap the
unbounded stderr builder, add `restic unlock` on the retry path. This is the
highest-severity operational finding: it is the one feature whose silent failure
is unrecoverable.

### 7.3 No deadline on Docker calls, no concurrency limit, a 5 s poll with no abort

`cmdrun.exec` adds no deadline of its own; the server sets `ReadTimeout: 0` /
`WriteTimeout: 0` with no `http.TimeoutHandler`. Only 2 of ~25 Docker call sites
add a deadline. Meanwhile `Containers.tsx` polls every 5 s through a bare
`fetch` with no `AbortController` and no in-flight guard.

Against a wedged dockerd — socket present, API not answering, the normal failure
under memory pressure — **one open tab spawns a new stuck `docker` child every
5 seconds.** Ten minutes is ~120 orphaned processes. On the stated 1 GB target
that is the OOM killer, not a slow page.

**Task O-5.** A default deadline inside `cmdrun.exec`, overridable. One change
bounds this, the runner reconciler and the security schedules at once. **O-6.**
An in-flight guard in `useList`.

### 7.4 Traefik's access log grows forever, unrotated, unmentioned

Traefik is started with `--accesslog.filepath=<mount>/access.log`. There is no
rotation anywhere — no logrotate fragment in `installer/`, no size cap, nothing
in `Housekeep`, nothing in Disk Doctor. Traefik has no built-in rotation; it
expects an external SIGUSR1 nobody sends. The Logs page does not even offer the
file — it offers nginx paths that do not exist on an Islet server.

At ~300 bytes/line, one site at 100k requests/day writes **~10 GB/year** into
the data dir, and Islet's own disk alert fires at 85% from a file Islet created
and never mentions.

**Task O-7.** Ship a logrotate fragment with the Traefik install, or drop
`--accesslog` and surface Traefik's stdout. Either way, a `DECISIONS.md` entry.

### 7.5 A restart marks work dead while the work keeps running

Four boot-time recovery statements exist and are the right ones. But the three
long-running subsystems derive work from `context.Background()`, and the systemd
unit sets `KillMode=process` (correctly, for tmux workspaces), so SIGTERM
reaches only the main process and every child is reparented to init.

- An `islet-restic-*` container survives holding the repo lock while its row
  says `failed` — so the *next* plan fails too.
- A cron script keeps running while its row says `killed`, and because the
  in-memory `active` map is empty at boot, a job marked `overlap: skip` starts a
  **second concurrent copy** on the next tick.
- `runner_jobs` has no recovery at all: a CI job whose completion webhook
  arrived during the restart stays `in_progress` forever.
- Recipes have **no table at all** and are bound to `r.Context()`: closing the
  tab aborts a multi-step server setup halfway, leaving a real database, app and
  domain behind with no record that a recipe ran.
- The login rate limiter is memory-only, so **every restart resets brute-force
  lockouts** — and failed logins are never logged, so fail2ban has nothing to
  read.

**Task O-8.** Reconcile children at boot rather than marking rows dead: ask
Docker and tmux what is actually running (the `CLAUDE.md` rule, applied to
recovery). **O-9.** Give recipes a table.

### 7.6 At 3 a.m. there is almost nothing to see

- **Five of the six highest-risk packages log nothing.** `internal/proxy` (0
  calls), `uptime` (0), `auth` (0), `store` (0), `docker` (0). `backup` and
  `deploy` each **accept a `*slog.Logger`, store it in a field, and never call
  it.** `internal/proxy` is the subsystem behind all three of the project's own
  documented worst bugs, and it emits no diagnostics whatsoever.
- **No pprof, no expvar, no `runtime.NumGoroutine`** anywhere, for a daemon with
  ~20 long-lived goroutines plus one per connection and one per monitor.
- `/api/v1/health` returns `ok` with Docker dead, SQLite read-only and Traefik
  gone — it carries no dependency status.
- No support-bundle or diagnostics export endpoint exists across all 290 routes.
- `/api/v1/commands` has **no pagination** — `limit` ≤ 500, no cursor, so you can
  never see past the most recent 500, and a single deploy can emit more.
  `/api/v1/audit` is keyset-paginated but has **no filter by actor, action or
  date**, so "who changed this last Tuesday" means paging 100 at a time through
  200k rows.
- The one loud path is `metrics/sampler.go:114`, which warns on every failed
  persist: a full disk yields **8,640 identical lines/day** with no dedup.

**Task O-10.** Dependency probes on `/health`, plus `goroutines`, `dbSizeBytes`,
`walSizeBytes` and queue depths. **O-11.** `before=` on `/commands`;
`actor`/`action`/`since` on `/audit`. **O-12.** Call the loggers `backup` and
`deploy` already hold.

### 7.7 Silent failures on the paths that most need to be loud

**67** `_, _ = …ExecContext` discards, with no `SQLITE_BUSY` / `SQLITE_FULL`
handling anywhere under `busy_timeout(5000)` and `MaxOpenConns(4)`. The worst:

- `notify.go:132` — if the `deliveries` INSERT fails, the event shows in the
  timeline and **is never delivered to any channel**.
- `notify.go:245` — if the `status='sent'` write fails, the row stays pending and
  the same alert **re-sends every 30 s forever**.
- `uptime.go:318,328` — the up/down transition itself.
- `cron.go:895` — a failed `overdue=1` write re-emits every minute.

Plus **134** discarded `st.Audit` calls: a systematically failing audit write
produces a silently incomplete audit log, the one failure mode an audit log must
not have. And `cmdrun`'s `limitedWriter` returns `len(p), nil` after the cap, so
truncation at 16 MB is silent — `restic ls --json` parses through it, and the
restore browser can show a plausible but incomplete listing.

Nothing anywhere checks free disk space: zero `Statfs`/`Bavail` in the tree. DB,
backups, builds and uploads share the data dir, so filling it turns all 67
swallowed writes into silent no-ops simultaneously — including the disk-full
alert.

**Task O-13.** Log, don't discard, on the ~10 sites where a failed write changes
user-visible behaviour. **O-14.** A free-space check before builds, backups and
uploads.

### 7.8 Smaller, cheap

- **SMTP has no timeout of any kind.** `sendMail(cfg, m)` takes no ctx; the dial
  is a bare `tls.Dial` with no deadline on any verb. The 20 s and 30 s `sendCtx`
  timeouts are dead code for email, and `deliverPending` is strictly serial — so
  **one silent SMTP host blocks Slack, Discord, webhook, ntfy and the digests
  behind it** while the daemon looks healthy. (**O-15**)
- **The Overview page is a per-process `/proc` walk every 10 s** — 276 ms +
  343 ms measured, ~6% of a core per open tab before any container exists, and
  it grows with host process count. `docker.cachedStats` is the right pattern
  and is already in the codebase. (**O-16**)
- **The stated audit retention is unreachable.** `AuditRetentionDays = 365` sits
  beside `maxAuditRows = 200_000`, and the comment measures the live rate at 700
  rows/day: 200,000 / 700 = **286 days**. The count cap always wins and nothing
  says so. (**O-17**)
- **Nothing is served compressed on the default path.** 3.4× on the entry
  bundle, 7.0× on `/metrics/latest`. Traefik compresses, so this only bites
  direct `:9443` — which is the shipped default and the state every new install
  is in. (**O-18**)
- **A failed migration is a 2-second crash loop with no explanation.**
  `Restart=always`, `RestartSec=2`, no `StartLimitBurst` override, and
  `migrate.go` logs nothing — not even which migration applied. The "database
  written by a newer Islet" error is reachable only via journalctl. And **no
  copy of `islet.db` is taken before migrating**, which for a product whose
  pitch is that the state is one small file costs nothing. (**O-19**)

### 7.9 What Islet should publish as its comfortable ceiling

| Dimension | Comfortable | Degrades | Do not |
|---|---|---|---|
| Uptime monitors | 10 @ 60 s | 20–30 @ 60 s | 50+, or any interval under 60 s. 100 cannot be served |
| Containers | 30–40 | 60–80 | 100+ without O-5 |
| Domains | 100+ | — | no ceiling found; this part is right |
| Deployed apps | 10–20 | — | 5+ simultaneous builds on 1 GB |
| Cron jobs | 50+ | — | — |
| DB size, year one | 50–60 MB; 120–130 MB with 20 monitors | — | 366 MB from `check_results` alone at 100 monitors. No `VACUUM` anywhere, so the file never shrinks |
| Open panel tabs | 1–2 | 3+ multiplies every poll | — |

**The first thing that breaks as a server fills up is not memory — it is the
panel's own polling loops.** The daemon's resident set is never the constraint.

**Task O-20.** Publish this table. The honest claim today is "1 vCPU, 1 GB, for a
server, not for a fleet of monitors" — and after O-1, O-2, O-5 and O-7 it is
worth re-measuring and restating.

---

## 8. UX

### 8.1 What is worth protecting through the freeze

The refusal copy is the best thing in the product. `Domains.tsx:87` does not say
"invalid configuration"; it says *"`shop.example.com` is not under
`example.com`, which is what the session cookie is scoped to, so a browser will
never send it there and the login will loop."* It explains the mechanism,
predicts the symptom and names the fix. `DnsCell` has three outcomes instead of
two because "behind Cloudflare" is not "not yet". Workspace dialogs describe what
happens to the *conversation*, not to the process. Every empty state says what
the thing is and what to do next.

Two flows are finished end to end: the catalog installer (warns *before* the
button that a second copy is a separate stack; stays open on success with an
address and an "Open it" link; splits removal into two questions because only
one is reversible) and the remote-server join (step chips that mark the *failed*
step, an explicit success Alert, a button that flips to "Close", and *"You can
close this. The install keeps going."*). **Those two are the bar; nothing else
meets it.**

### 8.2 Destructive-action guarding is inverted

Removing one catalog app makes you **type its name**. Meanwhile:

```jsx
<Button variant="danger" onClick={() => api.proxyRemove().then(load)}>Remove the proxy</Button>
```

One click, no dialog. **This takes every site on the server offline** and is the
most destructive unguarded control in the panel. Also unguarded: "Fix everything
safe" (root-level host changes, and its `try/finally` has no `catch`, so a
rejection surfaces nowhere), 2FA disable, API token revoke, production rollback,
dump delete, and *removing* the panel's IP restriction (the guard is
`if (c && …)`, so tightening asks and loosening does not). `Backups.tsx:65` is
the one surviving raw browser `confirm()`, in a product that built `useDialog`
precisely to avoid it.

**Task U-1.** One written rule: anything that can take a running site offline or
destroy data gets `ask.confirm` with `tone: "danger"`; anything irreversible
additionally gets `typeToConfirm`. Apply it to the seven sites above.

### 8.3 The dashboard and the Security page disagree

`attention.go:47` counts `c.Status == "fail"`; `Security.tsx:59` counts
`c.status !== "pass"`. Measured on a fresh instance: Overview said **66 · 3 to
fix**, `/security` said **66 · 7 items to fix**, one click apart. The score also
caches for five minutes with no invalidation after a fix, so the number stays
wrong after you act on it.

**Task U-2.** One definition, computed server-side, rendered by the panel;
invalidate on `securityFix`/`securityFixAll`.

### 8.4 Long operations end in a raw log, or in nothing

| Operation | How it ends |
|---|---|
| Catalog app **update** | `postStream(…, () => {})` — **output discarded**. "Updating…", then nothing |
| **Deploy** | no success branch; the build log stays in the black `<pre>` |
| **Backup run** | no message; restic's last line is the only outcome |
| **Restore test** | dialog titled *"Restore test finished"* — not passed or failed — with raw restic output as the body |
| **Lynis audit** | hundreds of raw lines + a bare `Hardening index 62/100` |
| **Security fix** | card titled with the internal fix id — `"Applied ssh-harden"` |
| **Fix everything safe** | `res.map(x => \`${x.fix}: ${x.status}\`)` — machine-readable ids |
| Image pull, file ops, vault store, check save, channel save, agent save | **silence** |
| **Remote server join** | ✅ the one that gets it right |

Catalog install is also the only multi-minute operation with **no server-side
record at all** — deploys have releases, backups have runs, the Assistant got run
ids in v0.15.0. Navigate away mid-install and there is nothing to come back to.

**Task U-3.** A completion line with a verdict and a next step for every
`postStream` caller, as an `<Alert>` *outside* the log pane — the pattern the
server-join flow already uses. **U-4.** Fix the discarded stream. **U-5.** Put
catalog installs behind the existing run-id mechanism.

### 8.5 One setting, three names, two of them wrong

The session cookie domain lives under **Settings → Team**. `Domains.tsx:98` and
`Returning.tsx:76` both say *"Settings, Sessions"* — and a **Sessions** card
really exists, under Account, without that field. `Databases.tsx:461` gives a
third name. So two of three pointers send the reader to a real place that is the
wrong place, on the exact flow where a wrong turn produces an infinite redirect
loop. All three are prose; `Settings.tsx:41` already reads `?tab=`, so a link
costs nothing.

**Task U-6.** Fix the three strings and make them `<Link to="/settings?tab=team">`.
Same for the `FixNote` strings in `internal/security/security.go`.

### 8.6 State does not carry between pages

- **`Domains.tsx` never calls `useSearchParams`.** Nothing can hand it a host or
  a container, and there is no "give this app a domain" action on Apps, on
  Containers, or on the install success card — which tells you to go to the
  Domains page and then makes you retype the container name from memory.
- **Three dead drill-downs:** `Overview.tsx:163`, `Deploys.tsx:120` and
  `Deploys.tsx:127` all link to `/containers?c=…`; `Containers.tsx:316` reads
  only `?stack=`.
- **Databases → app is clipboard-only**, with a one-shot secret — while
  `Deploys.tsx:93` already does the whole thing automatically. The good flow
  exists and is invisible from the page where people start.
- **Vault's Copy button copies the secret value.** The thing that needs pasting
  elsewhere is `@vault:NAME`.

**Task U-7.** `/domains` accepts `?host=&target=&port=&tls=` and opens the add
form pre-filled; add "Give it a domain" to app and container rows. **U-8.** Read
`?c=` in Containers, or route to `/containers/${id}`. **U-9.** Vault's row action
copies `@vault:NAME`.

### 8.7 Mobile: two tasks work, several pretend to, one is dangerous

No page scrolls horizontally at 390px — the layout hygiene is real. Checking
what broke, restarting a container and approving an agent all work well, and
`TermView` has genuine touch handling (drag-to-scroll with a measured line
ratio, a long-press menu because a phone has no right-click).

But `/security` at phone width is **6,081px tall with 26 actions, and the first
thumb-reachable control is Panic button at y=135** — the next action is 1,790px
further down. On the device most likely to be used in a hurry, the control that
blocks every inbound connection and revokes every token is the easiest thing on
the page to hit.

And **the voice is the first thing dropped**: four pages hide their subtitle
below 640px — Assistant, Terminal, Vault, Workspaces — precisely the four
hardest features to understand. On a phone `/vault` is the word "Vault" and a
list of uppercase names, with the only explanation of what `@vault:` means
removed.

**Task U-10.** Move Panic out of the header row at small widths, into its own
card at the foot behind a disclosure. **U-11.** Stop hiding page subtitles at
`sm`. **U-12.** Be honest about `/sql` and `/files` on phones — show the schema
or file list and a short banner. Deciding a task belongs on a desktop is a
legitimate product decision; pretending otherwise is not.

### 8.8 The sidebar is the roadmap, and says so

`nav.ts:1` — *"The sidebar is the roadmap. Each entry names the phase that
delivers it."* Nineteen items ordered by the phase that built them. A stranger's
first three items after Overview are a file browser, a shell and a tmux manager,
ranked above the two things the product exists to do. Logs sits under *Server*
and Uptime under *Operate*, though "is it up, and why not" is one question.
Terminal is a strict subset of Workspaces. Nothing reflects frequency.

Proposed: three groups, thirteen items — **Run** (Apps, Domains, Databases,
Containers), **Operate** (Logs & Uptime as one page, Backups, Cron, Security),
**Work** (Assistant, Workspaces with Terminal folded in, Files, Runners), with
Notifications, Servers and Settings at the foot and Vault moved into Settings →
Secrets.

**Task U-13.** Worth doing before a freeze precisely because navigation is the
hardest thing to change afterwards — but it is the one item here to hold if the
freeze is close.

### 8.9 The command palette can navigate and nothing else

It is built from `NAV` plus theme/server/sign-out. In a product with nineteen
pages and dozens of domains, containers, jobs and databases, ⌘K cannot find a
domain by name, open a container, run a job or reveal a secret.

**Task U-14.** Feed it objects and half a dozen verbs. This is also the cheapest
mitigation for §8.8 — a good palette makes nav depth matter much less.

### 8.10 Success and failure render identically on eight pages

`Uptime.tsx:69` puts `"OK in 42 ms"` and `"Failed: x509: certificate has
expired"` in the same 12px grey. A failed delete is typographically identical to
`"Saved."` Three different `err()` helpers exist with different behaviour, and
three have no `Error` branch — so a dropped connection renders as
**`TypeError: Failed to fetch`**.

**Task U-15.** One `<Note tone>` component; one shared `err()` that maps network
failures to *"Could not reach the daemon. It may be restarting."*

---

## 9. Corrections

Claims made by the reviews that did **not** survive verification, recorded so
they are not repeated:

- **"The panic button fires on a single click."** False. `Security.tsx:43` uses
  `ask.confirm` with `typeToConfirm: "PANIC"`. (The mobile placement finding in
  §8.7 is separate and does hold.)
- **"Notification channel configs are returned decrypted to any user."**
  Overstated. `handleChannels` passes `admin` into `notify.Channels(ctx,
  withConfig)`, so decryption is admin-gated. What remains true, and is still
  worth fixing, is that an admin GET returns every Telegram token, Slack webhook
  and SMTP password to the browser with no reveal step and no audit row — the
  opposite of how the vault next door is handled.

---

## 10. The work list

Ordered by (value) ÷ (risk of touching it). Everything in the first group is
cheaper before a freeze than after, because it changes a contract.

**Done so far**, each with a test that fails without the fix:

| Item | What shipped |
|---|---|
| D-1 | the SSE end event is JSON; a coloured build failure is readable again |
| S-1 | one `(method, pattern) → role` table, enforced in `requireAuth`, with a mirror test — closes the vault and `notify/emit` gaps (S-3) |
| S-2 | the workspace token loses `cron`; writing a stack needs `system`; the docs say what is left |
| S-4/S-5 | deleting a protected domain is admin-only; a url target cannot be the panel or link-local |
| S-6 | the fleet proxy re-checks scopes against the path it forwards to |
| S-14 | `govulncheck` in CI — which immediately found a reachable goldmark XSS, now fixed |
| C-2 | editing a domain no longer deletes its basic-auth users |
| D-3 | four `null`-slice crashes, and a test over every response type |
| D-6 | `UNIQUE (server_id, name)` on checks, channels and jobs, with existing duplicates renamed rather than the migration failing |
| D-10 | five indexes; the plans are asserted in a test |
| O-1/O-2 | the uptime probe stops recomputing two aggregates it discards |
| O-4 | the backup run has a deadline, a bounded stderr and an unlock |
| O-5 | `cmdrun` bounds a command whose caller did not |
| O-7 | Traefik's access log is rotated |
| U-1 | seven destructive controls ask first; the last `window.confirm` is gone |
| U-2 | one definition of "failing", computed once, cache dropped after a fix |
| U-6/U-8 | three wrong settings pointers; three dead drill-down links |

| D-2 | one `failed()`: conflicts are 409, command failures are 502 with the detail logged, 74 `bad_json` sites stop naming Go struct fields |
| D-11 | the `v1` policy is written down; unknown request fields are ignored so a newer client can talk to an older daemon |
| S-11 | a sealed value records which key sealed it — the prerequisite for rotation, with no migration and no loss of old values |
| P-1…P-5 | the README leads with the security surface and the gateway, names the five features it never mentioned, says what Islet is not for, and states which features are thin; `VISION.md` is marked historical |

**Deliberately not done**, with the reason:

- **D-9's five table rebuilds** — latent, and five rebuilds before a freeze is a
  poor trade; `CLAUDE.md`'s claim was corrected instead.
- **S-12, excluding `secret.key` from the backup** — removing it makes a restored
  Islet unable to decrypt what it just restored, so it needs a recovery-kit
  design first, not a one-line exclusion.
- **Dropping `GET /ping/{token}`** — it mutates, which a GET should not, but a
  heartbeat URL is pasted into a crontab as `curl <url>` and every service in
  this category accepts one. Removing it would break every heartbeat already
  configured. Documented as the deliberate exception instead.
- **The `databases` → `instances` rename and enumerating the container action
  wildcard** — both real naming faults, both pure churn through the panel, the
  MCP tool table and the tests for no behaviour change.

### Before the freeze — breaking or contract-changing

| # | Task | Why now |
|---|---|---|
| D-1 | SSE `end` becomes a JSON object with `ok` | a verified crash on the failure path of the most-used long operation; 4 lines now, a protocol break later |
| S-1 | declarative role table + mirror test | replaces 96 hand-written decisions; every authz gap below came from a missing one |
| S-2 | drop `cron`/`containers` from the workspace token, add a TTL, fix the doc | the doc claims a boundary the code does not hold |
| S-3 | `adminOnly` on vault write and delete | 2 lines; credential substitution today |
| S-4/5 | admin-only delete of a protected domain; reject loopback/private URL targets | delete+recreate removes a gate; a URL target publishes the panel |
| S-6 | re-check scopes against the remote path in the fleet proxy | 1 call; restores four deliberate carve-outs |
| S-11 | add the `key_version` column | a migration you cannot add later without a second one |
| D-2 | one `serviceErr` | fixes status codes, the leak, and the voice in one place |
| D-5 | the seven naming fixes | renames are never cheaper |
| D-6/9 | `UNIQUE (server_id, name)` on checks/channels/jobs; server-scope the five global indexes; four missing FKs | one migration; makes `CLAUDE.md`'s claim true |
| D-11 | write the `v1` policy; relax `DisallowUnknownFields`; `json:"-"` derived fields | a frozen `v1` with no stated meaning is the worst option |
| D-3 | `omitempty` reflection test + the two assignment-order bugs | ships four crash fixes |
| C-2 | basic-auth keep-if-empty | silent data loss on a routine save |

### Before the freeze — non-breaking, high value

| # | Task |
|---|---|
| O-4 | backup timeout + stderr cap + `restic unlock` — the only feature whose silent failure is unrecoverable |
| O-5 | a default deadline in `cmdrun.exec` — bounds Docker, the runner reconciler and the security schedules at once |
| O-1/2 | `checkOnly` probe + `check_results(check_id, at)` — removes up to 31% of a core |
| O-7 | rotate or drop Traefik's access log |
| U-1 | guard the seven destructive controls, starting with "Remove the proxy" |
| U-2 | one definition of "failing" |
| U-6 | the three wrong settings pointers, as links |
| U-3/4 | a verdict line on every long operation; render the discarded stream |
| U-8 | the three dead `?c=` links |
| S-12/13 | exclude `secret.key` from the islet backup; salt the recovery-code hash |
| S-14 | `govulncheck` in CI |
| P-1…P-5 | the README, the feature table, `VISION.md`, the "not for" paragraph, feature maturity labels |

### After the freeze

D-4 (`pkg/api` named types), D-7 (`If-Unmodified-Since`), D-8 and the settings→table
migrations, D-10 (the five-index migration — do it soon, it is free), A-1/A-2
(the empty packages and `notify.Subscribe`), O-8/O-9 (boot reconciliation,
recipes table), O-10/O-11/O-12 (health, pagination, the unused loggers),
O-13/O-14 (loud failures, free-space checks), O-15…O-19, S-7…S-10, S-15, U-7,
U-9…U-15, C-1 (the OpenAPI spec), C-3 (`@vault:` scope).

---

*Reviewed at v0.22.1 (`74406ca`). Every finding above was checked against the
source before it was written down; §9 records the ones that did not survive.*
