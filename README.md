# provenance

> Single-binary media collector, source archive, and knowledge vault.

Provenance downloads media from thousands of sites (YouTube, Twitter/X, Reddit, Instagram, TikTok, and more), supports incremental collection sync, and optionally archives content into a searchable, citable vault.

## Quick start

```bash
# Grab a video
provenance grab https://www.youtube.com/watch?v=dQw4w9WgXcQ

# Grab an Instagram profile (cookies required)
provenance grab --cookies cookies.txt https://www.instagram.com/user/

# Grab a Reddit subreddit, limit to 20 posts, include post text
provenance grab --cookies cookies.txt https://www.reddit.com/r/wallpapers --limit 20 --include-posts

# Grab a Twitter/X profile (cookies required)
provenance grab --cookies cookies.txt https://x.com/user --limit 100 --include-posts

# Subscribe to a source and sync only what's new
provenance collect add wallpapers "https://reddit.com/r/wallpapers" --cookies cookies.txt --limit 500
provenance collect sync wallpapers

# Archive downloaded content into a searchable vault
provenance collect sync wallpapers --record
provenance archive collection wallpapers --collection saved-media

# Import a web page directly to vault (no grab/collect needed)
provenance archive import-web https://docs.docker.com/compose/ --collection docker-docs
```

Single binary. No Python, Node, or runtime dependencies beyond [yt-dlp](https://github.com/yt-dlp/yt-dlp) + ffmpeg (auto-installed on first run).

## Install

```bash
go build -o provenance ./cmd/provenance
# or
go install ./cmd/provenance
```

Pre-warm tools into cache:

```bash
provenance install
```

## Four levels

| Level | Command | What it does |
|-------|---------|-------------|
| **grab** | `provenance grab URL` | Instant one-shot download. One URL or thousands. |
| **collect** | `provenance collect add NAME URL` | Subscribe to a source. Incremental sync — only new items each run. |
| **archive** | `provenance archive collection NAME --collection X` | Vault your downloads. SHA-256 verified, searchable, citable. |
| **import** | `provenance archive import-web|import-pdf|...` | Download AND vault in one step. No grab/collect needed. |

See [`docs/CLI.md`](docs/CLI.md) for the full command reference and every flag.

## Extractors

| Source | Engine |
|--------|--------|
| YouTube, TikTok, Twitch, Vimeo, SoundCloud, Patreon, Facebook & 1000+ sites | [`yt-dlp`](https://github.com/yt-dlp/yt-dlp) (auto-installed) |
| **Twitter / X** profiles | Native GraphQL client |
| **Reddit** user profiles & subreddits | Native JSON API client |
| **Instagram** profiles, posts, reels, stories | Native REST API v1 client |
| JS-heavy sites yt-dlp can't handle | Headless Chrome via `chromedp` |

Deep dive: [`docs/EXTRACTORS.md`](docs/EXTRACTORS.md)

## Documentation

| File | Contents |
|---|---|
| [`docs/CLI.md`](docs/CLI.md) | Full CLI reference — every command, flag, default, exit code |
| [`docs/EXAMPLES.md`](docs/EXAMPLES.md) | Cookbook — Twitter, Reddit, YouTube, Instagram, Patreon, batch, cron |
| [`docs/EXTRACTORS.md`](docs/EXTRACTORS.md) | Per-extractor internals — yt-dlp, Twitter GraphQL, Reddit JSON, Instagram REST, chromedp |
| [`docs/CONFIGURATION.md`](docs/CONFIGURATION.md) | Every config field, flag mapping, env var, filter semantics |
| [`docs/TROUBLESHOOTING.md`](docs/TROUBLESHOOTING.md) | Common issues — auth, rate limits, Docker, Chrome sandbox |
| [`docs/COOKIES.md`](docs/COOKIES.md) | Authentication — Netscape format, browser cookies, OAuth2, per-site requirements |
| [`docs/TUI.md`](docs/TUI.md) | Terminal UI — views, key bindings, state model |
| [`docs/SESSIONS.md`](docs/SESSIONS.md) | Session lifecycle, resume, optimistic concurrency |
| [`docs/WATCHES.md`](docs/WATCHES.md) | Recurring download subscriptions |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Architecture, data flow, design decisions, package reference |
| [`TREEVIEW.md`](TREEVIEW.md) | File-level reference — every source file |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Build, test, conventions |
| [`CHANGELOG`](CHANGELOG) | Version history |

## License

[GPL-3.0](https://www.gnu.org/licenses/gpl-3.0.txt)
