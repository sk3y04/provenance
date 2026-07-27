# Sessions

Named resumable download sessions that persist across restarts.

## Lifecycle

```
Create → Queue URLs → Download → Pause/Interrupt → Resume → Complete
                                     │
                                     ▼
                              Retry failed URLs
```

### 1. Create

```bash
provenance grab --session my-archive URL1 URL2 ...
provenance grab --session my-archive --batch urls.txt
```

Creates a new session (or reuses an existing one with the same name). URLs are added as queued entries with `pending` status.

### 2. Download runs

Each URL transitions through states:

```
pending → running → succeeded
                  → failed
                  → skipped
```

The session is saved to disk after every state transition.

### 3. Status check

```bash
provenance status my-archive
```

Prints: name, file path, last update, counts (total/succeeded/skipped/pending/running/failed), and up to 20 failed URLs with their error messages.

### 4. Resume

```bash
provenance resume my-archive
```

Resets any stuck `running` entries back to `pending`, then downloads all `pending` and `failed` URLs.

### 5. Retry only failures

```bash
provenance retry-failed my-archive
```

Downloads only URLs that previously failed. Pending URLs are not touched.

### 6. Complete

A session is "done" when all entries are `succeeded`, `failed`, or `skipped`. No special cleanup is needed - sessions persist on disk until explicitly deleted.

---

## Management commands

```bash
provenance sessions list                              # Table of all sessions
provenance sessions export my-archive /tmp/out.json   # Export to file
provenance sessions failed my-archive                 # Print failed URLs to stdout
provenance sessions failed my-archive failed.txt      # Save failed URLs to file
provenance sessions clean my-archive                  # Delete session
```

---

## JSON Schema

Each session is a JSON file at `<session_dir>/<name>.json`:

```json
{
  "name": "my-archive",
  "created_at": "2025-07-25T10:00:00Z",
  "updated_at": "2025-07-25T10:15:00Z",
  "version": 42,
  "options": {
    "output_dir": "./downloads",
    "concurrency": 4,
    "quality": "best"
  },
  "entries": {
    "https://x.com/user": {
      "url": "https://x.com/user",
      "source": "argument",
      "status": "succeeded",
      "attempts": 1,
      "last_error": "",
      "created_at": "2025-07-25T10:00:00Z",
      "updated_at": "2025-07-25T10:10:00Z"
    }
  }
}
```

### Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Session name (sanitized: spaces → dashes, no path separators) |
| `created_at` | RFC 3339 | When the session was first created |
| `updated_at` | RFC 3339 | Last modification time |
| `version` | int64 | Monotonic version for optimistic concurrency |
| `options` | `config.Config` | Download options used for this session |
| `entries` | `map[string]*Entry` | URL → entry mapping |

### Entry fields

| Field | Type | Description |
|-------|------|-------------|
| `url` | string | The download URL (key) |
| `source` | string | Where the URL came from: `"argument"`, `"batch:<path>"`, `"discovered"` |
| `status` | `pending` / `running` / `succeeded` / `failed` / `skipped` | Current state |
| `attempts` | int | Number of download attempts |
| `last_error` | string | Error message from the last failed attempt |
| `created_at` | RFC 3339 | When the entry was added |
| `updated_at` | RFC 3339 | Last status change |

### Statuses

| Status | Meaning |
|--------|---------|
| `pending` | Queued, not yet attempted |
| `running` | Currently being downloaded |
| `succeeded` | Download completed successfully |
| `failed` | Download failed (after retries) |
| `skipped` | Skipped (e.g., already in URL archive) |

---

## Storage

### Default location

| OS | Path |
|----|------|
| Linux | `~/.cache/provenance/sessions/<name>.json` |
| macOS | `~/Library/Caches/provenance/sessions/<name>.json` |
| Windows | `%LOCALAPPDATA%\provenance\sessions\<name>.json` |

Override with `PROVENANCE_SESSION_DIR`:

```bash
export PROVENANCE_SESSION_DIR=/mnt/data/provenance-sessions
```

### Session name sanitization

Session names cannot:
- Be empty
- Contain `.`, `..`, or `..` as a path component
- Contain path separators (`/`, `\`, `:`)

Spaces, tabs, and newlines in names are replaced with dashes.

Example: `"weekend archive 2025"` → saved as `weekend-archive-2025.json`.

---

## Optimistic Concurrency Control

Sessions use version-based optimistic concurrency to prevent data loss when multiple processes access the same session simultaneously.

### How it works

1. Every save increments `Version` (starting from 1).
2. Before saving, the on-disk file's `Version` is read and compared.
3. If the on-disk version differs from the in-memory version, another process modified the file. The save is **rejected** with `ErrConcurrentModification`.
4. The caller should reload and retry.

### Atomic writes

Saves use temp-file + rename:

```
1. Write to {name}.json.tmp
2. fsync
3. os.Rename(tmp, {name}.json)    ← atomic on same filesystem
```

This ensures the JSON file is never partially written.

### Thread safety

All public `Session` methods acquire `s.mu` (sync.Mutex). The `saveLocked()` method must be called under the lock. Concurrent goroutines within the same process are safe.

---

## URL Archives

Separate from sessions, provenance maintains per-output-directory URL archives to skip already-downloaded URLs across all sessions:

```
{output}/_provenance_cache/archive.txt
{output}/_provenance_cache/ytdlp_archive.txt
```

### Format

```
# provenance URL archive
ok:https://example.com/video1.mp4
perm-fail:https://example.com/deleted-video.mp4
```

| Prefix | Meaning |
|--------|---------|
| `ok:<url>` | Successfully downloaded (skipped on future runs) |
| `perm-fail:<url>` | Permanently failed this run (informational, retried by default) |
| Bare URL | Treated as `ok:` (backward compatibility) |

The `--no-archive` flag disables both archives.

---

## Batch persistence

When `--session` is used with `--batch`, URLs from the batch file are bulk-added to the session before downloading begins. The session tracks progress per-URL within the batch, enabling resume across batch runs.

```bash
provenance grab --session large-job --batch 5000-urls.txt
# ... interrupt ...
provenance resume large-job       # continues from where it left off
```
