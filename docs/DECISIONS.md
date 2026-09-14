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

## 2026-09-13 — Saving a domain never recreates the proxy
Reconcile compared a version label on the running container and, when it differed, rebuilt the container: pull, remove, run. A failure in that last step left nothing serving, and the usual failure is a port already taken, which is exactly the situation people are in when they reinstall. The trigger was worse than the mechanism, because domains reach Traefik through a file it watches, so saving one never needed the container touched at all.

Reconcile now only writes that file. A container started by an older Islet keeps serving every route; the status simply reports that its flags are out of date and the panel offers to recreate it, which is a deliberate act at a moment the owner chooses. When that act happens the swap keeps the old container: it is stopped and renamed aside, the new one has to start and still be running two seconds later, and only then is the old one removed. If it does not come up, the old one is renamed back, started, and the error says the previous proxy is serving again.

## 2026-09-13 — Uninstalling the panel leaves the server working
`--purge` deleted the whole data directory, which holds the Compose file for every app the panel installed, the Let's Encrypt certificates and account key, database dumps and the file trash. It then left Traefik running against a directory that no longer existed, holding ports 80 and 443, and printed that apps were not touched.

Uninstalling the panel and destroying what it set up are different intentions, and only the first one gets a flag. `--purge` now removes Islet's own memory, its database, its encryption key and the panel certificate, and keeps everything a person would be upset to lose. No container is stopped and no volume is deleted in any path, so the apps keep running and the proxy keeps serving. The script prints where the Compose files are so they can be managed with plain docker compose, warns when domains were set to require an Islet login because those have nobody left to ask, and names the one command that really removes everything for someone who wants that.

## 2026-09-13 — The panel asks its own questions
Fifty-two calls to the browser's confirm, prompt and alert carried every destructive action, including dropping a database and the panic button. Three things were wrong with that. They look like a browser error rather than part of the product. They cannot say which of two outcomes is the dangerous one, so the choice is always "OK" against "Cancel" no matter what is about to happen. And they do not appear at all inside the app frames the panel embeds, so the same action silently did nothing there.

There is one dialog now, behind a promise-shaped API, so a handler still reads top to bottom. Every action names itself in its own button, "Move to trash" rather than "OK". Anything irreversible is styled as destructive and says what cannot be undone. The worst of them ask you to type a word first: the panic button, deleting an app, dropping a database, removing every unused volume, and deleting a protected system path.

Moving a file to the trash asks now, which it never did, and says items are kept for seven days. Deleting from the trash asks separately, because that is the step that cannot be undone. Publishing a database port was three chained browser prompts, one of which asked the person to type "public"; it is one dialog per decision, and the choice between the two is a pair of buttons that each say what they do.

## 2026-09-13 — No settings for one hosting company
The panel carried a Hetzner API token so it could take a server snapshot before an SSH change. A panel that runs on any server should not have a page for one provider, and the snapshot was covering a change that already protects itself: SSH settings are validated, tested with `sshd -t`, and rolled back automatically after five minutes unless you confirm. The integration, its package, its endpoints and its settings card are gone.

## 2026-09-13 — The terminal opens as its own window
A shell wants height, and inside the panel it competes with a sidebar, a header and a page heading. `/console` renders the terminal and one thin bar naming what you are connected to, and "Open in a window" opens it as a popup sized like a terminal emulator. It is a route rather than a mode, so the window survives a reload and can be bookmarked. A folder in the file browser can open a shell that starts in it, which the PTY already supported and nothing had ever asked for.

## 2026-09-13 — Picking a folder beats typing a path
Copy and move asked for a destination in a text box, which requires knowing the answer before you start. They open a picker that walks the same tree the file list shows, can create a folder while you are in it, and refuses to drop something into itself. Row actions are icons with real labels, the name column says what a row is with an icon rather than a text arrow, and the actions stay reachable by keyboard and on a touch screen, which has no hover.

## 2026-09-13 — One login, more than one server
Islet installs on a server and manages that server. The second server was a
second install, a second password and a second bookmark, and nothing in the
panel knew the first one existed.

A managed server now joins from a button. You give an address and, once, a way
to log in; the panel installs its own key, runs the ordinary installer over SSH,
asks the new daemon for a token and proves it can reach it back through the
tunnel. The wizard shows that happening line by line, and the install keeps
running if you close it, because a server left with Docker and no daemon is the
worst outcome available.

What it is not: an agent. Every managed server runs a complete Islet with its
own database. Its cron jobs, backups, uptime checks and deploys keep running
when the controller is off, and if the controller is lost the server can still
be opened on its own. There is no second mode to write or to test.

The panel reaches it over SSH rather than over the internet, which means a
managed server needs no port open at all, and there is exactly one credential to
look after — the controller's key, revoked by deleting one line from
`authorized_keys`. Joining does not close that port by itself, because someone
may be using it; the card says when it is open and offers to close it, and
closing it runs through that server's own audited firewall route rather than a
command down the pipe.

Switching servers is one control in the header. It rewrites API paths, so no
page had to learn about it: a page asks for `/api/v1/domains` and does not need
to know which machine answers. Sessions, users and the server list stay on the
controller, because a managed server has one account on it and editing that is
not what anyone means by "users". The terminal follows too — a WebSocket cannot
travel on an HTTP client, so the handshake is rebuilt and the bytes spliced down
the same tunnel.

