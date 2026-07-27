# Terminal UI

The interactive TUI is launched with `provenance tui`. It uses [Bubble Tea](https://github.com/charmbracelet/bubbletea) (Elm Architecture) and [Lip Gloss](https://github.com/charmbracelet/lipgloss) for styling.

## Views

The TUI has 7 views:

| View | Purpose | Entry |
|------|---------|-------|
| **Main Menu** | Navigation hub with 10 options + resume banner | `provenance tui` startup |
| **New Grab** | Form-based download creation with live scan preview | Main menu → "New grab" |
| **Scan & Pick** | Scan a URL, multi-select items, download subset | Main menu → "Scan & pick" |
| **Sessions** | Browse saved sessions with detail view | Main menu → "Sessions" |
| **Watches** | Manage recurring watch subscriptions | Main menu → "Watches" |
| **Collections** | Browse named collections, sync, archive | Main menu → "Collections" |
| **History** | Browse recent runs, rerun or reveal | Main menu → "History" |
| **Archive Search** | Full-text search across vaulted content | Main menu → "Archive search" |
| **Vault** | Vault status and archive collection browser | Main menu → "Vault" |
| **Runner** | Live download progress with per-file bars | Triggered from any download action |

---

## Key Bindings

### Global

| Key | Action |
|-----|--------|
| `esc` | Back to previous screen |
| `q` | Quit (from main menu) |
| `Q` | Quit (anywhere) |
| `ctrl+c` | Quit / cancel running download |

### Main Menu

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Navigate |
| `enter` | Select |
| `R` | One-key resume of most-recent unfinished session |
| `Q` | Quit |

### New Grab Form

| Key | Action |
|-----|--------|
| `tab` / `shift+tab` | Navigate between fields |
| `↑` / `↓` | Navigate between fields |
| `enter` | Focus/unfocus field |
| `a` | Toggle advanced options (15 additional fields) |
| `ctrl+f` | Find cookie files in working directory |
| `ctrl+s` | Submit form → start download |
| `esc` | Back to main menu |

### Scan & Pick

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Navigate items |
| `space` | Toggle selection |
| `A` | Select all |
| `n` | Select none |
| `i` | Invert selection |
| `/` | Fuzzy filter |
| `r` | Reload/rescan |
| `a` | Toggle advanced options |
| `enter` | Start downloading selected items |
| `esc` | Back to main menu |

### Sessions

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Navigate |
| `enter` | View session detail |
| `r` | Resume (pending + failed URLs) |
| `R` | Retry-only failed URLs |
| `e` | Export session to JSON file |
| `f` | Show failed URLs |
| `d` (×2) | Delete session (double-press confirm within 3s) |
| `/` | Fuzzy filter by name |

### Session Detail

| Key | Action |
|-----|--------|
| `r` | Resume this session |
| `R` | Retry-only failed URLs |
| `esc` | Back to session list |

### Watches

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Navigate |
| `enter` | Run selected watch subscription |
| `n` | Add new watch subscription (opens form) |
| `d` (×2) | Delete subscription (double-press confirm within 3s) |
| `/` | Fuzzy filter by name or URL |

### Watches Add Form

| Key | Action |
|-----|--------|
| `tab` / `shift+tab` | Navigate fields |
| `ctrl+s` | Submit → save subscription |
| `esc` | Cancel → back to watches list |

### History

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Navigate |
| `enter` / `r` | Rerun with saved options |
| `o` | Reveal output directory in file manager |
| `d` | Delete entry |
| `/` | Fuzzy filter by title, URLs, output dir, file paths |

### Runner (Live Download)

| Key | Action |
|-----|--------|
| `esc` / `backspace` / `h` / `q` | Back to main menu (only when done) |

---

### Collections

| Key | Action |
|-----|--------|
| `enter` | Show collection detail (metadata, last sync result) |
| `s` | Sync selected collection (incremental, records capture manifests) |
| `S` | Sync all collections |
| `a` | Archive selected collection to the vault |
| `/` | Filter by name or URL |
| `esc` / `backspace` / `h` | Back to main menu |

Collection detail shows: URL, site, output directory, creation date, last sync time, seen item count, and last sync result.

### Archive Search

| Key | Action |
|-----|--------|
| `enter` | Execute search query |
| `up` / `k`, `down` / `j` | Navigate results |
| `esc` | Back to main menu |

Requires `vault init` first. Results show title, headline snippet, URL, collection, and capture date.

### Vault

| Key | Action |
|-----|--------|
| `enter` | Show revision details (when implemented) |
| `esc` / `q` | Back to main menu |

Shows vault initialization status and lists archive collections with revision counts. Prompts user to run `provenance vault init` if not yet initialized.

---

All state lives in a single `model` struct in `internal/tui/tui.go`:

```go
type model struct {
    ctx     context.Context
    program *tea.Program
    view    view              // current view
    err     string            // error message
    info    string            // info banner
    width, height int        // terminal dimensions

    mainCursor       int      // main menu position
    resumeCandidate  string   // name of newest unfinished session
    resumePendingURL int      // count for resume banner

    sessions     []session.Info
    sessCursor   int
    sessSelected *session.Session
    sessFilter   filterState
    sessPending  pendingDelete

    watches      []watch.Subscription
    watchesCur   int
    watchesFilt  filterState
    watchPending pendingDelete
    watchAddForm *watchAddForm

    history     []history.Run
    historyCur  int
    historyFilt filterState

    form         downloadForm
    formStep     int
    showAdvanced bool
    preview      scanPreview
    cookiePick   cookiePickerState

    scan         scanState
    scanAdvanced bool

    runner       runnerState
}
```

### Key sub-types

**`filterState`** - fuzzy-filter input model with active toggle. Press `/` to activate, type to filter, `esc`/`enter` to dismiss.

**`pendingDelete`** - double-press confirmation (name + timestamp). The second press must happen within `confirmWindow` (3 seconds) or it resets.

**`downloadForm`** - 5 basic fields (URL, output, concurrence, cookies, session name) + 15 advanced fields (quality, cookies browser, include/extend extensions, min/max size, title match/exclude, post limit, output layout, filename template, speed limit, audio only, no archive, include posts toggles).

**`scanPreview`** - debounced scan result shown under the URL field. Triggers after ~700ms of idle typing. Shows item count, total known size, and detected site.

**`scanState`** - multi-select item list: loaded manifest items, checked map, cursor, filter, advanced options form.

**`runnerState`** - live download state: URLs, options, counters (queued/running/OK/failed/skipped), per-file progress (map of `fileProgress` keyed by URL), streaming log lines, throughput EMA, cancel function, notification flag.

**`fileProgress`** - per-file download: URL, destination, total bytes, written bytes, started/done timestamps, error.

---

## Message Types

The TUI communicates between goroutines and the main loop via typed messages (all defined in `tui.go`):

| Message | Sent When | Carries |
|---------|-----------|---------|
| `sessionsLoadedMsg` | Session list loaded from disk | `infos []session.Info`, `err error` |
| `sessionLoadedMsg` | Single session loaded | `s *session.Session`, `err error` |
| `watchesLoadedMsg` | Watches loaded from disk | `subs []watch.Subscription`, `err error` |
| `historyLoadedMsg` | History loaded from disk | `runs []history.Run`, `err error` |
| `runnerEventMsg` | Download lifecycle event | `kind` (queue/start/ok/fail/skip), `url`, `note` |
| `runnerLogMsg` | Captured yt-dlp stderr line | `line string` |
| `runnerDoneMsg` | All downloads complete | `err error` |
| `fileStartMsg` | Per-file download starting | `url`, `dest`, `total int64` |
| `fileProgressMsg` | Progress update (EMA-sampled) | `url`, `written`, `total int64` |
| `fileDoneMsg` | Per-file download complete | `url`, `err error` |
| `scanPreviewMsg` | Debounced scan result | `url`, `count`, `size`, `site`, `err` |
| `scanPreviewTick` | Debounce timer fired | `url` (the URL to scan) |
| `scanLoadedMsg` | Scan & pick manifest loaded | `sourceURL`, `manifest`, `err` |
| `cookiesFoundMsg` | Cookie file search complete | `files []string`, `err error` |
| `tickMsg` | Periodic timer (throughput sampling) | `time.Time` |

---

## Rendering

All views are rendered in `internal/tui/views.go` using these lipgloss styles:

| Style | Hex | Usage |
|-------|-----|-------|
| `titleStyle` | Color 12 (blue) | Header "provenance interactive mode" |
| `highlight` | Color 13 (magenta) | Selected item / cursor |
| `dim` | Color 8 (gray) | Secondary text |
| `errStyle` | Color 9 (red) | Error messages |
| `okStyle` | Color 10 (green) | Success indicators |
| `infoStyle` | Color 14 (cyan) | Info messages |
| `warnStyle` | Color 11 (yellow) | Warnings |
| `cursorStyle` | Color 13 (magenta) | List cursor |
| `helpFooter` | Color 8 (gray) | Bottom-of-screen key hints |
| `sectionHeader` | Bold + underline | Section titles |
| `bannerStyle` | Yellow bg, black fg | Resume banner |

### View structure

Each view follows the same pattern:
1. **Header**: `titleStyle.Render(" provenance ") + dim.Render(" interactive mode")`
2. **Content area**: Scrollable list/table with cursor highlighting, status badges, and key hints
3. **Footer**: Context-dependent key bindings (`helpFooter.Render(...)`)

The runner view additionally renders:
- Per-URL counters with color-coded badges (✓ green, ✗ red, ⤼ yellow)
- Per-file progress bars with gradient styling
- Throughput (EMA-smoothed bytes/sec) and ETA
- Scrollable log window showing captured yt-dlp stderr

---

## Runner Lifecycle

1. **Setup** (`startRunner` in `runner.go`): Creates a cancellable context, initializes `runnerState`, redirects `os.Stdout`/`os.Stderr` to capture yt-dlp output.
2. **Launch**: A `tea.Cmd` goroutine runs `app.Download()` with a `teaReporter` (implements `dispatcher.Reporter`) and `fileReporter` (implements `downloader.ProgressReporter`). Each event is sent as a typed message to the TUI loop.
3. **Live updates**: `runnerEventMsg` updates counters. `fileProgressMsg` updates per-file bars. A `tickMsg` timer samples throughput every 1s for EMA speed/ETA calculation.
4. **Completion**: `runnerDoneMsg` records the run to history and triggers a desktop notification via `beeep`.
5. **Teardown**: User presses `esc`/`backspace`/`h`/`q` to return to main menu.

### Throughput calculation

Bytes written are sampled every 1s via `tickMsg`. The speed is calculated as an exponential moving average (EMA) for display stability:

```
speedBps = speedBps * 0.6 + instantBps * 0.4
ETA = (totalRemaining) / speedBps
```
