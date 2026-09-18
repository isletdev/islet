# Islet Services — the plan

> Status: plan, nothing built. Decisions here are settled unless marked open.
> Where the work stands is [STATUS.md](STATUS.md); why non-obvious calls were
> made goes in [DECISIONS.md](DECISIONS.md) as each one lands.

## What a service is, and why it is a new thing

Everything Islet has today serves **the operator**: the panel, the CLI, the
agents, the MCP tools. One person, one server, full authority, scoped by a role.

A service serves **the operator's applications, and their users**. An app POSTs
an image and gets back a URL. A browser uploads a video straight to a bucket. A
customer's phone fetches a thumbnail. None of those are the operator, none of
them should ever hold operator authority, and all of them arrive from the open
internet at a rate nobody controls.

That is the whole reason services are a separate concept rather than more
routes under `/api/v1`. The rule that follows from it is the one rule this
design cannot bend:

> **Service traffic and operator traffic never share an authentication path, a
> middleware chain, a rate limit, or a role.** A media key is refused by the
> panel API. A panel session is refused by a service. Neither knows the other
> exists.

The second rule is cheaper to state and just as important: **a service that is
off costs nothing** — no container, no goroutine, no table rows, no open port.

## The shape

```
app / browser ──▶ media.example.com ──▶ Traefik ──▶ isletd  ──▶ storage (disk, R2, S3, MinIO, GCS, Azure)
                                                      │
                                                      └──▶ islet-media container (libvips, ffmpeg, poppler)
```

The daemon owns the API, the keys, the metadata and the routing decision. The
container owns nothing and remembers nothing; it is handed a file and some
operations and produces a file. Storage is a driver behind an interface.

**Why the heavy work is a container.** libvips, ffmpeg and poppler are around
300 MB and several of them want cgo. `isletd` is a single static binary that has
to run on a 1 vCPU, 1 GB server, and that is not a constraint worth spending on
image resizing. Islet already runs containers well — Traefik, every catalog app
— so the media worker is one more thing it owns the lifecycle of. A server that
never turns media on never pulls the image.

**Why the daemon still fronts it.** Auth, quotas, metadata and storage routing
belong in one place, and that place already has the database, the audit log, the
secret sealing and the command transparency drawer. The worker being
replaceable is a feature: a bigger server can run more than one, and a different
transform engine changes nothing above it.

## Phase A — the framework

Nothing about this is media-specific, and it is worth building first so the
second service is cheap.

- A **Services** section in the panel: what exists, what is on, what it costs.
- Enable and disable per server. Enabling is a real action with a real cost —
  pulling an image, opening a port, taking a domain — so it is one screen that
  says all of it before it happens.
- **Service keys**: created in the panel, hashed at rest, shown once, scoped to
  operations and to a namespace, revocable, with last-used and a rate limit.
  This follows the existing API-token pattern and shares none of its authority.
- **A domain of its own.** Services are reached at a hostname the operator
  chooses — `media.example.com` — issued a certificate by the existing proxy.
  Not a path under the panel: the panel's origin holds a session cookie, and
  nothing that serves user-uploaded bytes belongs on it.
- **Its own middleware chain**: key auth, per-key and per-IP rate limits, CORS
  from an allowlist, request size caps, and a body of audit for anything
  destructive. It shares the router with nothing.
- **Health and usage** on the service's page: requests, bytes stored, bytes
  served, errors, and what the worker is doing.
- **Copy-paste for the app developer**: the service page shows working curl,
  JavaScript and Go against *this* server, with a real key, because "plug and
  play" means not reading a specification first.

## Phase B — media, first shippable version

Scope: **images and files.** Upload, store, serve, delete; images resized,
compressed and converted on the way out; anything else stored and served as it
was, scanned on the way in. PDF and video are Phase C and the API is shaped so
they slot in rather than change it.

### Storage

A media **bucket** is a named place to put bytes. Drivers:

| Driver | Covers | Notes |
|---|---|---|
| `local` | this server's disk | under the data directory, served by the daemon |
| `s3` | AWS S3, Cloudflare R2, MinIO, Backblaze, Wasabi, Hetzner | one driver: endpoint, region, path-style toggle |
| `gcs` | Google Cloud Storage | Phase C |
| `azure` | Azure Blob Storage | Phase C |

