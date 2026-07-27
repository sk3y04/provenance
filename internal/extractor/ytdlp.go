// Package extractor implements download and scan operations for Twitter/X, Reddit, Instagram,
// yt-dlp supported sites, and JS-heavy sites via headless Chrome.
package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lrstanley/go-ytdlp"

	"github.com/sk3y04/provenance/internal/downloader"
	"github.com/sk3y04/provenance/internal/manifest"
	"github.com/sk3y04/provenance/internal/resolve"
)

// ffmpeg detection (cached). go-ytdlp does NOT auto-install ffmpeg, so we
// degrade gracefully when it's missing.
var (
	ffmpegOnce sync.Once
	ffmpegPath string
	ffmpegWarn sync.Once
)

func resolveFfmpeg() string {
	ffmpegOnce.Do(func() {
		if p, err := exec.LookPath("ffmpeg"); err == nil {
			ffmpegPath = p
		}
	})
	return ffmpegPath
}

func hasFfmpeg() bool { return resolveFfmpeg() != "" }

// YtdlpOptions controls how the yt-dlp wrapper is invoked.
type YtdlpOptions struct {
	OutputDir          string // base output directory
	CookiesFile        string // optional Netscape cookies file
	CookiesFromBrowser string // optional browser name for yt-dlp --cookies-from-browser
	Quality            string // best | 1080 | 720 | 480
	AudioOnly          bool
	DryRun             bool
	DownloadArchive    string // path for yt-dlp --download-archive (skip already-downloaded IDs)
	OutputLayout       string // default | flat | site | creator | date
	OutputTemplate     string // raw yt-dlp output template, relative to OutputDir unless absolute
	SpeedLimit         int64  // bytes per second; 0 means unlimited
	Progress           downloader.ProgressReporter
	// MetadataDir is the subdirectory (relative to OutputDir) where yt-dlp
	// sidecar files like *.info.json, *.description, *.jpg thumbnails, and
	// subtitles will be placed. Empty means "_metadata"; set to "." to keep
	// the legacy behaviour of writing them next to the media file.
	MetadataDir string
}

const ytdlpProgressMarker = "PROVENANCE_YTDLP_PROGRESS:"

// EnsureInstalled installs yt-dlp + ffmpeg into the local cache if missing.
// Safe to call multiple times.
func EnsureInstalled(ctx context.Context) error {
	ytdlp.MustInstall(ctx, nil)
	return nil
}

// formatSelector translates a quality string into a yt-dlp format selector.
// When ffmpeg is missing we must pick a single pre-muxed file because yt-dlp
// can't merge separate video+audio streams without it.
func formatSelector(quality string, ffmpeg bool) string {
	if !ffmpeg {
		switch strings.ToLower(quality) {
		case "1080":
			return "best[height<=1080][ext=mp4]/best[height<=1080]/best"
		case "720":
			return "best[height<=720][ext=mp4]/best[height<=720]/best"
		case "480":
			return "best[height<=480][ext=mp4]/best[height<=480]/best"
		default:
			return "best[ext=mp4]/best"
		}
	}
	switch strings.ToLower(quality) {
	case "1080":
		return "bestvideo[height<=1080]+bestaudio/best[height<=1080]"
	case "720":
		return "bestvideo[height<=720]+bestaudio/best[height<=720]"
	case "480":
		return "bestvideo[height<=480]+bestaudio/best[height<=480]"
	default:
		return "bestvideo+bestaudio/best"
	}
}

// RunYtdlp invokes yt-dlp on the given URL. stdout/stderr are streamed to the
// terminal in real time. Errors include any text yt-dlp wrote to stderr that
// matches typical "site not supported" patterns, so the dispatcher can decide
// whether to fall back to the browser scraper.
func RunYtdlp(ctx context.Context, rawURL string, opts YtdlpOptions) error {
	_, err := runYtdlpInternal(ctx, rawURL, opts)
	return err
}

