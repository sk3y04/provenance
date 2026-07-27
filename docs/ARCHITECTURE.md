# Architecture

This document describes provenance's internal architecture, data flow, design decisions, and per-package reference.

## Overview

provenance is a single-binary Go CLI/TUI tool that downloads media from diverse sources by dispatching URLs to the correct engine. It has three layers:

```
┌─────────────────────────────────────────────┐
│  CLI (cobra)         │  TUI (Bubble Tea)    │  ← Transport
├─────────────────────────────────────────────┤
│  app.Download / app.Scan                    │  ← Application
├─────────────────────────────────────────────┤
│  dispatcher.Classify → extractor → downloader│  ← Core
├─────────────────────────────────────────────┤
│  session / watch / history / manifest / resolve / archive│  ← Shared services
└─────────────────────────────────────────────┘
```

- **Transport layer** (`cmd/provenance/main.go`, `internal/tui/`): Accepts user input and renders output. The CLI is a cobra command tree; the TUI uses Bubble Tea's Elm Architecture (Model/Update/View).
- **Application layer** (`internal/app/app.go`): `app.Download`, `app.Scan`, and `app.ScanResolved` coordinate between transport and core. They receive URLs + options, call into the dispatcher, and surface errors with diagnostic hints.
- **Core layer** (`internal/dispatcher/`, `internal/extractor/`, `internal/downloader/`, `internal/resolve/`): Classifies URLs, routes to extractors, downloads files, normalizes results into shared types.
- **Shared services** (`internal/session/`, `internal/watch/`, `internal/history/`, `internal/manifest/`, `internal/resolve/`, `internal/config/`, `internal/worker/`, `internal/ratelimit/`, `internal/diagnose/`): Persistence, filtering, concurrency, rate limiting, failure diagnostics, shared result types.

---

## Data Flow

### Download path

```
User input (URL or --batch file)
  │
  ▼
app.Download(ctx, urls, batchPath, opts)
  │
  ├─ Individual URLs ───────────► dispatcher.Dispatch(ctx, url, opts)
  │                                    │
  │                                    ▼
  │                               dispatcher.Classify(url)
  │                               returns Site (Patreon / Twitter / Reddit / Generic)
  │                                    │
  │              ┌─────────────────────┼─────────────────────┐
  │              ▼                     ▼                     ▼
│         SitePatreon           SiteTwitter           SiteReddit           SiteInstagram
│         SiteGeneric (yt-dlp)  extractor.DownloadTwitter()
│         extractor.RunYtdlp()  (GraphQL API)        extractor.DownloadReddit()
│              │                     │                (JSON API)        extractor.DownloadInstagram()
│              │                     │                     │              (REST API v1)
  │              └─────────────────────┴─────────────────────┴─────────────────────┘
  │                                    │
  │                                    ▼
  │                           downloader.Client.Download()
  │                           (resumable HTTP + progress)
  │
  ├─ Batch file ─────────────────► dispatcher.BatchDispatch(ctx, path, opts)
  │                                    │
  │                                    ├─ Patreon/Generic URLs → worker pool → extractor.RunYtdlp()
  │                                    └─ Twitter/Reddit URLs → sequential dispatcher.Dispatch()
  │
  ▼
Error with diagnostic hint (if any)
```

### Generic URL fallback chain

```
dispatcher.Dispatch(SiteGeneric)
  │
  ▼
extractor.RunYtdlpCaptured()     ← try yt-dlp first
  │
  ├─ Success → done
  │
  └─ Error → shouldBrowserFallback(stderr)?
       │
       ├─ No → return yt-dlp error as-is
       │
       └─ Yes →
            │
            ├─ 1. extractor.ScrapePlaylistVideoLinks()
            │      (headless Chrome: click Next, collect <a href>)
            │      → if links found: downloadVideoLinks() via worker pool
            │
            └─ 2. extractor.SniffOrFallback()
                   (headless Chrome: intercept network requests for media URLs)
                   → if found: feed sniffed URL back to yt-dlp
```

