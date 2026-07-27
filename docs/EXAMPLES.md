# Examples

Real-world cookbook for common use cases.

## Twitter / X

### Download all media from a profile (authenticated)

```bash
provenance grab --cookies cookies.txt https://x.com/artist
```

Cookies are **required** for Twitter/X. Use a browser extension to export `cookies.txt` (see [`docs/COOKIES.md`](COOKIES.md)).

### Only download videos, limit to 20 posts

```bash
provenance grab --cookies cookies.txt https://x.com/artist --include mp4 --limit 20
```

### Download photos only, no videos

```bash
provenance grab --cookies cookies.txt https://x.com/artist --include jpg,png --exclude mp4
```

### Download posts as markdown alongside media

```bash
provenance grab --cookies cookies.txt --include-posts https://x.com/artist
```

Creates `twitter/artist/posts/{tweet_id}.md` for each tweet with text, links, and metadata.

---

## Instagram

### Download all media from a profile

```bash
provenance grab --cookies cookies.txt https://www.instagram.com/username/
```

Cookies are **required** for Instagram. The `sessionid` cookie authenticates API requests. Export `cookies.txt` from your browser (see [`docs/COOKIES.md`](COOKIES.md)).

### Download a single reel or post

```bash
provenance grab --cookies cookies.txt https://www.instagram.com/reel/SHORTCODE/
provenance grab --cookies cookies.txt https://www.instagram.com/p/SHORTCODE/
```

### Download stories

```bash
provenance grab --cookies cookies.txt https://www.instagram.com/stories/username/
```

### Only download videos (no images)

```bash
provenance grab --cookies cookies.txt https://www.instagram.com/username/ --include mp4
```

### Download posts as markdown alongside media

```bash
provenance grab --cookies cookies.txt --include-posts https://www.instagram.com/username/
```

Creates `instagram/username/posts/{shortcode}.md` for each post with captions, timestamps, and source links.

### Limit to recent 20 posts

```bash
provenance grab --cookies cookies.txt --limit 20 https://www.instagram.com/username/
```

### Preview before downloading

```bash
provenance scan --cookies cookies.txt https://www.instagram.com/username/
provenance scan --cookies cookies.txt --json https://www.instagram.com/username/ > scan.json
```

### Resumable large archive

```bash
provenance grab --cookies cookies.txt --session ig-archive https://www.instagram.com/username/
# ... interrupt with Ctrl+C ...
provenance status ig-archive
provenance resume ig-archive
```

---

## Reddit

### Download all media from a user

```bash
provenance grab --cookies cookies.txt https://www.reddit.com/user/username/submitted
```

### Download all media from a subreddit

```bash
provenance grab --cookies cookies.txt https://www.reddit.com/r/subreddit
```

### Subreddit with OAuth for higher rate limits

```bash
export REDDIT_CLIENT_ID="your-id"
export REDDIT_CLIENT_SECRET="your-secret"
provenance grab https://www.reddit.com/r/dataisbeautiful
```

### Only images, exclude videos

```bash
provenance grab --cookies cookies.txt https://www.reddit.com/r/wallpapers --include jpg,png --exclude mp4,webm
```

### Download posts as markdown alongside media

```bash
provenance grab --cookies cookies.txt --include-posts https://www.reddit.com/r/programming
```

### Download posts with comments

```bash
provenance grab --cookies cookies.txt --include-posts --include-comments --max-comments 50 https://www.reddit.com/r/programming
```

Fetches Reddit comments via `{permalink}.json`, bounded to 50 comments per post. Comment text flows into archive search.

---

## YouTube

### Download a single video

```bash
provenance grab https://www.youtube.com/watch?v=dQw4w9WgXcQ
```

### Download an entire playlist

```bash
provenance grab https://www.youtube.com/playlist?list=PLABC123...
```

### Audio-only playlist (mp3)

