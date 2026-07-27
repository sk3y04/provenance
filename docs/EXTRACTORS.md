# Extractors

Deep reference for each of provenance's five media extractors: how they fetch metadata, extract media URLs, handle authentication, rate limiting, and error recovery.

---

## yt-dlp Extractor (`internal/extractor/ytdlp.go`)

The primary engine for video platforms and generic sites. Wraps the `yt-dlp` binary via `github.com/lrstanley/go-ytdlp`.

### Capabilities

- Downloads from thousands of sites (anything yt-dlp supports)
- Quality selection with ffmpeg-aware fallback
- Audio-only extraction (MP3 320kbps)
- Output layout presets (creator, site, flat, date)
- Advanced output template support
- Download archives for deduplication
- Metadata sidecars (`*.info.json`, `*.description`, thumbnails, subtitles) written to `_metadata/` subdirectory
- Speed limiting via `--limit-rate`

### Installation

`EnsureInstalled()` calls `ytdlp.MustInstall(ctx, nil)` which downloads yt-dlp and ffmpeg into the local cache (`~/.cache/go-ytdlp` on Linux/macOS, `%LOCALAPPDATA%\go-ytdlp` on Windows). Called automatically on first `grab`/`scan`.

### Quality selectors

The `formatSelector(quality, ffmpeg)` function builds format strings:

| Quality | ffmpeg present | ffmpeg missing |
|---------|---------------|----------------|
| `best` | `bestvideo+bestaudio/best` | `best[ext=mp4]/best` |
| `1080` | `bestvideo[height<=1080]+bestaudio/best[height<=1080]` | `best[height<=1080][ext=mp4]/best[height<=1080]/best` |
| `720` | Same, ≤720 | Same |
| `480` | Same, ≤480 | Same |

When ffmpeg is present: merge video+audio to mp4, embed thumbnail. Without ffmpeg: select pre-muxed mp4 only, warn once.

### Audio-only mode

Sets `--extract-audio --audio-format mp3 --audio-quality 0`. Returns an error and refuses to run if ffmpeg is not installed.

### Progress protocol

yt-dlp is configured with `--progress-template "download:PROVENANCE_YTDLP_PROGRESS:%(progress)j"`. This writes JSON progress lines to stdout. A `ytdlpProgressWriter` intercepts these lines:

1. Lines containing `PROVENANCE_YTDLP_PROGRESS:` are parsed as JSON
2. The progress payload is forwarded to `downloader.ProgressReporter` (for TUI progress bars)
3. Non-progress lines are forwarded to stdout/stderr and optionally captured for fallback detection
4. Per-file events are throttled to 250ms for TUI smoothness
5. On completion or error, `finishAll()` marks all active downloads as done

### Progress JSON format

```json
{
  "status": "downloading|finished|error",
  "filename": "/path/to/output.mp4",
  "tmpfilename": "/path/to/output.mp4.part",
  "downloaded_bytes": 12345678,
  "total_bytes": 987654321,
  "total_bytes_estimate": 987654321
}
```

The `downloaded_bytes` and `total_bytes` fields use `flexibleInt64` which handles numeric, string, and null values gracefully.

### Output template rendering

`renderYtdlpOutputTemplate(outDir, opts)`:

1. If `OutputTemplate` is set (absolute) → use as-is
2. If `OutputTemplate` is set (relative) → `filepath.Join(outDir, filepath.FromSlash(tmpl))`
3. If `OutputLayout` is set:

   | Layout | yt-dlp template |
   |--------|----------------|
   | `creator` (default) | `%(extractor)s/%(uploader)s/%(title)s.%(ext)s` |
   | `site` | `%(extractor)s/%(title)s.%(ext)s` |
   | `flat` | `%(title)s.%(ext)s` |
   | `date` | `%(upload_date>%Y-%m)s/%(title)s.%(ext)s` |

4. Join with `OutputDir`

### Metadata sidecars

