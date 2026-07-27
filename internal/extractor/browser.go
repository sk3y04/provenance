package extractor

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// BrowserOptions configures the chromedp fallback extractor.
type BrowserOptions struct {
	CookiesFile string
	Timeout     time.Duration
	NoSandbox   bool
	ChromePath  string
}

// mediaURLPatterns are substrings that indicate the response is a media stream.
var mediaURLPatterns = []string{
	".m3u8", ".mpd", ".mp4", ".mkv",
	"/hls/", "/dash/", "/stream/", "/video/",
}

func looksLikeMedia(u string) bool {
	low := strings.ToLower(u)
	for _, p := range mediaURLPatterns {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

var chromeCandidates = []string{
	"google-chrome",
	"google-chrome-stable",
	"google-chrome-beta",
	"chromium",
	"chromium-browser",
	"brave-browser",
	"brave",
	"brave-origin",
	"chrome",
	"microsoft-edge",
}

func FindChromeExecutable() string {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		return p
	}
	for _, name := range chromeCandidates {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func chromedpAllocOpts(opts BrowserOptions) []chromedp.ExecAllocatorOption {
	flags := []func(*chromedp.ExecAllocator){
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
	}
	if opts.NoSandbox {
		flags = append(flags, chromedp.Flag("no-sandbox", true))
	}
	path := opts.ChromePath
	if path == "" {
		path = FindChromeExecutable()
	}
	if path != "" {
		flags = append(flags, chromedp.ExecPath(path))
	}
	return append(chromedp.DefaultExecAllocatorOptions[:], flags...)
}

// SniffMediaURL launches a headless Chrome, navigates to `pageURL`, and returns
// the first network request URL that looks like a media stream.
func SniffMediaURL(ctx context.Context, pageURL string, opts BrowserOptions) (string, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, chromedpAllocOpts(opts)...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	timeoutCtx, cancelTimeout := context.WithTimeout(browserCtx, opts.Timeout)
	defer cancelTimeout()

	found := make(chan string, 1)
	fetchSem := make(chan struct{}, 20)

	chromedp.ListenTarget(timeoutCtx, func(ev interface{}) {
		if e, ok := ev.(*fetch.EventRequestPaused); ok {
			u := e.Request.URL
			if looksLikeMedia(u) {
				select {
				case found <- u:
				default:
				}
			}
			fetchSem <- struct{}{}
			go func(id fetch.RequestID) {
				defer func() { <-fetchSem }()
				_ = fetch.ContinueRequest(id).Do(timeoutCtx)
			}(e.RequestID)
		}
	})

	actions := []chromedp.Action{fetch.Enable()}

	if opts.CookiesFile != "" {
		cookies, err := loadNetscapeCookies(opts.CookiesFile)
		if err != nil {
			return "", fmt.Errorf("load cookies: %w", err)
		}
		actions = append(actions, chromedp.ActionFunc(func(c context.Context) error {
			for _, ck := range cookies {
				exp := cdp.TimeSinceEpoch(time.Unix(ck.Expires, 0))
				if err := network.SetCookie(ck.Name, ck.Value).
					WithDomain(ck.Domain).
					WithPath(ck.Path).
					WithSecure(ck.Secure).
					WithHTTPOnly(ck.HTTPOnly).
					WithExpires(&exp).
					Do(c); err != nil {
					return err
				}
			}
			return nil
		}))
	}

	actions = append(actions, chromedp.Navigate(pageURL))

	errCh := make(chan error, 1)
	go func() {
		errCh <- chromedp.Run(timeoutCtx, actions...)
	}()

	select {
	case u := <-found:
		cancelTimeout()
		return u, nil
	case <-timeoutCtx.Done():
		return "", fmt.Errorf("browser timeout after %s: no media url captured (try --cookies)", opts.Timeout)
	case err := <-errCh:
		select {
		case u := <-found:
			return u, nil
		default:
		}
		if err != nil {
			return "", fmt.Errorf("chromedp run: %w", err)
		}
		return "", fmt.Errorf("no media url captured (try --cookies)")
	}
}

// netscapeCookie matches a single line of a Netscape cookies.txt file.
type netscapeCookie struct {
	Domain   string
	Path     string
	Secure   bool
	HTTPOnly bool
	Expires  int64
	Name     string
	Value    string
}

func loadNetscapeCookies(path string) ([]netscapeCookie, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []netscapeCookie
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		httpOnly := false
		switch {
		case strings.HasPrefix(line, "#HttpOnly_"):
			httpOnly = true
			line = strings.TrimPrefix(line, "#HttpOnly_")
		case strings.HasPrefix(line, "#"):
			continue
		case strings.TrimSpace(line) == "":
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}
		expires, _ := strconv.ParseInt(strings.TrimSpace(parts[4]), 10, 64)
		out = append(out, netscapeCookie{
			Domain:   parts[0],
			Path:     parts[2],
			Secure:   strings.EqualFold(parts[3], "TRUE"),
			HTTPOnly: httpOnly,
			Expires:  expires,
			Name:     parts[5],
			Value:    parts[6],
		})
	}
	return out, nil
}

// IsBrowserCandidate returns true if `pageURL` is an http(s) URL.
func IsBrowserCandidate(pageURL string) bool {
	u, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// ResolvedTarget is plumbed back to yt-dlp after a browser sniff.
type ResolvedTarget struct {
	OriginalURL string
	MediaURL    string
}

// SniffOrFallback runs the sniffer and packages the result.
func SniffOrFallback(ctx context.Context, pageURL, cookiesFile, chromePath string) (ResolvedTarget, error) {
	media, err := SniffMediaURL(ctx, pageURL, BrowserOptions{
		CookiesFile: cookiesFile,
		Timeout:     30 * time.Second,
		ChromePath:  chromePath,
	})
	if err != nil {
		return ResolvedTarget{OriginalURL: pageURL}, err
	}
	return ResolvedTarget{OriginalURL: pageURL, MediaURL: media}, nil
}

// ScrapeVideoLinks loads `pageURL` in headless Chrome and returns a deduplicated
// list of <a href> values that look like individual-video pages on the same
// host. This is the right tool for "playlist / index" pages that yt-dlp can't
// crawl on its own (e.g. xmegadrive.com/playlists/...).
//
// Heuristic: keep links whose path contains "/video", "/videos/", "/watch",
// "/embed/", "/v/" or "/player/", and that resolve to the same host (or a
// subdomain of it).
func ScrapeVideoLinks(ctx context.Context, pageURL, cookiesFile string, timeout time.Duration) ([]string, error) {
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx,
		chromedpAllocOpts(BrowserOptions{CookiesFile: cookiesFile, Timeout: timeout})...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	timeoutCtx, cancelTimeout := context.WithTimeout(browserCtx, timeout)
	defer cancelTimeout()

	actions := []chromedp.Action{}

	if cookiesFile != "" {
		cookies, err := loadNetscapeCookies(cookiesFile)
		if err != nil {
			return nil, fmt.Errorf("load cookies: %w", err)
		}
		actions = append(actions, chromedp.ActionFunc(func(c context.Context) error {
			for _, ck := range cookies {
				exp := cdp.TimeSinceEpoch(time.Unix(ck.Expires, 0))
				if err := network.SetCookie(ck.Name, ck.Value).
					WithDomain(ck.Domain).
					WithPath(ck.Path).
					WithSecure(ck.Secure).
					WithHTTPOnly(ck.HTTPOnly).
					WithExpires(&exp).
					Do(c); err != nil {
					return err
				}
			}
			return nil
		}))
	}

	var hrefs []string
	actions = append(actions,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('a[href]')).map(a => a.href)`, &hrefs),
	)

	if err := chromedp.Run(timeoutCtx, actions...); err != nil {
		return nil, fmt.Errorf("chromedp run: %w", err)
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	baseHost := strings.ToLower(base.Host)

	patterns := []string{"/video", "/videos/", "/watch", "/embed/", "/v/", "/player/"}
	seen := map[string]struct{}{}
	var out []string
	for _, h := range hrefs {
		hu, err := url.Parse(h)
		if err != nil || hu.Host == "" {
			continue
		}
		host := strings.ToLower(hu.Host)
		if host != baseHost && !strings.HasSuffix(host, "."+baseHost) && !strings.HasSuffix(baseHost, "."+host) {
			continue
		}
		// Exclude the originating playlist page itself.
		if hu.Path == base.Path {
			continue
		}
		match := false
		lp := strings.ToLower(hu.Path)
		for _, p := range patterns {
			if strings.Contains(lp, p) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		// Strip fragments / tracking query params for dedup.
		hu.Fragment = ""
		clean := hu.String()
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out, nil
}

// ScrapePlaylistVideoLinks loads `pageURL` in headless Chrome and harvests
// every video link across all pages of the playlist. It supports both kinds
// of pagination:
//
//  1. URL-changing pagination (?page=N, /page/N/, /N/) - handled by following
//     the discovered links.
//  2. AJAX pagination where clicking "Next" rewrites the DOM in place WITHOUT
//     changing the URL - handled by clicking a "next" control via JS and
//     waiting for the visible video set to change.
//
// A single browser instance is reused for the whole crawl. `perPageTimeout`
// bounds how long we wait for each page transition. `maxPages` is a hard cap
// on the number of distinct pages visited.
func ScrapePlaylistVideoLinks(ctx context.Context, pageURL, cookiesFile, chromePath string, perPageTimeout time.Duration, maxPages int) ([]string, error) {
	if perPageTimeout == 0 {
		perPageTimeout = 30 * time.Second
	}
	if maxPages <= 0 {
		maxPages = 500
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	baseHost := strings.ToLower(base.Host)
	basePath := strings.TrimRight(base.Path, "/")

	browserOpts := BrowserOptions{CookiesFile: cookiesFile, Timeout: perPageTimeout, ChromePath: chromePath}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, chromedpAllocOpts(browserOpts)...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	// Inject cookies once (browser-scoped, applies to every navigation).
	if cookiesFile != "" {
		cookies, err := loadNetscapeCookies(cookiesFile)
		if err != nil {
			return nil, fmt.Errorf("load cookies: %w", err)
		}
		injectCtx, cancelInject := context.WithTimeout(browserCtx, 15*time.Second)
		err = chromedp.Run(injectCtx, chromedp.ActionFunc(func(c context.Context) error {
			for _, ck := range cookies {
				exp := cdp.TimeSinceEpoch(time.Unix(ck.Expires, 0))
				if err := network.SetCookie(ck.Name, ck.Value).
					WithDomain(ck.Domain).
					WithPath(ck.Path).
					WithSecure(ck.Secure).
					WithHTTPOnly(ck.HTTPOnly).
					WithExpires(&exp).
					Do(c); err != nil {
					return err
				}
			}
			return nil
		}))
		cancelInject()
		if err != nil {
			return nil, fmt.Errorf("inject cookies: %w", err)
		}
	}

	videoPatterns := []string{"/video", "/videos/", "/watch", "/embed/", "/v/", "/player/"}

	// Initial navigation. We don't wrap the chromedp context with a timeout
	// here, because cancelling that derived context can later confuse chromedp
	// into returning "context canceled" on subsequent Runs (the browser target
	// goroutine reacts to cancel signals). Instead we run in a goroutine and
	// select on time.After for the per-page timeout.
	if err := runChromedpWithTimeout(ctx, browserCtx, perPageTimeout, "initial navigate",
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return nil, err
	}

	seenVideos := map[string]bool{}
	var allVideos []string

	// Helpers ---------------------------------------------------------------

	// scrapeVideoHrefs returns the set of href values on the current page that
	// look like individual video pages on the same host.
	scrapeVideoHrefs := func() ([]string, error) {
		var hrefs []string
		if err := runChromedpWithTimeout(ctx, browserCtx, perPageTimeout, "scrape hrefs",
			chromedp.Evaluate(`Array.from(document.querySelectorAll('a[href]')).map(a => a.href)`, &hrefs),
		); err != nil {
			return nil, err
		}
		var out []string
		for _, h := range hrefs {
			hu, err := url.Parse(h)
			if err != nil || hu.Host == "" {
				continue
			}
			host := strings.ToLower(hu.Host)
			if host != baseHost && !strings.HasSuffix(host, "."+baseHost) && !strings.HasSuffix(baseHost, "."+host) {
				continue
			}
			hu.Fragment = ""
			lp := strings.ToLower(hu.Path)
			// Don't treat the playlist path itself as a video.
			if strings.TrimRight(hu.Path, "/") == basePath {
				continue
			}
			if !matchesAny(lp, videoPatterns) {
				continue
			}
			out = append(out, hu.String())
		}
		return out, nil
	}

	// JS that locates a "next page" element and clicks it. Returns true if
	// something was clicked, false if no enabled "next" control exists.
	const clickNextJS = `
(() => {
  const isDisabled = (el) =>
    !el || el.classList.contains('disabled') ||
    el.hasAttribute('disabled') ||
    el.getAttribute('aria-disabled') === 'true';

  const selectors = [
    'a[rel="next"]',
    'link[rel="next"]',
    'a.next',
    'li.next > a',
    '.pagination .next a',
    '.pagination a.next',
    '.pagination__next',
    '.pagination-next',
    'button.next',
    '[aria-label="Next"]',
    '[aria-label="Next page"]',
    '[aria-label="next page"]',
    '[data-action="next"]',
  ];
  for (const sel of selectors) {
    const el = document.querySelector(sel);
    if (el && !isDisabled(el)) {
      try { el.scrollIntoView({block:'center'}); } catch (_) {}
      el.click();
      return true;
    }
  }
  // Text-based fallback: look for a clickable element labeled "Next", ">", "»".
  const candidates = Array.from(document.querySelectorAll('a, button, [role="button"]'));
  for (const el of candidates) {
    const t = (el.textContent || '').trim().toLowerCase();
    if (t === 'next' || t === 'next page' || t === '>' || t === '»' || t === '›') {
      if (isDisabled(el)) continue;
      try { el.scrollIntoView({block:'center'}); } catch (_) {}
      el.click();
      return true;
    }
  }
  return false;
})()
`

	// JS that returns a fingerprint of the current set of video-looking hrefs.
	// Used to detect when the AJAX pagination has finished swapping the DOM.
	const fingerprintJS = `
(() => {
  const pats = ['/video','/videos/','/watch','/embed/','/v/','/player/'];
  const hrefs = Array.from(document.querySelectorAll('a[href]'))
    .map(a => a.href)
    .filter(h => pats.some(p => h.toLowerCase().includes(p)));
  hrefs.sort();
  return hrefs.join('|');
})()
`

	clickNext := func() (bool, error) {
		var clicked bool
		if err := runChromedpWithTimeout(ctx, browserCtx, perPageTimeout, "click next",
			chromedp.Evaluate(clickNextJS, &clicked),
		); err != nil {
			return false, err
		}
		return clicked, nil
	}

	fingerprint := func() (string, error) {
		var fp string
		if err := runChromedpWithTimeout(ctx, browserCtx, perPageTimeout, "fingerprint",
			chromedp.Evaluate(fingerprintJS, &fp),
		); err != nil {
			return "", err
		}
		return fp, nil
	}

	// waitForChange polls the page fingerprint until it differs from `prev`
	// or the timeout fires. Returns the new fingerprint.
	waitForChange := func(prev string) (string, error) {
		deadline := time.Now().Add(perPageTimeout)
		for time.Now().Before(deadline) {
			fp, err := fingerprint()
			if err != nil {
				return "", err
			}
			if fp != prev && fp != "" {
				return fp, nil
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
		return "", fmt.Errorf("timed out waiting for next page to load")
	}

	// Crawl loop -----------------------------------------------------------

	pageNum := 0
	prevFingerprint := ""

	for pageNum < maxPages {
		pageNum++

		// Harvest video links from the current page.
		hrefs, err := scrapeVideoHrefs()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[provenance] page %d scrape failed: %v\n", pageNum, err)
			break
		}
		newVideos := 0
		for _, h := range hrefs {
			if !seenVideos[h] {
				seenVideos[h] = true
				allVideos = append(allVideos, h)
				newVideos++
			}
		}

		fp, err := fingerprint()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[provenance] page %d fingerprint failed: %v\n", pageNum, err)
			break
		}
		fmt.Printf("[provenance]   page %d: +%d videos (total: %d)\n", pageNum, newVideos, len(allVideos))

		// Try to advance to the next page.
		clicked, err := clickNext()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[provenance] click next failed: %v\n", err)
			break
		}
		if !clicked {
			// No next button → we're done.
			break
		}
		// Wait for the DOM/URL to actually change.
		newFp, err := waitForChange(fp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[provenance] %v (stopping at page %d)\n", err, pageNum)
			break
		}
		if newFp == prevFingerprint {
			// Pagination cycled - bail to avoid infinite loop.
			break
		}
		prevFingerprint = fp
	}

	if pageNum >= maxPages {
		fmt.Fprintf(os.Stderr, "[provenance] WARNING: hit maxPages=%d cap; some videos may have been missed\n", maxPages)
	}
	return allVideos, nil
}

// matchesAny returns true if `s` contains any of the given substrings.
func matchesAny(s string, subs []string) bool {
	for _, p := range subs {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// runChromedpWithTimeout runs the given chromedp actions with a per-call wall
// clock timeout by deriving a child context from chromeCtx. chromedp.Run
// respects context cancellation, so when the timeout fires chromedp stops
// cleanly without leaking goroutines or browser targets.
func runChromedpWithTimeout(parent context.Context, chromeCtx context.Context, timeout time.Duration, label string, actions ...chromedp.Action) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(chromeCtx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- chromedp.Run(runCtx, actions...)
	}()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		return nil
	case <-parent.Done():
		return fmt.Errorf("%s: %w", label, parent.Err())
	}
}