```bash
provenance grab --audio-only https://www.youtube.com/playlist?list=PLABC123...
```

### Limit to 720p for space savings

```bash
provenance grab --quality 720 https://www.youtube.com/watch?v=...
```

### Flat output layout (all files in one directory)

```bash
provenance grab --layout flat https://www.youtube.com/playlist?list=PLABC123...
```

### Date-based output layout

```bash
provenance grab --layout date https://www.youtube.com/playlist?list=PLABC123...
```

Creates: `2025-07/Video Title.mp4`, `2025-08/Another Video.mp4`

### Custom filename template

```bash
provenance grab \
  --filename-template "%(extractor)s/%(uploader)s/%(upload_date>%Y-%m-%d)s - %(title)s.%(ext)s" \
  https://www.youtube.com/watch?v=...
```

---

## Patreon

### Download a locked post with cookies

```bash
provenance grab --cookies cookies.txt https://www.patreon.com/posts/some-post-12345678
```

### Use browser cookies directly (no export needed)

```bash
provenance grab --cookies-from-browser chrome https://www.patreon.com/posts/some-post-12345678
```

### Batch from a file

```bash
# urls.txt
# Lines starting with # are comments
https://www.patreon.com/posts/post-1
https://www.youtube.com/watch?v=video1
https://www.youtube.com/watch?v=video2

provenance grab --cookies cookies.txt --batch urls.txt
```

---

## Batch Processing

### Download from a URL list

```bash
# urls.txt (one URL per line, # for comments)
https://x.com/artist1
https://x.com/artist2
https://www.reddit.com/user/user1/submitted
https://www.youtube.com/playlist?list=PL...

provenance grab --cookies cookies.txt --concurrency 8 --batch urls.txt
```

Batch URLs use parallel worker pools. Twitter/Reddit URLs are dispatched sequentially (their extractors handle parallelism internally).

### Combine positional URLs with batch

```bash
provenance grab --batch extra-urls.txt https://x.com/main-user
```

### Large batch with session for resume

```bash
provenance grab --session huge-batch --concurrency 16 --batch 5000-urls.txt
```

---

## Watch Subscriptions

### Set up recurring downloads

```bash
provenance watch add my-artist https://x.com/artist --cookies cookies.txt --include mp4 --limit 20
provenance watch add daily-wallpapers https://www.reddit.com/r/wallpapers --cookies cookies.txt
provenance watch add weekly-playlist https://www.youtube.com/playlist?list=PL... --audio-only
```

### View your subscriptions

```bash
provenance watch list
```

### Run one subscription

```bash
provenance watch run my-artist
```

### Run all subscriptions

```bash
provenance watch run
```

### Cron job for daily sync

```bash
# Run every morning at 6 AM
0 6 * * * /usr/local/bin/provenance watch run >> ~/provenance.log 2>&1
```

Each run only downloads **new** content (URL archives skip previously downloaded files).

---

## Sessions

### Session workflow

```bash
# Start a large session
provenance grab --session my-session --batch 1000-urls.txt --concurrency 8

# Check progress (from another terminal)
provenance status my-session

# Resume after interruption
provenance resume my-session

# Retry only failures
provenance retry-failed my-session

# Export session data
provenance sessions export my-session /tmp/backup.json

# Get failed URLs to investigate
provenance sessions failed my-session failed.txt

# Clean up when done
provenance sessions clean my-session
```

### List all sessions

```bash
provenance sessions list
```

---

## Collect & Provenance

### Subscribe to sources (once, then sync incrementally)

```bash
# Reddit subreddit
provenance collect add wallpapers "https://reddit.com/r/wallpapers" \
  --cookies cookies.txt --output ~/Media/Reddit --limit 1000 --include-posts

# Twitter/X profile
provenance collect add my-artist "https://x.com/artist" \
  --cookies cookies.txt --include-posts --limit 100

# Instagram profile
provenance collect add photographer "https://www.instagram.com/username/" \
  --cookies cookies.txt --include-posts

# YouTube playlist
provenance collect add lectures "https://www.youtube.com/playlist?list=PL..." --audio-only
```