## 2026-09-13 — A field's width has to survive the component
Every input carried `w-full` from the component and then the caller's `w-24`
after it. Tailwind resolves two width utilities by their order in the
stylesheet, not by the order they are written, so `w-full` quietly won and five
fields across the panel came out as wide as their own label instead of the width
they asked for. The default is now left off when a width is given, and
`hack/width-audit.mjs` measures every sized field in a real browser so the next
one is caught rather than shipped.

Two smaller things from the same pass. Not every success carries a body: a join
returns 202 with nothing in it, and parsing that as JSON threw a syntax error
that was then shown to the person as if the request had failed. And several
clickable things in dense tables were sixteen pixels tall; they have a
fingertip's worth of height now without changing the row's.

## 2026-09-13 — The SQL client is part of the server, not a container next to it
Islet installs databases, and the only way to look inside one was Adminer: a
second container, a second login, and a tool that has not changed since 2010.
NocoDB was measured at 770 MB resident while idle and stores your credentials in
its own database. `docs/SQL_CLIENT.md` is the specification that came out of
rejecting both; this is what shipped against it.

It runs in the daemon. Idle it costs a map and a ticker: no pool, no connection
and no memory until somebody opens a connection, which is the entire argument
against a second container and had to stay true. It reaches a database Islet
installed on the container's own bridge address, so nothing is published to the
internet to query it, and there is no "add connection" step for the common case
— the panel already knows the host, the port and the password.

Real drivers rather than `docker exec psql`. Text output carries no column
types, cannot tell `NULL` from an empty string, cannot stream and gives nothing
to hang foreign-key navigation on. `pgx` and `go-sql-driver/mysql` cost **4.06
MB** on the binary, measured `linux/amd64` with `CGO_ENABLED=0` against the
previous release. The specification budgeted under 5 MB; the staging area's own
measurement said 6.88 MB, which was a synthetic program that exercised every
`pgtype` codec rather than the daemon, and the reduced type map it recommended
turned out not to be needed.

Every representation is decided once, in `value.go`, because the obvious
alternative loses information in silence. `NULL` is null and an empty string is
`""`. `int8`, `numeric` and `decimal` are strings, always and per column, since
a JavaScript number rounds past 2^53 and a column that changes shape halfway
down a result is worse than one that is consistently a string. A date is a day,
not a day with a midnight glued to it. `jsonb` is embedded rather than
stringified so the grid can open it. `bytea` is base64 with a byte count.

Statement boundaries are found by a lexer, not by splitting on semicolons, and
the same lexer exists twice: once in Go, because the server decides what it will
run, and once in TypeScript, because the editor outlines what Run is about to
send. Two answers to that question is a tool that highlights one statement and
runs another. `internal/sqlclient/testdata/statements.json` is a shared corpus of
47 cases — dollar-quoted bodies, nested comments, MySQL's `--` rule, an `UPDATE`
written inside a comment — and `hack/sql-split-agree.mjs` runs both
implementations over it. The parse tree from `@codemirror/lang-sql` drives
highlighting and completion, which is what a grammar is good at, and does not
drive the ranges.

Safety is in the daemon, not the interface. A read-only connection refuses a
write in the classifier before it is sent and on a connection opened with
`default_transaction_read_only`, so both would have to fail. An unfiltered
`UPDATE` or `DELETE`, a `DROP`, a `TRUNCATE`, an `ALTER`, a `GRANT`, and any
write at all on a connection marked production are refused with 409 until the
request says it was confirmed — the panel collects that with its own dialog,
naming the connection, and the worst of them ask you to type the connection's
name. `EXPLAIN ANALYZE` goes through the same preflight, because explaining a
`DELETE` with `ANALYZE` deletes the rows. Every statement reaches the audit log
through `cmdrun.Redact` and the history table, whether it succeeded or not. The
whole surface is admin-only: arbitrary SQL is equivalent to root on the data,
and `hack/e2e-privileges.py` proves a viewer is refused all twenty routes.

Four bugs found by pointing it at a real database rather than a fake one.
Postgres renders `relkind` as the `"char"` type, which a driver is free to hand
back as a number, so every view was reported as a table until the query asked
for text. `pgx` decodes `numeric` into its own struct, which `fmt.Sprint`
rendered as `{725 -2 false finite true}`; every `pgtype` value satisfies
`driver.Valuer`, so asking is better than guessing. MySQL has no `program_name`,
and an unknown DSN parameter is sent as an unquoted `SET` at connect, which made
every MySQL connection fail on its first statement. And the worst one: a
statement the server killed came back as a cursor with no columns whose error
only appears once it is asked for a row, so skipping the row loop for a
statement that returned no columns turned a cancelled query into a silent
success — no error, no rows, nothing wrong. The cursor is always drained now,
and there is a test with a driver that fails exactly that way.

## 2026-09-13 — The way into the SQL client is on the database
The client shipped reachable from the sidebar and from the slow-query list, and
from nowhere else. Somebody looking at their database saw one button, "Open in
Adminer", which installs a container on demand — so it was still offered after
the Adminer stack had been deleted, and it looked like the only option there
was. The database page leads with "Open in the SQL editor" now, and Adminer is
a quiet second line that says it installs a container, because it does.

