// Package dispatcher classifies URLs, routes them to the correct extractor, and manages batch and single-URL download workflows.
package dispatcher

import (
	"bufio"
	"context"
	"crypto/sha1"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sk3y04/provenance/internal/config"
	"github.com/sk3y04/provenance/internal/downloader"
	"github.com/sk3y04/provenance/internal/extractor"
	"github.com/sk3y04/provenance/internal/manifest"
	"github.com/sk3y04/provenance/internal/ratelimit"
	"github.com/sk3y04/provenance/internal/resolve"
	"github.com/sk3y04/provenance/internal/worker"
)

// Options bundles the serializable config with runtime-only fields.
// The embedded Config is the single source of truth for fields that can be
// persisted in sessions, watches, and history.
type Options struct {
	config.Config

	DryRun      bool
	Reporter    Reporter
	RateLimiter *ratelimit.Manager
	// FileProgress, when set, receives per-file lifecycle events from the
	// HTTP downloader and yt-dlp structured progress output.
	FileProgress downloader.ProgressReporter
}

// Reporter receives user-visible URL lifecycle events. It is intentionally tiny
// so callers can persist resumable sessions without coupling dispatcher to the
// CLI package.
type Reporter interface {
	Queue(url, source string)
	Start(url string)
	Success(url string)
	Failure(url string, err error)
	Skip(url, reason string)
}

type Counts struct {
	Discovered atomic.Int64
	Succeeded  atomic.Int64
	Failed     atomic.Int64
	Skipped    atomic.Int64
}

type CountsReporter struct {
	Inner  Reporter
	Counts Counts
}

func NewCountsReporter(inner Reporter) *CountsReporter {
	return &CountsReporter{Inner: inner}
}

func (r *CountsReporter) Queue(url, source string) {
	if r.Inner != nil {
		r.Inner.Queue(url, source)
	}
	r.Counts.Discovered.Add(1)
}

func (r *CountsReporter) Start(url string) {
	if r.Inner != nil {
		r.Inner.Start(url)
	}
}

func (r *CountsReporter) Success(url string) {
	if r.Inner != nil {
		r.Inner.Success(url)
	}
	r.Counts.Succeeded.Add(1)
}

func (r *CountsReporter) Failure(url string, err error) {
	if r.Inner != nil {
		r.Inner.Failure(url, err)
	}
	r.Counts.Failed.Add(1)
}

func (r *CountsReporter) Skip(url, reason string) {
	if r.Inner != nil {
		r.Inner.Skip(url, reason)
	}
	r.Counts.Skipped.Add(1)
}

func PrintSummary(c *Counts, outputDir, sessionName string) {
	_, _ = fmt.Fprintf(os.Stderr, "\n")
	_, _ = fmt.Fprintf(os.Stderr, "discovered: %d\n", c.Discovered.Load())
	_, _ = fmt.Fprintf(os.Stderr, "downloaded: %d\n", c.Succeeded.Load())
	_, _ = fmt.Fprintf(os.Stderr, "skipped:    %d\n", c.Skipped.Load())
	_, _ = fmt.Fprintf(os.Stderr, "failed:     %d\n", c.Failed.Load())
	_, _ = fmt.Fprintf(os.Stderr, "output:     %s\n", outputDir)
	if sessionName != "" {
		_, _ = fmt.Fprintf(os.Stderr, "session:    %s\n", sessionName)
	}
}

// Site identifies the destination extractor.
type Site int

const (
	SiteUnknown Site = iota
	SitePatreon
	SiteTwitter
	SiteReddit
	SiteInstagram
	SiteGeneric
)

func (s Site) String() string {
	switch s {
	case SitePatreon:
		return "patreon"
	case SiteTwitter:
		return "twitter"
	case SiteReddit:
		return "reddit"
	case SiteInstagram:
		return "instagram"
	case SiteGeneric:
		return "generic"
	}
	return "unknown"
}

// Classify returns the Site for a URL based on the hostname.
func Classify(rawURL string) (Site, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return SiteUnknown, fmt.Errorf("parse url: %w", err)
	}
	host := strings.ToLower(u.Host)
	switch {
	case strings.Contains(host, "patreon"):
		return SitePatreon, nil
	case strings.Contains(host, "x.com") || strings.Contains(host, "twitter"):
		return SiteTwitter, nil
	case strings.Contains(host, "reddit"):
		return SiteReddit, nil
	case strings.Contains(host, "instagram"):
		return SiteInstagram, nil
	default:
		return SiteGeneric, nil
	}
}

