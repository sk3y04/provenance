# Troubleshooting

Common issues and how to fix them.

## Authentication

### "cookies file is required for Twitter/X"

Twitter/X requires browser cookies. Export `cookies.txt` from Chrome or Firefox using a
browser extension (see [COOKIES.md](COOKIES.md)).

```bash
provenance grab --cookies cookies.txt https://x.com/user
```

### "cookies file is required for Instagram"

Instagram requires an authenticated session cookie. Export `cookies.txt` from your browser
after logging in to instagram.com.

```bash
provenance grab --cookies cookies.txt https://www.instagram.com/user/
```

### Instagram returns no posts or empty results

Instagram requires cookies. Without cookies the API will return empty or restricted results.
Make sure your session cookie is fresh - if it expired, re-export from your browser.

## Missing tools

### "ffmpeg not found" / audio-only fails

Install ffmpeg or run `provenance install`:

```bash
provenance install
# or system package manager:
#   Linux: sudo apt install ffmpeg
#   macOS: brew install ffmpeg
#   Windows: winget install Gyan.FFmpeg
```

## Download failures

### "unsupported URL" / generic site doesn't work

provenance automatically falls back to headless Chrome for JS-heavy sites. If that also fails,
try with cookies:

```bash
provenance grab --cookies cookies.txt https://js-heavy-site.com/video
```

### Rate limited (429 errors)

Lower concurrency or use sessions to resume later:

```bash
provenance grab --concurrency 1 --session my-session URL
# ... wait, then ...
provenance resume my-session
```

### "context deadline exceeded" / timeout

Network issues. The download likely progressed - resume the session:

```bash
provenance resume my-session
```

### Partial download or interrupted by Ctrl+C

Sessions persist across interruptions. Resume from where you left off:

```bash
provenance resume my-session
# or retry only failed URLs:
provenance retry-failed my-session
```

## Docker / containerized environments

### Chrome fails with "No usable sandbox" or "SUID sandbox helper"

Headless Chrome requires either a working sandbox or the `--no-sandbox` flag. In Docker
containers without `--privileged` or seccomp profiles, run Chrome with:

```bash
provenance grab --no-sandbox https://js-heavy-site.com/video
```

Without this flag, Chrome will fail to start inside most containers and the browser
fallback extractor will be unavailable.

### yt-dlp / ffmpeg auto-install in ephemeral containers

In ephemeral containers, the auto-installed tools in `~/.cache/go-ytdlp` are lost on
restart. Pre-warm the cache during image build:

```bash
provenance install
```

Or bind-mount the cache directory persistently.

## Preview issues

### Dry run to see what will download

```bash
provenance grab --dry-run https://www.youtube.com/playlist?list=PL...
provenance scan https://x.com/user
provenance scan --json https://www.reddit.com/r/subreddit
```

### Scan multiple sources to JSON

```bash
provenance scan --json URL1 URL2 URL3 > scan-report.json
provenance scan --save report.json URL1 URL2 URL3
```

## Reddit

### Low rate limits or limited results without OAuth

Without OAuth, Reddit's public API has lower rate limits and may return fewer posts.
Register a Reddit app and set environment variables:

```bash
export REDDIT_CLIENT_ID="your-id"
export REDDIT_CLIENT_SECRET="your-secret"
provenance grab https://www.reddit.com/r/subreddit
```

## Sessions

### Session file locked or corrupted

Sessions use atomic writes with optimistic concurrency. If you experience issues:

```bash
provenance sessions list             # check all sessions
provenance sessions export <name>    # export before deleting
provenance sessions clean <name>     # remove a session cleanly
```

### Session not found

Session names are sanitized (spaces, slashes replaced). Use exact names from:

```bash
provenance sessions list
```