yt-dlp sidecar files (`*.info.json`, `*.description`, thumbnails, subtitles) are written to an `_metadata/` subdirectory using `--paths TYPE:<dir>` flags. This keeps the media directory clean.

Types handled: `infojson`, `description`, `thumbnail`, `subtitle`, `annotation`, `pl_thumbnail`, `pl_description`, `pl_infojson`.

Set `MetadataDir` to `"."` to disable (sidecars next to media files).

### Scan mode

`ScanYtdlp()` runs yt-dlp with `--simulate --print-json`. Each output line is parsed as `ytdlpInfo`:

```go
type ytdlpInfo struct {
    ID, Title, WebpageURL, OriginalURL, URL string
    Extractor, Uploader, Ext string
    Filesize, FilesizeAlt int64
    UploadDate string
    Duration float64
}
```

Items are assembled into `manifest.Item` structs. If yt-dlp produces no items (unsupported site), a placeholder item is created with the source URL.

---

## Twitter/X Extractor (`internal/extractor/twitter.go`)

Native X.com downloader using the GraphQL API.

### Entry points

```go
func DownloadTwitter(ctx, rawURL, outputDir, cookiesFile string, opts TwOptions, dryRun bool) error
func ScanTwitter(ctx, rawURL, outputDir, cookiesFile string, opts TwOptions) (manifest.Manifest, error)
func FetchAllTwTweets(ctx, rawURL, cookiesFile string, opts TwOptions) ([]twTweet, error)
```

### API architecture

Three GraphQL operations:

| Operation | Query ID env var | Hardcoded default | Purpose |
|-----------|-----------------|-------------------|---------|
| `UserByScreenName` | `TWITTER_QUERY_USER_BY_SCREEN_NAME` | `2qvSHpkWTMS9i0zJAwDNiA` | Resolve username → user ID |
| `UserTweets` | `TWITTER_QUERY_USER_TWEETS` | `6r5OLCC_wFH4CpRyXKuAmQ` | Fetch tweet timeline with media |
| `UserMedia` | `TWITTER_QUERY_USER_MEDIA` | `IS3w9vvPg1SJysLErvnFGg` | Reserved for media-only endpoint |

### Authentication

- `auth_token` cookie from `cookies.txt` is required for API access
- `ct0` cookie becomes the `x-csrf-token` header
- Bearer token from `TWITTER_BEARER_TOKEN` env var, runtime-refreshed guest token, or built-in public fallback
- Headers: `User-Agent` (Chrome 131), `Authorization` (Bearer), `x-csrf-token`, `Cookie`

### Query ID lifecycle

1. First request: use env var or hardcoded default query ID
2. If the response has status 404 or 400 with "query" in the body → query ID is stale
3. `twInvalidateQueryIDs()` clears the cache
4. `twRefreshQueryIDsForce()`:
   - Fetches `https://x.com/home` and extracts the web client JS URL from the HTML
   - Fetches the JS file and regex-extracts query IDs for `UserByScreenName`, `UserMedia`, `UserTweets`
   - Uses `operationName:"<Name>"` and `queryId:"<ID>"` patterns
5. Retries with fresh IDs (up to 2 refresh cycles)

### Data flow (download)

```
ParseTwURL(rawURL) → extract username
  │
  ▼
twClient(cookiesFile) → HTTP client + csrfToken
  │
  ▼
resolveTwUserID() → UserByScreenName query
  │
  ▼
fetchTwMediaPage(cursor) → UserTweets query
  │  loop with cursor pagination (20 tweets/page)
  │  stop when cursor is empty or limit reached
  ▼
twTweetResult[] → extract media from legacy.extended_entities.media[]
  │
  ├─ type==photo → fetch https://{url}?format=jpg&name=orig
  ├─ type==video → select highest-bitrate MP4 variant
  │                from video_info.variants[]
  ├─ type==animated_gif → MP4 variant
  ▼
Worker pool → download each media file
  │
  ▼
(optional) Write post markdown → {output}/twitter/{user}/posts/{id}.md
```