// runYtdlpInternal does the actual work. It tees stderr to both the user's
// terminal and an in-memory buffer; on failure the captured stderr is folded
// into the returned error so callers can pattern-match it.
func runYtdlpInternal(ctx context.Context, rawURL string, opts YtdlpOptions) (string, error) {
	if err := EnsureInstalled(ctx); err != nil {
		return "", fmt.Errorf("install yt-dlp: %w", err)
	}

	ffmpeg := hasFfmpeg()
	if !ffmpeg {
		ffmpegWarn.Do(func() {
			fmt.Fprintln(os.Stderr,
				"[provenance] WARNING: ffmpeg not found on PATH. Falling back to single-file "+
					"downloads (no merge, no thumbnail embed, no audio-only conversion).")
			fmt.Fprintln(os.Stderr,
				"[provenance]   Install ffmpeg for full quality:")
			fmt.Fprintln(os.Stderr,
				"[provenance]     Windows: winget install Gyan.FFmpeg   (or)   choco install ffmpeg")
			fmt.Fprintln(os.Stderr,
				"[provenance]     macOS:   brew install ffmpeg")
			fmt.Fprintln(os.Stderr,
				"[provenance]     Linux:   sudo apt install ffmpeg")
		})
	}

	cmd := ytdlp.New()

	outDir := opts.OutputDir
	if outDir == "" {
		outDir = "downloads"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir output: %w", err)
	}

	tmpl := renderYtdlpOutputTemplate(outDir, opts)
	cmd = cmd.Output(tmpl)

	if opts.AudioOnly {
		if !ffmpeg {
			return "", fmt.Errorf("--audio-only requires ffmpeg; please install ffmpeg first")
		}
		cmd = cmd.ExtractAudio().AudioFormat("mp3").AudioQuality("0")
	} else {
		cmd = cmd.Format(formatSelector(opts.Quality, ffmpeg))
		if ffmpeg {
			cmd = cmd.MergeOutputFormat("mp4").EmbedThumbnail()
		}
	}

	cmd = cmd.WriteInfoJSON()

	if opts.DownloadArchive != "" {
		cmd = cmd.DownloadArchive(opts.DownloadArchive)
	}

	if ffmpeg {
		cmd = cmd.FFmpegLocation(resolveFfmpeg())
	}

	if opts.CookiesFile != "" {
		cmd = cmd.Cookies(opts.CookiesFile)
	}

	if opts.DryRun {
		cmd = cmd.Simulate().PrintJSON()
	}

	// Build the underlying *exec.Cmd so we can stream stdout/stderr live AND
	// capture stderr for post-mortem inspection by the dispatcher.
	execCmd := cmd.BuildCommand(ctx, rawURL)
	addYtdlpArgs(execCmd, rawURL, cookiesFromBrowserArgs(opts.CookiesFromBrowser)...)
	addYtdlpArgs(execCmd, rawURL, speedLimitArgs(opts.SpeedLimit)...)
	addYtdlpArgs(execCmd, rawURL, metadataPathArgs(outDir, opts.MetadataDir)...)
	if opts.Progress != nil && !opts.DryRun {
		addYtdlpArgs(execCmd, rawURL, ytdlpProgressArgs()...)
	}
	var stderrBuf bytes.Buffer
	stdoutWriter := newYtdlpProgressWriter(rawURL, opts.Progress, os.Stdout, nil)
	stderrWriter := newYtdlpProgressWriter(rawURL, opts.Progress, os.Stderr, &stderrBuf)
	execCmd.Stdout = stdoutWriter
	execCmd.Stderr = stderrWriter
	if err := execCmd.Run(); err != nil {
		stdoutWriter.finishAll(err)
		stderrWriter.finishAll(err)
		return stderrBuf.String(), fmt.Errorf("yt-dlp: %w", err)
	}
	stdoutWriter.finishAll(nil)
	stderrWriter.finishAll(nil)
	return stderrBuf.String(), nil
}

func ytdlpProgressArgs() []string {
	return []string{
		"--newline",
		"--progress-template", "download:" + ytdlpProgressMarker + "%(progress)j",
	}
}

// metadataPathArgs builds `--paths TYPE:DIR` flags so yt-dlp writes sidecar
// files (info JSON, description, thumbnails, subtitles, annotations) into a
// dedicated subfolder rather than littering the media directory.
//
// Returns nil when the user explicitly opts out (metaDir == ".") or when the
// resolved directory is empty.
func metadataPathArgs(outDir, metaDir string) []string {
	metaDir = strings.TrimSpace(metaDir)
	if metaDir == "" {
		metaDir = "_metadata"
	}
	if metaDir == "." {
		return nil
	}
	target := metaDir
	if !filepath.IsAbs(target) {
		base := strings.TrimSpace(outDir)
		if base == "" {
			base = "downloads"
		}
		target = filepath.Join(base, metaDir)
	}
	types := []string{"infojson", "description", "thumbnail", "subtitle", "annotation", "pl_thumbnail", "pl_description", "pl_infojson"}
	args := make([]string, 0, len(types)*2)
	for _, t := range types {
		args = append(args, "--paths", t+":"+target)
	}
	return args
}