---

## Extractor Design

Each extractor follows a common pattern with three entry points:

```go
Download(ctx, url, outputDir, cookiesFile, opts) error
Scan(ctx, url, outputDir, cookiesFile, opts) (manifest.Manifest, error)
Scan*Resolved(ctx, url, outputDir, cookiesFile, opts) (resolve.Source, error)
```

The resolved path preserves raw platform metadata (`json.RawMessage`) for downstream archive and collection use. `provenance scan --json` emits the `resolve.Source` format.

| Extractor | Engine | Output | Auth |
|-----------|--------|--------|------|
| `ytdlp.go` | yt-dlp binary | Video/audio files | cookies.txt, browser cookies |
| `twitter.go` | X.com GraphQL API | Images (orig), videos (best bitrate MP4), GIFs, markdown posts | cookies + guest bearer token (auto-refreshed; `TWITTER_BEARER_TOKEN` overrides) |
| `reddit.go` | Reddit JSON API | Gallery images, Reddit videos (via yt-dlp), external embeds (yt-dlp), markdown posts | cookies; optional OAuth2 (`REDDIT_CLIENT_ID` + `REDDIT_CLIENT_SECRET`) |
| `instagram.go` | Instagram REST API v1 | Images (highest res), videos (highest bitrate), carousels, markdown posts | `sessionid` + `csrftoken` cookies from `cookies.txt` |
| `browser.go` | Headless Chrome (chromedp) | Media URLs for yt-dlp passthrough | Netscape cookies injected via `fetch.enable` |

### Twitter/X extractor details

1. **Username resolution**: `UserByScreenName` GraphQL query →
2. **Tweet fetching**: `UserTweets` cursor-based pagination (20 per page, configurable limit) →
3. **Media extraction**: For each tweet, parse `legacy.extended_entities.media[]`:
   - Photos: highest-quality `media_url_https` → `jpg:orig` format
   - Videos: highest-bitrate variant from `video_info.variants[]`
   - GIFs: MP4 variant at original quality

Query IDs are loaded from environment variables (`TWITTER_QUERY_*`) or hardcoded defaults. If a request returns a "query ID not found" error, the IDs are refreshed by scraping the X.com web client JS.

Rate limiting: respects `Retry-After` headers on 429 responses. Uses `ratelimit.Manager` for preemptive throttling (2 req/s).

### Reddit extractor details

1. **Post fetching**: JSON API (`/user/{name}/submitted.json` or `/r/{name}/new.json`), `after` cursor pagination (100 per page) →
2. **Media extraction**: For each post, parse the media:
   - **Gallery** (`gallery_data.items[]`): look up URLs in `media_metadata`, download highest-resolution
   - **Reddit video** (`media.reddit_video`): route the post permalink through yt-dlp
   - **External embeds** (Imgur, YouTube, etc.): route through yt-dlp
   - **Direct image** (`url_overridden_by_dest`): download directly

Rate limiting: 1 req/s.

### Instagram extractor details

1. **URL parsing**: `ParseIgURL()` supports profile (`/username`), post (`/p/SHORTCODE`), and reel (`/reel/SHORTCODE`, `/reels/SHORTCODE`) URLs →
2. **Username resolution**: `users/web_profile_info/?username=X` API endpoint →
3. **Post fetching**: Single posts via `media/{media_id}/info/`, profiles via `feed/user/{user_id}/` with `max_id` cursor pagination (12 per page) →
4. **Media extraction**: Based on `media_type`:
   - Type 1 (image): highest-resolution candidate from `image_versions2.candidates[]`
   - Type 2 (video): highest-resolution variant from `video_versions[]` (sorted by height, then width)
   - Type 8 (carousel): recurse into `carousel_media[]` for each slide

