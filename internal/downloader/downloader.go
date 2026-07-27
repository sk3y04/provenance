// Package downloader provides a resumable HTTP file downloader with progress reporting and speed limiting.
package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/schollz/progressbar/v3"

	"github.com/sk3y04/provenance/internal/ratelimit"
)

const userAgent = "provenance/1.0"

// ProgressReporter receives per-file lifecycle events. It is intentionally
// tiny so the TUI (or any other observer) can render live per-file progress
// bars without coupling the downloader to a specific UI framework.
//
// All methods may be called from worker goroutines and must be safe for
// concurrent use.
type ProgressReporter interface {
	OnStart(url, dest string, total int64)
	OnProgress(url string, written, total int64)
	OnDone(url string, err error)
}

// Client is a resumable HTTP downloader.
type Client struct {
	HTTP       *http.Client
	SpeedLimit int64 // bytes per second; 0 means unlimited
	// Progress, when non-nil, replaces the default schollz/progressbar output
	// with structured callbacks. Useful for the TUI / external dashboards.
	Progress ProgressReporter

	// BrowserTLS, when true, replaces the default Go TLS stack with a
	// uTLS-based transport that mimics Chrome's TLS ClientHello fingerprint.
	// This helps bypass CDNs that reject Go's default TLS handshake.
	BrowserTLS bool

	// RateLimiter, when non-nil, is used to throttle per-host request rates.
	RateLimiter *ratelimit.Manager

	// LastHash is the SHA-256 hex digest of the most recently downloaded file.
	// Set by Download on success; empty on failure.
	LastHash string

	// LastPath is the destination path of the most recently downloaded file.
	LastPath string
}

// New returns a Client with sane defaults.
//
// The HTTP transport bounds dial and TLS handshake durations so an unreachable
// CDN host fails in seconds instead of stalling for the full per-request
// budget. The 30 minute Timeout still caps the total streaming time.
func New() *Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &Client{
		HTTP: &http.Client{
			Timeout:       30 * time.Minute,
			Transport:     transport,
			CheckRedirect: SafeRedirect,
		},
	}
}

var privateNetworks = []string{
	"127.0.0.0/8",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"::1/128",
	"fc00::/7",
}

func isPrivateHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, cidr := range privateNetworks {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

func SafeRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if isPrivateHost(req.URL.Hostname()) {
		return fmt.Errorf("redirect to private network %q blocked", req.URL.Hostname())
	}
	return nil
}

