# CLI Reference

Full command and flag reference for `provenance`.

## Command Tree

```
provenance
├── grab <URL...> [flags]            Grab media
├── collect                          Manage named collection syncs
│   ├── add <NAME> <URL> [flags]     Add or update a collection
│   ├── list                         List collections
│   ├── show <NAME>                  Show collection details
│   ├── sync [NAME]                  Sync one or all collections
│   └── remove <NAME>                Remove a collection
├── scan <URL...> [flags]            Preview items without downloading
├── status <SESSION>                 Show session progress
├── resume <SESSION>                 Resume pending + failed URLs
├── retry-failed <SESSION>           Retry only failed URLs
├── sessions                         Manage reservations
│   ├── list                         List all sessions
│   ├── export <SESSION> <FILE>      Export session to JSON file
│   ├── clean <SESSION>              Delete a session
│   └── failed <SESSION> [FILE]      Show or save failed URLs
├── watch                            Manage subscriptions
│   ├── add <NAME> <URL> [flags]     Add or update a watch
│   ├── list                         List watches
│   ├── run [NAME]                   Run one or all watches
│   └── remove <NAME>                Remove a watch
├── manifest                          Show and verify capture manifests
│   ├── show <PATH>                   Display a capture manifest
│   └── verify <DIR>                  Verify files against SHA-256 hashes
├── archive                            Preserve content in the durable vault
│   ├── url <URL>                      Archive a single URL
│   ├── collection <NAME>              Archive a collection's content
│   ├── session <NAME>                Archive a session's downloads
│   ├── import <DIR>                  Import a directory with capture manifests
│   ├── import-pdf <PATH>             Import a PDF into the vault
│   ├── import-git <URL>              Import a Git repository
│   ├── import-docs <URL>             Import a static documentation site
│   └── import-openapi <PATH>         Import an OpenAPI specification
│   └── import-web <URL>               Archive a web page via headless Chrome
├── vault                               Manage the durable vault
│   ├── init                            Initialize vault + PostgreSQL schema
│   ├── show <ID>                       Show revision details
│   ├── cite <ID>                       Generate citation for a revision
│   ├── diff <IDA> <IDB>                Diff two revisions
│   ├── gc                              Garbage-collect orphaned blobs
│   ├── retention prune                 Prune old revisions (--collection, --keep)
│   └── backup                          Backup the vault directory
├── search <QUERY>                      Full-text search across archived content
├── install                           Pre-install yt-dlp + ffmpeg
├── tui                              Launch interactive terminal UI
└── completion <bash|zsh|fish|powershell>
```

---

## `provenance grab <URL...> [flags]`

Download media from one or more URLs.

### URL treatment

- Individual positional URLs are dispatched **sequentially**.
- URLs from `--batch <file>` are dispatched **in parallel** (worker pool sized by `--concurrency`).
- Within batch, Patreon/Generic URLs use a yt-dlp worker pool. Twitter/Reddit URLs fall back to sequential dispatch (their extracts handle internal parallelism).

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-o`, `--output` | string | `./downloads` | Output directory (created if missing) |
| `-c`, `--cookies` | string | - | Path to Netscape `cookies.txt` file |
| `--cookies-from-browser` | string | - | Browser name for yt-dlp's built-in cookie extraction: `chrome`, `edge`, `firefox`, `brave`, `opera`, `vivaldi`, `chromium` |
| `--concurrency` | int | `4` | Max parallel HTTP downloads and yt-dlp workers |
| `--quality` | string | `best` | Video quality: `best`, `1080`, `720`, `480` |
| `--audio-only` | bool | `false` | Extract audio only. Outputs MP3 320kbps. Requires ffmpeg. |
| `--dry-run` | bool | `false` | Print what would be downloaded without actually downloading |
| `--session` | string | - | Save progress as a named resumable session. Incompatible with `--dry-run`. |
| `--no-archive` | bool | `false` | Disable all archives (URL archive + yt-dlp download archive). Re-attempt every URL. |
| `--batch` | string | - | Path to a text file with one URL per line. `#` lines are comments. |
| `--include` | string | - | Comma-separated extensions to include: `mp4,jpg,zip` |
| `--exclude` | string | - | Comma-separated extensions to exclude: `psd,zip,rar` |
| `--min-size` | string | - | Minimum known file size: `10MB`, `1GB` |
| `--max-size` | string | - | Maximum known file size: `2GB`, `500KB` |
| `--title-match` | string | - | Regex - only include items where title, filename, or URL matches |
| `--title-exclude` | string | - | Regex - exclude items where title, filename, or URL matches |
| `--limit` | int | `0` (unlimited) | Twitter/Reddit/Instagram: max number of posts to fetch |
| `--layout` | string | `creator` | Output layout preset (see below) |
| `--filename-template` | string | - | Advanced yt-dlp output template. Overrides `--layout`. Relative to `--output`. |
| `--speed-limit` | string | - | Limit download speed: `5MB`, `500KB`, `1M` |
| `--include-posts` | bool | `false` | Download post text, links, and metadata as markdown. Twitter/Reddit/Instagram. |
| `--include-comments` | bool | `false` | Download post comments/replies (Reddit) |
| `--max-comments` | int | `100` | Maximum number of comments to fetch per post |