Shortcode-to-ID conversion uses base-64 encoding with Instagram's alphabet. The `sessionid` cookie is mandatory for all API requests; `csrftoken` cookie provides the `X-CSRFToken` header.

Rate limiting: 1 req/s, burst 2.

---

## Persistence Model

All persistent state uses JSON files. No database.

| Data | Default Path | Env Override |
|------|-------------|-------------|
| Sessions | `~/.cache/provenance/sessions/{name}.json` | `PROVENANCE_SESSION_DIR` |
| Watch subscriptions | `~/.cache/provenance/watch.json` | `PROVENANCE_WATCH_FILE` |
| Run history | `~/.cache/provenance/history.json` | `PROVENANCE_HISTORY_FILE` |
| Collections | `~/.cache/provenance/collections.json` | `PROVENANCE_COLLECTION_FILE` |
| URL archives | `{output}/_provenance_cache/archive.txt` | (per output directory) |
| Link caches | `{output}/_provenance_cache/{host}__{hash}.txt` | (per output directory) |
| Capture manifests | `{output}/.provenance/runs/{timestamp}.json` | (per output directory, opt-in via `--record`) |
| Capture items | `{output}/.provenance/items/{id}.json` | (per output directory, opt-in via `--record`) |
| Vault blobs | `{vault}/blobs/sha256/ab/abcdef...` | (content-addressed, deduplicated) |
| Vault revisions | `{vault}/revisions/{id}/` | (immutable, `id` = SHA-256 of manifest) |
| Vault collections | `{vault}/collections.json` | (archive collection definitions) |

### Vault architecture

The archive vault is an opt-in durable preservation layer. Files are stored by SHA-256 content address (deduplicated across revisions). Each archive operation creates an immutable revision with:

- **Entities**  -  a video, post, PDF, etc. with external ID, canonical URL, author, capture time
- **Artifacts**  -  files referenced by SHA-256 hash, with original path and media kind
- **Documents**  -  searchable text (captions, post body, page content)
- **Relations**  -  links between entities (`contains`, `attached_to`, `reply_to`, etc.)

Archiving consumes existing capture manifests (`.provenance/`), verifies SHA-256 integrity, copies files to the blob store, and writes revision records. Grab and collect output folders are never modified.

Sessions use **optimistic concurrency control**:
- Each save increments `Version`.
- Before writing, the on-disk JSON is read and its version compared.
- If versions differ, `ErrConcurrentModification` is returned.
- Writes are atomic: data is written to `{name}.json.tmp`, then renamed to `{name}.json`.

### URL archive format

```
ok:https://example.com/video.mp4         ← successfully downloaded
perm-fail:https://example.com/deleted    ← permanently failed
```

Lines without a prefix are treated as `ok:` for backward compatibility. The `--no-archive` flag disables both URL archives and yt-dlp's download archive.

---

## Concurrency Model

### Worker pool (`internal/worker/pool.go`)

A bounded goroutine pool with configurable concurrency, used for batch downloads and playlist crawls.

```
Pool.SubmitWithHooks(job, onSuccess, onFinalFailure)
  ├─ Blocks if pool is saturated (semaphore channel)
  ├─ Runs job in a goroutine
  ├─ Retries up to 3 times with exponential backoff (1s → 2s → 4s)
  ├─ Skips retries for Permanent errors
  ├─ Calls onSuccess once after first successful attempt
  └─ Calls onFinalFailure once after last failed attempt
```

`Pool.Wait()` blocks until all submitted jobs complete. The pool derives a cancellable context from the parent, so cancelling the parent aborts all in-flight work.

### Download dispatch concurrency

- **Individual URLs** (CLI `provenance grab <URL>`): dispatched **sequentially**. Each URL's extractor (Twitter/Reddit) uses its own worker pool internally for parallel media downloads.
- **Batch files** (`--batch`): dispatched via `dispatcher.BatchDispatch`, which classifies URLs:
  - Patreon/Generic URLs → `downloadVideoLinks()` using a worker pool
  - Twitter/Reddit/Instagram URLs → sequential `dispatcher.Dispatch()` (their extractors handle parallelism internally)
