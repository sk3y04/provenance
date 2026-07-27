# Treeview - File Reference

Every source and config file in the repository, with purpose, key exports, and internal dependencies.

---

## Root

| Path | Purpose |
|---|---|
| `go.mod` | Go module `github.com/sk3y04/provenance`, Go 1.26, 11 direct dependencies |
| `go.sum` | Go module checksums |
| `.gitignore` | Git ignore rules (binary, IDE files, `.cache`) |

---

## `cmd/provenance/main.go` - CLI Entry Point

**Package:** `main`

Builds the cobra command tree (`grab`, `scan`, `status`, `resume`, `retry-failed`, `sessions`, `watch`, `install`, `tui`, `completion`). Defines all CLI flags, builds `dispatcher.Options` from them, and handles signal-based cancellation (`SIGINT`, `SIGTERM`).

**Exports:**
- `func main()` - root cobra command setup

**Key functions:**
- `downloadCmd()`, `scanCmd()` - primary dispatch commands
- `sessionsCmd()`, `watchCmd()` - subcommand trees
- `buildOptions(dryRun) (dispatcher.Options, error)` - flag → config conversion
- `runSessionDownload(ctx, name, args, batchPath, opts)` - session-backed download
- `readBatchURLs(path)` - batch file parser
- `shouldSkipAutoInstall(cmd)` - skips yt-dlp auto-install for read-only commands

**Internal deps:** `app`, `config`, `diagnose`, `dispatcher`, `extractor`, `manifest`, `ratelimit`, `session`, `tui`, `watch`

---

## `internal/`

### `internal/app/app.go` - Application Coordination

**Package:** `app`

Top-level orchestration between CLI/TUI and the dispatcher.

**Exports:**
- `func Download(ctx, urls []string, batchPath string, opts dispatcher.Options) error` - dispatches URLs (batch uses parallel pool, individual URLs sequentially)
- `func Scan(ctx, urls []string, opts dispatcher.Options) ([]manifest.Manifest, error)` - discovers items without downloading
- `func ScanResolved(ctx, urls []string, opts dispatcher.Options) ([]resolve.Source, error)` - discovers items with resolved metadata and raw API payloads

**Internal deps:** `diagnose`, `dispatcher`, `manifest`, `resolve`

---

### `internal/config/config.go` - Configuration

**Package:** `config`

Serializable download configuration shared across dispatcher, session, watch, and history packages.

**Exports:**
- `type Config struct` - `OutputDir`, `CookiesFile`, `Concurrency`, `Quality`, `AudioOnly`, `NoArchive`, `Filter`, `PostLimit`, `IncludePosts`, `CookiesFromBrowser`, `OutputLayout`, `OutputTemplate`, `SpeedLimit`

**Internal deps:** `manifest`

---

### `internal/dispatcher/dispatcher.go` - URL Dispatch & Routing

**Package:** `dispatcher`

Classifies URLs by hostname and routes to the appropriate extractor. Handles batch dispatching, browser fallback, URL archives, and link cache persistence.

**Exports:**
- `type Options struct` - bundles `config.Config` with runtime fields (`DryRun`, `Reporter`, `RateLimiter`, `FileProgress`)
- `type Reporter interface` - `Queue`, `Start`, `Success`, `Failure`, `Skip` - URL lifecycle events
- `type Site int` - `SiteUnknown`, `SitePatreon`, `SiteTwitter`, `SiteReddit`, `SiteGeneric`
- `func Classify(rawURL string) (Site, error)` - hostname → Site
- `func Dispatch(ctx, rawURL string, opts Options) error` - main routing entry point
- `func Scan(ctx, rawURL string, opts Options) (manifest.Manifest, error)` - discovers items for TUI / human output
- `func ScanResolved(ctx, rawURL string, opts Options) (resolve.Source, error)` - discovers items with shared resolve types for JSON export and archive
- `func BatchDispatch(ctx, path string, opts Options) error` - parallel batch from file
- `type Counts struct` - atomic counters: `Discovered`, `Succeeded`, `Failed`, `Skipped`
- `type CountsReporter struct` - wraps a Reporter to track event counts via atomic increments
- `func NewCountsReporter(inner Reporter) *CountsReporter` - creates a counting reporter wrapper
- `func PrintSummary(c *Counts, outputDir, sessionName string)` - prints structured terminal summary after download

**Internal deps:** `config`, `downloader`, `extractor`, `manifest`, `ratelimit`, `resolve`, `worker`

---

### `internal/extractor/`

#### `internal/extractor/ytdlp.go` - yt-dlp Wrapper

**Package:** `extractor`