// Dispatch routes `rawURL` to the appropriate extractor based on its hostname.
func Dispatch(ctx context.Context, rawURL string, opts Options) (err error) {
	if opts.Reporter != nil {
		opts.Reporter.Queue(rawURL, "input")
		opts.Reporter.Start(rawURL)
		defer func() {
			if err != nil {
				opts.Reporter.Failure(rawURL, err)
			} else {
				opts.Reporter.Success(rawURL)
			}
		}()
	}

	site, err := Classify(rawURL)
	if err != nil {
		return err
	}

	ytOpts := extractor.YtdlpOptions{
		OutputDir:          opts.OutputDir,
		CookiesFile:        opts.CookiesFile,
		CookiesFromBrowser: opts.CookiesFromBrowser,
		Quality:            opts.Quality,
		AudioOnly:          opts.AudioOnly,
		DryRun:             opts.DryRun,
		OutputLayout:       opts.OutputLayout,
		OutputTemplate:     opts.OutputTemplate,
		SpeedLimit:         opts.SpeedLimit,
		Progress:           opts.FileProgress,
	}

	switch site {
	case SitePatreon:
		return extractor.RunYtdlp(ctx, rawURL, ytOpts)

	case SiteTwitter:
		return extractor.DownloadTwitter(ctx, rawURL, opts.OutputDir, opts.CookiesFile, extractor.TwOptions{
			Filter:       opts.Filter,
			SpeedLimit:   opts.SpeedLimit,
			Progress:     opts.FileProgress,
			Limit:        opts.PostLimit,
			RateLimiter:  opts.RateLimiter,
			IncludePosts: opts.IncludePosts,
		}, opts.DryRun)

	case SiteReddit:
		return extractor.DownloadReddit(ctx, rawURL, opts.OutputDir, opts.CookiesFile, extractor.RdOptions{
			Filter:          opts.Filter,
			SpeedLimit:      opts.SpeedLimit,
			Progress:        opts.FileProgress,
			RateLimiter:     opts.RateLimiter,
			IncludePosts:    opts.IncludePosts,
			IncludeComments: opts.IncludeComments,
			CommentLimit:    opts.CommentLimit,
		}, opts.DryRun)

	case SiteInstagram:
		return extractor.DownloadInstagram(ctx, rawURL, opts.OutputDir, opts.CookiesFile, extractor.IgOptions{
			Filter:       opts.Filter,
			SpeedLimit:   opts.SpeedLimit,
			Progress:     opts.FileProgress,
			Limit:        opts.PostLimit,
			RateLimiter:  opts.RateLimiter,
			IncludePosts: opts.IncludePosts,
		}, opts.DryRun)

	case SiteGeneric:
		stderr, err := extractor.RunYtdlpCaptured(ctx, rawURL, ytOpts)
		if err == nil {
			return nil
		}
		combined := strings.ToLower(err.Error() + "\n" + stderr)
		if !shouldBrowserFallback(combined) {
			return err
		}
		return browserFallback(ctx, rawURL, opts, ytOpts, err)
	}
	return fmt.Errorf("no extractor for url: %s", rawURL)
}

func browserFallback(ctx context.Context, rawURL string, opts Options, ytOpts extractor.YtdlpOptions, originalErr error) error {
	if !extractor.IsBrowserCandidate(rawURL) {
		return fmt.Errorf("browser fallback unavailable: %w", originalErr)
	}

	fmt.Println("[provenance] crawling the page (and any pagination) for video links...")
	links, scrapeErr := extractor.ScrapePlaylistVideoLinks(ctx, rawURL, opts.CookiesFile, opts.ChromePath, 30*time.Second, 500)
	if scrapeErr == nil && len(links) > 0 {
		if cachePath, cerr := writeLinkCache(opts.OutputDir, rawURL, links); cerr == nil {
			fmt.Fprintf(os.Stderr, "[provenance] saved %d link(s) to %s\n", len(links), cachePath)
			fmt.Fprintf(os.Stderr, "[provenance]   (re-run later with: provenance grab --batch %q)\n", cachePath)
		} else {
			fmt.Fprintf(os.Stderr, "[provenance] WARNING: could not save link cache: %v\n", cerr)
		}
		return downloadVideoLinks(ctx, links, opts)
	}
	if scrapeErr != nil {
		fmt.Printf("[provenance] link scrape failed: %v - falling back to media-URL sniffer\n", scrapeErr)
	} else {
		fmt.Println("[provenance] no video links found on page - falling back to media-URL sniffer")
	}

	resolved, berr := extractor.SniffOrFallback(ctx, rawURL, opts.CookiesFile, opts.ChromePath)
	if berr != nil {
		return fmt.Errorf("browser fallback: %w (original error: %v)", berr, originalErr)
	}
	fmt.Printf("[provenance] sniffed media URL: %s\n", resolved.MediaURL)
	return extractor.RunYtdlp(ctx, resolved.MediaURL, ytOpts)
}