The address a managed database is reached on got a second try at the same time.
The bridge address is right on a Linux server, where the daemon and Docker share
a host, and cannot work where they do not: Docker Desktop keeps containers in a
VM, and a daemon in a container of its own is on another network. When the port
is published there is a second route, and taking it beats telling somebody their
own database is unreachable. The bridge address is still tried first and the
fallback costs one dial, only on failure.

## 2026-09-13 — A tree that behaves like a tree, and SQL that is not a terminal
Clicking a table opened its rows and only the chevron expanded it, so the
obvious gesture did the unobvious thing. The row toggles now; opening the rows
is its own control on the row, which is two different actions with two
different targets rather than one target guessing.

Clicking a column appended its name to the end of the document. Not at the
cursor — at the end, which is rarely where anyone is looking and never what
they meant. A column is something to read; it is text now.

Filtering searches columns as well as table names, so a table could be in the
list for a reason invisible on its own row. A table matched by a column opens
itself, shows the columns that matched and nothing else, and the matched run of
characters is marked in both. A result you have to go looking for is not a
result.

And the generated SQL in the table viewer was drawn on the always-dark code
surface, which is right for a terminal and for a log and wrong here: in the
light theme it was a black slab of text in a white page, and it is a statement
you are meant to read and take into the editor, not output. It follows the
theme. Terminal and log surfaces keep the dark treatment they are named for.

The environment control said development, staging, production and nothing about
what any of them did, and only one of them does anything. It says so now, next
to the control: only Production changes what the panel does, and what it
changes is that every write asks first.

## 2026-09-13 — The SQL client is a view of a database, not a place of its own
It had a sidebar entry, which made it look like a third thing beside Databases
and Apps. It is not: it is what you do to a database, the same way dumps and a
connection string are. The entry is gone. You reach it from the database you
want to look at, and the page carries a way back because nothing in the sidebar
says where you are any more.

That leaves databases somebody added by hand, which have no card on the
Databases page and would have had no way in at all. They are listed there now,
under their own heading, marked production or read-only where they are. A
database is a database whether Islet installed it or somebody pointed at one.

And a table tab now belongs to the connection it was opened on. Carrying it to
the next connection showed "relation does not exist" for a table that is simply
somewhere else. Query tabs still follow you: the SQL in one is the person's and
may well be what they want to run here.

## 2026-09-13 — The SQL client is one database, and less of everything else
Six controls came off the page. A connection picker, because the database is
the one you opened and switching is going back and opening another. An "add a
connection" button, because adding a database belongs on the page where
databases live. An environment select with three tiers of which one did
anything. A transaction checkbox. A statement-timeout dropdown. Two EXPLAIN
buttons that were disabled whenever the cursor was not in a statement, which is
how they came to look like buttons that do nothing.

What that bought, beyond a page you can read: everything on it now belongs to
one database. Tabs and their text are remembered per connection, saved queries
are filtered to the connection they were written against, and history already
was. Switching databases used to carry a table tab across and show "relation
does not exist"; there is nothing to carry now.

Two fixes fell out of it. A query tab always holds its own connection, which it
did not when the transaction checkbox was off — so `BEGIN` typed by hand opened
a transaction on a pooled connection and handed it to the next borrower. And
MySQL's tree now shows the database the connection selected, through
`DATABASE()`, rather than every database on the server: a MySQL connection can
see them all and a Postgres one cannot, and the same panel showing a whole
server in one engine and a single database in the other was the asymmetry
behind "in MySQL I can make databases and in Postgres I cannot".

Confirmation is now a property of the statement alone. An unfiltered DELETE
deserves a second look wherever it runs; an UPDATE with a WHERE does not become
dangerous because somebody labelled the connection.

## 2026-09-13 — A CodeMirror wrapper has to accept text from outside
The editor component built its document once, on mount, and ignored the `value`
prop after that. A comment said so, as though it were a design: "the editor
owns the document after mount".

Three controls were quietly dead because of it. Choosing a cron template filled
in the name and the schedule and left the script box empty, which is what it
looked like from the outside: a template that does not work. Picking a shebang
from the dropdown did nothing. Restoring an older version of a script did
nothing. All three set state that reached the editor as a new `value` and was
dropped on the floor.

The rule, for any wrapper around an editor that owns its own document: the
document is created once, and a `value` that differs from it is dispatched as a
change. Comparing before dispatching is what makes that safe on every render —
typing sends the document up and it comes back identical, so there is nothing
to apply and the cursor is never moved. The SQL editor already did this; the
file editor now does too.

## 2026-09-13 — Sorting belongs to the table, not to each page
Three pages listed rows nobody could reorder: files, containers, images. Each
would have grown its own `useState` for a key and a direction, its own compare,
and its own header markup, and they would have drifted.

`lib/sortable` is the one implementation: `useSort` takes the rows, a column
map, an initial choice, and an optional grouping function that runs before the
comparison. The grouping is the part worth naming — directories stay above
files and running containers above stopped ones no matter which column is
sorted, because that grouping is not a sort order, it is what the list *is*.
Comparison is `Intl.Collator` with `numeric: true`, so `img10` follows `img9`,
and blanks sort last in both directions, because a missing value is not
"smallest", it is missing. The choice is remembered per table in
`localStorage`.