### Media URL extraction

```go
type twMedia struct {
    MediaURLHTTPS string       // base URL for photos
    Type          string       // "photo", "video", "animated_gif"
    VideoInfo     *twVideoInfo // video variants
}

type twVideoInfo struct {
    Variants []twVideoVariant
}

type twVideoVariant struct {
    Bitrate     int64
    ContentType string   // "video/mp4" or "application/x-mpegURL"
    URL         string
}
```

Photos: append `?format=jpg&name=orig` to `MediaURLHTTPS`.
Videos: filter to `ContentType == "video/mp4"`, sort by bitrate descending, pick highest.

### Rate limiting

- Preemptive: `ratelimit.Manager` limits to 2 req/s for `x.com` and `twitter.com`
- Reactive: 429 responses read `Retry-After` header and sleep accordingly
- Max retries: 4 per request with exponential backoff (2s → 4s → 8s → 16s)

### Error recovery

- Network errors: retry with backoff, up to 4 attempts
- Stale query IDs: invalidate cache, refresh from web client JS, retry
- 429 rate limits: sleep for `Retry-After` duration or max 15s
- 400/500 client errors: retry with backoff, eventual permanent failure

### Output layout

```
{outputDir}/twitter/{username}/
├── images/
│   ├── {tweet_id}_img_{n}.jpg
│   └── ...
├── videos/
│   ├── {tweet_id}_vid_0.mp4
│   └── ...
└── posts/                       ← only with --include-posts
    └── {tweet_id}.md
```

---

## Reddit Extractor (`internal/extractor/reddit.go`)

Native Reddit downloader using the JSON API.

### Entry points

```go
func DownloadReddit(ctx, rawURL, outputDir, cookiesFile string, opts RdOptions, dryRun bool) error
func ScanReddit(ctx, rawURL, outputDir, cookiesFile string, opts RdOptions) (manifest.Manifest, error)
func FetchAllRdPosts(ctx, client, cookiesFile, target, rl) ([]rdPost, error)
```

### URL parsing

`ParseRdURL()` supports two formats:

| Format | Example | Result |
|--------|---------|--------|
| User profile | `reddit.com/user/username/submitted` | `RdTarget{Name: "username", IsSubreddit: false}` |
| Subreddit | `reddit.com/r/subredditname` | `RdTarget{Name: "subredditname", IsSubreddit: true}` |

API paths:
- User: `/user/{name}/submitted.json?limit=100&raw_json=1`
- Subreddit: `/r/{name}/new.json?limit=100&raw_json=1`

### Authentication

- API base: `old.reddit.com` (more permissive JSON API than `www.reddit.com`)
- Cookie-based: `Cookie` header from `cookies.txt`
- OAuth2 Basic: `REDDIT_CLIENT_ID` + `REDDIT_CLIENT_SECRET` → `Authorization: Basic base64(id:secret)`
- OAuth token: `REDDIT_OAUTH_TOKEN` → `Authorization: <token>` (takes precedence)
- User-Agent: Chrome 131
- Headers: `Sec-Fetch-Dest`, `Sec-Fetch-Mode`, `Sec-Fetch-Site`, `Accept-Language`, `Referer`

### Pagination

Cursor-based via `after` field. The response includes an `after` token; if empty or unchanged, pagination stops.

### Media type detection

`rdPostMedia(post)` classifies each post into one of several categories:

| Category | Detect by | Handling |
|----------|-----------|----------|
| **Gallery** | `gallery_data` + `media_metadata` present | Download each item at highest resolution (prefer `.gif` over `.u`) |
| **Reddit video** | `is_video==true` and `media.reddit_video` present | Route post permalink through yt-dlp |
| **Direct image** | URL ends with image extension (`.jpg`, `.png`, `.gif`, `.webp`, `.bmp`) | Download directly via `downloader.Client` |
| **Imgur link** | URL contains `imgur.com` but no image extension | Append `.jpg`, `.png`, `.mp4` extensions |
| **External video** | URL from `youtube.com`, `youtu.be`, `vimeo.com`, `streamable.com` | Route through yt-dlp |
| **Direct URL** | Any other `http(s)` URL ending with image extension | Download directly |