Go wrapper around `yt-dlp` via `github.com/lrstanley/go-ytdlp`. Handles auto-install, structured progress parsing, quality selectors, output layout presets, metadata sidecars, and speed limits.

**Exports:**
- `type YtdlpOptions struct` - `OutputDir`, `CookiesFile`, `CookiesFromBrowser`, `Quality`, `AudioOnly`, `DryRun`, `DownloadArchive`, `OutputLayout`, `OutputTemplate`, `SpeedLimit`, `Progress`
- `func EnsureInstalled(ctx) error` - auto-installs yt-dlp + ffmpeg
- `func RunYtdlp(ctx, url string, opts YtdlpOptions) error` - download via yt-dlp
- `func RunYtdlpCaptured(ctx, url string, opts YtdlpOptions) (stderr string, err error)` - download with captured stderr for fallback detection
- `func ScanYtdlp(ctx, url string, opts YtdlpOptions) (manifest.Manifest, error)` - scan without download
- `func ScanYtdlpResolved(ctx, url string, opts YtdlpOptions) (resolve.Source, error)` - scan with shared resolve types
- `func YtdlpInfoToSource(info ytdlpInfo, rawURL string) (resolve.Source, []resolve.Item)` - convert yt-dlp output to resolve types

**Internal deps:** `downloader`, `manifest`

**Progress protocol:** writes `PROVENANCE_YTDLP_PROGRESS:`-prefixed JSON lines to a pipe, parsed by `ProgressWriter`.

---

#### `internal/extractor/twitter.go` - Twitter/X GraphQL Client

**Package:** `extractor`

Native X.com downloader using the GraphQL API with dynamic query ID refresh. Supports photos (original quality), videos (highest bitrate MP4), animated GIFs, and optional post markdown export.

**Exports:**
- `type TwOptions struct` - `Filter`, `SpeedLimit`, `Progress`, `Limit`, `RateLimiter`, `IncludePosts`
- `func DownloadTwitter(ctx, rawURL, outputDir, cookiesFile string, opts TwOptions, dryRun bool) error`
- `func ScanTwitter(ctx, rawURL, outputDir, cookiesFile string, opts TwOptions) (manifest.Manifest, error)`
- `func ScanTwitterResolved(ctx, rawURL, outputDir, cookiesFile string, opts TwOptions) (resolve.Source, error)` - scan with shared resolve types
- `func TwTweetToItem(t twTweetResult, username string) resolve.Item` - convert tweet to resolve types
- `func FetchAllTwTweets(ctx, rawURL, cookiesFile string, twOpts TwOptions) ([]twTweet, error)`

**Environment:** `TWITTER_BEARER_TOKEN` (optional override), `TWITTER_QUERY_USER_BY_SCREEN_NAME`, `TWITTER_QUERY_USER_MEDIA`, `TWITTER_QUERY_USER_TWEETS`

**Query IDs:** auto-refreshed from X.com web client JS when queries return stale errors. Falls back to hardcoded defaults.

**Internal deps:** `downloader`, `manifest`, `ratelimit`, `worker`

---

#### `internal/extractor/reddit.go` - Reddit JSON API Client

**Package:** `extractor`

Native Reddit downloader using the JSON API. Handles gallery images, Reddit-hosted videos (routed through yt-dlp), Imgur links, YouTube embeds, and optional post markdown export.

**Exports:**
- `type RdOptions struct` - `Filter`, `SpeedLimit`, `Progress`, `RateLimiter`, `IncludePosts`, `IncludeComments`, `CommentLimit`
- `func DownloadReddit(ctx, rawURL, outputDir, cookiesFile string, opts RdOptions, dryRun bool) error`
- `func ScanReddit(ctx, rawURL, outputDir, cookiesFile string, opts RdOptions) (manifest.Manifest, error)`
- `func ScanRedditResolved(ctx, rawURL, outputDir, cookiesFile string, opts RdOptions) (resolve.Source, error)` - scan with shared resolve types
- `func FetchAllRdPosts(ctx, rawURL, cookiesFile string, opts RdOptions) ([]rdPost, error)`
- `func fetchRdComments(ctx, client, cookiesFile, permalink string, maxComments int, rl) ([]rdComment, error)` - fetch comment tree via `{permalink}.json`
- `type rdComment struct` - `ID`, `ParentID`, `Author`, `Body`, `CreatedAt`, `Depth`, `Replies` — bounded by `maxComments`

**Environment:** `REDDIT_CLIENT_ID` + `REDDIT_CLIENT_SECRET`, `REDDIT_OAUTH_TOKEN`

**Internal deps:** `downloader`, `manifest`, `ratelimit`, `worker`

---

#### `internal/extractor/browser.go` - Headless Chrome Extractor

**Package:** `extractor`