Written by hand against each provider's HTTP API, not through their SDKs — the
same call `internal/assistant` makes for the same reason. SigV4 is about three
hundred lines and covers four vendors; the AWS SDK alone is larger than the
whole daemon. Credentials are sealed with the existing key.

**Media keeps its own buckets** rather than sharing one storage section with
backup destinations. That is a decision with a cost, and the cost is that an R2
key is entered twice and can drift. It buys not touching a backup path that
works, and no migration of a table people's restores depend on. If a third
consumer of object storage ever appears, that is the moment to reconsider.

### Uploading

Two paths, because the right one depends on where the bytes are going.

1. **Through Islet.** The app or browser POSTs to the service. Islet checks the
   key, the size, the type, scans it, writes it to the bucket, records it, and
   answers with an id and a URL. This is the only option for `local`, and the
   right one when the file must be validated before it exists.

2. **Straight to the bucket.** The app asks for an upload ticket; Islet returns
   a presigned PUT; the browser uploads directly to R2 or S3; the app tells
   Islet it is done and Islet records the object. A 300 MB video never touches
   the 1 vCPU box. Scanning becomes a choice per bucket, because it means
   reading the object back — off by default here, and the panel says so rather
   than implying a check that is not happening.

Both paths produce the same object. An app can use either without knowing.

### Serving

- **Public objects**: a stable URL, far-future cache headers, an ETag, and
  nothing to authenticate. Put a CDN in front and Islet stops seeing the
  traffic, which is the point.
- **Private objects**: a signed URL with an expiry, minted by the app through
  its key.
- **Variants**: `.../object/{id}/{variant}` where a variant is a **named preset**
  the operator defined — `thumb`, `hero`, `avatar` — not a free-form query
  string. Free-form transforms are an invitation to ask a 1 vCPU server for ten
  thousand sizes of the same photograph. Signed arbitrary transforms are
  available for the cases presets cannot cover.
- Derivatives are produced on first request, cached in the bucket beside the
  original under a deterministic key, and served from there afterwards. No table
  for them: the object store is the state, and a derivative that is missing is
  one that gets made again.

### Limits that are not optional

A 1 vCPU server that will happily resize images on demand is a server anyone can
stop with a loop. So, from the first version:

- a global cap on concurrent transforms, defaulting to one,
- a per-key request rate and a per-key byte quota,
- a maximum source dimension and file size, refused before any decoding,
- a type allowlist per bucket,
- and a queue that sheds rather than grows.

### Data

New tables, all scoped `(server_id, …)` and unique per server:

- `media_buckets` — name, driver, config, sealed credentials, public base URL.
- `media_keys` — name, hash, prefix, scopes, namespace, limits, last used.
- `media_objects` — bucket, namespace, key, size, type, checksum, width, height,
  visibility, metadata, who uploaded it.
- `media_presets` — name, operations.

No table for derivatives, and no `status` column anywhere: what exists in the
bucket is what exists.

## Phase C — the rest of the media brief

- **GCS and Azure drivers.**
- **PDF**: page count, a thumbnail of page one, text extraction for search.
- **Video**: probe, thumbnail, and transcode to web formats. Transcoding is not
  a request — it is a job with progress, failure and a notification, and it
  needs a queue that does not exist yet. On a small server it is one at a time
  and the panel says plainly what it will do to the box before it is switched
  on.
- **Image extras** as they earn their place: focal-point cropping, AVIF, EXIF
  stripping by default with an opt-out.

## Phase D — proving the framework

A second service, chosen because it is wanted rather than to make a point.
Candidates: transactional email with a sending domain, full-text search over an
app's own data, a queue. The test of the framework is whether the second one is
mostly configuration.

## Open questions

- Whether a service is per-server or can be per-app. Per-server is the
  assumption; namespaces inside a key cover most of what per-app would.
- Whether the worker fetches from storage itself or is always handed a staged
  file. Staging is simpler and costs a copy; fetching needs the worker to hold
  credentials, which is an argument against it.
- Usage accounting granularity, and whether it is worth storing per-object
  served-bytes on a small server.

## Deliberately not in scope

- A hosted, multi-tenant media service. This runs on the operator's server, for
  the operator's apps.
- Image editing in the panel. This is infrastructure, not a tool.
- Replacing a CDN. Islet serves correctly and caches well so that a CDN in front
  of it works; it is not one.