// Scan discovers downloadable items without writing media files. Native
// extractors return richer metadata; generic URLs use yt-dlp's JSON simulation
// where possible and fall back to a single URL item.
func Scan(ctx context.Context, rawURL string, opts Options) (manifest.Manifest, error) {
	site, err := Classify(rawURL)
	if err != nil {
		return manifest.Manifest{}, err
	}
	var m manifest.Manifest
	switch site {
	case SiteTwitter:
		m, err = extractor.ScanTwitter(ctx, rawURL, opts.OutputDir, opts.CookiesFile, extractor.TwOptions{
			Filter:       opts.Filter,
			Limit:        opts.PostLimit,
			RateLimiter:  opts.RateLimiter,
			IncludePosts: opts.IncludePosts,
		})
	case SiteReddit:
		m, err = extractor.ScanReddit(ctx, rawURL, opts.OutputDir, opts.CookiesFile, extractor.RdOptions{
			Filter:          opts.Filter,
			RateLimiter:     opts.RateLimiter,
			IncludePosts:    opts.IncludePosts,
			IncludeComments: opts.IncludeComments,
			CommentLimit:    opts.CommentLimit,
			Limit:           opts.PostLimit,
		})
	case SiteInstagram:
		m, err = extractor.ScanInstagram(ctx, rawURL, opts.OutputDir, opts.CookiesFile, extractor.IgOptions{
			Filter:       opts.Filter,
			Limit:        opts.PostLimit,
			RateLimiter:  opts.RateLimiter,
			IncludePosts: opts.IncludePosts,
		})
	default:
		m, err = extractor.ScanYtdlp(ctx, rawURL, extractor.YtdlpOptions{
			OutputDir:          opts.OutputDir,
			CookiesFile:        opts.CookiesFile,
			CookiesFromBrowser: opts.CookiesFromBrowser,
			Quality:            opts.Quality,
			AudioOnly:          opts.AudioOnly,
			OutputLayout:       opts.OutputLayout,
			OutputTemplate:     opts.OutputTemplate,
			SpeedLimit:         opts.SpeedLimit,
		})
	}
	if err != nil {
		return manifest.Manifest{}, err
	}
	return m.Filter(opts.Filter)
}

func ScanResolved(ctx context.Context, rawURL string, opts Options) (resolve.Source, error) {
	site, err := Classify(rawURL)
	if err != nil {
		return resolve.Source{}, err
	}
	switch site {
	case SiteTwitter:
		return extractor.ScanTwitterResolved(ctx, rawURL, opts.OutputDir, opts.CookiesFile, extractor.TwOptions{
			Filter:       opts.Filter,
			Limit:        opts.PostLimit,
			RateLimiter:  opts.RateLimiter,
			IncludePosts: opts.IncludePosts,
		})
	case SiteReddit:
		return extractor.ScanRedditResolved(ctx, rawURL, opts.OutputDir, opts.CookiesFile, extractor.RdOptions{
			Filter:          opts.Filter,
			RateLimiter:     opts.RateLimiter,
			Limit:           opts.PostLimit,
			IncludePosts:    opts.IncludePosts,
			IncludeComments: opts.IncludeComments,
			CommentLimit:    opts.CommentLimit,
		})
	case SiteInstagram:
		return extractor.ScanInstagramResolved(ctx, rawURL, opts.OutputDir, opts.CookiesFile, extractor.IgOptions{
			Filter:       opts.Filter,
			Limit:        opts.PostLimit,
			RateLimiter:  opts.RateLimiter,
			IncludePosts: opts.IncludePosts,
		})
	default:
		return extractor.ScanYtdlpResolved(ctx, rawURL, extractor.YtdlpOptions{
			OutputDir:          opts.OutputDir,
			CookiesFile:        opts.CookiesFile,
			CookiesFromBrowser: opts.CookiesFromBrowser,
			Quality:            opts.Quality,
			AudioOnly:          opts.AudioOnly,
			OutputLayout:       opts.OutputLayout,
			OutputTemplate:     opts.OutputTemplate,
			SpeedLimit:         opts.SpeedLimit,
		})
	}
}

