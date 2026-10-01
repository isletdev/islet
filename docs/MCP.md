# Driving Islet from an agent

Everything the panel does, Islet also does through one HTTP API, and that API is
exposed to agents as MCP tools. This page is what an agent needs to know, and
what a person needs to know to let one in.

There are two kinds of agent here and they reach the same tools by different
routes:

- **The assistant** (the Assistant page). It is given a token per question,
  scoped to the asker's own role and narrowed further — see *What an agent may
  not do* below. Nothing to set up.
- **Anything else**: a workspace agent, Claude Code on your laptop, any MCP
  client. It needs the MCP endpoint switched on and a token of its own.

## Turning it on

MCP is off by default. Settings → turn on the MCP endpoint, then mint a token
under Settings → API tokens with the scopes you want it to have.

```
POST /mcp            Streamable HTTP, one endpoint
Authorization: Bearer islet_…
```

In an MCP client's configuration that is:

```json
{"mcpServers": {"islet": {"type": "http", "url": "https://panel.example.com/mcp",
  "headers": {"Authorization": "Bearer islet_…"}}}}
```

The endpoint answers `tools/list` and `tools/call` and nothing else. Every call
goes through the same authorisation table the panel's own requests do, and is
written to the audit log with the token's owner as the actor — a tool is not a
back door around a role, it is the same door.

## What the tools are

Seventy-three of them, in two kinds. Seventeen are written by hand where the
answer is not simply an endpoint; the rest are the REST API's own routes,
described. Ask the endpoint for `tools/list` rather than trusting any list
written down here, including this one, which will drift.

By area:

| Area | What is there |
|---|---|
| The server | `server_status`, `attention`, `metrics_history`, `diagnostics`, `disk_usage`, `audit_log`, `commands_run`, `security_report`, `host_audit`, `apply_security_fix`, `add_firewall_rule` |
| Containers | `list_containers`, `get_container`, `container_action`, `container_logs`, `list_stacks`, `create_stack`, `stack_action` |
| Apps and deploys | `list_apps`, `get_app`, `create_app`, `delete_app`, `deploy_app`, `app_releases`, `app_deploy_log`, `cancel_deploy`, `add_app_service`, `inspect_repo` |
| GitHub | `list_github_repos`, `publish_to_github`, `wire_github_webhook` |
| Domains and TLS | `list_domains`, `create_domain`, `delete_domain`, `domain_dns`, `dns_check`, `list_certificates` |
| Databases | `databases`, `get_database`, `create_db_in_instance`, `dump_database`, `database_slow_queries` |
| Files | `list_files`, `read_file`, `search_files`, `file_operation` |
| Backups | `backup_status`, `run_backup`, `list_snapshots`, `verify_destination` |
| Uptime | `uptime_checks`, `create_uptime_check`, `delete_uptime_check` |
| Cron | `list_jobs`, `get_job`, `create_job`, `run_job`, `job_runs`, `delete_job` |
| Workspaces | `list_workspaces`, `create_workspace`, `delete_workspace` |
| Catalog and recipes | `list_catalog`, `catalog_app`, `install_catalog_app`, `installed_catalog_apps`, `list_recipes`, `run_recipe` |
| Secrets | `list_secrets`, `store_secret` — names and storing only; nothing reveals a value |
| Notifications | `notify`, `recent_events` |
| Anything else | `islet_request`, which reaches any endpoint the token's scopes allow. The curated tools exist because a described one is easier to use correctly; this is the way out when there is no tool for what you need |

## Putting an application on GitHub, and getting it back

This is the loop Islet exists for, and the thing it is most worth an agent
knowing how to drive. **Neither direction requires anybody to open a
repository's settings page, paste a webhook URL, or copy a secret into GitHub.**
That was the arrangement until v0.38.0 and it was the single most tedious thing
about deploying from here.

First, once, a person connects GitHub under Settings → GitHub. Either or both
of:

- **A GitHub App**, created in one click: Islet writes the manifest, the browser
  posts it to github.com, the person presses Create, and GitHub hands the
  credentials straight back. The App carries one webhook for every repository it
  is installed on, so nothing is ever needed per repository.
- **A token**, pasted once. With it Islet creates repositories, pushes to them,
  and adds each webhook itself through the API.

An agent cannot do this part, and should not: it is the credential itself.

### Direction one — a repository that exists

```
list_github_repos                      → pick one
create_app  name, source=git, repoUrl  → clones it, builds it, runs it
create_domain  host, targetType=app    → puts it behind a hostname with a certificate
```

`create_app` adds the push webhook as part of creating the app. The reply
carries a `webhook` line saying what happened — "the GitHub App already delivers
this repository's pushes", or "webhook added to owner/repo". If an app was made
before the GitHub connection existed, `wire_github_webhook` does that one step
on its own.

From then on every push deploys. `app_deploy_log` is what to read when one
fails.

### Direction two — an application that only exists here

The assistant writes an app into a directory — a workspace checkout, anywhere on
this server — and then:

```
publish_to_github  path, name, private, createApp=true, domain=shop.example.com
```

That one call: writes a `.gitignore` if there is none, commits what is there,
creates the repository, pushes it, creates an Islet app for it, adds the
webhook, and starts the first deploy. It answers with the repository's URL.

`.env` is always ignored, whatever the directory's own `.gitignore` says. An
application's secrets belong in the app's environment here, not in its
repository, and "publish this directory" is exactly how they would otherwise
end up on GitHub.

Publishing needs the **token** connection: creating a repository under a
personal account is the account's own authority, and an App installation token
does not have it.

## What an agent may not do

The assistant's token is minted per question, expires in forty-five minutes, is
revoked when the answer ends, and carries the asker's own role narrowed to:

```
read deploy containers domains db files backups uptime runners catalog notify logs system media
```

Four scopes are deliberately absent, and a tool needing one of them is refused
no matter who asked:

| Absent | Why |
|---|---|
| `shell` | a root shell is the whole server |
| `security` | the firewall is how the server stays reachable |
| `vault` | the secrets, which it has no reason to read out |
| `cron` | a root command on a timer is a shell by post |

GitHub splits along the same line: `list_github_repos`, `publish_to_github` and
`wire_github_webhook` are **deploy**, because what comes out of them is an app, a
webhook and a push. The credential — the App's private key, the personal token —
is **settings**, which the assistant does not carry. An agent can put an
application on GitHub and cannot change the account it does that as.

A token you mint yourself can carry any scopes you like, including `*`. A
workspace agent gets one when you tick the box on the agent, and it is worth
giving it less than everything for the same reason.

## When something is refused

Every refusal says which scope would have allowed it, and the audit log records
what was asked for and by whom. A tool that answers "this key may not …" is a
scope problem; a tool that answers "no such …" is usually a name that does not
exist on this server rather than a permission.
