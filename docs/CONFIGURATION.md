# Configuration Reference

Every `config.Config` field, its corresponding CLI flag, environment variable, and semantics.

## Config struct

```go
type Config struct {
    OutputDir          string                     // Base output directory
    CookiesFile        string                     // Netscape cookies.txt path
    Concurrency        int                        // Max parallel downloads
    Quality            string                     // Video quality selector
    AudioOnly          bool                       // Extract audio only
    NoArchive          bool                       // Disable URL + yt-dlp archives
    Filter             manifest.FilterOptions     // Include/exclude/size/title filters
    PostLimit          int                        // Twitter/Reddit/Instagram post limit
    IncludePosts       bool                       // Download post markdown
    CookiesFromBrowser string                     // Browser name for yt-dlp cookie extraction
    OutputLayout       string                     // Output layout preset
    OutputTemplate     string                     // Advanced yt-dlp output template
    SpeedLimit         int64                      // Bytes per second limit
}
```

This struct is serialized as JSON in session files, watch subscriptions, and history records.

---

## Field Reference

### `OutputDir`

| Property | Value |
|----------|-------|
| CLI flag | `-o`, `--output` |
| Default | `./downloads` |
| Env var | none |

Base output directory for all downloaded files. Created if it doesn't exist. Each extractor organizes files into subdirectories:

| Source | Subdirectory |
|--------|-------------|
| yt-dlp (generic) | `{OutputDir}/{extractor}/{uploader}/` (or per layout preset) |
| Twitter | `{OutputDir}/twitter/{username}/images/`, `.../videos/`, `.../posts/` |
| Reddit | `{OutputDir}/reddit/{creator}/`, `.../posts/` |
| Instagram | `{OutputDir}/instagram/{username}/images/`, `.../videos/`, `.../posts/` |
| Browser crawl links | Per-URL via yt-dlp |

### `CookiesFile`

| Property | Value |
|----------|-------|
| CLI flag | `-c`, `--cookies` |
| Default | (empty) |
| Env var | none |

Path to a Netscape-format `cookies.txt` file. Used by:

- **yt-dlp**: passed via `--cookies`
- **Twitter extractor**: loaded directly, `auth_token` cookie required
- **Reddit extractor**: loaded directly (optional)
- **Browser extractor (chromedp)**: injected via `network.SetCookie`

The same file is shared across all extractors. See [`docs/COOKIES.md`](COOKIES.md).

### `Concurrency`

| Property | Value |
|----------|-------|
| CLI flag | `--concurrency` |
| Default | `4` |
| Env var | none |

Maximum parallel HTTP downloads. Applies to:

- yt-dlp worker pool in batch dispatch
- Twitter/Reddit internal media download worker pool
- Browser crawl link download worker pool

Individual URL dispatch is always sequential (each extractor handles its own parallelism).

### `Quality`

| Property | Value |
|----------|-------|
| CLI flag | `--quality` |
| Default | `best` |
| Env var | none |

Video quality selector for yt-dlp. Valid values: `best`, `1080`, `720`, `480`.

Behavior depends on ffmpeg availability:

| Quality | ffmpeg present | ffmpeg missing |
|---------|---------------|----------------|
| `best` | `bestvideo+bestaudio/best` → merge to mp4 | `best[ext=mp4]/best` (pre-muxed) |
| `1080` | `bestvideo[height<=1080]+bestaudio/best[height<=1080]` | same, ext=mp4 variant |
| `720` | Same, height ≤720 | Same |
| `480` | Same, height ≤480 | Same |

When ffmpeg is available, yt-dlp merges video+audio streams to mp4 and embeds thumbnails. Without ffmpeg, it selects pre-muxed mp4 files only. The Twitter/Reddit extractors use their native quality selection, not this field.

### `AudioOnly`

| Property | Value |
|----------|-------|
| CLI flag | `--audio-only` |
| Default | `false` |
| Env var | none |

When true, yt-dlp extracts audio only (`-x --audio-format mp3 --audio-quality 0`). Requires ffmpeg. Produces 320kbps MP3 files. Twitter/Reddit extractors are unaffected (they download media files as-is).

### `NoArchive`

| Property | Value |
|----------|-------|
| CLI flag | `--no-archive` |
| Default | `false` |
| Env var | none |

Disables both URL archives and yt-dlp's `--download-archive`. Without archives, every URL is re-attempted on every run (no skipping of previously successful downloads).

**URL archive**: `<output>/_provenance_cache/archive.txt` - tracks `ok:<url>` and `perm-fail:<url>` entries.

**yt-dlp archive**: `<output>/_provenance_cache/ytdlp_archive.txt` - passed as `--download-archive` to yt-dlp.

### `Filter` (manifest.FilterOptions)

| Field | CLI flag | Default | Description |
|-------|----------|---------|-------------|
| `IncludeExt` | `--include` | (empty) | Include only these extensions: `mp4,jpg,zip` |
| `ExcludeExt` | `--exclude` | (empty) | Exclude these extensions: `psd,zip` |
| `MinSize` | `--min-size` | `0` | Minimum known file size: `10MB` |
| `MaxSize` | `--max-size` | `0` | Maximum known file size: `2GB` |
| `TitleMatch` | `--title-match` | (empty) | Regex: include items matching title/filename/URL |
| `TitleReject` | `--title-exclude` | (empty) | Regex: exclude items matching title/filename/URL |

**Include/Exclude logic:**
1. If `IncludeExt` is set, items with extensions NOT in the set are dropped.
2. Items with extensions in `ExcludeExt` are dropped.
3. Items with known size below `MinSize` or above `MaxSize` are dropped.
4. Items whose title+filename+URL concatenation doesn't match `TitleMatch` regex are dropped.
5. Items whose title+filename+URL concatenation matches `TitleReject` regex are dropped.