Headless Chrome automation via `chromedp`. Navigates to JS-heavy pages, intercepts network requests to sniff media URLs, and crawls paginated playlists by clicking "Next" buttons with DOM fingerprinting.

**Exports:**
- `type BrowserOptions struct` - `CookiesFile`, `Timeout`
- `func SniffMediaURL(ctx, pageURL string, opts BrowserOptions) (string, error)` - returns first media-like URL
- `func SniffOrFallback(ctx, pageURL, cookiesFile string) (ResolvedTarget, error)` - sniff or fall back to page URL
- `func ScrapePlaylistVideoLinks(ctx, pageURL, cookiesFile string, perPageTimeout time.Duration, maxPages int) ([]string, error)` - crawls paginated playlists
- `func IsBrowserCandidate(rawURL string) bool` - checks if page is suitable for browser extraction
- `type ResolvedTarget struct` - `OriginalURL`, `MediaURL`

**Internal deps:** none (pure chromedp)

---

#### `internal/extractor/helpers.go` - Shared Helpers

**Package:** `extractor`

Small utilities: `sanitizeFilename`, `writeFile`, `mkdirAll`, `firstNonEmpty`, `firstFailedURL`.

---

### `internal/downloader/`

#### `internal/downloader/downloader.go` - HTTP Downloader

**Package:** `downloader`

Resumable HTTP downloader with `.part` files, progress reporting, speed limiting, and per-host rate limiting. Replaces `http.DefaultTransport` with custom configuration and optional uTLS transport.

**Exports:**
- `type ProgressReporter interface` - `OnStart(url, dest string, total int64)`, `OnProgress(url string, written, total int64)`, `OnDone(url string, err error)`
- `type Client struct` - `HTTP *http.Client`, `SpeedLimit int64`, `Progress ProgressReporter`, `BrowserTLS bool`, `RateLimiter *ratelimit.Manager`, `LastHash string`, `LastPath string`
- `func (c *Client) Download(ctx, url, dest, referer string) error` - downloads with resume, progress, speed limit. Computes SHA-256 hash, stored in `c.LastHash` and `c.LastPath` on success.

**Internal deps:** `ratelimit`

---

#### `internal/downloader/utls_transport.go` - uTLS Transport

**Package:** `downloader`

Constructs an `http.RoundTripper` that mimics Chrome's TLS ClientHello fingerprint using `github.com/refraction-networking/utls`. Helps bypass CDNs that reject Go's default TLS handshake.

**Exports:**
- `func NewUTLSTransport() http.RoundTripper` - Chrome-fingerprinted transport

---

### `internal/ratelimit/ratelimit.go` - Per-Domain Rate Limiter

**Package:** `ratelimit`

Manages per-hostname `rate.Limiter` instances via `golang.org/x/time/rate`.

**Exports:**
- `type Manager struct` - thread-safe limiter factory
- `func New() *Manager`
- `func (m *Manager) GetLimiter(host string) *rate.Limiter` - returns or creates limiter for host

**Rate profiles:**
| Host | Rate | Burst |
|------|------|-------|
| x.com / twitter | 2/s | 2 |
| reddit.com | 1/s | 2 |
| default | 10/s | 10 |

---

### `internal/worker/pool.go` - Worker Pool

**Package:** `worker`

Bounded goroutine pool with retry and permanent-error semantics.

**Exports:**
- `type Job func() error` - unit of work
- `func Permanent(err error) error` - marks error as non-retryable
- `func IsPermanent(err error) bool`
- `type Pool struct`
- `func NewPool(ctx, concurrency int) *Pool` - creates pool, derives cancellable context
- `func (p *Pool) Submit(job Job)` - schedules a job
- `func (p *Pool) SubmitWithHooks(job Job, onSuccess func(), onFinalFailure func(error))` - with lifecycle callbacks
- `func (p *Pool) Wait()` - blocks until all jobs finish
- `func (p *Pool) Cancel()` - cancels all in-flight work

**Retry policy:** 3 attempts, exponential backoff (1s → 2s → 4s). `Permanent` errors skip retries.

---

### `internal/session/store.go` - Resumable Sessions

**Package:** `session`

Named resumable session persistence. JSON files stored at `PROVENANCE_SESSION_DIR` or `~/.cache/provenance/sessions/`. Implements optimistic concurrency control (version-based) with atomic writes (temp-file + rename).