### Output layout presets

| Value | yt-dlp template | Result |
|-------|----------------|--------|
| `creator` (default) | `%(extractor)s/%(uploader)s/%(title)s.%(ext)s` | `youtube/ChannelName/Video Title.mp4` |
| `site` | `%(extractor)s/%(title)s.%(ext)s` | `youtube/Video Title.mp4` |
| `flat` | `%(title)s.%(ext)s` | `Video Title.mp4` |
| `date` | `%(upload_date>%Y-%m)s/%(title)s.%(ext)s` | `2025-07/Video Title.mp4` |

When `--filename-template` is set, it takes precedence. If the template is absolute, the `--output` directory is ignored.

### Quality behavior

| Quality | ffmpeg present | ffmpeg missing |
|---------|---------------|----------------|
| `best` | `bestvideo+bestaudio/best` (merge → mp4 + thumbnail) | `best[ext=mp4]/best` (pre-muxed only) |
| `1080` | `bestvideo[height<=1080]+bestaudio/best[height<=1080]` | `best[height<=1080][ext=mp4]/...` |
| `720` | same pattern, height ≤720 | same pattern |
| `480` | same pattern, height ≤480 | same pattern |

### Speed limit format

Accepts suffixes: `B`, `KB`/`K`, `MB`/`M`, `GB`/`G`. Case-insensitive. Examples: `5MB`, `500KB`, `1G`. Passed to yt-dlp as `--limit-rate`.

### Terminal summary

After every grab, provenance prints a structured summary to stderr:

```
discovered: 240
downloaded: 226
skipped:     10
failed:       2
output:     ./downloads
```

When `--session` is used, an additional `session:` line appears. A detailed session status follows for session-backed runs.

---

## `provenance scan <URL...> [flags]`

Preview downloadable items without downloading.

### Flags

All `grab` flags apply, plus:

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--json` | bool | `false` | Print resolved source data as formatted JSON to stdout (see `resolve.Source` format) |
| `--save` | string | - | Save scan manifest JSON to a file |

---

## `provenance status <SESSION>`

Show progress for a saved session: total/succeeded/skipped/pending/running/failed counts, last update, and failed URL list (up to 20).

---

## `provenance resume <SESSION>`

Resume all pending and failed URLs in a session. Resets any stuck `running` entries to `pending`. If nothing remains, prints status and exits.

---

## `provenance retry-failed <SESSION>`

Retry only the failed URLs in a session. Existing pending URLs are left untouched.

---

## `provenance sessions`

| Subcommand | Description |
|------------|-------------|
| `list` | Table of all sessions: name, updated, total, OK, failed, pending. Sorted by most recent first. |
| `export <SESSION> <FILE>` | Copy the session JSON file to the given path |
| `clean <SESSION>` | Delete a session file from disk |
| `failed <SESSION> [FILE]` | Print failed URLs to stdout, or save to file |

---

## `provenance collect`

Named collection sync sources. A collection is a saved source URL with its own configuration, seen-item tracking, and incremental sync support. Each sync run scans the source, skips already-captured items, downloads new content, and records the result.

| Subcommand | Description |
|------------|-------------|
| `add <NAME> <URL> [flags]` | Create or update a collection. Accepts all `grab` flags for per-collection config. |
| `list` | Table of all collections: name, last sync, total/new/fail, URL. |
| `show <NAME>` | Full details: URL, site, output, created, last sync, seen count, last result. |
| `sync [NAME]` | Scan the source, skip seen items, download new content. `--all` to sync all collections. `--dry-run` to preview without downloading. `--record` to save capture manifests with SHA-256 hashes. `--every 24h` for repeating sync. |
| `remove <NAME>` | Delete a collection. |

### Incremental sync

Collections track seen item IDs (post IDs, video IDs) in `~/.cache/provenance/collections.json`. Each `collect sync` run:

1. Scans the source URL (discover all items without downloading)
2. Compares discovered item IDs against the seen set
3. Downloads only new items via a resumable session (`collect-<name>-<date>`)
4. Records the sync result (total, new, skipped, failed)

Repeated runs only pick up new content  -  a Reddit subreddit with 1000 wallpapers scans immediately and downloads nothing if no new posts have appeared since the last sync.

### --dry-run

```
provenance collect sync wallpapers --dry-run
```

Shows what would be synced without downloading: total items discovered, how many are new, how many skipped.

### --record

```
provenance collect sync wallpapers --record
```

Writes capture manifests to `<outputDir>/.provenance/`:

```
~/.provenance/
  collection.json                  ← collection config at sync time
  runs/
    2026-07-26T230000Z.json        ← per-sync CaptureManifest
  items/
    abc123.json                    ← per-item record with SHA-256, paths, metadata