type ytdlpProgressWriter struct {
	rep      downloader.ProgressReporter
	fallback io.Writer
	capture  io.Writer
	source   string

	mu      sync.Mutex
	partial []byte
	active  map[string]struct{}
	started map[string]struct{}
	last    map[string]time.Time
}

func newYtdlpProgressWriter(source string, rep downloader.ProgressReporter, fallback, capture io.Writer) *ytdlpProgressWriter {
	return &ytdlpProgressWriter{
		rep:      rep,
		fallback: fallback,
		capture:  capture,
		source:   source,
		active:   map[string]struct{}{},
		started:  map[string]struct{}{},
		last:     map[string]time.Time{},
	}
}

func (w *ytdlpProgressWriter) Write(p []byte) (int, error) {
	if w.rep == nil {
		if w.fallback != nil {
			_, _ = w.fallback.Write(p)
		}
		if w.capture != nil {
			_, _ = w.capture.Write(p)
		}
		return len(p), nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.partial = append(w.partial, p...)
	for {
		idx := bytes.IndexByte(w.partial, '\n')
		if idx < 0 {
			break
		}
		line := append([]byte(nil), w.partial[:idx+1]...)
		w.partial = w.partial[idx+1:]
		w.handleLine(line)
	}
	return len(p), nil
}

func (w *ytdlpProgressWriter) finishAll(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.partial) > 0 {
		w.handleLine(append([]byte(nil), w.partial...))
		w.partial = nil
	}
	if w.rep == nil {
		return
	}
	for key := range w.active {
		w.rep.OnDone(key, err)
	}
	w.active = map[string]struct{}{}
}

func (w *ytdlpProgressWriter) handleLine(line []byte) {
	text := strings.TrimSpace(stripCR(string(line)))
	if w.rep != nil {
		if i := strings.Index(text, ytdlpProgressMarker); i >= 0 {
			payload := strings.TrimSpace(text[i+len(ytdlpProgressMarker):])
			if w.handleProgress(payload) {
				// Do not forward machine-readable progress JSON to stderr/capture;
				// the TUI already renders it as bars and the dispatcher fallback
				// heuristic should not see it as a real yt-dlp error.
				return
			}
		}
	}
	if w.fallback != nil {
		_, _ = w.fallback.Write(line)
	}
	if w.capture != nil {
		_, _ = w.capture.Write(line)
	}
}

func (w *ytdlpProgressWriter) handleProgress(payload string) bool {
	var p ytdlpProgressPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return false
	}
	key := firstNonEmpty(p.Filename, p.TmpFilename, w.source)
	if key == "" {
		key = w.source
	}
	dest := firstNonEmpty(p.Filename, p.TmpFilename, key)
	total := p.total()
	written := p.downloaded()
	if _, ok := w.started[key]; !ok {
		w.started[key] = struct{}{}
		w.active[key] = struct{}{}
		w.rep.OnStart(key, dest, total)
	}
	status := strings.ToLower(strings.TrimSpace(p.Status))
	if status == "finished" && total <= 0 && written > 0 {
		total = written
	}
	if written > 0 || total > 0 {
		// yt-dlp can emit progress very rapidly; throttle to keep the TUI smooth.
		now := time.Now()
		if last := w.last[key]; last.IsZero() || now.Sub(last) >= 250*time.Millisecond || status == "finished" {
			w.last[key] = now
			w.rep.OnProgress(key, written, total)
		}
	}
	if status == "finished" {
		delete(w.active, key)
		w.rep.OnDone(key, nil)
	}
	return true
}

type ytdlpProgressPayload struct {
	Status             string          `json:"status"`
	Filename           string          `json:"filename"`
	TmpFilename        string          `json:"tmpfilename"`
	DownloadedBytes    flexibleInt64   `json:"downloaded_bytes"`
	TotalBytes         flexibleInt64   `json:"total_bytes"`
	TotalBytesEstimate flexibleInt64   `json:"total_bytes_estimate"`
	FragmentIndex      flexibleInt64   `json:"fragment_index"`
	FragmentCount      flexibleInt64   `json:"fragment_count"`
	Raw                json.RawMessage `json:"-"`
}

func (p ytdlpProgressPayload) downloaded() int64 { return int64(p.DownloadedBytes) }

func (p ytdlpProgressPayload) total() int64 {
	if p.TotalBytes > 0 {
		return int64(p.TotalBytes)
	}
	if p.TotalBytesEstimate > 0 {
		return int64(p.TotalBytesEstimate)
	}
	return 0
}

type flexibleInt64 int64