**Exports:**
- `type Status string` - `StatusPending`, `StatusRunning`, `StatusSucceeded`, `StatusFailed`, `StatusSkipped`
- `type Entry struct` - `URL`, `Source`, `Status`, `Attempts`, `LastError`, `CreatedAt`, `UpdatedAt`
- `type Session struct` - `Name`, `CreatedAt`, `UpdatedAt`, `Version`, `Options`, `Entries`
- `type Counts struct` - `Pending`, `Running`, `Succeeded`, `Failed`, `Skipped`, `Total`
- `type Info struct` - `Name`, `Path`, `CreatedAt`, `UpdatedAt`, `Counts`
- `func List() ([]Info, error)` - list all sessions
- `func Load(name string) (*Session, error)` - load a session
- `func OpenOrCreate(name string, opts) (*Session, error)` - load or create
- `func Delete(name string) error`
- `func (s *Session) AddURLs(urls []string, source string) (int, error)`
- `func (s *Session) Queue(url, source string)` - implements `Reporter`
- `func (s *Session) Start(url string)` - implements `Reporter`
- `func (s *Session) Success(url string)` - implements `Reporter`
- `func (s *Session) Failure(url string, err error)` - implements `Reporter`
- `func (s *Session) Skip(url, reason string)` - implements `Reporter`
- `func (s *Session) ResetRunning() (int, error)` - reset stuck running entries
- `func (s *Session) Counts() Counts`
- `func (s *Session) EntriesByStatus(statuses ...Status) []Entry`
- `func (s *Session) URLsByStatus(statuses ...Status) []string`

**Concurrency safety:** All public methods acquire `s.mu`. `saveLocked()` checks on-disk version to prevent lost updates (`ErrConcurrentModification`). Atomic writes via `path.tmp` → `path` rename.

**Internal deps:** `config`

---

### `internal/manifest/manifest.go` - Scan Results & Filtering

**Package:** `manifest`

Types for scan manifests, filter options, size parsing, and human-readable display.

**Exports:**
- `type Item struct` - `ID`, `URL`, `Title`, `Filename`, `Extension`, `Size`, `Source`, `Creator`, `PostID`, `PublishedAt`, `Destination`, `Kind`
- `type Manifest struct` - `SourceURL`, `Site`, `ScannedAt`, `Items`
- `type Summary struct` - `Count`, `KnownSize`, `Extensions`
- `type FilterOptions struct` - `IncludeExt`, `ExcludeExt`, `MinSize`, `MaxSize`, `TitleMatch`, `TitleReject`
- `func New(sourceURL, site string, items []Item) Manifest`
- `func FilterItems(items []Item, opts FilterOptions) ([]Item, error)`
- `func ParseSize(s string) (int64, error)` - "10MB" → 10485760
- `func ParseCSV(s string) []string` - "mp4,jpg" → ["mp4", "jpg"]
- `func HumanSize(n int64) string` - 1073741824 → "1.0 GB"
- `func ExtOf(name string) string` - extract extension
- `func PrintHuman(w io.Writer, m Manifest)` - pretty-print scan results

#### `internal/manifest/capture.go` - Capture Manifests

**Exports:**
- `type CaptureManifest struct` — post-download record: `Format`, `SourceURL`, `Site`, `DownloadedAt`, `OutputDir`, `Tool`, `ToolVersion`, `Options`, `Items`
- `type CaptureItem struct` — per-item capture: `ExternalID`, `URL`, `Title`, `Author`, `PublishedAt`, `Kind`, `Extractor`, `DownloadedPath`, `ByteSize`, `Sha256`, `CapturedAt`, `RawMetadata`, `Text`, `SessionName`, `Status`, `Error`
- `type CaptureOptions struct` — serializable subset of `config.Config`
- `type TextCapture struct` — `Body`, `Format`
- `type VerificationResult struct` — `Path`, `Expected`, `Actual`, `OK`, `Missing`, `ByteSize`

#### `internal/manifest/provenance.go` - .provenance/ Filesystem

Writes and reads capture manifests to `<outputDir>/.provenance/`.

**Exports:**
- `func WriteCaptureManifest(dir, path string, cm *CaptureManifest) error`
- `func WriteCaptureItem(dir, externalID string, item *CaptureItem) error`
- `func WriteCollectionConfig(dir string, data []byte) error`
- `func ReadCaptureManifest(path string) (*CaptureManifest, error)`
- `func VerifyCaptureDir(outputDir string) ([]VerificationResult, error)` — checks all files against recorded SHA-256
- `func FileSha256(path string) (string, error)` — compute SHA-256 hash of a file
- `func ListRunManifests(outputDir string) ([]string, error)`
- `func RunPath(outputDir string) string` — timestamped run manifest path
- `func ItemPath(outputDir, externalID string) string` — per-item manifest path
- `func CollectionPath(outputDir string) string`

---

### `internal/resolve/` - Shared Result Types

#### `internal/resolve/resolve.go` - Domain Types

**Package:** `resolve`

