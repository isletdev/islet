# Platform audit — 2026-09-15

Everything below was found by measuring this server, not by reading the code and
imagining. Each item says how it was found, so it can be checked again.

The order is what to do first, and the reason is the same throughout: the things
that cost the most are the ones that happen every few seconds without anyone
asking.

## Done

Carried by **v0.18.0**:

- **1. Polling noise.** `cmdrun.Read` runs a command without writing it down, for
  commands that only observe; tmux's reads and the `claude` lookup go through it,
  and the lookup is cached for a minute besides. Measured on the development
  server: ten polls of the workspaces page wrote **thirty** command rows before
  and **zero** after.
  The thirty were not what was expected, either. Silencing tmux revealed that
  every *read* was calling `ensureServer`, so a server with workspaces but
  nothing attached — tmux exits when its last session ends — ran `systemctl
  stop`, `systemctl reset-failed` and `systemd-run` every ten seconds, forever.
  Reads no longer start anything, and a start that fails is left alone for
  thirty seconds rather than retried on every request.
- **2. Unbounded tables.** `store.Housekeep` prunes `commands` after a fortnight
  and `audit_log` after a year, with a 200,000-row ceiling on each as a backstop
  against a loop that writes a fortnight's worth in an hour. It runs at startup
  and daily. Retention differs because the questions differ: one is "what has
  this been running", the other "who did what to this server".
- **3. The 4.8-second update check.** The registry lookups run together now, six
  at a time, under a 25-second deadline, and the answer is kept for fifteen
  minutes.
- **4. Files over 64 KB.** `decodeLarge` gives the routes that carry a document —
  a file, a Compose project, an env group — an 8 MB ceiling, and the message
  says what the limit is instead of blaming JSON. Verified: 300 KB saves, 9 MB
  is refused with "that is larger than 8192 KB".
- **5. What `/health` tells a stranger.** `{"status":"ok","time":…}` and nothing
  else unless the caller has a session, a token, or came in over the local
  socket. `islet status` sends its saved token and says so when it has none.

## ~~1.~~ (done) The command log is 61% polling noise — and every row is a process

`commands` holds 8,681 rows on this server, three days old. What is in them:

    2,336  tmux display-message
    1,344  tmux list-windows
    1,219  sh -c 'command -v claude'
    1,178  tmux -V
      428  tmux display-message -t islet-ws-…
      292  docker ps

Five thousand of those are the workspaces page asking tmux what it is doing,
every ten seconds. Twelve hundred are `ClaudePath`, which shells out on every
call and caches nothing — a lookup whose answer changes about once a month.

Three costs, and the third is the worst: a process spawn and a database insert
several times a second on a 1 vCPU box; a table growing 3,600 rows a day; and a
command-transparency drawer in which what somebody actually did is buried under
polling. The drawer exists to answer "what did this panel run on my server", and
it currently answers "tmux display-message, four thousand times".

**Do:** cache the binary lookup; stop recording pure state reads as commands, or
record them somewhere they do not drown the record of change.

## ~~2.~~ (done) `commands` and `audit_log` grow forever

`metrics_samples` is pruned. These two are not: ~3,600 and ~700 rows a day, so
1.3M and 250k rows a year, on a box whose whole database is meant to be small
enough to back up in a second. Both have the right index; neither has a ceiling.

**Do:** retention, with the audit log kept far longer than the command log,
because they answer different questions.

## ~~3.~~ (done) Checking for catalog updates takes 4.8 seconds and hits the network serially

`GET /api/v1/catalog/installed/updates` runs, per app and per image in it,
`docker image inspect` and then `docker buildx imagetools inspect` — a registry
round trip — one after another, with no cache and no deadline of its own. Two
apps on this server cost 4.8s. Ten apps would cost twenty.

**Do:** run them together, cache the answer, and give the whole thing a deadline.

## ~~4.~~ (done) A file over 64 KB cannot be saved, and the error blames JSON

Measured against the API: 60,000 bytes saves (204), 90,000 bytes fails with
`{"error":"bad_json","message":"http: request body too large"}`. Every request
body in the panel is capped at 64 KB by one shared decoder, which is right for a
form and wrong for a file editor, a Compose file or an env group.

**Do:** per-route limits, and an error that says what the limit is.

## ~~5.~~ (done) `/api/v1/health` tells a stranger the version, commit, host and server id

    {"status":"ok","version":"0.17.3","commit":"cbb48ee",
     "serverId":"7cba…","hostname":"ubuntu-8gb-fsn1-1","uptimeSeconds":2636}

Unauthenticated, from the internet. The version and commit are what somebody
matches against a CVE list; the hostname and server id are free reconnaissance.

**Do:** keep `status` unauthenticated and move the rest behind a session.

## 6. Restoring a backup needs a path nobody could guess

Backups restore, byte for byte — verified here for the first time: a 200 KB
random file and a text file backed up to a local repository, restored to a fresh
directory, md5 identical. That is Phase 5's definition of done and it holds.

Getting there by hand took four attempts. `Include` must be
`/data/paths/<the host path>`, an artefact of how the source is mounted into the
restic container; `/data/<host path>` answers `path data/tmp: not found`.

**Do:** accept the host path and translate it, and say what is available when it
does not match.

## 7. Packages carrying real risk with no tests

    internal/backup  1,369 lines   restore proven by hand, nothing automated
    internal/db        951 lines   creates and drops databases on a live server
    internal/runner    596 lines   registers CI runners with a token
    internal/uptime    406 lines   the thing that decides whether to wake you
    internal/watch     206 lines
    internal/terminal  136 lines

**Do:** the backup round trip as a `hack/e2e-*` script first, since it is the one
whose failure is silent until the day it matters.

## 8. The e2e suites still are not in CI

`hack/e2e-privileges.py` passes today — twenty checks, run by hand against a
daemon built from this working tree. `hack/e2e-assistant.mjs` passes too.
Neither runs on a push, so neither will catch the change that breaks them.

**Do:** wire the ones that need nothing but a daemon into the pipeline.

## 9. Twenty-nine `set-state-in-effect` warnings

Each is a render that schedules another render. Most are harmless; at least one
is a real bug of the kind already found by hand this week (a poll resetting a
selection), and `Domains.tsx` also calls `Date.now()` during render, which is
the same class: state that changes without an event.

**Do:** read them one by one. They are a list of places where the panel does work
it was not asked to do.

## 10. Smaller things

- CI logs a Node 20 deprecation on every run; four actions need a bump.
- `islet.dev` docs site and the `isletdev/catalog` repository are both 404.
- The VPS e2e workflow has never run: it skips itself without `HCLOUD_TOKEN`.
- The assistant's MCP token on this server carries `*`. That is the operator's
  choice, but it is worth stating in the panel where the token is made, because
  it is the only fence around what the assistant can do.