**Size format:** Number with optional suffix: `B`, `KB`/`K`, `MB`/`M`, `GB`/`G`. Case-insensitive. Examples: `10MB` = 10485760, `1GB` = 1073741824.

### `PostLimit`

| Property | Value |
|----------|-------|
| CLI flag | `--limit` |
| Default | `0` (unlimited) |
| Env var | none |

Maximum number of posts to fetch from Twitter/X, Reddit, or Instagram. Applied after scanning, before downloading. Set to 0 for unlimited.

**Twitter**: API page size is 20 tweets. The extractor stops fetching when the limit is reached.  
**Reddit**: API page size is 100 posts. Same behavior.  
**Instagram**: API page size is ~33 posts. Same behavior.

### `IncludePosts`

| Property | Value |
|----------|-------|
| CLI flag | `--include-posts` |
| Default | `false` |
| Env var | none |

When true, saves a `.md` file for each tweet, Reddit post, or Instagram post alongside media files.

**Twitter posts:** Post text, author, timestamp, external links, source URL.  
**Reddit posts:** Title, subreddit, author, timestamp, self-text, link, source URL.  
**Instagram posts:** Username, full name, timestamp, caption, source URL.

Files are written to `{output}/(twitter|reddit|instagram)/{creator}/posts/{id}.md`.

### `CookiesFromBrowser`

| Property | Value |
|----------|-------|
| CLI flag | `--cookies-from-browser` |
| Default | (empty) |
| Env var | none |

Browser name for yt-dlp's built-in cookie extraction. Valid values: `chrome`, `edge`, `firefox`, `brave`, `opera`, `vivaldi`, `chromium`. Passed to yt-dlp as `--cookies-from-browser <name>`.

This is a **yt-dlp-only** feature. Native Twitter/Reddit extracts and the browser sniffer still use `--cookies` (Netscape file).

### `OutputLayout`

| Property | Value |
|----------|-------|
| CLI flag | `--layout` |
| Default | `creator` |
| Env var | none |

Output layout preset for yt-dlp downloads. Determines the directory structure.

| Value | yt-dlp template | Example output |
|-------|----------------|----------------|
| `creator` | `%(extractor)s/%(uploader)s/%(title)s.%(ext)s` | `youtube/ChannelName/Video Title.mp4` |
| `site` | `%(extractor)s/%(title)s.%(ext)s` | `youtube/Video Title.mp4` |
| `flat` | `%(title)s.%(ext)s` | `Video Title.mp4` |
| `date` | `%(upload_date>%Y-%m)s/%(title)s.%(ext)s` | `2025-07/Video Title.mp4` |

The final template is constructed as: `{OutputDir}/{layout-template}`.

Twitter, Reddit, and Instagram extractors ignore this field (they always use `{output}/{source}/{username}/`).

### `OutputTemplate`

| Property | Value |
|----------|-------|
| CLI flag | `--filename-template` |
| Default | (empty) |
| Env var | none |

Advanced yt-dlp output template. When set, overrides `--layout`. Supports all [yt-dlp output template variables](https://github.com/yt-dlp/yt-dlp#output-template).

If the template is an absolute path, `--output` is ignored. If relative, it's joined with `--output`.

Example: `--filename-template "%(extractor)s/%(uploader)s/%(upload_date>%Y-%m-%d)s - %(title)s.%(ext)s"`

### `SpeedLimit`

| Property | Value |
|----------|-------|
| CLI flag | `--speed-limit` |
| Default | `0` (unlimited) |
| Env var | none |

Download speed limit in bytes per second. Accepts human-readable format: `5MB`, `500KB`, `1G`.

Applied at two levels:
1. Passed to yt-dlp as `--limit-rate <bytes>`
2. Set on `downloader.Client.SpeedLimit` for native HTTP downloads (Twitter/Reddit media files)

---

## Environment Variables

### Session / Watch / History paths

| Variable | Default | Purpose |
|----------|---------|---------|
| `PROVENANCE_SESSION_DIR` | `~/.cache/provenance/sessions/` | Custom directory for session JSON files |
| `PROVENANCE_WATCH_FILE` | `~/.cache/provenance/watch.json` | Custom path for watch subscriptions JSON |
| `PROVENANCE_HISTORY_FILE` | `~/.cache/provenance/history.json` | Custom path for history JSON |
| `PROVENANCE_COLLECTION_FILE` | `~/.cache/provenance/collections.json` | Custom path for collection definitions JSON |
| `PROVENANCE_DATABASE_URL` |  -  | PostgreSQL connection string for vault/search (`postgres://user:pass@localhost/dbname`) |

### Twitter/X API

| Variable | Purpose |
|----------|---------|
| `TWITTER_BEARER_TOKEN` | Override the guest bearer token. Auto-refreshed from X.com web client JS at runtime. Accepts the full `Bearer <token>` string or just the token. |
| `TWITTER_QUERY_USER_BY_SCREEN_NAME` | Override `UserByScreenName` query ID |
| `TWITTER_QUERY_USER_MEDIA` | Override `UserMedia` query ID |
| `TWITTER_QUERY_USER_TWEETS` | Override `UserTweets` query ID |

Query IDs are normally auto-refreshed from X.com's web client JS. These env vars skip the refresh. The `TWITTER_BEARER_TOKEN` env var accepts the full `Bearer <token>` string or just the token itself.

### Reddit API

| Variable | Purpose |
|----------|---------|
| `REDDIT_CLIENT_ID` + `REDDIT_CLIENT_SECRET` | OAuth2 Basic auth for higher rate limits (10 req/min → 60 req/min) |
| `REDDIT_OAUTH_TOKEN` | Pre-fetched OAuth token. Takes precedence over client credentials. |