Normalized types that every extractor maps into, preserving raw platform metadata for archive and collection workflows. Emitted by `provenance scan --json`.

**Exports:**
- `type SourceKind string` - `KindFeed`, `KindSingle`, `KindPlaylist`
- `type Source struct` - `URL`, `CanonicalURL`, `Kind`, `Extractor`, `Title`, `Author`, `Items`
- `type Item struct` - `ExternalID`, `URL`, `Title`, `Author`, `PublishedAt`, `Media`, `Text`, `RawMetadata`
- `type MediaAsset struct` - `URL`, `Filename`, `Extension`, `Size`, `Kind`
- `type MediaKind string` - `MediaImage`, `MediaVideo`, `MediaAudio`
- `type TextContent struct` - `Body`, `Format`
- `type TextFormat string` - `FormatPlain`, `FormatMarkdown`, `FormatHTML`

#### `internal/resolve/errors.go` - Standardized Errors

**Exports:**
- `type ErrorCategory string` - `ErrAuth`, `ErrRateLimit`, `ErrUnavailable`, `ErrUnsupported`, `ErrResolveFailure`, `ErrTransfer`
- `type Error struct` - `Category`, `Message`, `Cause` - categorised, machine-readable error

**Internal deps:** none

---

### `internal/importers/` - Technical Knowledge Importers

**Package:** `importers`

Imports PDF files, Git repositories, static documentation sites, and OpenAPI specifications into the archive vault. Each importer creates a `Revision` with `Entities`, `Artifacts`, `Documents`, and `Relations`.

#### `internal/importers/common.go` - Shared Helpers

**Exports:** `newRevision()`, `storeFile()`, `persistRevision()` — create revisions, store files in blobstore, persist to filesystem.

#### `internal/importers/pdf.go` - PDF Importer

**Exports:** `func ImportPDF(vaultRoot, pdfPath, collectionName string) (*archive.Revision, error)` — extracts text per page via `ledongthuc/pdf`, stores original PDF as blob, creates one `Document` per page linked via `RelContains` relations.

#### `internal/importers/git.go` - Git Importer

**Exports:** `func ImportGit(vaultRoot, repoURL, ref, collectionName string) (*archive.Revision, error)` — shallow clone, resolves tag→commit SHA, walks doc files (`.md`/`.adoc`/`.txt`/README/LICENSE/CHANGELOG), stores as markdown `Document`s.

#### `internal/importers/docs.go` - Docs Site Importer

**Exports:** `func ImportDocs(vaultRoot, siteURL, scope string, maxPages int, collectionName string) (*archive.Revision, error)` — sitemap discovery or link crawl, bounded by scope/maxPages, extracts headings+text, stores per-page `Document`s.

#### `internal/importers/openapi.go` - OpenAPI Importer

**Exports:** `func ImportOpenAPI(vaultRoot, specPath, collectionName string) (*archive.Revision, error)` — parses YAML spec, creates `Entity` with `Kind: "api_spec"`, generates `Document` per endpoint, stores original spec as artifact.

**Internal deps:** `archive`, `blobstore`, `github.com/ledongthuc/pdf`, `gopkg.in/yaml.v3`

#### `internal/importers/web.go` - Web Page Archiver

**Exports:** `func ImportWeb(vaultRoot, seedURL, scope string, maxPages int, screenshot bool, collectionName string) (*archive.Revision, error)` — headless Chrome crawl via `chromedp`. Navigates pages, extracts rendered HTML (`ArtifactHTML`), visible text (`Document`), and page titles. Multi-page link discovery via `querySelectorAll('a[href]')` bounded by `maxPages` and same-host+path scope. No Docker dependency.

**Internal deps:** `archive`, `blobstore`, `github.com/chromedp/chromedp`

---

### `internal/collection/` - Named Collection Sync

#### `internal/collection/store.go` - Collection Persistence

**Package:** `collection`

JSON persistence for named collection sources. Single file at `$PROVENANCE_COLLECTION_FILE` or `~/.cache/provenance/collections.json`. Follows the same atomic-write, safe-name, mutex-locking pattern as the watch store.

**Exports:**
- `type Collection struct` - `Name`, `URL`, `Site`, `Options`, `CreatedAt`, `UpdatedAt`, `LastSync`, `LastResult`, `SeenIDs`
- `type SyncResult struct` - `At`, `Total`, `New`, `Skipped`, `Failed`, `SessionName`
- `func Add(name, rawURL, site string, opts config.Config) error` - upsert
- `func List() ([]Collection, error)`
- `func Get(name string) (Collection, error)`
- `func Remove(name string) error`
- `func AddSeen(name string, ids ...string) error` - mark IDs as seen
- `func RecordSync(name string, result SyncResult) error` - record sync result

