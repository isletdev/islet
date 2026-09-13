# Decisions log

Short record of decisions that shape the code. Newest at the bottom. Each entry: what was decided, why, and what it rules out.

## 2026-09-10 — Product name: Islet
Chosen after four naming rounds. Registry lookups on that date showed `islet.dev`, `islet.sh` and `islet.run` unregistered. GitHub org will be `isletdev`. Binaries: `isletd` (daemon), `islet` (CLI).

## 2026-09-10 — Open source first, paid later
Ship the complete single-server panel under AGPL-3.0 through v1.0 and build a user base before any paid work. Pro design is captured in `VISION.md` section 4 and `REPOS_AND_MONETIZATION.md` but nothing paid is scheduled.

## 2026-09-10 — Daemon language: Go
Single static binary, low memory on cheap VPSes, native Docker SDK, easy `curl | sh` install, arm64 for free. Rules out a Node or Bun runtime on the server.

## 2026-09-10 — Frontend: React + Vite + TypeScript
Static single-page app embedded in the Go binary via `embed.FS`. Tailwind and shadcn/ui for components. Chosen over Next.js because the panel needs no server-side rendering and must add zero runtime cost on the box. Rules out any Node process on the server for the panel.

## 2026-09-10 — Resource budget
The panel must stay usable on a 1 vCPU, 2 GB VPS with 30 containers. Targets: daemon under 60 MB RSS idle, UI bundle under 1 MB gzipped, no background CPU above 1% when idle. These numbers are goals for phase 0 and gates for v1.0.