Sizes come from Docker as `"1.09GB"` and `"10.4kB"`, which sort as text into
nonsense. `sizeToBytes` parses them back to numbers, decimal and binary units
alike, so the images table can default to largest-first — which is the only
reason anyone opens it.

## 2026-09-13 — An import wizard reads whatever is already there
"Import from nginx" was a textarea on the Domains page, and the name was a
guess about what the person is running. Somebody migrating onto Islet may have
Caddy, Apache, or Nginx Proxy Manager in a container, and telling them their
setup is not supported when the parser is thirty lines away is a poor welcome.

`internal/proxy/discover.go` reads every location those four keep virtual hosts
in — including `/var/lib/docker/volumes/*/_data/nginx/proxy_host/*.conf`, which
is where NPM writes the only machine-readable copy of its hosts. It only reads:
nothing is stopped and nothing is written until the person picks rows from a
table and presses the button. The parsers are deliberately shallow — they look
for the names and the upstream and ignore everything else — because a reverse
proxy can express things Islet has no equivalent for, and half-understanding
those is worse than not reading them.

Pasted text is not asked about either: `ParseText` tells the three formats
apart, and a `$forward_scheme` in the file is what distinguishes Nginx Proxy
Manager from nginx.

## 2026-09-13 — A CDN in front of a domain is not a misconfiguration
The DNS check reported "Not yet — currently points to 104.21.83.116" for a
domain that was working perfectly, because it was behind Cloudflare. A check
that calls a correct setup broken is worse than no check: it teaches people to
ignore it.

`internal/proxy/cdn.go` carries the Cloudflare and Fastly ranges and answers
which one an address belongs to. The check now says "Behind Cloudflare, which
is why the record does not point here directly", and tells them the two things
that actually matter in that setup — that the origin has to be this server and
port 80 has to reach it, or the certificate has to be issued over DNS-01.

## 2026-09-13 — Say when the certificate renews, rather than offering to renew it
The question was whether certificates auto-renew, and whether it should be an
option. They do: Traefik renews about thirty days before expiry, and
`acme.json` is on a mount so a restart does not lose them. There was nothing to
build. The gap was that nothing on the page *said* so, and an absence of
information reads as an absence of the feature.

The proxy card is now a status strip — ports, certificates with the next
renewal date, any renewal notices, wildcards — with the settings form behind a
button. The facts are what somebody opens the page for; the form is what they
open it for once.

## 2026-09-13 — Installing is a dialog, and it does not end by offering to install again
Clicking a card in the catalog rendered the installer *below* the grid, off the
bottom of the screen. From the person's side nothing had happened. It is a
dialog now, at the top of the viewport, over the grid it came from.

Worse was the ending. The install finished, the form reset, and the Install
button came back — the exact state that invites a second copy of the same app.
The dialog now stays open and says what happened: the stack name, the address
if it has one, and a way to go to it under Your apps. Install something else is
a deliberate second choice, not the default one.

The same argument applies before the install: if the app is already installed,
the dialog says so and offers the existing one, and the suggested stack name
becomes the first free `slug-2`, so the form is never pre-filled with a name
that will be rejected.

Catalog cards are uniform height, three lines of description, with a line
reserved for the status badge whether or not there is a badge — a row of cards
that changes height with its text reads as a mistake rather than a catalogue.

## 2026-09-13 — Delete lives where the thing is listed
Removing an installed app or a database meant knowing it was a Compose stack
and going to the stacks list. Two different mental models for one act.

Apps installed from the catalog now appear under **Your apps**, which is where
somebody looks for something they installed, and each row has Remove. Database
cards have Remove, and so do external connections. Every one of them asks twice
— the container, then the data — because those are two decisions and only one
of them can be undone. Forgetting an external connection says what it does
*not* do: the database keeps running, untouched.

An app with a domain also appears in the sidebar on its own. The panel
installed it and knows its address; making somebody type a link to it is asking
them to tell us what we just told them. Those links are marked `auto` and are
never written into the stored list, so removing the app removes the link.

## 2026-09-13 — Never report an update from output you did not understand
Every installed app claimed an update was available, forever. The check
compares the local image digest with the registry's, and asked buildx for the
latter with `--format '{{.Manifest.Digest}}'`. buildx ignores that template and
prints its whole human report, which is never equal to a digest.

The template is fixed, but the fix that matters is `digestOf`: anything that is
not a bare `sha256:` string is "" and the comparison is skipped. A badge that
cries wolf is worse than no badge, and the failure mode of a loose comparison
is always to claim there is news.

## 2026-09-13 — The editor follows the page, and the syntax colours follow the tokens
A log pane is dark in both themes, because a console is a console. A document
somebody is writing is not: the file and cron editor was a black rectangle in
the middle of a white page. It takes its colours from the panel's tokens now,
as the SQL editor already did.

That exposed the second half. CodeMirror's stock highlight style is a fixed
light palette — dark red comments, dark blue keywords — and on the dark theme's
near-black ground the comments were barely legible. `lib/codetheme` maps the
syntax roles onto the panel's own semantic tokens, so every colour is one the
brand already guarantees is AA against the surface behind it, in whichever
theme the reader is in. Comments recede, strings and numbers are the literal
values, keywords carry weight, everything else is ink.