#### `internal/collection/sync.go` - Sync Logic

**Exports:**
- `type SyncOptions struct` - `DryRun`, `RateLimiter`
- `func Sync(ctx, name string, opts SyncOptions) (*SyncResult, error)` - incremental sync for one collection
- `func SyncAll(ctx, opts SyncOptions) error` - sync all collections

**Sync flow:** scan via `dispatcher.Scan` → extract item IDs from `manifest.Item.PostID` / `Item.ID` → filter against `SeenIDs` → create resumable session → download new items via `app.Download` → record results.

**Internal deps:** `app`, `config`, `dispatcher`, `manifest`, `session`, `ratelimit`

---

### `internal/blobstore/` - Content-Addressed Storage

#### `internal/blobstore/blobstore.go` - SHA-256 Blob Store

**Package:** `blobstore`

Filesystem-backed content-addressed storage. Files stored as `blobs/sha256/<first-2-hex>/<full-hex>`. Identical content (same SHA-256) is stored only once (deduplication). Atomic writes via `.tmp` → rename.

**Exports:**
- `type Store struct` - `Root`
- `func New(root string) *Store`
- `func (s *Store) Put(srcPath string) (hash string, error)` - store a file, returns hash. Returns `ErrExists` if already present.
- `func (s *Store) Get(hash string) (*os.File, error)` - retrieve a blob by hash
- `func (s *Store) Exists(hash string) bool`
- `func (s *Store) Remove(hash string) error`
- `func (s *Store) List() ([]string, error)` — enumerate all stored blob hashes

---

### `internal/archive/` - Archive Domain Model

#### `internal/archive/types.go` - Domain Types

**Package:** `archive`

Immutable vault domain model. Every archive operation creates a new `Revision`; no overwrites.

**Exports:**
- `type ArchiveCollection struct` - `Name`, `VaultRoot`, `Description`, `CreatedAt`, `UpdatedAt`
- `type Source struct` - `URL`, `Kind` (`url`/`collection`/`session`/`import`), `Reference`
- `type Revision struct` - `ID` (SHA-256), `CapturedAt`, `Source`, `Tool`, `ToolVersion`, `Entities`, `Artifacts`, `Documents`, `Relations`
- `type Entity struct` - `ExternalID`, `URL`, `CanonicalURL`, `Title`, `Author`, `PublishedAt`, `CapturedAt`, `Kind`, `Extractor`, `Text`, `Artifacts`, `Documents`, `Relations`, `RawMetadata`
- `type Artifact struct` - `Sha256`, `Path`, `Size`, `MimeType`, `Kind` (`image`/`video`/`audio`/`text`/`binary`)
- `type Document struct` - `ExternalID`, `Content`, `Format` (`plain`/`markdown`/`html`)
- `type Relation struct` - `From`, `To`, `Kind` (`contains`/`attached_to`/`reply_to`/`derived_from`/`belongs_to`)

#### `internal/archive/ingest.go` - Ingest Logic

**Exports:**
- `type IngestOptions struct` - `VaultRoot`, `CollectionName`, `Source`, `Tool`, `ToolVersion`
- `func IngestCaptureManifest(bs *blobstore.Store, opts IngestOptions, cm *manifest.CaptureManifest) (*Revision, error)` - verifies SHA-256, copies files to blob store, builds revision with entity/artifact/document records
- `func WriteRevision(vaultRoot string, rev *Revision) error` - writes manifest.json plus entities/artifacts/documents/relations as JSON Lines
- `func ReadRevision(vaultRoot, id string) (*Revision, error)`

**Internal deps:** `blobstore`, `manifest`

#### `internal/archive/diff.go` - Revision Diffing

**Exports:** `func DiffRevisions(old, new *Revision) *DiffResult` — matches entities by `ExternalID`, reports added/removed/changed entities with field-level diffs (title, author, kind, text size, artifacts). `type DiffResult` with `RevisionA`, `RevisionB`, `Added[]`, `Removed[]`, `Changed[]`, `Unchanged`.

---

### `internal/catalog/` - Archive Persistence

#### `internal/catalog/interface.go` - Store Interface

**Package:** `catalog`

Defines `CatalogStore` interface for archive persistence. Supports both filesystem (JSON) and PostgreSQL backends. Global store set via `SetStore()`.

**Exports:**
- `type CatalogStore interface` — `Init`, `Close`, `CollectionStore`, `RevisionStore`, `EntityStore`, `SearchStore`
- `type SearchOptions struct` — `CollectionName`, `Kind`, `Limit`, `Offset`
- `type SearchHit struct` — `Title`, `Headline`, `URL`, `CollectionName`, `RevisionID`, `EntityID`, `CapturedAt`, `Author`, `Kind`, `Rank`
- `type SearchResult struct` — `Total`, `Hits`
- `func SetStore(s CatalogStore)`, `func Store() CatalogStore`, `func HasStore() bool`