### Data flow (download)

```
ParseRdURL(rawURL) → RdTarget
  │
  ▼
fetchRdPage(after) → Reddit JSON API
  │  loop with cursor pagination (100 posts/page)
  ▼
rdPost[] → deduplicate (by post name)
  │
  ▼
For each post → rdPostMedia() → classify
  │
  ├─ Gallery → download each image directly
  ├─ Reddit video → yt-dlp(post_permalink)
  ├─ Direct image → downloader.Client.Download()
  ├─ External video → yt-dlp(url)
  └─ Crosspost parent → rdPostMedia(parent_post) recursively
  │
  ▼
Worker pool → parallel download
  │
  ▼
(optional) Write post markdown → {output}/reddit/{creator}/posts/{id}.md
```

### Rate limiting

- 1 req/s, burst 2
- 429 responses: sleep for `rdMaxRetryBackoff` (20s) and retry
- Max retries: 4 per request with exponential backoff (3s → 6s → 12s → 24s)

### Output layout

```
{outputDir}/reddit/{creator}/
├── {post_id}_{n}.{ext}        ← images/gallery
├── {post_id}_xpost_{n}.{ext}  ← crosspost media
├── {post_id}.mp4              ← external video (via yt-dlp)
└── posts/                      ← only with --include-posts
    └── {post_id}.md
```

Reddit-hosted `v.redd.it` videos are routed through yt-dlp (not downloaded directly) because they use DASH/HLS streaming.

---

## Instagram Extractor (`internal/extractor/instagram.go`)

Native Instagram downloader using the REST API v1.

### Entry points

```go
func DownloadInstagram(ctx, rawURL, outputDir, cookiesFile string, opts IgOptions, dryRun bool) error
func ScanInstagram(ctx, rawURL, outputDir, cookiesFile string, opts IgOptions) (manifest.Manifest, error)
func FetchAllIgPosts(ctx, client, cookiesFile, csrfToken, userID, limit, rl) ([]igFeedMedia, error)
```

### URL parsing

`ParseIgURL()` supports five URL formats:

| Format | Example | Result |
|--------|---------|--------|
| User profile | `instagram.com/username/` | `IgTarget{Type: "profile", Username: "username"}` |
| Single post | `instagram.com/p/SHORTCODE/` | `IgTarget{Type: "post", Shortcode: "SHORTCODE"}` |
| Single reel | `instagram.com/reel/SHORTCODE/` | `IgTarget{Type: "reel", Shortcode: "SHORTCODE"}` |
| Stories | `instagram.com/stories/USERNAME/` | `IgTarget{Type: "stories", Username: "USERNAME"}` |
| IGTV | `instagram.com/tv/SHORTCODE/` | `IgTarget{Type: "post", Shortcode: "SHORTCODE"}` |

Unsupported types (`/explore/`, `/share/`) return errors.

### API architecture

Uses Instagram's REST API v1 (`i.instagram.com/api/v1/`):

| Endpoint | Purpose |
|----------|---------|
| `/users/web_profile_info/?username=X` | Resolve username → numeric user ID |
| `/feed/user/{user_id}/?count=12&max_id=CURSOR` | Paginate user's posts |
| `/feed/reels_media/?reel_ids={user_id}` | Fetch user's stories |
| `/media/{media_id}/info/` | Get single post/reel metadata + media URLs |

No GraphQL query hash tracking required (unlike Twitter). The REST API is simpler and more stable.

**User ID resolution** employs a multi-method cascade: the primary `web_profile_info` API call (with `Referer` header and fallback to `id` field if `pk` is empty), followed by web page HTML scraping (`profilePage_` pattern, iOS deep link meta tags, `user_id` JSON blobs), the `?__a=1&__d=1` JSON endpoint, and finally the `users/search` endpoint as a last resort.