The environment column on a saved connection went at the same time. 0022
dropped the marks table; the column outlived it, defaulting every row to
"development" and read by nothing.

## 2026-09-13 — A certificate method is a choice, and a host can have more than one target
Two gaps against Nginx Proxy Manager, both reported by somebody moving off it,
and both the same shape: a thing the panel could already do, reachable only
down one path.

**DNS-01 was reachable only by asking for a wildcard.** The provider
credentials, the resolver, the encrypted storage were all there; `Render` chose
the DNS resolver if and only if the host started with `*.`. So a plain host
behind Cloudflare — whose port 80 does not reach the origin, which is the whole
point of Cloudflare — had no way to be issued at all. The Cloudflare fix
shipped in v0.6.0 made that worse by advising exactly the thing the panel would
not do: "issue the certificate over DNS-01".

`tls` gains `letsencrypt-dns`, so the challenge is a stored choice rather than
a consequence of the hostname. A wildcard is normalised to it in `Validate`,
which turns the old special case into the ordinary one and leaves a single
check for the missing provider instead of two. `tlsFor` is the one place that
decides, and every router on a host — root, locations, www — takes the same
block, because a path presenting a different certificate from the page linking
to it is not a configuration anybody wants.

**A host had one target.** `path_prefix` scoped a host to a single path, which
is not the same feature: NPM's custom locations put `/` on one backend and
`/api` on another, and Islet refused the second row with "that host is already
routed". Importing such a host kept the root and dropped the rest, in silence.

Locations are their own table, owned by the domain, rather than more rows in
`domains`. The host is what holds the certificate, the login, the allowlist and
the limits — those guard a name, and a path is not a different name — so they
stay in one place and locations inherit them. Only the path, the target and
whether the prefix is stripped belong to a location.

Priority is the part that needed thought. Traefik ranks by rule length only
when no priority is set, and the root already sets one. Two bands: exact hosts
at 100+, wildcards at 1+, each location at its band's base plus its path
length. That keeps a longer path beating a shorter one within a host, and any
route on a named host beating any route on a wildcard — so a location under
`*.example.com` cannot take `/api` away from `shop.example.com`.

**The importer had to learn the same thing four times.** nginx location blocks,
NPM's per-location `set $server`, Caddy's `handle`/`handle_path`/`route` and
inline matchers, Apache's repeated `ProxyPass`. The detail every one of them
hinges on is whether the prefix reaches the backend: nginx says it with a
trailing slash on `proxy_pass`, Caddy with `handle_path` rather than `handle`,
Apache with a target path of `/`. All three mean Traefik's `stripPrefix`, and
reading a trailing slash as decoration silently changes every URL the app sees.

What cannot be translated is now named rather than dropped: regular-expression
locations, named locations, exact matches. An import that quietly loses a path
looks complete and is not, and the person has no way to find out except in
production.

## 2026-09-14 — Traefik turns Let's Encrypt off quietly, so the panel has to say so
Reported from a real migration: imported every site from nginx, installed the
proxy, entered an email — and got no certificates, no explanation, and every
site marked insecure by the browser.

Traefik refuses to load an ACME account from a file more permissive than 0600.
Its response is not to fail: it logs `The ACME resolve is skipped from the
resolvers list`, drops the resolver, and carries on. Every router then asks for
a resolver that no longer exists, so Traefik serves its own self-signed
certificate for every host and never requests anything. From the panel that
looks like a running proxy, an empty certificate list, and nothing connecting
the two.

Islet created `acme.json` with 0600 and never looked at it again. The file
outlives the install that made it — a restored backup, a copied data
directory, an older version, a bind mount that reports its own mode — so the
mode is now checked and repaired on every install, not only at creation.

The same shape had a second instance. Installing with no email produced a
proxy with no certificate resolver at all, which is the identical dead end,
and the install returned 200. It is now refused, naming the domains that were
asking and what to do instead.

Neither fix would have helped the person who hit this, because nothing in the
panel could tell them what was wrong. `Diagnose` reads Islet's own
configuration and Traefik's recent log and answers the actual question — why
are there no certificates — in words: a store Traefik refused to open, an
empty email, a missing DNS provider, a rejected challenge, a port already
held by nginx. The answer was always in `docker logs islet-proxy`, which is
precisely the place somebody running a control panel should not have to look.

One more, found while testing the fix: Apply closed the settings panel on
success, and the settings panel was where the result message was rendered, so
a successful apply was indistinguishable from a dead button. The confirmation
now lives outside the form it reports on.

## 2026-09-14 — Never ask how long a request body is before reading it
The proxy install handler read its body only `if r.ContentLength > 0`.

ContentLength is **-1** when the length is unknown, and unknown is what a
chunked request looks like. A request that reaches the panel through a reverse
proxy is routinely re-encoded that way — including through Islet's own Traefik,
as soon as somebody routes the panel to a domain, which is the first thing a
new user does.

So for exactly the users who had finished setting Islet up, the body was never
decoded. Every field arrived at its zero value: the Let's Encrypt email, the
DNS provider, its credentials. `Install` was then called with an empty email,
started Traefik with no certificate resolver at all, and the request answered
200. Every site on the server was served Traefik's own self-signed
certificate, and the panel showed a running proxy with an empty certificate
list and an email box that still held what the person had typed — because the
value had never left the browser.