// shouldBrowserFallback returns true when yt-dlp's error/stderr suggests the
// site simply isn't supported, as opposed to a real download/post-processing
// failure (in which case we want to surface the original error). The caller
// passes a single lower-cased haystack containing both the Go error string and
// the captured yt-dlp stderr.
func shouldBrowserFallback(haystack string) bool {
	if haystack == "" {
		return false
	}
	indicators := []string{
		"unsupported url",
		"no video formats found",
		"unable to extract",
		"no suitable extractor",
		"is not a valid url",
	}
	for _, s := range indicators {
		if strings.Contains(haystack, s) {
			return true
		}
	}
	return false
}

// downloadVideoLinks downloads each link via yt-dlp using a worker pool sized
// by opts.Concurrency. Each link is fed straight to RunYtdlp (NOT through the
// full Dispatch path) because we already know yt-dlp's generic extractor can
// handle individual video pages - going through Dispatch again would risk a
// recursive browser crawl on a transient yt-dlp failure.
func downloadVideoLinks(ctx context.Context, links []string, opts Options) error {
	links = dedupeLinks(links)
	if len(links) > 0 {
		items := make([]manifest.Item, 0, len(links))
		for _, link := range links {
			items = append(items, manifest.Item{URL: link, Filename: filepath.Base(strings.Split(link, "?")[0]), Source: "links", Kind: "video"})
		}
		filtered, err := manifest.FilterItems(items, opts.Filter)
		if err != nil {
			return err
		}
		links = links[:0]
		for _, it := range filtered {
			links = append(links, it.URL)
		}
	}
	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	outDir := opts.OutputDir
	if outDir == "" {
		outDir = "downloads"
	}

	// ------------------------------------------------------------------
	// Archives
	// ------------------------------------------------------------------
	// 1. provenance URL archive: skips URLs that already succeeded, across runs.
	//    Permanent failures are recorded for visibility but retried by default
	//    on future runs (sites can restore removed files).
	// 2. yt-dlp download archive: tells yt-dlp not to re-download a video ID
	//    even if it's reached via a different URL.
	var grabArch *urlArchive
	var ytdlpArchivePath string
	if !opts.DryRun && !opts.NoArchive {
		cacheDir := filepath.Join(outDir, "_provenance_cache")
		_ = os.MkdirAll(cacheDir, 0o755)

		var err error
		grabArch, err = openURLArchive(filepath.Join(cacheDir, "archive.txt"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "[provenance] WARNING: could not open URL archive: %v\n", err)
			grabArch = nil
		}
		ytdlpArchivePath = filepath.Join(cacheDir, "ytdlp_archive.txt")
	}
	if grabArch != nil {
		defer grabArch.close()
	}

	// Filter out previously successful URLs.
	if grabArch != nil {
		before := len(links)
		var filtered []string
		for _, l := range links {
			if grabArch.hasSuccessful(l) {
				fmt.Fprintf(os.Stderr, "[provenance] skip (already downloaded): %s\n", l)
				if opts.Reporter != nil {
					opts.Reporter.Skip(l, "already downloaded")
				}
			} else {
				filtered = append(filtered, l)
			}
		}
		if skipped := before - len(filtered); skipped > 0 {
			fmt.Fprintf(os.Stderr, "[provenance] skipped %d already-downloaded URL(s)\n", skipped)
		}
		links = filtered
	}

	total := len(links)
	if total == 0 {
		fmt.Fprintln(os.Stderr, "[provenance] nothing to download (all URLs already in archive)")
		return nil
	}
	fmt.Fprintf(os.Stderr, "[provenance] downloading %d item(s) with concurrency=%d\n", total, concurrency)

	ytOpts := extractor.YtdlpOptions{
		OutputDir:          opts.OutputDir,
		CookiesFile:        opts.CookiesFile,
		CookiesFromBrowser: opts.CookiesFromBrowser,
		Quality:            opts.Quality,
		AudioOnly:          opts.AudioOnly,
		DryRun:             opts.DryRun,
		DownloadArchive:    ytdlpArchivePath,
		OutputLayout:       opts.OutputLayout,
		OutputTemplate:     opts.OutputTemplate,
		SpeedLimit:         opts.SpeedLimit,
		Progress:           opts.FileProgress,
	}

	pool := worker.NewPool(ctx, concurrency)

	var (
		started   atomic.Int64
		completed atomic.Int64
		failed    atomic.Int64
		firstErr  error
		errMu     sync.Mutex
	)

	for _, link := range links {
		link := link
		var attempt int64
		if opts.Reporter != nil {
			opts.Reporter.Queue(link, "discovered")
		}
		pool.SubmitWithHooks(func() error {
			a := atomic.AddInt64(&attempt, 1)
			if a == 1 {
				n := started.Add(1)
				fmt.Fprintf(os.Stderr, "[provenance] (%d/%d) starting: %s\n", n, total, link)
				if opts.Reporter != nil {
					opts.Reporter.Start(link)
				}
			} else {
				fmt.Fprintf(os.Stderr, "[provenance] retry %d/3 for %s\n", a, link)
			}

			stderr, err := extractor.RunYtdlpCaptured(ctx, link, ytOpts)
			if err == nil {
				return nil
			}
			if isPermanentYtdlpFailure(err, stderr) {
				return worker.Permanent(err)
			}
			return err
		}, func() {
			c := completed.Add(1)
			fmt.Fprintf(os.Stderr, "[provenance] OK (%d/%d done, %d failed): %s\n",
				c, total, failed.Load(), link)
			if opts.Reporter != nil {
				opts.Reporter.Success(link)
			}
			if grabArch != nil {
				grabArch.recordStatus(link, archiveStatusOK)
			}
		}, func(err error) {
			failed.Add(1)
			errMu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			errMu.Unlock()
			fmt.Fprintf(os.Stderr, "[provenance] FAILED %s: %v\n", link, err)
			if opts.Reporter != nil {
				opts.Reporter.Failure(link, err)
			}
			// Record permanent failures so they are skipped on the next run.
			if worker.IsPermanent(err) && grabArch != nil {
				grabArch.recordStatus(link, archiveStatusPermFail)
			}
		})
	}
	pool.Wait()

	fmt.Fprintf(os.Stderr, "[provenance] batch complete: %d/%d succeeded, %d failed\n",
		completed.Load(), total, failed.Load())
	return firstErr
}

