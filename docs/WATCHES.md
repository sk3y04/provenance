# Watch Subscriptions

Recurring download subscriptions for sources like Twitter/X profiles and Reddit subreddits. Watches remember a URL + download options and can be run on demand or in bulk.

## Lifecycle

```
Add → Run → (repeat) → Remove
```

### Add

```bash
provenance watch add artist-a https://x.com/<user> --include mp4 --limit 20
```

Creates (or updates) a watch subscription named `artist-a` with the given URL and download flags. All `grab` flags are supported.

### List

```bash
provenance watch list
```

Output:

```
Name                     Last run             URL
artist-a                 2025-07-25 10:00     https://x.com/<user>
daily-reddit             never                https://www.reddit.com/r/<subreddit>
```

### Run one

```bash
provenance watch run artist-a
```

1. Loads the watch subscription
2. Creates a session named `watch-artist-a` (or reuses existing)
3. Downloads just the subscription URL with the saved options
4. Marks the watch as run (updates `LastRunAt`)

### Run all

```bash
provenance watch run
```

Runs every watch subscription sequentially. The first error is returned but remaining watches continue.

### Remove

```bash
provenance watch remove artist-a
```

---

## How it works

### Session integration

Each watch run creates a session named `watch-<name>`. This means:

- **Resumable**: Interrupted runs can be resumed with `provenance resume watch-artist-a`
- **Progress tracking**: `provenance status watch-artist-a` shows progress
- **Deduplication**: The session tracks already-succeeded URLs, and yt-dlp archives prevent re-downloading identical content

### Archive behavior

Watches rely on two deduplication mechanisms:

1. **provenance URL archive**: `<output>/_provenance_cache/archive.txt` - tracks successfully downloaded URLs
2. **yt-dlp download archive**: `<output>/_provenance_cache/ytdlp_archive.txt` - tracks downloaded video IDs

This means subsequent watch runs only download **new** content. For Twitter/Reddit, each post has a unique URL, so new posts are naturally detected. For yt-dlp sources, the download archive prevents re-downloading videos already saved.

### Options persistence

When you add a watch, all current `grab` flags are saved in `watch.json`:

```json
{
  "subscriptions": {
    "artist-a": {
      "name": "artist-a",
      "url": "https://x.com/<user>",
      "options": {
        "output_dir": "./downloads",
        "concurrency": 4,
        "filter": {
          "include_ext": ["mp4"]
        },
        "post_limit": 20
      },
      "created_at": "2025-07-25T10:00:00Z",
      "updated_at": "2025-07-25T10:00:00Z",
      "last_run_at": "2025-07-25T10:15:00Z"
    }
  }
}
```

### Storage

JSON file at `PROVENANCE_WATCH_FILE` or `~/.cache/provenance/watch.json`.

---

## TUI

In the interactive TUI, watches are managed from the "Watches" screen:

| Action | Key | Description |
|--------|-----|-------------|
| Run | `enter` | Run selected watch subscription |
| Add | `n` | Open add-form (name + URL + download flags) |
| Delete | `d` ×2 | Remove with double-press confirmation |
| Filter | `/` | Fuzzy filter by name or URL |

---

## Use cases

### Track a Twitter artist for new videos only

```bash
provenance watch add artist-x https://x.com/artist --include mp4 --limit 20
```

Run daily. Each run downloads only new video posts.

### Archive a Reddit subreddit

```bash
provenance watch add wallpapers https://www.reddit.com/r/wallpapers --include jpg,png
```

### Bulk background job

```bash
# Run all watches via cron
0 6 * * * /usr/local/bin/provenance watch run >> ~/provenance.log 2>&1
```