This one defect produced every symptom of two separate bug reports, and the
two fixes shipped before it — repairing acme.json permissions, refusing an
install with no email — were both treating its downstream effects. The second
one turned a silent failure into a loud and baffling one: *you have an email
there, and the panel insists you do not*.

The rule is the one in the heading. A handler reads the body and decides from
what it finds; `io.EOF` means there was none, which is a different answer from
a malformed one and the only case worth special-casing. Length is transport
framing and says nothing about whether content exists.

Worth noting how long this hid. Every test, every scripted check and every
local browser session talked to the daemon directly, where Go's client sets
Content-Length on a byte body, so the path was always taken. The condition was
only false in the deployed configuration none of them reproduced.

## 2026-09-14 — The Host header is the setting, not the WebSocket
Reported as "WebSockets do not work after the import". Traefik proxies a
WebSocket natively and needs nothing turned on, which was verified before
anything was changed: a handshake through Islet completed on a container
target, a URL target and a custom location.

What differed was the Host header. Islet sent the visitor's hostname to a
container target and the upstream's own `host:port` to a URL target — and a
URL target is what an imported site almost always becomes, because the
upstream in somebody's nginx config is usually an address rather than a
container Islet can see. nginx and Nginx Proxy Manager both send `$host`, so
an imported app started being told a different name than it had been told for
years.

Every WebSocket library checks Origin or Host before completing an upgrade, so
that is the half people notice. The quiet half is worse: absolute redirects,
cookie domains, generated links and anything that renders its own URL.

The default is now to pass the visitor's hostname, for every target type,
which is what the configurations being imported already did. The rewrite
remains available for the case it was written for — proxying to a genuine
external service, where the caller's Host means nothing to the far end.

The import reads it rather than assuming: `proxy_set_header Host` naming
anything other than `$host` or `$http_host` means rewrite, and its absence
means pass, because that is nginx's own default.

## 2026-09-14 — Block what a scanner asks for, not what a payload looks like
The same report asked for Nginx Proxy Manager's "Block Common Exploits". Half
of it is expressible in Traefik and half is not, and the honest thing was to
build the half that is and say so.

What is built: the paths. A new host appears in a certificate transparency log
and within seconds something asks for `/.env`, `/.git/config`,
`/vendor/phpunit`, a stray `.sql` backup. Those requests have no legitimate
form — no application is served from them — so refusing them costs nothing and
removes the most common way a server is given away. It is a router whose rule
matches those paths, above every other router on the host, answering 403 from
the daemon, because Traefik has no middleware that refuses a request and the
rule language is the only place this can be written at all.

`/.well-known/` is reachable by construction: no pattern names it. ACME
answers its challenge there, and a filter that broke certificate issuance
would be a worse bug than the one it prevents.

What is not built: query strings. NPM greps the whole query for SQL and XSS
fragments; Traefik's rule language can only match a named parameter, so the
same expression cannot be written. It is also the half that ages worst,
blocking payloads someone wrote down years ago while breaking search boxes
that legitimately contain the word "select". An application that parameterises
its queries is not helped by it and one that does not is not saved by it. The
panel says what it blocks rather than implying a firewall it does not have.

Both settings are read out of the configuration being imported — NPM records
the checkbox as an include — so a host that had them keeps them, and the
review says which settings came across.

## 2026-09-14 — Say which scheme the browser used, not which one the socket did
Reported as WebSockets refusing on a Phoenix app behind Islet. Everything about
the route was right: the request reached the app, the path was not rewritten,
the Host was correct, and the middleware chain was proved not to break upgrades
on Traefik 2.11, 3.0, 3.4 and 3.5.

The app was answering the upgrade with `301` to the exact URL it had just been
asked for — a redirect loop — while an ordinary request to the same path
worked. The asymmetry was the clue, and it is this:

    plain request:   X-Forwarded-Proto: https
    upgrade request: X-Forwarded-Proto: wss

Traefik reports the *connection's* protocol, so an upgrade over TLS is "wss".
Almost nothing downstream understands that. `Plug.SSL`, Rails, Django and
Laravel all compare the value with "https", find "wss", conclude the request
arrived in the clear, and redirect to HTTPS — which is where it came from. The
socket dies in a loop while every other request on the host is fine, and
nothing in the proxy or the app looks wrong, because separately they are.

Islet knows which scheme each route answers on, so it now states it: a
`customRequestHeaders` middleware at the front of every chain, https for a TLS
route and http for a plain one. The value no longer depends on whether the
request happened to upgrade, which is the property an app is relying on when
it reads the header at all.

Worth noting what this is not. It is not Traefik being wrong — "wss" is a true
statement about the connection. It is that the header's only real consumers
read it as "was this HTTPS", and a proxy that knows the answer should give the
answer rather than a synonym its readers do not know.

The same investigation turned up a second failure on the same machine, which
belongs to the app and not here: compose recreates a container with only the
networks in its own file, so a container the panel attached to `islet-proxy`
by hand loses that attachment on the next deploy and every route 502s. The
durable fix is to name Islet's network in the app's compose, not to reattach
it after the fact.