// BatchDispatch reads URLs (one per line, blanks/# comments allowed) from
// `path` and downloads each in parallel using opts.Concurrency. It uses the
// same fast path as the playlist crawler: each URL is sent straight to yt-dlp.
// Non-yt-dlp-friendly URLs still go through the full Dispatch path so
// their custom extractors run.
func BatchDispatch(ctx context.Context, path string, opts Options) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open batch file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var urls []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		urls = append(urls, line)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read batch file: %w", err)
	}
	if len(urls) == 0 {
		return fmt.Errorf("batch file %q contained no URLs", path)
	}
	fmt.Fprintf(os.Stderr, "[provenance] batch: %d URL(s) loaded from %s\n", len(urls), path)

	// Decide per URL: yt-dlp-friendly hosts (anything that classifies as
	// SiteGeneric or SitePatreon) go straight to RunYtdlp in the worker pool.
	// URLs with custom extractors still need the full Dispatch path.
	var fast, slow []string
	for _, u := range urls {
		site, err := Classify(u)
		if err != nil {
			slow = append(slow, u)
			continue
		}
		switch site {
		case SiteTwitter, SiteReddit, SiteInstagram:
			slow = append(slow, u)
		default:
			fast = append(fast, u)
		}
	}

	var firstErr error
	if len(fast) > 0 {
		if err := downloadVideoLinks(ctx, fast, opts); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, u := range slow {
		if err := Dispatch(ctx, u, opts); err != nil {
			fmt.Fprintf(os.Stderr, "[provenance] %s: %v\n", u, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func dedupeLinks(links []string) []string {
	seen := make(map[string]struct{}, len(links))
	out := make([]string, 0, len(links))
	for _, link := range links {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}
		if _, ok := seen[link]; ok {
			continue
		}
		seen[link] = struct{}{}
		out = append(out, link)
	}
	return out
}

func isPermanentYtdlpFailure(err error, stderr string) bool {
	if err == nil {
		return false
	}
	haystack := strings.ToLower(err.Error() + "\n" + stderr)
	permanentIndicators := []string{
		"http error 410: gone",
		"http error 404: not found",
		"requested format is not available",
		"unsupported url",
		"this video is unavailable",
		"video unavailable",
		"private video",
		"members-only",
	}
	for _, indicator := range permanentIndicators {
		if strings.Contains(haystack, indicator) {
			return true
		}
	}
	return false
}

// writeLinkCache persists a discovered link list under
// `<outDir>/_provenance_cache/<host>__<short-hash>.txt` so the user can reuse it
// later via `provenance download --batch <file>`. Returns the absolute path of the
// written file.
func writeLinkCache(outDir, sourceURL string, links []string) (string, error) {
	if outDir == "" {
		outDir = "downloads"
	}
	dir := filepath.Join(outDir, "_provenance_cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	host := "links"
	if u, err := url.Parse(sourceURL); err == nil && u.Host != "" {
		host = strings.ReplaceAll(u.Host, ".", "_")
		// Append a tail of the path to disambiguate multiple playlists per host.
		tail := strings.TrimRight(u.Path, "/")
		if i := strings.LastIndex(tail, "/"); i >= 0 {
			tail = tail[i+1:]
		}
		if tail != "" {
			host += "_" + sanitizeFilename(tail)
		}
	}
	sum := sha1.Sum([]byte(sourceURL))
	name := fmt.Sprintf("%s__%x.txt", host, sum[:4])
	full := filepath.Join(dir, name)

	header := fmt.Sprintf("# provenance link cache\n# source: %s\n# count:  %d\n", sourceURL, len(links))
	body := strings.Join(links, "\n") + "\n"
	if err := os.WriteFile(full, []byte(header+body), 0o644); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return full, nil
	}
	return abs, nil
}

// sanitizeFilename replaces characters disallowed on common filesystems with
// underscores. Kept local to avoid dragging in the extractor package.
func sanitizeFilename(s string) string {
	repl := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_",
	)
	return repl.Replace(s)
}

// ---------------------------------------------------------------------------
// urlArchive - thread-safe URL archive persisted to a plain-text file.
// ---------------------------------------------------------------------------
//
// File format (one entry per line):
//
//	ok:<url>        - successfully downloaded
//	perm-fail:<url> - permanently failed in this run (record only)
//
// Every line not matching these prefixes is treated as "ok:<line>" for
// compatibility with manually edited files or old formats.
const (
	archiveStatusOK       = "ok"
	archiveStatusPermFail = "perm-fail"
)

type urlArchive struct {
	mu     sync.Mutex
	status map[string]string // url -> latest status token
	f      *os.File
	w      *bufio.Writer
}

func openURLArchive(path string) (*urlArchive, error) {
	a := &urlArchive{status: make(map[string]string)}

	// Load existing entries.
	if data, err := os.ReadFile(path); err == nil {
		for _, raw := range strings.Split(string(data), "\n") {
			line := strings.TrimSpace(raw)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			u, st := archiveEntry(line)
			if u != "" {
				a.status[u] = st
			}
		}
	}

	// Open for appending (create if needed).
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	a.f = f
	a.w = bufio.NewWriter(f)
	return a, nil
}

// archiveEntry parses one archive line and returns (url, status).
// Backward compatibility:
//   - bare URL lines are treated as successful downloads.
//   - perm-fail lines remain perm-fail (retryable by default).
func archiveEntry(line string) (string, string) {
	if strings.HasPrefix(line, archiveStatusOK+":") {
		return strings.TrimPrefix(line, archiveStatusOK+":"), archiveStatusOK
	}
	if strings.HasPrefix(line, archiveStatusPermFail+":") {
		u := strings.TrimPrefix(line, archiveStatusPermFail+":")
		return u, archiveStatusPermFail
	}
	if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
		return line, archiveStatusOK
	}
	return "", ""
}

func (a *urlArchive) hasSuccessful(u string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status[u] == archiveStatusOK
}

func (a *urlArchive) recordStatus(u, status string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status[u] = status
	_, _ = fmt.Fprintf(a.w, "%s:%s\n", status, u)
	_ = a.w.Flush()
}

func (a *urlArchive) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.w.Flush()
	_ = a.f.Close()
}
