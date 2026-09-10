# Security policy

Islet runs as root on servers people care about. Reports are taken seriously and handled quickly.

## Reporting a vulnerability

Email **security@islet.dev** with a description, steps to reproduce and the version (`islet version`). Please do not open a public issue for anything exploitable. You will get an acknowledgement within two working days and a fix or a mitigation plan within seven for anything rated high or critical.

If you prefer encrypted mail, ask for the current PGP key in your first message.

## Scope

- The `isletd` daemon, the `islet` CLI, the web panel and the installer scripts in this repository.
- Catalog templates in `catalog/` (a template that exposes a service unexpectedly counts).

Out of scope: vulnerabilities in third-party images the catalog pulls (report those upstream, but tell us so the template can pin a fixed version), and issues that need an already-compromised admin account.

## Supported versions

The latest minor release receives fixes. Older releases update in place with `islet update`, which verifies the Ed25519 signature on every download.

## What Islet does to protect you

- Sessions and API tokens are stored hashed; secrets (channel configs, repository URLs, backup credentials) are encrypted at rest with a key that never leaves the server.
- Every command the daemon runs is recorded and visible in the panel.
- Releases are signed; the daemon refuses unsigned updates.
- Cross-site requests are refused for state-changing endpoints.