## 2026-09-10 — Legal owner: Torsten Labs DOO
Torsten Labs DOO, North Macedonia (https://torstenlabs.com) owns the copyright, trademark and domains. The CLA names Torsten Labs DOO as the licensee. Billing provider must accept sellers in North Macedonia; verify Paddle or Lemon Squeezy eligibility before any paid work.

## 2026-09-10 — Scale-out not scheduled
Design captured in `VISION.md` section 3.18. Not on the roadmap.

## 2026-09-10 — Server id on machine-bound records from the first migration
Every table that describes something running on a machine (containers, app copies, processes, metrics, cron runs, logs, backups runs) carries a `server_id` column from the first migration, always the local server's id for now. Cheap today, avoids rewriting every table and query if scale-out (`VISION.md` section 3.18) is ever built. This is a schema convention only; no multi-server behaviour is scheduled.

## 2026-09-10 — Brand: Islet visual identity v2 (v1 rejected)
v1 (split-disc mark, lowercase wordmark, teal and sand palette, "Your own little island") was rejected as soft and generic. v2: abstract geometric mark, a square with its corner piece set apart; wordmark "Islet" in Geist 600; black and white palette with one blue accent for links and focus only; Geist and Geist Mono, both SIL OFL, self-hosted; tagline "Own your infrastructure." References: Vercel, Linear, Raycast for finish, Tailscale and Cloudflare for trust. Guidelines in `brand/README.md`, tokens in `brand/tokens/`.

## 2026-09-10 — Website: static, separate repo, Astro
The marketing site and legal pages are static content, so no Next.js: static HTML is the best SEO and adds no runtime. Plan: Astro in a separate public repo (`isletdev/website`), deployed to Cloudflare Pages, legal pages as Markdown routes, fonts self-hosted. A single-file sample in the v2 brand lives outside the core repo at `../islet-website/index.html`; its legal texts are drafts and must be reviewed by a lawyer and completed with the company's registration details before publishing.

## 2026-09-10 — Panel TLS: self-signed on first boot, ACME with the proxy
The daemon serves HTTPS from the first request with a certificate it issues itself (ECDSA P-256, ten years, SANs for hostname, local IPs and the public IP's sslip.io name). The installer prints the SHA-256 fingerprint. Publicly trusted certificates for the panel come with Traefik in v0.3, because ACME needs ports 80 or 443 and those belong to the proxy; running a second ACME client in the daemon would fight it. Plain HTTP exists only behind `-tls off` for development.

## 2026-09-10 — Release signing: Ed25519 key held by the maintainer
Releases are built by goreleaser on tag push. `checksums.txt` is signed with an Ed25519 key (`tools/sign`); the public key is embedded in `internal/update`, and the daemon refuses any update whose checksums do not verify, then refuses any archive whose SHA-256 does not match. The private key lives only on the maintainer's machine at `~/.islet/release-signing.key` and in the GitHub Actions secret `ISLET_SIGNING_KEY`. Losing it means shipping a key rotation release signed by nothing, so it is backed up offline. Sigstore keyless signing was considered and deferred: verifying it in the daemon pulls in a large dependency tree for no gain over a pinned key at this stage.

## 2026-09-10 — Notifications: one bus, delivery outbox, criticals always pass
Every feature emits typed events (category, severity, title, message, deep link) on one bus. Channels filter by category, minimum severity and quiet hours; criticals ignore quiet hours. Deliveries are rows in an outbox retried with backoff, so a channel outage never loses an alert. Warnings with the same title are suppressed for ten minutes and recoveries are info events with the same title, which closes the loop without a separate "resolved" concept.

## 2026-09-10 — Cron: robfig/cron for parsing and scheduling, heartbeats for external jobs
Jobs are scheduled in-process (no system crontab), scripts are files under the data directory with the executable bit, every run is recorded with its output. Jobs that run elsewhere register as heartbeats with a ping URL, which gives a real dead man's switch instead of only "did our own scheduler fire".

## 2026-09-10 — Databases: everything through docker exec
Database work (listing, users, dumps, restores, extensions) runs inside the instance's own container, so the host needs no client tools and versions always match the server. Dumps land under the data directory; scheduled dumps are ordinary cron jobs so they inherit history and alerts. Publishing a port edits the stack's Compose file rather than tweaking the container, so the change survives recreation.

## 2026-09-10 — Deploys: generated Dockerfiles instead of Railpack for now
Node, Python, Go and static sites build from a Dockerfile Islet writes into `.islet/` in the checkout and shows in the deploy log. It keeps the build transparent and needs no extra binary. Railpack or Nixpacks stay planned for languages without a generated Dockerfile. Zero downtime comes from starting the new container, health-checking it over the proxy network, re-pointing the Traefik route and draining the old one; rollbacks re-point to a kept image.

## 2026-09-10 — Proxy: per-host HTTP to HTTPS redirect, not a global one
Traefik's entrypoint-level redirect made HTTP-only routes unreachable. The redirect is now a router per host on the `web` entrypoint, and the proxy container carries an `islet.proxy.args` label so the daemon recreates it when the arguments change.

## 2026-09-10 — HTTP server: no read timeout on the panel listener
A read timeout on the listener cancels handler contexts for long streams (deploys, logs, backups) after it elapses. Header reads stay bounded at ten seconds; bodies are drained before streaming so closing a connection never resets the client.

## 2026-09-10 — API tokens: scoped bearer tokens, tokens cannot mint tokens
Tokens act with their owner's role narrowed by scopes matched against method and path. They never create other tokens, so a leaked CI token cannot escalate. The MCP endpoint accepts only tokens, never browser sessions, and is off by default.

## 2026-09-10 — Runners: personal access token until the GitHub App exists
Ephemeral GitHub runners need a registration token per job, which needs either a GitHub App or a PAT. The PAT is stored encrypted and only used to mint short-lived registration tokens; the GitHub App replaces it once registered. GitLab and Gitea runners are long-lived containers because their tokens are per runner.

## 2026-09-10 — Security fixes run as root through the daemon
One-click fixes (ufw, fail2ban, unattended upgrades, sshd drop-in) are shell commands recorded in the command log, not a separate agent. SSH changes are validated with `sshd -t` and roll back after five minutes unless confirmed, so a mistake cannot lock the admin out permanently.

## 2026-09-10 — Backups: restic in a container, one repository per destination
restic runs from the pinned `restic/restic` image with sources mounted read-only, so the host installs nothing and volumes are backed up by mount rather than by path. Each destination has its own repository and generated key, kept encrypted and exported in the recovery kit. Retention always keeps the last three snapshots on top of the daily/weekly/monthly/yearly policy.

## 2026-09-11 — MCP: minimal JSON-RPC over HTTP, tools mirror the API scopes
The MCP server is a stateless Streamable HTTP endpoint implemented in-house (a few hundred lines) rather than a dependency, exposing sixteen tools that call the same service methods as the HTTP API and are gated by the same scope table.

## 2026-09-12 — "Protect with Islet login" through Traefik forwardAuth and a shared cookie domain
Protected routes ask the daemon at `/_islet/auth`; a valid panel session passes with `X-Islet-User`, anything else is redirected to the panel login with a return address. The session cookie is scoped to an admin-chosen parent domain (say `example.com`) so one sign-in covers the panel and every protected app under it; with no cookie domain set, cookies stay host-only as before. No OAuth provider is needed and nothing is exposed that the panel does not already serve.

## 2026-09-12 — Weekly report is a notification event, not a separate mailer
The Monday summary is emitted as an event in the `report` category, so it reaches whatever channels accept that category (email, Telegram, webhook) and stays in the timeline. Existing digests and quiet hours apply; there is no second delivery path to maintain.

## 2026-09-12 — More generated Dockerfiles rather than a buildpack
PHP (serversideup/php with nginx and PHP-FPM), Ruby and Rails, Rust, Java (Maven or Gradle) and .NET join Node, Python and Go as generated multi-stage Dockerfiles. Each stays a few lines, is shown in the deploy log and can be replaced by the project's own Dockerfile at any time.

## 2026-09-12 — Processes are sibling containers from the same image
A worker or scheduler is one more `docker run` of the release image with `sh -c <command>`, the same env file, network, limits and volumes as the web process, labelled `islet.process=<name>`. They start after the web container is healthy and routed, and the previous release's process containers are removed on success (or the new ones on failure). No supervisor inside the container, so per-process logs and restarts come from Docker as usual.

## 2026-09-12 — Promote re-tags rather than sharing an image
Promoting staging to production tags the live image as `islet/<target>:r<N>` and deploys that; the target app then owns its own tag history, so pruning or deleting either app never breaks the other's rollbacks.

## 2026-09-12 — "After CI passes" reuses the existing hooks
An app set to deploy after CI ignores push payloads. With the GitHub App a successful `workflow_run` on the app's branch starts the deploy; on any other CI a final job step calls the app's deploy hook with the secret token and no body. No new credentials or endpoints.

## 2026-09-12 — Events carry a subject; channels filter on it
Every event about a specific thing (an app, container, uptime check, cron job or backup plan) names it in `subject`. A channel's "Only for" list (globs allowed) filters only events that carry a subject, so a channel dedicated to one app still receives server-wide warnings such as a full disk. This replaces a per-app channel matrix with one field.

## 2026-09-12 — Uploads are a source like git, not a separate deploy path
An app whose source is "upload" keeps its files under the app's workspace; each upload replaces them and the normal pipeline (detection, generated Dockerfile, health check, route, rollback) runs unchanged. Zip entries and file paths that would leave the upload folder are dropped rather than rejected, and a lone top-level folder is flattened so zips made from a project folder just work.

## 2026-09-12 — Sidebar embedding is an iframe, protected by the same cookie
"Add to sidebar" renders the app's URL in an iframe. With the session cookie domain set and the app's domain marked "Protect with Islet login", the panel and the app are same-site, so the session cookie flows into the frame and no second login is needed. Apps that send X-Frame-Options stay reachable through the new-tab link; Islet does not strip headers.

## 2026-09-12 — Location on login alerts is opt-in
Looking up the city and country of a new sign-in address means sending that address to a third party (ipapi.co). It is off by default and, when on, applies only to the new-address alert, never to routine logins.

## 2026-09-12 — PgBouncer is a service added to the instance's own stack
The pooler rides in the Postgres stack's Compose file as a `pgbouncer` service (edoburu image, transaction mode, scram auth), so it shares the stack's network and lifecycle and disappears cleanly when removed. Apps switch by changing the host to `<name>-pgbouncer-1`; credentials stay the same.

## 2026-09-12 — Sample apps live in the main repo
`examples/` holds one tiny app per framework, deployable with the repository URL plus a root directory. Keeping them in the same repo means detection changes are tested against them in CI and there is no second repository to keep in sync.

## 2026-09-12 — Backup hooks are host shell commands; pausing stops containers by volume
Pre and post hooks run `sh -c` on the host (recorded in the command log) rather than inside a container, because the common cases are `docker exec … artisan down` and touching a maintenance flag. "Pause during snapshot" finds running containers that mount the plan's volumes and stops them for the snapshot only; the post-hook and restart run before the run is stored, so a failed snapshot never leaves an app down.

## 2026-09-12 — Restore into a new instance instead of over the live one
A dump from a snapshot is restored to disk, a fresh engine is installed from the catalog and the dump is loaded there. The user then switches apps to the new URL when satisfied. Overwriting a live database from the backup UI stays impossible by design.

## 2026-09-12 — Peer backups use restic's rest-server, append-only, private repos
"Host backups for another Islet server" runs `restic/rest-server` as a stack with a bcrypt htpasswd, `--append-only` (a compromised peer cannot delete its history) and `--private-repos` (each user only sees its own path). The credentials are shown once in the panel and the endpoint is an ordinary routed domain.

## 2026-09-12 — Container stats are cached, stack status comes from docker ps
`docker stats --no-stream` waits for two samples and took two seconds per list request; `docker compose ls` took three. The list now serves the last stats reading (refreshed in the background, at most every ten seconds) and derives stack status from one `docker ps` over Compose labels. Measured with 38 containers: 2.0 s to 175 ms and 3.2 s to 100 ms.

## 2026-09-12 — DCO instead of a CLA, two signatures on releases
Contributors keep their copyright and sign off commits (Developer Certificate of Origin), checked by a small workflow rather than a bot that needs installing. Releases carry Islet's own Ed25519 signature (what the updater checks) plus a keyless cosign signature and SLSA provenance, so anyone can verify a download with standard tools even if they distrust the embedded key.

## 2026-09-12 — Recipes are declarative YAML run by a small engine with undo
A recipe is inputs plus a list of typed steps (database, app, deploy, install, uptime, cron, backup) with `{{templates}}` for inputs and earlier outputs. The engine streams progress and keeps an undo list; a failed step removes everything the run created, in reverse order, so a half-finished setup never lingers. Steps call the same service methods as the API, so a wizard can do nothing the user could not do by hand.

## 2026-09-12 — Outbound mail is a Postfix relay stack, not a mail server
The relay (bokysan/docker-postfix with auto-generated DKIM) only sends, only for the configured domain, only from this server: containers reach it on the proxy network without credentials and the panel on 127.0.0.1:2525. Islet reads the generated DKIM key and shows SPF, DKIM and DMARC records with live checks, because deliverability is a DNS problem more than a software one. Inbound mail stays out of scope.

## 2026-09-12 — Host audits are plain find and sha256, not an agent
Setuid and world-writable listings come from `find`, /etc integrity from a sha256 baseline stored under the data directory, rootkit checks from rkhunter installed on demand. Results are files, not database rows, so a compromised database cannot rewrite them without also touching the data directory (which the backup plan for Islet state covers).

## 2026-09-12 — Database allowlists ride on ufw
Publishing a database port with an allowlist adds `ufw allow from <cidr> to any port <p>` rules and removes the open rule; the DOCKER-USER chain installed by the firewall fix makes Docker honour them. Without an active firewall the list is stored and shown but not enforced, and the panel says so.

## 2026-09-12 — Provider integration is one call: snapshot before a risky change
The Hetzner token (encrypted at rest) is used for exactly one thing: a snapshot right before SSH changes, requested but not awaited. No server creation, no DNS, no billing calls; those belong to the provider's own tools. Other providers can follow the same two-endpoint shape (find this server, create image).

## 2026-09-12 — Append-only credentials are guided, not automated
Creating restricted keys or turning on object lock needs master credentials for the storage account, which is exactly what should never sit on the server. The destination form explains the per-provider recipe instead, and the built-in peer host runs rest-server append-only.

## 2026-09-12 — i18n is a JSON dictionary per language with English fallback
No i18n library: `t("key")` reads `src/locales/<lang>.json`, unknown keys fall back to English and then to the key, and the language is a per-browser choice (localStorage, defaulting to the browser language). Strings move behind keys page by page as they are touched; the picker shows how complete each language is so nobody is surprised by mixed text.

## 2026-09-12 — Mobile is the same layout, stacked
Below the medium breakpoint the sidebar becomes a drawer, two-pane screens (files) stack, wide tables scroll inside their card and log panes take the viewport height. No separate mobile app shell; every page keeps one code path.

## 2026-09-12 — End-to-end tests run on real throwaway servers, not in containers
The installer, firewall, sshd, systemd and Docker-in-Docker behaviour cannot be trusted from a container. The e2e workflow creates real Hetzner servers per distribution and architecture, installs the CI-built binary through the unchanged installer (`ISLET_BINARY`), runs a smoke script through the HTTP API and deletes the servers in a trap. It is opt-in through one secret so forks pay nothing.

## 2026-09-12 — Project scopes are app-name globs, derived for everything else
A project is not a new object: a deployer or viewer gets a list of app-name globs, and the scope for containers (`islet-<app>-…`, `<app>-<service>-1`), database instances (`<app>-<engine>`) and domains (routed to those containers) is derived from the app names. That covers what a team member of one app needs without a tagging scheme across every resource, and it composes with the naming the add-service and recipe flows already use. Scoped accounts lose Files and Terminal entirely, since both would leak the rest of the host.

## 2026-09-12 — The catalog is embedded and overlaid, never only remote
A fresh install has every app and recipe inside the binary, so nothing depends on GitHub being reachable. A configured source (the public catalog repository by default) is fetched as a tarball, validated (every app must parse) and swapped in atomically; fetched entries win over embedded ones by slug. The daemon never executes anything from the tarball, only reads YAML.

## 2026-09-12 — The docs site is generated from the repository with a 200-line tool
`tools/docsite` renders the Markdown in the repo with goldmark and one template. No static-site framework, no theme dependencies, no separate docs source of truth: what is in `docs/` is the site.

## 2026-09-12 — The nginx importer resolves Nginx Proxy Manager's variables
NPM writes `set $server "app";` above `proxy_pass $forward_scheme://$server:$port;`, so a literal read of the config yields nothing usable. The parser now resolves `set` variables inside each server block, and the importer turns an upstream that names a running container into a container target rather than a URL, which makes Islet attach that container to the proxy network itself instead of depending on the two proxies sharing one. Hosts whose variables cannot be resolved are listed with the reason instead of being imported wrong.

## 2026-09-12 — Traefik runs without the Docker provider or the daemon socket
Every route Islet serves comes from the file provider it writes. The Docker provider added nothing, required mounting `/var/run/docker.sock` into the proxy, and its client negotiates Docker API 1.24, which Docker 29 refuses outright: the log filled with "client version 1.24 is too old" every second. Dropping the provider removes the noise and takes the daemon socket away from an internet-facing container. The `islet.proxy.args` label carries a version so existing proxies are recreated on the next reconcile.

## 2026-09-12 — Attaching a container to the proxy network cannot depend on install order
A domain can be created before the proxy container exists, which is exactly what happens when someone imports their nginx or Nginx Proxy Manager hosts and installs the proxy afterwards. The attach used to fail because the network was not there yet, and the domain was saved anyway, so the route existed and answered 502. The network is now created on demand, and installing the proxy re-attaches every enabled container target, so an install repairs whatever was saved earlier.

## 2026-09-12 — The www redirect gets its own router and its own certificate
"Redirect www" used to widen the host's router rule to cover `www.<host>`, so Let's Encrypt was asked for one certificate covering both names. A domain whose `www` has no DNS record then failed the whole request, and the host that did exist was served Traefik's default certificate: one missing record broke TLS for a site that was otherwise correct. The redirect now lives on a separate router, so a missing `www` record costs only the redirect.

## 2026-09-12 — Database ports publish to 127.0.0.1 by default, and the tunnel hint uses an address that resolves
A domain routes HTTP; Postgres, MySQL and Redis speak their own protocols on their own ports, so a domain does nothing for them and the panel now says where a client should connect instead. Publishing asks where the port should live: bound to 127.0.0.1 for an SSH tunnel, which is the answer for a desktop client, or on every interface with a firewall allowlist. The tunnel command the panel printed named the container, which the SSH host cannot resolve; it prints the container's address now, with the caveat that it moves when the container is recreated.

## 2026-09-12 — Adminer is asked for, not assumed, and databases do not take a domain
Opening a database in Adminer used to install it silently on an sslip.io host with a Let's Encrypt certificate. That name is shared by everyone using sslip.io, which is one registered domain for rate limiting, so the certificate often would not issue. The first open now asks where Adminer should answer, which certificate it should carry and whether the panel's login should guard it, and warns when the session cookie domain does not cover both hosts. One Adminer serves every database: it joins each instance's network as that database is opened.

The catalog install form no longer offers a domain for database apps. Databases speak their own protocol on their own port, so an HTTP route to them can only fail, and offering the field invited exactly that.

## 2026-09-12 — The theme boot script is a file, because the panel's own CSP blocked it inline
The panel sends `default-src 'self'` with no `unsafe-inline` for scripts, so the snippet in `index.html` that stamped the saved theme before first paint never ran. The theme therefore followed the operating system on every load and the chosen one only survived until a reload. It moved to `/theme.js`, served from the same origin, and the app stamps again on mount as a fallback. The same rule means no inline script anywhere in the panel will run; put boot code in a file.

## 2026-09-12 — Light or dark, never "follow the system"
Three states made the header button a riddle: the label had to name a mode, and "System" told nobody what they were looking at. The operating system now only seeds the very first visit; after that the choice is a saved preference and the button is a single icon showing the mode it switches to.

## 2026-09-12 — The panel is English only
The German and Macedonian dictionaries were machine translated and half complete, and a partly translated control panel is harder to use than an English one. The picker is gone and `t()` reads English directly. The other files stay in `src/locales` for whoever wants to finish one properly.

## 2026-09-12 — The shell holds still; only the page scrolls
The sidebar and header are fixed panes and the content area is the only scroll container, so navigation and the server's name stay on screen on a long log or a long container list. Pages that fill the viewport ask for `h-full` instead of subtracting header heights from `100vh`, which was wrong whenever a banner appeared.

## 2026-09-13 — Catalog entries wear the project's own mark
A wall of identical cards told nobody which one was Postgres. Entries now carry a monochrome brand mark from Simple Icons, which is CC0, kept as single paths in `web/app/src/components/brandIcons.ts`. A recipe takes the mark of the framework that names it, not of the database it happens to use, so "Next.js with Postgres" is a Next.js card. Anything without a mark falls back to an icon for its category, so nothing renders blank.

## 2026-09-13 — One Field, one Select, and a spacer for buttons in a row
Inputs across the panel were a pixel garden: nine different hand-written `select` class strings, and rows that used `items-end` so any field carrying a hint sat higher than its neighbours. There is now a `Select` primitive with the same box as `Input`, every label reserves one line so labels and controls line up whether or not a hint is present, and a button standing in a row of fields wears `FieldAction`, which puts an invisible label-height spacer above it. The caller's classes go on an inner row inside `FieldAction`: putting them on the outer box let a `flex` there pull the spacer alongside the button and reintroduce the very gap it exists to close.

## 2026-09-13 — /containers/stacks is a tab, not a container called "stacks"
`:id` and the tabbed lists sat in different Routes, so React Router matched `/containers/stacks` as a container detail and the page failed with "No such container: stacks". The static tabs and the dynamic detail now sit in one Routes, where a static segment outranks a parameter. "Manage stack" also names the stack it means, `?stack=<name>`, which opens that stack's editor, and a stack that cannot be opened says so inline instead of raising a browser alert that blocks the page.

## 2026-09-13 — Firewall rules are generated from what the server runs, and know which chain a port arrives on
A port on a Docker host arrives on one of two chains. A port the host itself listens on is INPUT, which is what `ufw allow` writes. A port published by a container is FORWARDed to it and needs `ufw route allow`. The old EnableFirewall set `default deny routed`, installed the ufw-docker rules, then wrote input rules only. The result was a silent outage: every domain behind the proxy stopped answering while the server still replied on its own ports, which reads as a proxy fault and is not.

Openings are now a described thing rather than a hardcoded list. Each one names its port, protocol, the chains it needs and where it may come from, and `FirewallPlan` builds the set from the real SSH port, the proxy's real published ports and the panel's real port. A database published by a container is a routed port too, so its allowlist finally does something.

Three things keep it from coming back. `Opening.Commands` is the single place that renders ufw arguments, and it emits the route rule whenever a port is routed. A `firewall-routes` check fails loudly while any proxy port lacks a forward rule, with a one-click repair that adds only the missing rules. Tests assert the rendered command lines, including the forward rules, the real-port cases and the ufw status parser, which previously could not tell a forward rule from an input rule.

## 2026-09-13 — The panel port is not open to the internet by default
Only the proxy ports face the world. The panel is the daemon on the host, so it never needs a forward rule, and how far it opens is the one real choice: an explicit range if one is set, otherwise the admin's own address, otherwise closed when a domain already routes to the panel. It falls back to open only when there is no domain and no known address, because closing it then would lock the admin out, and it says so in the output rather than doing it quietly.

## 2026-09-13 — Settings is tabbed, not one long scroll
Seventeen cards in a single column meant hunting by scrollbar, and the jump links only moved the scroll position rather than reducing what was on screen. Each section is a tab now and only the chosen one renders, so Account shows four cards instead of everything. The tab lives in the URL as `?tab=`, which keeps a link to a section shareable, and an old `#anchor` link selects the matching tab instead of scrolling.

## 2026-09-13 — Tabs live in one component, and grids get a column template at every width
A tab strip built as an `overflow-x-auto` box with `-mb-px` on the buttons shows a scrollbar for one stray pixel: once either axis is not `visible`, the browser promotes the other to `auto` too, and the negative margin makes the content one pixel taller than the box. The rule now sits on a wrapper and the scrolling on the inner row, so nothing overflows its own scroll box. The row still scrolls sideways, because five tabs genuinely do not fit a phone, but the scrollbar chrome is hidden since that only happens where the row is dragged.

Tabs are one component now, used by Settings and Apps, so the two cannot drift apart and a fix lands in both.

Separately, a grid that set columns only at a breakpoint had no template below it, so items fell into an implicit `auto` column that sized to its content and pushed whole cards past a phone screen. Every grid now declares `grid-cols-1`, and custom templates use `minmax(0, …)` so an `fr` track can shrink below its content and let the scroll container inside do its job.

## 2026-09-13 — A layout audit, because these faults only appear at some widths
`hack/layout-audit.mjs` drives a headless browser over the panel and reports scrollbars nobody asked for, content spilling past the viewport with nothing able to reach it, and controls too small to hit. It found both faults above, and it exits non-zero so it can gate a release. Reviewing a diff does not catch a fault that needs a 390px viewport to appear.

## 2026-09-13 — A second audit round, and what it found
Ten reviewers went over the codebase in two rounds: architecture, concurrency, React structure, the design system, tests and release, then destructive paths, security, proxy and TLS, frontend runtime, and install and upgrade. Both structural reviews said the same thing, that no refactor is needed before launch. Everything below came out of the behavioural ones.

The most serious was a privilege bug rather than a design one. The protected-path guard compared the raw query string while the reader that followed cleaned it, so `/var/lib/islet//secret.key` slipped past and handed the daemon's own encryption key to any signed-in user. The guard now normalises first, covers `/root`, `.ssh`, `.aws` and the auth log, and there is a test for each shape that used to pass. In the same family: the token scope table ended in "any GET is a read", so a read-only token opened the host terminal, which is a root shell that happens to start as a GET; shells now need their own scope and unknown paths deny by default. A token can no longer carry more than its owner's role. Writing a Compose file is root on the host, so it is admin-only rather than open to deployers. The MCP tools checked the token scope but never the caller's role, which let a viewer run a cron job as root. Host log sources are admin-only.

Every command Islet ran was recorded with its arguments, which carry the restic repository password, cloud access keys and runner tokens, and the drawer that shows them needed only a login. Values are redacted at the single point that renders a command line, and the drawer is admin-only.

Four ways to lose data. Retention grouped snapshots by tag alone, so a second server pointed at the same repository pruned the first server's history, which is exactly what the recovery kit tells people to do when they migrate. A partial backup was recorded as a success, so a plan whose source went missing stayed green. Restoring into an existing volume overwrote live data because creating a volume that already exists succeeds. Volume pruning removed named volumes, which is where a stopped database keeps its data.

Two ways to take every site offline. A wildcard domain outranked every specific host, because Traefik sorts by rule length and the generated regular expression is always longer, so one wildcard swallowed the panel and every named site in the zone. And closing the panel port to the internet also closed it to the proxy, which dials the daemon for forward auth and the panel route, so every protected domain answered 502. Routers now carry an explicit priority, and the firewall allows the proxy network specifically.

The rest: a backup check that ran longer than a minute was restarted every minute until the box fell over; the deploy logger leaked a goroutine per deploy and wrote its buffer from two of them without a lock, which could panic outside the recovery middleware and take the daemon down; streams never closed their reader, so a closed tab left an orphan process; an app update ran on the request context and left the stack down if you navigated away; runner scale-up counted and started without a lock and ignored its own limit.

Reaching the release: the test workflow did not trigger on tags, so every release this week published without the suite running once. It is a required job now. The installer verified a checksum it downloaded from the same host as the tarball, which proves nothing; it verifies the published signature with the key inlined as PEM, tested against a real release and a tampered one. A bad update had no way back, so the previous binary is kept and the new one must start before it is moved into place. An older binary refused to open a newer database only by accident; it now says so.

In the panel: nothing reacted to a session ending, so an expired tab sat on stale numbers polling forever; a 401 anywhere returns to the sign-in screen. Streamed output grew without a limit and was re-joined on every line, which froze a tab on any real build log. A dropped packet ended a log follow permanently because the code closed the stream the browser was about to retry. A truncated stream reported success. Polling continued in hidden tabs, which cost a small server thousands of calls overnight.