#### `internal/catalog/catalog.go` - JSON Adapter

Persists archive collections to `<vault>/collections.json`. Used as fallback when no PostgreSQL store is configured.

#### `internal/catalog/pg.go` - PostgreSQL Implementation

**Exports:**
- `func NewPgStore(ctx, connString string) (*PgStore, error)` — creates pool with connection string (`PROVENANCE_DATABASE_URL`)
- `PgStore` implements `CatalogStore` with transactional revision inserts, GIN-indexed `tsvector` full-text search via `plainto_tsquery`/`ts_rank`/`ts_headline`

#### `internal/catalog/migrations.go` - Schema

Embedded SQL migration creating `archive_collections`, `revisions`, `entities`, `documents`, `artifacts`, `relations` tables with GIN indexes and auto-update `tsvector` triggers.

**Internal deps:** `archive`, `github.com/jackc/pgx/v5`

#### `internal/catalog/gc.go` - Garbage Collection

**Exports:** `func GarbageCollect(vaultRoot string, dryRun bool) ([]string, error)` — finds orphaned blobs by diffing filesystem blob directory against revision manifest references (SHA-256 extraction). Safely removes unreferenced blobs.

#### `internal/catalog/retention.go` - Retention Policies

**Exports:** `func PruneRevisions(vaultRoot, collectionName string, keep int) ([]string, error)` — prunes old revisions keeping the last N (chronological), never deletes still-referenced blobs.

---

### `internal/citation/` - Stable References

**Exports:**
- `type Reference struct` — `Collection`, `RevisionID`, `EntityID`, `Title`, `URL`, `Author`, `CapturedAt`, `Sha256`
- `func Parse(s string) (*Reference, error)` — parse `provenance://col@rev#entity`
- `func (r *Reference) Markdown() string` — Markdown citation link
- `func (r *Reference) Plain() string` — multi-line plain text with title, URL, date, author, SHA-256
- `func (r *Reference) ToJSON() ([]byte, error)` — JSON marshalled reference
- `func FromEntity(collection, revisionID string, ent *archive.Entity) *Reference` — build reference from entity
- `func Short(revisionID, entityID string) string` — `<12-char-hex>@<entityID>`

---

### `internal/watch/store.go` - Watch Subscriptions

**Package:** `watch`

Persistent recurring download subscriptions. JSON file at `PROVENANCE_WATCH_FILE` or `~/.cache/provenance/watch.json`.

**Exports:**
- `type Subscription struct` - `Name`, `URL`, `Options`, `CreatedAt`, `UpdatedAt`, `LastRunAt`
- `func Add(name, rawURL string, opts config.Config) error`
- `func List() ([]Subscription, error)`
- `func Get(name string) (Subscription, error)`
- `func Remove(name string) error`
- `func MarkRun(name string) error` - updates `LastRunAt`

**Internal deps:** `config`

---

### `internal/history/store.go` - Run History

**Package:** `history`

Persistent download history. JSON file at `PROVENANCE_HISTORY_FILE` or `~/.cache/provenance/history.json`. Keeps last `DefaultLimit` (25) runs.

**Exports:**
- `const DefaultLimit = 25`
- `type File struct` - `URL`, `Path`, `Size`, `Success`, `Error`
- `type Run struct` - `ID`, `Title`, `URLs`, `Options`, `StartedAt`, `CompletedAt`, `Duration`, `Succeeded`, `Failed`, `Skipped`, `TotalBytes`, `Files`, `Error`
- `func List() ([]Run, error)`
- `func Get(id string) (Run, error)`
- `func Add(run Run) error`
- `func Delete(id string) error`
- `func Clear() error`

**Internal deps:** `config`

---

### `internal/diagnose/diagnose.go` - Failure Hints

**Package:** `diagnose`

Pattern-matches error strings to produce actionable hints for common failures.

**Exports:**
- `func Hint(err error) string` - returns hint or empty string

**Patterns matched:** 401/403 (auth), 429 (rate limit), ffmpeg missing, disk full, unsupported URL, 404/410 (deleted), DNS failure, network timeout.

---

### `internal/tui/` - Terminal UI

#### `internal/tui/tui.go` - TUI Core

**Package:** `tui`

Bubble Tea program entry point, model definition, init, and all TUI state types.

**Exports:**
- `func Run(ctx context.Context) error` - launches the TUI