func (n *flexibleInt64) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" || s == `"NA"` || s == `"N/A"` {
		*n = 0
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var v string
		if err := json.Unmarshal(data, &v); err != nil {
			return nil
		}
		s = strings.TrimSpace(v)
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		*n = flexibleInt64(i)
		return nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		*n = flexibleInt64(int64(f))
		return nil
	}
	*n = 0
	return nil
}

func stripCR(s string) string {
	return strings.ReplaceAll(s, "\r", "")
}

// RunYtdlpCaptured behaves like RunYtdlp but also returns whatever yt-dlp
// wrote to stderr. Useful for the dispatcher's "is this site unsupported?"
// heuristic.
func RunYtdlpCaptured(ctx context.Context, rawURL string, opts YtdlpOptions) (stderr string, err error) {
	return runYtdlpInternal(ctx, rawURL, opts)
}

type ytdlpInfo struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	WebpageURL  string  `json:"webpage_url"`
	OriginalURL string  `json:"original_url"`
	URL         string  `json:"url"`
	Extractor   string  `json:"extractor"`
	Uploader    string  `json:"uploader"`
	Ext         string  `json:"ext"`
	Filesize    int64   `json:"filesize"`
	FilesizeAlt int64   `json:"filesize_approx"`
	UploadDate  string  `json:"upload_date"`
	Duration    float64 `json:"duration"`
}

func ScanYtdlp(ctx context.Context, rawURL string, opts YtdlpOptions) (manifest.Manifest, error) {
	if err := EnsureInstalled(ctx); err != nil {
		return manifest.Manifest{}, fmt.Errorf("install yt-dlp: %w", err)
	}
	cmd := ytdlp.New().Simulate().PrintJSON()
	if opts.CookiesFile != "" {
		cmd = cmd.Cookies(opts.CookiesFile)
	}
	execCmd := cmd.BuildCommand(ctx, rawURL)
	addYtdlpArgs(execCmd, rawURL, cookiesFromBrowserArgs(opts.CookiesFromBrowser)...)
	addYtdlpArgs(execCmd, rawURL, speedLimitArgs(opts.SpeedLimit)...)
	var stdoutBuf, stderrBuf bytes.Buffer
	execCmd.Stdout = &stdoutBuf
	execCmd.Stderr = &stderrBuf
	if err := execCmd.Run(); err != nil {
		return manifest.Manifest{}, fmt.Errorf("yt-dlp scan: %w: %s", err, strings.TrimSpace(stderrBuf.String()))
	}
	var items []manifest.Item
	for _, line := range strings.Split(stdoutBuf.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var info ytdlpInfo
		if err := json.Unmarshal([]byte(line), &info); err != nil {
			continue
		}
		u := firstNonEmpty(info.WebpageURL, info.OriginalURL, info.URL, rawURL)
		size := info.Filesize
		if size == 0 {
			size = info.FilesizeAlt
		}
		items = append(items, manifest.Item{
			ID:        info.ID,
			URL:       u,
			Title:     info.Title,
			Filename:  info.Title + "." + info.Ext,
			Extension: info.Ext,
			Size:      size,
			Source:    firstNonEmpty(info.Extractor, "yt-dlp"),
			Creator:   info.Uploader,
			Kind:      "video",
		})
	}
	if len(items) == 0 {
		items = append(items, manifest.Item{URL: rawURL, Title: rawURL, Source: "yt-dlp", Kind: "url"})
	}
	return manifest.New(rawURL, "yt-dlp", items), nil
}

func renderYtdlpOutputTemplate(outDir string, opts YtdlpOptions) string {
	if outDir == "" {
		outDir = "downloads"
	}
	if strings.TrimSpace(opts.OutputTemplate) != "" {
		tmpl := opts.OutputTemplate
		if filepath.IsAbs(tmpl) {
			return tmpl
		}
		return filepath.Join(outDir, filepath.FromSlash(tmpl))
	}
	switch strings.ToLower(strings.TrimSpace(opts.OutputLayout)) {
	case "flat":
		return filepath.Join(outDir, "%(title)s.%(ext)s")
	case "site":
		return filepath.Join(outDir, "%(extractor)s/%(title)s.%(ext)s")
	case "date":
		return filepath.Join(outDir, "%(upload_date>%Y-%m)s/%(title)s.%(ext)s")
	case "creator", "":
		fallthrough
	default:
		return filepath.Join(outDir, "%(extractor)s/%(uploader)s/%(title)s.%(ext)s")
	}
}

func cookiesFromBrowserArgs(browser string) []string {
	browser = strings.TrimSpace(browser)
	if browser == "" {
		return nil
	}
	return []string{"--cookies-from-browser", browser}
}

