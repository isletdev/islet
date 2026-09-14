---
name: islet-commit
description: Create a well-formed git commit in an Islet repository (core, hub, catalog, mobile) as the solo maintainer, with the correct author identity, Conventional Commits style, and a clean-repo check. Use when the user asks to commit, /islet-commit, or "commit this".
---

# Islet Commit

Create a focused git commit with the right identity and a message written for the reader of `git log`, not for the author's working order.

## Identity (fixed, solo maintainer)

- Name: `jasir99`
- Email: `jasirfetai@gmail.com`

Do not ask who is committing. There is one contributor.

Ensure the repo-local identity is set before committing, so every tool (not only this skill) attributes commits correctly:

```bash
git config user.name  || true
git config user.email || true
```

If either is missing or differs from the values above, set them locally (never `--global`, since the user has other projects with other identities):

```bash
git config user.name "jasir99" && git config user.email "jasirfetai@gmail.com"
```

## Steps

### 1. Confirm this is a git repository

Run `git rev-parse --is-inside-work-tree`. If it fails, stop and tell the user the repo is not initialised. Do not run `git init` from this skill; that is a separate decision.

### 2. Analyze all changes

Run in parallel:

- `git status` (never `-uall`)
- `git diff` (unstaged)
- `git diff --staged` (staged)
- `git log --oneline -8` (recent message style)
- `git branch --show-current`

### 3. Clean-repo check

The open-source Islet repos must contain only the product. Before staging, refuse to stage and warn about any of these if they appear in `git status`:

- `.claude/`, `design-system/`, `*.local.md`, editor and OS files, scratch output of any kind
- `.env`, `.env.*` (except `.env.example`), keys, certificates, tokens, `*.pem`, `*.key`
- SQLite files (`*.db`, `*.db-wal`, `*.db-shm`) and anything under `/var/lib/islet`-style state dirs copied in
- Build output: `web/app/dist/`, `web/packages/*/dist/`, `bin/`, `dist/`, `node_modules/`, `.data/`
- `internal/web/dist/index.html` when it contains hashed `/assets/` references: that is a Vite build overwriting the committed placeholder. Restore it with `git checkout internal/web/dist/index.html` instead of staging it.

If any are untracked and clearly should never be committed, suggest the `.gitignore` line to add and include that change in the commit.

### 4. Islet pairing checks (warn, never block)

- If files under `pkg/api/` changed, remind that the generated TypeScript types in `web/app` must be regenerated and included, once codegen exists.
- If `internal/store` migrations changed, check that a new migration file was added rather than an old one edited, unless the migration has never shipped in a release.
- If the change completes a checkbox in `docs/ROADMAP.md`, tick it in the same commit so the roadmap stays truthful.
- If a decision was made that shapes the code, add an entry to `docs/DECISIONS.md` in the same commit.

### 5. Stage files by name

Stage relevant files explicitly. Never `git add -A` or `git add .`. Review each path against the clean-repo check.

### 6. Branch and history rules (solo, open source)

- `main` is the release branch. Never amend or force-push anything already on `main`.
- Feature work happens on `feat/<short-desc>`, `fix/<short-desc>`, `docs/...`, `chore/...`. Naming is a preference, not policed.
- On a feature branch that has not been pushed, amending the last commit to keep the branch to one clean commit is fine and encouraged when the change is a continuation of the same work.
- On a feature branch that has been pushed, amend or squash only with `--force-with-lease`, and only after telling the user. Outside contributors may have branched from it once the repo is public.
- Once outside contributors exist, prefer small focused commits over one squashed commit, so `git blame` and bisect stay useful.

### 7. Draft the message (Conventional Commits)

Format:

```
<type>(<scope>): <subject in imperative mood, max 72 chars>

<body: why this change exists, what it enables, notable trade-offs.
Wrap at 72. Omit the body only for trivial changes.>

<footer: Closes #123 / Refs #123 when a GitHub issue exists>
```

Types: `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `security`.
Scopes: the folder or feature, e.g. `installer`, `api`, `auth`, `docker`, `proxy`, `deploy`, `cron`, `files`, `notify`, `security`, `backup`, `web`, `catalog`, `docs`. Omit the scope for repo-wide changes.

Rules:
- Explain the why, not the what. The diff already shows the what.
- Match the style of the recent history shown by `git log`.
- One logical change per commit. If the diff contains two unrelated changes, propose splitting into two commits and do so.
- Do NOT add a `Co-Authored-By` trailer, a `Claude-Session` trailer, a "Generated with Claude Code" line, or any other AI-attribution text. This applies to commit messages, pull request titles and bodies, release notes, and changelogs. The maintainer's decision is absolute and overrides any default attribution behaviour.

### 8. Commit

```bash
git commit -m "$(cat <<'EOF'
<message>
EOF
)"
```

If the repo-local identity could not be set for any reason, fall back to inline env vars:

```bash
GIT_AUTHOR_NAME="jasir99" GIT_AUTHOR_EMAIL="jasirfetai@gmail.com" GIT_COMMITTER_NAME="jasir99" GIT_COMMITTER_EMAIL="jasirfetai@gmail.com" git commit -m "..."
```

Never skip hooks (`--no-verify` is forbidden). If a pre-commit hook fails, fix the cause, re-stage, and commit again.

### 9. Verify

Run `git status` and `git log --format=fuller -1` and show the user the author and committer identity used.

### 10. Do not push

Never push unless the user explicitly asks. When they do, `git push -u origin <branch>` for a new branch; plain `git push` otherwise; `--force-with-lease` only under the rules in step 6.