```

Each item manifest records: external ID, URL, title, author, publication time, capture time, downloaded path, byte size, SHA-256 hash, extractor metadata, post text, and session name. Use `provenance manifest verify <dir>` to check file integrity.

### --every

```
provenance collect sync wallpapers --every 24h
```

Runs the sync in a loop at the specified interval (e.g., `24h`, `7d`, `30m`). Ctrl+C to stop. Useful as a simple daemon replacement  -  no cron required.

---

## `provenance manifest`

Show and verify capture manifests recorded via `collect sync --record`.

| Subcommand | Description |
|------------|-------------|
| `show <PATH>` | Pretty-print a capture manifest JSON file |
| `verify <DIR>` | Verify all files in a `.provenance/items/` directory against recorded SHA-256 hashes |

### verify

```
provenance manifest verify ~/Media/Reddit/Wallpapers
```

Reads every `.json` file in `<dir>/.provenance/items/`, computes SHA-256 of the referenced file on disk, and reports matches, mismatches, and missing files.

```
FAIL     ~/Media/Reddit/Wallpapers/reddit/username/abc.jpg
  expected: a1b2c3...
  actual:   d4e5f6...

42 ok, 1 failed, 0 missing
```

---

## `provenance archive`

Opt-in durable vault. Archives captured content into an immutable, content-addressed blob store with versioned revisions. Never overwrites prior captures.

There are two paths into the vault:

- **Download-then-archive** (`url`, `collection`, `session`, `import`): Copy content already on disk (downloaded via `grab` or `collect sync --record`) into the vault with SHA-256 verification. These commands do not download anything themselves.
- **Import-direct** (`import-pdf`, `import-git`, `import-docs`, `import-openapi`, `import-web`): Download AND archive in one step — no `grab` or `collect` needed.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--collection` | string | *(required)* | Archive collection name |
| `--vault` | string | `./provenance-vault` | Vault root directory |

### Subcommands

| Command | Description |
|---------|-------------|
| `url <URL>` | Download a URL, then archive it with capture metadata |
| `collection <NAME>` | Archive all captured content from a named collection |
| `session <NAME>` | Archive all downloads from a session |
| `import <DIR>` | Import an existing directory that already has `.provenance/` capture manifests |
| `import-pdf <PATH>` | Import a PDF file  -  extracts text per page, stores as searchable documents |
| `import-git <URL>` | Import a Git repository  -  shallow clone, extract doc files, resolve tag→commit |
| `import-docs <URL>` | Import a static documentation site  -  crawl bounded by scope and max-pages |
| `import-openapi <PATH>` | Import an OpenAPI spec  -  generate endpoint documents, store original spec |
| `import-web <URL>` | Archive a web page or site via headless Chrome  -  extracts HTML, text, discovers same-scope links, bounded by `--max-pages` |

### Importer flags

| Flag | Type | Default | Applies to | Description |
|------|------|---------|-----------|-------------|
| `--ref` | string | `main` | `import-git` | Git branch or tag to clone |
| `--scope` | string | - | `import-docs` | URL path prefix for crawl scope |
| `--max-pages` | int | `10` | `import-docs`, `import-web` | Maximum pages to crawl |
| `--screenshot` | bool | `false` | `import-web` | Capture page screenshots |

### Vault layout

```
<vault-root>/
  collections.json                  ← archive collection definitions
  blobs/
    sha256/
      ab/
        abcdef123...                ← content-addressed files (deduplicated)
  revisions/
    <revision-id>/                  ← immutable, id = SHA-256 of manifest
      manifest.json                 ← revision metadata + source + tool
      entities.jsonl                ← one Entity per line
      artifacts.jsonl               ← one Artifact per line
      documents.jsonl               ← one Document per line
      relations.jsonl               ← one Relation per line
  staging/
  exports/
```