## 2026-09-14 — A session that outlives the daemon has to belong to something else
Workspaces exist so an agent can run on the server it is changing rather than on
a laptop that has to stay awake. That only works if the session survives
everything the panel does to itself, and the panel restarts itself on every
update.

So the session cannot belong to the daemon. Islet's terminal starts a PTY inside
the WebSocket handler and `defer sess.Close()` kills it when the handler
returns, which is why a refresh, a navigation, or an `islet update` all take the
shell with them. tmux is the whole answer: the command is a child of tmux, and
the daemon is only ever a client attaching to it. The test that matters is
`pkill isletd` — the process in the session has to still be there afterwards,
and it is.

The second reason for tmux over a session registry inside the daemon is that it
leaves a way in that is not us. `ssh in && tmux attach -t islet-ws-<id>` reaches
the same session with Islet stopped. A panel that is the only route to your own
work is a panel you cannot afford to have go down.

Three smaller decisions fell out of it.

**The command is sent with `send-keys`, not passed to `new-session`.** It lands
in the scrollback as if it had been typed — visible to whoever attaches next,
and repeatable with the up arrow — instead of being a process nobody can see the
provenance of.

**Attach uses `-d`.** tmux sizes a session to its smallest attached client, so a
tab forgotten on a phone would squeeze a desktop session to its width. The most
recent viewer wins.

**After a reboot the session comes back, the command does not.** Nothing in
memory survives a reboot, so the sessions are recreated in the right directory
at a shell prompt. Re-running the command would mean an agent resuming by
itself, mid-task, with nobody watching, on somebody's live server. That is a
thing to ask for, not a default.

## 2026-09-14 — A credential belongs where it cannot be committed
A workspace can give its agent an Islet API token so it can read logs and act on
containers through audited, scoped calls rather than working it out as root.
Claude Code reads `.mcp.json` from a repository root, which is the obvious place
to put it and the wrong one: a bearer token in a repository root is one
`git add .` away from being published, and the person who does it will not
notice.

It is written to `<data dir>/workspaces/<id>/mcp.json` at 0600 instead, and the
agent is started with `--mcp-config <that path>`. `--strict-mcp-config` is
deliberately not passed, so somebody's own MCP servers keep working alongside
Islet's rather than being silently replaced by it.

The scopes are `read, logs, containers, cron, notify` and pointedly not `shell`.
A token that could open a terminal would be a way around the admin-only check on
the feature that mints it.

## 2026-09-14 — Reconnecting must not throw away the screen
Two things in the terminal transport were survivable while a shell died with its
socket, and stopped being survivable once the session on the other end outlived
it.

`servePTY` had no keepalive. A terminal being read rather than typed into sends
nothing for minutes, and an idle WebSocket is exactly what a reverse proxy
closes — so a workspace left open while something long ran would drop for no
reason the person could see. It pings every 25 seconds now, the same interval
the SSE endpoints already used.

`TermView` built the terminal and the socket in one effect, so reconnecting
disposed the xterm and wiped the scrollback. Against a workspace, where the
session is still running and the screen would have come straight back, that
threw away the only record of what happened while nobody was watching. The
terminal is created once and the socket is replaced under it.

Automatic reconnection is offered only to endpoints that reattach to something
already running. A plain shell must not reconnect on its own: the process is
already dead, and quietly opening a second one would leave somebody typing into
a fresh shell believing it was the old one.

## 2026-09-14 — Ctrl+C cannot just be copy
Reported as copy and paste not working in the terminal. The obvious fix is
wrong: in a terminal Ctrl+C is interrupt, and a panel that takes that away
leaves no way to stop a running command — which matters most in exactly the
sessions people leave open for hours.

So it depends on whether there is anything to copy. With a selection, Ctrl+C
copies and then clears the selection, so the next press interrupts again; a
stray selection quietly disabling interrupt would be the same bug in a smaller
place. With no selection it is passed straight through. Ctrl+V is always paste,
because no shell wants it. macOS uses Cmd for both, where nothing conflicts at
all. This is what Windows Terminal does, and the reason it does it.

Right-click was showing the browser's menu — Back, Reload, View Source, Inspect
— which is four things a terminal cannot use and none of the two it can. It is
Copy, Paste, Select all and Clear now.

The clipboard API needs a secure context, so on a panel served over plain HTTP
the browser refuses. That says so in words rather than doing nothing, because a
paste that silently fails reads as a broken terminal.

## 2026-09-14 — Finding a binary is not the same as it being on PATH
The Claude Code preset ran `claude` and got "command not found" on a server
where Claude Code was simply not installed — and would have kept failing after
installing it, for a different reason.

Anthropic's installer puts the binary in `~/.local/bin`. A non-login shell, such
as the one tmux starts, frequently does not have that on its PATH. So the
natural sequence — install it, press Run, watch it fail again — would have
looked like the same bug twice with no way to tell them apart.

Islet looks for the binary in PATH and then in the places installers use, and
runs whatever it found by absolute path. Pressing Run before it is installed now
says what is missing and how to install it, instead of leaving a shell to say
"command not found" about a name the panel chose.

## 2026-09-14 — A managed server keeps its own accounts
`apiPath` forwarded almost everything to the selected server and deliberately
kept `/api/v1/users` local, on the reasoning that "a managed server has one
account on it, the controller's, and editing that is not what anyone means by
users".