// Download fetches `rawURL` and writes it to `dest`. Resumes if a .part file
// already exists. The destination directory is created if missing.
func (c *Client) Download(ctx context.Context, rawURL, dest, referer string) (err error) {
	c.LastHash = ""
	c.LastPath = ""

	if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
		if c.Progress != nil {
			c.Progress.OnStart(rawURL, dest, fi.Size())
			c.Progress.OnProgress(rawURL, fi.Size(), fi.Size())
			c.Progress.OnDone(rawURL, nil)
		}
		c.LastPath = dest
		if fh, err := fileHash(dest); err == nil {
			c.LastHash = fh
		}
		return nil
	}

	if c.BrowserTLS && c.HTTP.Transport == nil {
		c.HTTP.Transport = NewChromeTLSRoundTripper()
	}

	if u, err := url.Parse(rawURL); err == nil && c.RateLimiter != nil {
		if err := c.RateLimiter.GetLimiter(u.Host).Wait(ctx); err != nil {
			return fmt.Errorf("ratelimit wait: %w", err)
		}
	}

	partPath := dest + ".part"
	var existing int64
	if fi, err := os.Stat(partPath); err == nil {
		existing = fi.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	if c.BrowserTLS {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Sec-Fetch-Mode", "no-cors")
		req.Header.Set("Sec-Fetch-Dest", "video")
	} else {
		req.Header.Set("User-Agent", userAgent)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if existing > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(existing, 10)+"-")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored Range; restart from scratch.
		if existing > 0 {
			_ = os.Remove(partPath)
			existing = 0
		}
	case http.StatusPartialContent:
		// good, resuming
	case http.StatusRequestedRangeNotSatisfiable:
		// Already fully downloaded.
		_ = os.Remove(partPath)
		if _, err := os.Stat(dest); err == nil {
			if c.Progress != nil {
				c.Progress.OnDone(rawURL, nil)
			}
			return nil
		}
		return fmt.Errorf("range not satisfiable and no final file")
	default:
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); strings.Contains(ct, "text/html") {
		return fmt.Errorf("refusing to save HTML response as media (content-type %q)", ct)
	}

	flag := os.O_CREATE | os.O_WRONLY
	if existing > 0 {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_EXCL
	}
	// Defer directory creation until we are sure the response is good, so
	// failed downloads do not leave behind empty post folders.
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.OpenFile(partPath, flag, 0o600)
	if err != nil {
		return fmt.Errorf("open part: %w", err)
	}

	total := resp.ContentLength + existing
	if total <= 0 {
		total = -1
	}

	// Pick exactly one progress sink: structured reporter wins, otherwise
	// fall back to the schollz progressbar so the CLI still gets bars.
	var sinks []io.Writer
	sinks = append(sinks, f)

	hash := sha256.New()
	if existing > 0 {
		pf, err := os.Open(partPath)
		if err == nil {
			_, _ = io.Copy(hash, pf)
			_ = pf.Close()
		}
	}
	sinks = append(sinks, hash)

	var bar *progressbar.ProgressBar
	var pTracker *progressTracker
	if c.Progress != nil {
		c.Progress.OnStart(rawURL, dest, total)
		if existing > 0 {
			c.Progress.OnProgress(rawURL, existing, total)
		}
		pTracker = newProgressTracker(c.Progress, rawURL, total, existing)
		sinks = append(sinks, pTracker)
		// Notify completion regardless of error path.
		defer func() { c.Progress.OnDone(rawURL, err) }()
	} else {
		bar = progressbar.NewOptions64(total,
			progressbar.OptionSetDescription(filepath.Base(dest)),
			progressbar.OptionShowBytes(true),
			progressbar.OptionShowCount(),
			progressbar.OptionSetWidth(30),
			progressbar.OptionThrottle(100*time.Millisecond),
			progressbar.OptionClearOnFinish(),
		)
		if existing > 0 {
			_ = bar.Add64(existing)
		}
		sinks = append(sinks, bar)
	}

	reader := io.Reader(resp.Body)
	if c.SpeedLimit > 0 {
		reader = newLimitReader(resp.Body, c.SpeedLimit)
	}
	reader = newContextReader(ctx, reader)
	_, copyErr := io.Copy(io.MultiWriter(sinks...), reader)
	closeErr := f.Close()
	if pTracker != nil {
		pTracker.flush()
	}
	if copyErr != nil {
		return fmt.Errorf("copy: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close: %w", closeErr)
	}
	if err := os.Rename(partPath, dest); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	c.LastPath = dest
	c.LastHash = hex.EncodeToString(hash.Sum(nil))
	return nil
}

// progressTracker is an io.Writer that counts bytes and forwards throttled
// updates to a ProgressReporter (~10 Hz).
type progressTracker struct {
	rep      ProgressReporter
	url      string
	total    int64
	written  atomic.Int64
	lastSent atomic.Int64 // unix-nano of last OnProgress call
}

func newProgressTracker(rep ProgressReporter, url string, total, initial int64) *progressTracker {
	t := &progressTracker{rep: rep, url: url, total: total}
	t.written.Store(initial)
	return t
}

func (t *progressTracker) Write(p []byte) (int, error) {
	n := len(p)
	cur := t.written.Add(int64(n))
	now := time.Now().UnixNano()
	last := t.lastSent.Load()
	if now-last >= int64(100*time.Millisecond) {
		if t.lastSent.CompareAndSwap(last, now) {
			t.rep.OnProgress(t.url, cur, t.total)
		}
	}
	return n, nil
}

// flush forces one final progress emission so the UI shows 100% when copy finishes.
func (t *progressTracker) flush() {
	if t == nil || t.rep == nil {
		return
	}
	t.rep.OnProgress(t.url, t.written.Load(), t.total)
}

type limitReader struct {
	r       io.Reader
	limit   int64
	start   time.Time
	read    int64
	started bool
}

func newLimitReader(r io.Reader, limit int64) io.Reader {
	return &limitReader{r: r, limit: limit}
}

func (r *limitReader) Read(p []byte) (int, error) {
	if !r.started {
		r.started = true
		r.start = time.Now()
	}
	n, err := r.r.Read(p)
	if n > 0 && r.limit > 0 {
		r.read += int64(n)
		expected := time.Duration(float64(r.read) / float64(r.limit) * float64(time.Second))
		if sleep := time.Until(r.start.Add(expected)); sleep > 0 {
			time.Sleep(sleep)
		}
	}
	return n, err
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func newContextReader(ctx context.Context, r io.Reader) io.Reader {
	return &contextReader{ctx: ctx, r: r}
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
	}
	return r.r.Read(p)
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