### Example

```bash
# Sync with capture manifests
provenance collect sync wallpapers --record
# Archive the captured content
provenance archive collection wallpapers --collection visual-refs --vault ~/Vault
# → verifies SHA-256, copies to blob store, creates immutable revision
# → "archived revision abc123... (226 entities)"
```

---

## `provenance vault`

Manage the durable archive vault. Requires PostgreSQL (`PROVENANCE_DATABASE_URL` env var).

| Subcommand | Description |
|------------|-------------|
| `init` | Initialize the vault and create PostgreSQL schema (tables, GIN indexes, tsvector triggers) |
| `show <ID>` | Show revision details: entities, artifacts, timestamps |
| `cite <ID>` | Generate a citation for a revision entity |
| `diff <ID_A> <ID_B>` | Diff two revisions  -  reports added, removed, and changed entities with field-level diffs |
| `gc` | Garbage-collect orphaned blobs. `--dry-run` previews without deleting |
| `retention prune` | Prune old revisions keeping the last N (`--collection`, `--keep` required) |
| `backup` | Print backup guidance for vault directory |

### cite flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--collection` | string | - | Archive collection name |
| `--entity` | string | (first entity) | Entity external ID to cite |
| `--format` | string | `plain` | Citation format: `plain`, `markdown`, `json` |

### Example

```bash
export PROVENANCE_DATABASE_URL=postgres://user:pass@localhost/provenance
provenance vault init
provenance vault show abc123def456
provenance vault cite abc123def456 --entity my-post --format markdown
```

---

## `provenance search`

Full-text search across archived content. Uses PostgreSQL `tsvector` + `tsquery` with GIN indexes. Searches entity titles, authors, post text, captions, and document content.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--collection` | string | - | Filter by archive collection name |
| `--kind` | string | - | Filter by entity kind (video, image, post) |

### Example

```bash
provenance search "sunset mountains" --collection visual-refs
```

Results show ranked hits with title, headline snippet, source URL, collection, revision ID, and capture date.

---

## `provenance watch`

| Subcommand | Description |
|------------|-------------|
| `add <NAME> <URL> [flags]` | Create or update a watch subscription. Accepts all `grab` flags. |
| `list` | Table of watches: name, last run (or "never"), URL |
| `run [NAME]` | Run one watch (or all if NAME omitted). Each creates a `watch-<name>` session and downloads just the subscription URL. |
| `remove <NAME>` | Remove a watch subscription |

Watch runs use yt-dlp/native archives to skip previously-downloaded content.

---

## `provenance install`

Download yt-dlp and ffmpeg into the local cache. Normally runs automatically on first `grab`/`scan`. Useful for pre-warming a deployment image.

Note: ffmpeg is downloaded by go-ytdlp into the cache but may not be on PATH. The yt-dlp wrapper passes the cache path explicitly via `--ffmpeg-location`.

---

## `provenance tui`

Launch the interactive terminal UI. No flags. Full reference in [`docs/TUI.md`](TUI.md).

---

## `provenance completion <shell>`

Generate shell completion for one of: `bash`, `zsh`, `fish`, `powershell`.

```bash
provenance completion bash | sudo tee /etc/bash_completion.d/provenance
provenance completion zsh > "${fpath[1]}/_provenance"
provenance completion fish > ~/.config/fish/completions/provenance.fish
provenance completion powershell | Out-String | Invoke-Expression
```

---

## Auto-install behavior

The root command's `PersistentPreRunE` calls `extractor.EnsureInstalled()` (installs yt-dlp + ffmpeg) before every subcommand **except**:

- `help`
- `install`
- `status`
- `sessions` and all subcommands
- `completion`
- `tui`
- `watch` (for `list`, `add`, `remove` - but **not** `watch run`)

---

## Signal handling

`SIGINT` (Ctrl+C) and `SIGTERM` cancel the root context, which propagates to all workers. Running yt-dlp subprocesses are killed. Sessions saved to disk survive.

---

## Global flags

These flags are available on all commands (registered on the root command):

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--chrome-path` | string | (auto-detect) | Path to Chrome/Chromium executable. If unset, auto-detects from: `CHROME_PATH` env var, then searches PATH for `google-chrome`, `chromium`, `brave-browser`, `brave`, `brave-origin`, `chrome`, `microsoft-edge`. |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success (all URLs downloaded or nothing to do) |
| 1 | Error - error message printed to stderr, plus diagnostic hint if applicable |