**Type definitions:**
- `type model struct` - all TUI state (current view, cursor, loaded data, form state, scan state, runner state)
- `type view int` - `viewMain`, `viewSessions`, `viewSessionDetail`, `viewWatches`, `viewHistory`, `viewNewDownload`, `viewScanPick`, `viewRunner`
- `type downloadForm struct` - new-download form fields (5 basic + 15 advanced)
- `type scanPreview struct` - debounced scan results for live preview
- `type scanState struct` - scan & pick multi-select state
- `type runnerState struct` - live progress: counters, per-file progress bars, throughput/ETA, logs
- `type fileProgress struct` - per-file download progress
- 14 message types: `sessionsLoadedMsg`, `sessionLoadedMsg`, `watchesLoadedMsg`, `historyLoadedMsg`, `runnerEventMsg`, `runnerLogMsg`, `runnerDoneMsg`, `fileStartMsg`, `fileProgressMsg`, `fileDoneMsg`, `scanPreviewMsg`, `scanPreviewTick`, `cookiesFoundMsg`, `tickMsg`

**Internal deps:** `config`, `history`, `manifest`, `ratelimit`, `session`, `watch`

---

#### `internal/tui/update.go` - Message Dispatcher

**Package:** `tui`

Bubble Tea `Update` function - routes `tea.KeyMsg` and custom messages to view-specific handlers.

**Key functions:**
- `func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd)` - top-level message dispatcher
- `func (m *model) updateView(msg tea.KeyMsg) (tea.Model, tea.Cmd)` - routes to view-specific update functions

**Internal deps:** `diagnose`

---

#### `internal/tui/views.go` - View Rendering

**Package:** `tui`

Renders all 7 TUI screens using lipgloss styling. Each view renders a scrollable list/table with highlighted cursor, status indicators, and help footers.

**Styling constants:**
- `titleStyle`, `highlight`, `dim`, `errStyle`, `okStyle`, `infoStyle`, `warnStyle`, `cursorStyle`, `helpFooter`, `sectionHeader`, `bannerStyle`

**View functions:**
- `func (m *model) viewMain() string` - main menu with options + resume banner
- `func (m *model) viewSessions() string` - session list with filter
- `func (m *model) viewSessionDetail() string` - single session detail
- `func (m *model) viewWatches() string` - watch subscription list
- `func (m *model) viewHistory() string` - history list
- `func (m *model) viewNewDownload() string` - form + scan preview
- `func (m *model) viewScanPick() string` - multi-select item list
- `func (m *model) viewRunner() string` - live progress + logs

**Internal deps:** `history`, `session`

---

#### `internal/tui/mainmenu.go` - Main Menu

Main menu navigation and one-key resume (`R`).

**Internal deps:** none

---

#### `internal/tui/form.go` - New Grab Form

Form input definitions, update logic, scan preview commands, and form submission that builds `dispatcher.Options` and starts a runner.

**Internal deps:** `config`, `dispatcher`, `manifest`, `session`

---

#### `internal/tui/scanpick.go` - Scan & Pick

Multi-select item list view. Loads scan results, allows toggling items with `space`, generates `dispatcher.Options` and starts a runner for selected items.

**Internal deps:** `config`, `dispatcher`, `manifest`

---

#### `internal/tui/sessions.go` - Sessions Screen

Session list, detail view, resume/retry-failed/export/delete operations, fuzzy filter.

**Internal deps:** `dispatcher`, `session`

---

#### `internal/tui/watches.go` - Watches Screen

Watch subscription list, add form, run, double-press confirm to delete, fuzzy filter.

**Internal deps:** `config`, `dispatcher`, `session`, `watch`

---

#### `internal/tui/history.go` - History Screen

History list, rerun (rebuilds dispatcher.Options from saved config), reveal-in-finder, delete, fuzzy filter.

**Internal deps:** `dispatcher`, `history`

---

#### `internal/tui/runner.go` - Live Runner

Sets up the download context, captures stdout/stderr, streams events to the TUI via channel-to-msg bridge, and records completed runs to history.

**Key types:**
- `teaReporter` - implements `dispatcher.Reporter` by sending `runnerEventMsg` to TUI
- `fileReporter` - implements `downloader.ProgressReporter` by sending `fileStartMsg`/`fileProgressMsg`/`fileDoneMsg` to TUI

**Internal deps:** `dispatcher`, `history`, `session`

---

#### `internal/tui/helpers.go` - TUI Helpers

`trim()`, `humanBytes()`, `findCookieFiles()` - filesystem walk for `cookies.txt`.

**Internal deps:** none

---

## `docs/`

| File | Purpose |
|---|---|
| `docs/ARCHITECTURE.md` | Comprehensive architecture, data flow, design decisions, package reference |
| `docs/TUI.md` | Terminal UI - views, key bindings, message types, state model, rendering |