- **Playlist crawls** (browser fallback): discovered video links are downloaded in parallel via the same worker pool.

---

## Rate Limiting

`internal/ratelimit/ratelimit.go` maintains per-hostname `golang.org/x/time/rate.Limiter` instances:

| Domain | Rate | Burst | Rationale |
|--------|------|-------|-----------|
| `x.com`, `twitter.com` | 2/s | 2 | Guest token limits are tight |
| `reddit.com` | 1/s | 2 | Unauthenticated: ~10/min; Auth: ~60/min |
| `instagram.com`, `cdninstagram.com` | 1/s | 2 | API rate limits are strict |
| default | 10/s | 10 | yt-dlp and generic hosts are more tolerant |

Each extractor's HTTP client calls `limiter.Wait(ctx)` before making requests. Twitter additionally respects `Retry-After` headers on 429 responses.

---

## Downloader

`internal/downloader/downloader.go` provides a resumable HTTP downloader:

- **Resume**: Checks for an existing `.part` file; sends `Range: bytes={size}-` to resume. If the server doesn't support range requests, falls back to full download.
- **Speed limit**: Token bucket limiting `SpeedLimit` bytes/sec.
- **Progress**: Reports progress via `ProgressReporter` interface (structured callbacks for TUI) or falls back to `schollz/progressbar` (CLI).
- **TLS fingerprinting**: Optional uTLS transport (`internal/downloader/utls_transport.go`) mimics Chrome's TLS ClientHello to bypass CDNs that reject Go's default handshake. Uses `github.com/refraction-networking/utls`.

---

## Terminal UI Architecture

The TUI follows **Bubble Tea's Elm Architecture** (Model/Update/View):

```
tea.NewProgram(model)
  ├─ Init() → returns initial Cmd (load sessions)
  ├─ Update(msg) → returns (model, Cmd)
  │     ├─ tea.KeyMsg → route to view-specific handler
  │     ├─ tea.WindowSizeMsg → store dimensions
  │     ├─ sessionsLoadedMsg → update session list + refresh resume banner
  │     ├─ runnerEventMsg → update live counters
  │     ├─ fileProgressMsg → update per-file progress bar
  │     └─ runnerDoneMsg → record to history, show notification
  └─ View() → returns string (rendered with lipgloss)
```

### State model

The `model` struct holds all state for all views simultaneously. The `view` field determines which view renders. This avoids complex navigation stacks - switching views just changes the `view` field and triggers a load Cmd for the new view's data.

### Async operations

Long-running operations (scan previews, session loading, downloads) run in goroutines and communicate results back to the TUI via `tea.Cmd` functions that send typed messages:

```go
// Example: debounced scan preview
func (m *model) startPreview(url string) tea.Cmd {
    return func() tea.Msg {
        ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
        defer cancel()
        manifest, err := dispatcher.Scan(ctx, url, opts)
        return scanPreviewMsg{url: url, count: len(manifest.Items), ...}
    }
}
```

### Key bindings

Full key binding reference is in [`docs/TUI.md`](TUI.md).

---

## Design Decisions

### Why yt-dlp + native clients + browser fallback?

No single tool handles everything well. yt-dlp is excellent for video platforms but can't handle JS-heavy paywalled sites. Twitter/X and Reddit have rate-limited, frequently-changing APIs that benefit from native GraphQL/JSON clients with dedicated rate limiters and query-ID refresh logic. Headless Chrome is slow but is the only reliable way to scrape JS-rendered playlists.

### Why Bubble Tea TUI instead of a web frontend?

A terminal UI keeps the tool zero-dependency (single binary, no server, no browser), works over SSH, and uses the same Go code as the CLI - no API layer needed.

### Why JSON persistence instead of a database?