### Shortcode ↔ ID conversion

Instagram post IDs are base-64 numbers using a custom alphabet:

```
ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_
```

`igShortcodeToID(shortcode)` converts a shortcode like `"Chunk8-jurw"` to its numeric media ID. Media IDs longer than 28 characters have the tail trimmed before conversion (the trailer encodes additional metadata).

### Authentication

- `sessionid` cookie from `cookies.txt` is **required** for all API access
- `csrftoken` cookie becomes the `X-CSRFToken` header
- `X-IG-App-ID` header set to `936619743392459` (web app ID, overridable via `INSTAGRAM_APP_ID` env var)
- Additional headers: `X-ASBD-ID: 359341`, `X-IG-WWW-Claim: 0`, `Accept: */*`
- User-Agent: Chrome 131

If `cookiesFile` is empty, the extractor returns an error: `"cookies file is required for Instagram"`.

### Media type detection

Posts have a `media_type` field:

| Type | Value | Handling |
|------|-------|----------|
| Image | 1 | Highest-resolution candidate from `image_versions2.candidates[]` |
| Video | 2 | Highest-resolution variant from `video_versions[]` (sorted by height, then width) |
| Carousel | 8 | Recurse into `carousel_media[]`, extract each slide individually |

### Data flow (download)

```
ParseIgURL(rawURL) → IgTarget (profile | stories | post | reel)
  │
  ├─ Profile URL:
  │   resolveIgUserID() → multi-method cascade (API → web scrape → JSON → search)
  │   FetchAllIgPosts() → feed/user/{id}/ with max_id cursor
  │
  ├─ Stories URL:
  │   resolveIgUserID() → multi-method cascade
  │   fetchIgStories() → feed/reels_media/?reel_ids={id}
  │
  └─ Post/Reel URL:
      fetchSingleIgPost() → media/{media_id}/info/
  │
  ▼
igFeedMedia[] → igPostItems() → manifest.Item[]
  │
  ├─ type==1 (image) → first candidate from image_versions2.candidates[]
  ├─ type==2 (video) → highest-resolution from video_versions[]
  ├─ type==8 (carousel) → igCarouselBestURLs() per slide
  ▼
Worker pool → download each media file
  │
  ▼
(optional) Write post markdown → {output}/instagram/{user}/posts/{id}.md
```

### Rate limiting

