# Contributing

## Prerequisites

- **Go 1.26** or later
- **Chrome/Chromium** (for browser extractor tests - not required for unit tests)
- **yt-dlp** auto-installed on first build (no manual setup needed)

## Build

```bash
go build -o provenance ./cmd/provenance
```

To install globally:

```bash
go install ./cmd/provenance
```

## Test

```bash
go test ./internal/...
```

### Test files

| File | What it tests |
|------|---------------|
| `internal/session/store_test.go` | Session lifecycle: create, add URLs, status transitions, reset running, load, counts, invalid names |
| `internal/watch/store_test.go` | Watch store: add, list, get, mark run, remove |
| `internal/manifest/manifest_test.go` | ParseSize (various units), FilterItems (include/exclude/size/title), Summary |
| `internal/history/store_test.go` | Add/list/trim, normalization and file total calculation, delete and clear |
| `internal/diagnose/diagnose_test.go` | Hint matching for 403, 429, disk full, unsupported URL |
| `internal/extractor/ytdlp_test.go` | Progress writer: parsing marked JSON, forwarding stderr, finishAll, flexibleInt64, metadata path args |

Tests use `t.Setenv()` to isolate filesystem state. No external services are required.

### Coverage

```
go test -cover ./internal/...
```

## Code conventions

### Package structure

- `cmd/provenance/` - single `main` package, cobra CLI entry point
- `internal/` - all application packages (not importable from outside this module)
  - One concern per package
  - Package name = directory name
  - Test files in same package

### Naming

- Exported types: `PascalCase` (`Snapshot`, `HTTPClient`)
- Exported functions: `PascalCase` (`ParseURL`, `NewPool`)
- Unexported: `camelCase` (`parseURL`, `normalizeHost`)
- Interface names: `-er` suffix for single-method interfaces (`Reporter`, `ProgressReporter`)

### No comments

The codebase favors self-documenting code. Package-level doc comments describe exported types and functions. Inline comments are used sparingly - only where the intent isn't obvious from the code itself.

### Dependencies

- Add dependencies sparingly - the tool compiles to a single binary
- Prefer the standard library where possible
- External deps: cobra (CLI), Bubble Tea (TUI), lipgloss (styling), chromedp (browser), go-ytdlp, uTLS, progressbar, beeep, `golang.org/x/time/rate`

## Adding a new extractor

1. Create `internal/extractor/newsite.go`
2. Implement:
   ```go
   func DownloadSite(ctx context.Context, rawURL, outputDir, cookiesFile string, opts SiteOptions, dryRun bool) error
   func ScanSite(ctx context.Context, rawURL, outputDir, cookiesFile string, opts SiteOptions) (manifest.Manifest, error)
   func ScanSiteResolved(ctx context.Context, rawURL, outputDir, cookiesFile string, opts SiteOptions) (resolve.Source, error)
   func SiteToItem(nativeType) resolve.Item
   ```
3. Add site constant in `internal/dispatcher/dispatcher.go`:
   ```go
   const SiteNew Site = iota + 1  // (must be unique, after existing values)
   func (s Site) String() string { ... }
   ```
4. Add hostname matching in `Classify()`:
   ```go
   case strings.Contains(host, "newsite.com"):
       return SiteNew, nil
   ```
5. Add `case` arms in `Dispatch()` and `Scan()`:
   ```go
   case SiteNew:
       return extractor.DownloadSite(ctx, rawURL, outputDir, cookiesFile, opts, dryRun)
   ```
6. Add rate limits in `internal/ratelimit/ratelimit.go`:
   ```go
   case strings.Contains(host, "newsite"):
       return rate.Limit(2), 2
   ```
7. If the site uses a worker pool for parallel media downloads, follow the pattern in `twitter.go` or `reddit.go` (submit media URLs to `worker.Pool`, use `downloader.Client` for HTTP downloads).

### Extractor patterns

- **Scan returns manifest before downloading**: Build `manifest.Item` list, apply filters, return for preview. Same items are then downloaded.
- **Worker pool for media**: Individual media files use `worker.Pool` with `SubmitWithHooks` for lifecycle callbacks.
- **Rate limiter**: Call `rl.GetLimiter(host).Wait(ctx)` before each API request.
- **Progress reporting**: Pass `opts.Progress` to `downloader.Client.Progress` for per-file progress bars.
- **Dry run**: Check `dryRun` flag and skip `client.Download()` calls, but still run the API calls and print what would happen.

## Lint

```bash
go vet ./...
gofmt -d .    # check formatting
gofmt -w .    # fix formatting
```

## Commit style

Follow existing convention:
- Prefix with package/feature: `twitter: fix ...`, `tui: add ...`, `reddit: support ...`
- Lowercase first word after colon
- No trailing period

Examples from history:
```
add --include-posts flag to download tweet/reddit post text as markdown
reddit: route external videos through yt-dlp
fix: index out of range in viewNewDownload when advanced options visible
tui: add advanced options, watch add, session export, cookie file picker
refactor: architecture overhaul
```

## Project layout conventions

- `cmd/` - one directory per binary, each with `main.go`
- `internal/` - application code not for external import
- `internal/app/` - top-level coordination (thin, delegates to dispatcher)
- `internal/config/` - shared, serializable configuration types
- `internal/dispatcher/` - URL routing, batch orchestration, archives
- `internal/extractor/` - site-specific download and scan implementations
- `internal/downloader/` - HTTP-level operations
- `internal/tui/` - terminal UI (no business logic, only presentation)
- `internal/worker/` - generic concurrency primitives
- `internal/manifest/` - data types shared between scan and download
- `internal/session/`, `internal/watch/`, `internal/history/` - persistence
- `internal/ratelimit/`, `internal/diagnose/` - cross-cutting utilities
- `docs/` - documentation