The data volume is small (tens of sessions, hundreds of URLs each). JSON files are human-readable, version-controllable, syncable via cloud storage, and require zero setup or dependencies.

### Why uTLS fingerprinting?

Go's standard `crypto/tls` sends a distinctive ClientHello that some CDNs (Cloudflare, Akamai) use to block automated clients. The uTLS transport mimics Chrome's handshake, allowing provenance to download from sites that would otherwise return 403s.

### Why per-domain rate limits?

Twitter imposes aggressive rate limits on guest tokens (~150 requests/15min). Reddit similarly throttles unauthenticated access. Preemptive rate limiting avoids hammering APIs and reduces the chance of IP bans.

---

## Package Reference

### `config`
`Config` struct with all user-configurable download options. JSON-serializable for session/watch/history persistence.

### `dispatcher`
URL classification, routing, batch dispatch, browser fallback orchestration, URL archives, link cache persistence, and the `Options`/`Reporter` types that glue the system together.

### `extractor` 
Five extractors: `ytdlp.go`, `twitter.go`, `reddit.go`, `instagram.go`, `browser.go`, plus `helpers.go`.

### `downloader`
Resumable HTTP client with uTLS transport (`utls_transport.go`). Computes SHA-256 hash during download, stored in `Client.LastHash` and `Client.LastPath` on success.

### `session`
Named resumable session persistence with optimistic concurrency, atomic writes, and `Reporter` interface implementation.

### `watch`
Recurring watch subscription persistence.

### `history`
Download history persistence (last 25 runs).

### `manifest`
Scan result types (`Manifest`, `Item`, `Summary`), filter options, size/CSV parsing, human-readable display. Used internally for discovery, filtering, and TUI display. Also provides `CaptureManifest` and `CaptureItem` types for post-download provenance records with SHA-256 verification, plus `.provenance/` filesystem I/O.

### `resolve`
Shared result types (`Source`, `Item`, `MediaAsset`, `TextContent`) that all extractors map into via per-platform adapter functions (`YtdlpInfoToSource`, `TwTweetToItem`, `RdPostToItem`, `IgPostToItem`). Used by `scan --json` to emit platform-normalized JSON with preserved raw metadata. Standardized error types (`Error`, `ErrorCategory`). This is the type layer future collection and archive workflows will consume.

### `archive`
Domain model for the opt-in durable vault: `ArchiveCollection`, `Source`, `Revision`, `Entity`, `Artifact`, `Document`, `Relation`. `IngestCaptureManifest` consumes capture manifests, verifies SHA-256, copies files to the content-addressed blob store, and builds immutable revision records.

### `blobstore`
SHA-256 content-addressed filesystem storage. Files stored as `blobs/sha256/<first-2-hex>/<full-hex>`. Deduplication via hash collision  -  identical content is stored only once. Atomic writes via `.tmp` → rename.

### `catalog`
Persistence for archive collections. `CatalogStore` interface with JSON (filesystem) and PostgreSQL (`PgStore`) adapters. PostgreSQL adapter provides transactional revision inserts and GIN-indexed `tsvector` full-text search via `plainto_tsquery`/`ts_rank`/`ts_headline`. Schema managed by embedded SQL migration. Global store set via `SetStore()` and accessed via `Store()`.

### `citation`
Stable archive citations with multi-format output. `Reference` struct with `Markdown()`, `Plain()`, `ToJSON()` formatters, `FromEntity()` constructor. URI format: `provenance://<collection>@<revision>#<entity>`. Includes title, author, URL, capture date, and SHA-256 in formatted output.

### `collection`
### `worker`
Bounded goroutine pool with 3-retry exponential backoff and permanent-error semantics.

### `ratelimit`
Per-domain rate limiter factory.

### `diagnose`
Pattern-matches error strings to produce actionable hints.

### `tui` 
Bubble Tea terminal UI across 10 files. Full details in [`docs/TUI.md`](TUI.md).