- Preemptive: `ratelimit.Manager` limits to 1 req/s, burst 2 for `instagram.com` and `cdninstagram.com`
- Reactive: 429 responses sleep for `Retry-After` duration or max 20s
- Max retries: 4 per request with exponential backoff (3s → 6s → 12s → 24s)
- Concurrency: 3 parallel downloads (lower than Twitter's 4 - Instagram is stricter)

### Error recovery

- Network errors: retry with backoff, up to 4 attempts
- 429 rate limits: sleep for `Retry-After` duration or max 20s
- 400/500 client errors: retry with backoff, eventual permanent failure
- Rate-limited mid-scan: returns partial results with a warning

### Output layout

```
{outputDir}/instagram/{username}/
├── images/
│   ├── {shortcode}.jpg
│   └── {shortcode}_carousel_{n}.jpg
├── videos/
│   ├── {shortcode}.mp4
│   └── {shortcode}_carousel_{n}.mp4
└── posts/                       ← only with --include-posts
    └── {shortcode}.md
```

Markdown posts include: username, full name, timestamp, caption text, and source link.

---

## Browser Extractor (`internal/extractor/browser.go`)

Headless Chrome automation via `chromedp` for JS-heavy sites yt-dlp can't handle.

### Entry points

```go
func SniffMediaURL(ctx, pageURL string, opts BrowserOptions) (string, error)
func SniffOrFallback(ctx, pageURL, cookiesFile string) (ResolvedTarget, error)
func ScrapePlaylistVideoLinks(ctx, pageURL, cookiesFile string, perPageTimeout time.Duration, maxPages int) ([]string, error)
func IsBrowserCandidate(pageURL string) bool
```

### SniffMediaURL - Media URL interception

1. Launch headless Chrome with `--headless`, `--disable-gpu` (add `--no-sandbox` via `BrowserOptions.NoSandbox` for containerized environments)
2. Enable `fetch.Enable()` to intercept all network requests
3. Listen for `fetch.EventRequestPaused` events
4. Check request URLs against `mediaURLPatterns`:
   - `.m3u8`, `.mpd` (streaming manifests)
   - `.mp4`, `.mkv` (video files)
   - `/hls/`, `/dash/`, `/stream/`, `/video/` (streaming paths)
5. When a matching URL is found, send it through a buffered channel
6. Inject cookies via `network.SetCookie` if `cookies_file` is provided
7. Navigate to the target page and wait for either:
   - A media URL is found → return immediately
   - Timeout (30s default) → return error
   - Page load completes with no media → return error

### ScrapePlaylistVideoLinks - Playlist crawling

Two-phase pagination support:

**Phase 1: URL-based pagination**

1. Navigate to the page
2. Extract all `<a href>` values via JS
3. Filter to links matching video path patterns: `/video`, `/videos/`, `/watch`, `/embed/`, `/v/`, `/player/`
4. Same-host validation (subdomain allowed)
5. Exclude the playlist URL itself
6. Follow discovered next-page links

**Phase 2: AJAX button pagination**

When URL-based pagination exhausts:

1. Click "Next" button via JS (`click()`)
2. Wait for the DOM to change (fingerprint visible video links)
3. Extract new links
4. Repeat until no new content or `maxPages` reached (default 500)

### Deduplication

Links are deduplicated across all pages. Query parameters and fragments are stripped for dedup comparison.

### Cookie injection

Browser cookies are loaded from `cookies.txt` (Netscape format) and injected via `network.SetCookie` before navigation. The `loadNetscapeCookies()` function is shared with Twitter and Reddit extractors.

### ResolvedTarget

```go
type ResolvedTarget struct {
    OriginalURL string   // the page URL
    MediaURL    string   // the sniffed media URL (for yt-dlp passthrough)
}
```

If media sniffing succeeds, the media URL is fed to yt-dlp. If it fails, the original page URL is used as a fallback.

### When it's used

The browser extractor is invoked by the dispatcher when:

1. yt-dlp returns an "unsupported URL" error, AND
2. `IsBrowserCandidate(url)` returns true (must be http/https), AND
3. `shouldBrowserFallback(err, stderr)` returns true

The fallback chain:
```
yt-dlp error → ScrapePlaylistVideoLinks() → if links found: download links via yt-dlp
             → SniffOrFallback()         → if media URL found: yt-dlp on that URL
             → return original error     → no fallback worked
```

---

## Shared Helpers (`internal/extractor/helpers.go`)

```go
func sanitizeFilename(name string) string    // replace / \ : * ? " < > | and newlines/tabs, truncate to 200 chars
func mkdirAll(p string) error                 // os.MkdirAll with 0o755
func writeFile(dest string, content []byte) error  // mkdirAll + os.WriteFile
func firstNonEmpty(values ...string) string
func firstFailedURL(m map[string]string) string
```

---

## Adding a New Extractor

To add support for a new site:

1. Create `internal/extractor/newsite.go`
2. Implement `DownloadSite(ctx, url, outputDir, cookiesFile, opts) error` and `ScanSite(ctx, url, outputDir, cookiesFile, opts) (manifest.Manifest, error)`
3. Add a `SiteNew` constant in `dispatcher/dispatcher.go`
4. Add hostname matching in `Classify()`
5. Add a `case` in `Dispatch()` and `Scan()`
6. Add rate limits in `ratelimit/ratelimit.go`
7. Register the site's hostname for auto-install skip if needed in `cmd/provenance/main.go`

See existing extractors for patterns around worker pools, rate limiters, progress reporting, and manifest building.