func speedLimitArgs(limit int64) []string {
	if limit <= 0 {
		return nil
	}
	return []string{"--limit-rate", fmt.Sprintf("%d", limit)}
}

func addYtdlpArgs(cmd *exec.Cmd, rawURL string, args ...string) {
	if len(args) == 0 || cmd == nil {
		return
	}
	idx := len(cmd.Args)
	for i := len(cmd.Args) - 1; i >= 0; i-- {
		if cmd.Args[i] == rawURL {
			idx = i
			break
		}
	}
	newArgs := make([]string, 0, len(cmd.Args)+len(args))
	newArgs = append(newArgs, cmd.Args[:idx]...)
	newArgs = append(newArgs, args...)
	newArgs = append(newArgs, cmd.Args[idx:]...)
	cmd.Args = newArgs
}

func YtdlpInfoToSource(info ytdlpInfo, rawURL string) (resolve.Source, []resolve.Item) {
	canonicalURL := firstNonEmpty(info.WebpageURL, info.OriginalURL, info.URL, rawURL)
	src := resolve.NewSource(rawURL, canonicalURL, resolve.KindSingle, info.Extractor)
	if info.Title != "" {
		src.Title = info.Title
	}
	if info.Uploader != "" {
		src.Author = info.Uploader
	}

	var published *time.Time
	if info.UploadDate != "" {
		if t, err := time.Parse("20060102", info.UploadDate); err == nil {
			published = &t
		}
	}

	u := firstNonEmpty(info.WebpageURL, info.OriginalURL, info.URL, rawURL)
	size := info.Filesize
	if size == 0 {
		size = info.FilesizeAlt
	}
	ext := info.Ext

	kind := resolve.MediaVideo
	switch strings.ToLower(info.Ext) {
	case "mp3", "m4a", "opus", "ogg", "flac", "wav", "aac", "wma", "weba":
		kind = resolve.MediaAudio
	}

	item := resolve.NewItem(info.ID, u)
	item.Title = info.Title
	item.Author = info.Uploader
	item.PublishedAt = published
	item.Media = []resolve.MediaAsset{{
		URL:       info.URL,
		Filename:  info.Title + "." + ext,
		Extension: ext,
		Size:      size,
		Kind:      kind,
	}}
	if raw, err := json.Marshal(info); err == nil {
		item.RawMetadata = raw
	}

	return src, []resolve.Item{item}
}

func ScanYtdlpResolved(ctx context.Context, rawURL string, opts YtdlpOptions) (resolve.Source, error) {
	if err := EnsureInstalled(ctx); err != nil {
		return resolve.Source{}, fmt.Errorf("install yt-dlp: %w", err)
	}
	cmd := ytdlp.New().Simulate().PrintJSON()
	if opts.CookiesFile != "" {
		cmd = cmd.Cookies(opts.CookiesFile)
	}
	execCmd := cmd.BuildCommand(ctx, rawURL)
	addYtdlpArgs(execCmd, rawURL, cookiesFromBrowserArgs(opts.CookiesFromBrowser)...)
	addYtdlpArgs(execCmd, rawURL, speedLimitArgs(opts.SpeedLimit)...)
	var stdoutBuf, stderrBuf bytes.Buffer
	execCmd.Stdout = &stdoutBuf
	execCmd.Stderr = &stderrBuf
	if err := execCmd.Run(); err != nil {
		return resolve.Source{}, fmt.Errorf("yt-dlp scan: %w: %s", err, strings.TrimSpace(stderrBuf.String()))
	}
	src := resolve.NewSource(rawURL, "", resolve.KindSingle, "yt-dlp")
	itemCount := 0
	for _, line := range strings.Split(stdoutBuf.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var info ytdlpInfo
		if err := json.Unmarshal([]byte(line), &info); err != nil {
			continue
		}
		itemCount++
		if itemCount == 1 {
			playlistExtractors := map[string]bool{"youtube:tab": true, "youtube:playlist": true}
			if playlistExtractors[info.Extractor] {
				src.Kind = resolve.KindPlaylist
			}
		}
		s, items := YtdlpInfoToSource(info, rawURL)
		src.CanonicalURL = s.CanonicalURL
		src.Title = s.Title
		src.Author = s.Author
		src.Extractor = s.Extractor
		src.Items = append(src.Items, items...)
	}
	if len(src.Items) == 0 {
		src.Items = append(src.Items, resolve.NewItem(rawURL, rawURL))
		src.Title = rawURL
	}
	return src, nil
}