That holds only while a member is headless. It stops holding the moment the
member serves a panel on its own domain, or protects a site with an Islet
login — both ask *that* server who you are, and neither can be answered by an
account on the controller. Somebody adding a second machine, routing a domain
to its panel and then finding themselves locked out of it is not an edge case;
it is the ordinary next step.

Users follow the selection now. What stays local is what is genuinely about
this panel and follows you everywhere: your session, your password, your second
factor, and the list of servers. The Users card names the machine whose
accounts are on screen, because editing the wrong server's users should not be
possible by forgetting which one is selected.

## 2026-09-14 — Two names that cannot share a cookie cannot share a login
Protecting a domain with an Islet login sent somebody to sign in and then
dropped them on the dashboard instead of the site they asked for.

The redirect was not the bug. Forward auth asks the panel whether the visitor
is signed in, and the visitor proves it with the session cookie their browser
sent to the protected host. A cookie can only be scoped to a parent of the
panel's own name, so a panel at one registrable domain can never authenticate a
site at another — there is no cookie that reaches both. The login page knew
this and handled it by doing nothing, which reads as "the login failed" when
the login in fact worked.

Two changes, neither of which makes the impossible possible. The login page
says why it cannot send you on, naming the two domains. And the Domains form
says it at the moment the box is ticked, rather than leaving it to be
discovered from a redirect loop in production — including the case where no
cookie domain is set at all, where nothing but the panel's own host is covered.

What does work is a panel on a name under the domain being protected, which is
worth saying in the same breath as the refusal.

## 2026-09-14 — xterm already pastes; handling it again pasted twice
Ctrl+V pasted everything twice, and paste from the right-click menu left the
keyboard somewhere else.

The double was mine. xterm keeps a hidden textarea and the browser's own paste
event delivers into it, so Ctrl+V and Cmd+V have always worked without help.
Adding a key handler that also read the clipboard and wrote to the socket meant
both paths ran. The handler now covers copy only — the one thing that genuinely
cannot be left to the browser, because Ctrl+C in a terminal is interrupt.

The only paste that needs code is the one from the context menu, where there is
no native event to ride on, and that one has to hand the keyboard back
afterwards: clicking a menu moves focus out of the terminal, and a terminal you
have just pasted into is one you are about to type into. Every menu action ends
by focusing it again.

## 2026-09-14 — A table row is not the place for a sentence
The domains table gave every row up to three lines of DNS advice, three words
of actions, and a date in whatever format the browser felt like. Four rows
filled a screen.

The advice now appears only when something is actually wrong. A domain behind a
CDN resolves exactly as it should, so printing "change the A record to…"
beneath it was both untrue and the largest single source of noise in the table
— the reason it was added, in an earlier fix, was to stop calling that state
broken, and leaving the remedy underneath undid half of it.

Actions became icons with their names kept as titles and labels. Three words
repeated down every row cost more width than the columns that carry the
information. Dates are written the way people write them down.

The two header actions were spread apart by `justify-between` with three
children in the row; they are one group now, so they sit together at the right.
And the edit form, which opens under the table, scrolls into view — a form
below the fold is the same "nothing happened" that the workspace terminal and
the catalog installer both had.

## 2026-09-14 — The CLI was documented, built and never shipped
`islet` did not exist on any installed server. The README has a CLI section,
Phase 4 ticks a line for it, the recipes use it, and `STRUCTURE.md` calls it
"the same binary via symlink" — but `.goreleaser.yaml` declared one build,
`./cmd/isletd`, so the 797 lines in `cmd/islet` were never compiled into a
release. The installer then ran `ln -sf isletd islet`, and `isletd` had no
`argv[0]` dispatch, so the name resolved to the daemon. `islet update` started
a second daemon and died on `bind: address already in use`. Nothing failed
loudly enough to notice, because the symlink was created exactly as intended
and every asset the release job checks was present.

The obvious fix is a second binary: add an `islet` build and archive, and have
the installer place it. That is the one that breaks. `update.Apply` takes
`os.Executable()`, resolves it with `EvalSymlinks`, and overwrites what it
finds with the archive member named `isletd`. Today the symlink makes that
correct — updating through either name replaces the one real binary. With a
separate `islet` on disk, `islet update` would have written the daemon over the
command line and left no way to notice until the next `islet` call printed
daemon flags. Shipping two binaries means teaching the updater which member
belongs at which path, changing the asset list the release checks, and
changing the installer.

So the symlink became true instead: the CLI moved to `internal/cli`, `cmd/islet`
is a four-line wrapper kept so it can still be built alone during development,
and `isletd` dispatches into `cli.Run` when `filepath.Base(os.Args[0])` is
`islet`. One artifact, five assets, installer untouched, self-update unchanged.

The cost is that `argv[0]` is now load-bearing — renaming the symlink silently
turns the CLI back into a daemon — so `InvokedAsCLI` is a named function with a
table test rather than an inline comparison, and `cli.Run` returns an exit code
instead of calling `os.Exit`, so the daemon can dispatch into it and the test
can assert on it. What reverses this is the second binary: add the build and
archive, and give `update.Apply` the member name for the path it is replacing.
