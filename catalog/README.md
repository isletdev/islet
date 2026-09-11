# Islet catalog

One folder per app under `apps/`, embedded into the daemon at build time. This folder is meant to become its own repository (`isletdev/catalog`, MIT) once the format settles; the layout already matches that.

## Adding an app

```
apps/<slug>/
  islet.yaml      # metadata and the install form
  compose.yaml    # the stack; ${VARS} come from the form
```

`islet.yaml`:

```yaml
name: Umami
slug: umami                 # lowercase, becomes the stack name
category: analytics         # database | developer | media | analytics | productivity | networking | security | cms | other
description: Privacy-friendly web analytics.
website: https://umami.is
service: app                # the Compose service that receives the domain
port: 3000                  # what that service listens on
fields:
  - key: APP_SECRET
    label: App secret
    type: secret            # text | secret | number | select; secrets are generated when left empty
  - key: POSTGRES_PASSWORD
    label: Database password
    type: secret
volumes: [db]               # named volumes the app owns; backups and "remove with data" use this list
notes: |
  Shown after install. Say what the first login is and what to do next.
```

Rules that keep installs boring:

- Pin image tags (`umami-software/umami:postgresql-v2.14.0`, never `latest`). The update checker compares digests, so a moving tag defeats it.
- Everything the app needs is in the stack: databases, caches, workers. Never assume a host service.
- No published ports unless the app must be reached on a raw port; the domain goes through the proxy.
- Health checks on the main service so "Installed" means "answers".
- `restart: unless-stopped` on every service.
- Secrets only through `fields` of type `secret`; the panel generates and stores them encrypted.

## Testing an app

Install it through the panel on a real server (or `ISLET_TLS=off` locally), open it on its domain, then run **Update check**, **Back up** (add the stack's volumes to a plan) and **Remove**. All four must work before the app is merged.

## Recipes and scripts

`recipes/` and `scripts/` are reserved for in-panel wizards and shared install scripts; guides live in `docs/recipes/` for now.