### Sync — incremental, only new content each run

```bash
provenance collect sync wallpapers
provenance collect sync my-artist --record
provenance collect sync              # sync all collections at once
```

### Incremental subreddit sync with capture manifests

```bash
provenance collect add wallpapers "https://reddit.com/r/wallpapers" \
  --output ~/Media/Reddit/Wallpapers \
  --limit 1000 \
  --include-posts

provenance collect sync wallpapers --record
```

Writes capture manifests to `~/Media/Reddit/Wallpapers/.provenance/` with SHA-256 hashes for every downloaded file.

### Verify capture integrity

```bash
provenance manifest verify ~/Media/Reddit/Wallpapers
```

Reads every `items/*.json` in `.provenance/`, computes SHA-256 of referenced files on disk, reports mismatches or missing files.

### Inspect a capture manifest

```bash
provenance manifest show ~/Media/Reddit/Wallpapers/.provenance/runs/2026-07-26T230000Z.json
```

Pretty-prints the full capture manifest JSON  -  source URL, site, output dir, options, and per-item metadata.

### Daily cron sync

```bash
0 6 * * * /usr/local/bin/provenance collect sync wallpapers --record >> ~/provenance.log 2>&1
```

Only downloads new wallpapers each day. Capture manifests accumulate per-sync run.

### Run as a daemon with --every

```bash
# Sync every 6 hours until stopped
provenance collect sync wallpapers --every 6h
```

---

## Archive & Import

### Archive collected content to vault

```bash
# Download + record manifests
provenance collect sync wallpapers --record
# Copy into immutable vault
provenance archive collection wallpapers --collection saved-media --vault ~/Vault
```

### Import a web page directly to vault (no grab/collect needed)

```bash
provenance archive import-web https://docs.docker.com/compose/ --collection docker-docs
provenance archive import-web https://docs.docker.com/compose/ --collection docker-docs --max-pages 50
```

### Import a PDF, Git repo, or API spec

```bash
provenance archive import-pdf ./rfc9110.pdf --collection rfcs
provenance archive import-git https://github.com/org/repo --ref main --collection vendor-docs
provenance archive import-openapi ./openapi.yaml --collection internal-api
```

### Specify Chrome/Chromium path

```bash
provenance archive import-web https://example.com --collection my-docs --chrome-path /usr/bin/brave-browser
```

---

## TUI Workflow

### Interactive download

```bash
provenance tui
# Main menu → "New grab"
# Paste URL, configure options, Ctrl+S to start
```

### Scan and pick (download only what you want)

```bash
provenance tui
# Main menu → "Scan & pick"
# Paste URL, wait for scan, space to select/deselect items, enter to download
```

### Resume an interrupted session

```bash
provenance tui
# Main menu → "Sessions"
# Navigate to session, press r to resume
```

---

## Quality and Formats

### Video quality levels

```bash
provenance grab --quality 1080 URL   # Max 1080p
provenance grab --quality 720 URL    # Max 720p
provenance grab --quality 480 URL    # Max 480p
provenance grab --quality best URL   # Highest available (default)
```

### Audio only for music/playlists

```bash
provenance grab --audio-only URL
```

Outputs MP3 at 320kbps. Requires ffmpeg.

### Extension filtering

```bash
provenance grab --include mp4,jpg URL        # Only MP4 and JPG
provenance grab --exclude webp,gif URL       # Skip WebP and GIF
```

---

## Speed Control

```bash
provenance grab --speed-limit 5MB URL       # Cap at 5 MB/s
provenance grab --speed-limit 500KB URL      # Cap at 500 KB/s
provenance grab --concurrency 2 URL          # Fewer parallel downloads
```

---

## Troubleshooting

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) for common issues and solutions.


