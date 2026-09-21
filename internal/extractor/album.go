package extractor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sk3y04/provenance/internal/downloader"
	"github.com/sk3y04/provenance/internal/manifest"
	"github.com/sk3y04/provenance/internal/ratelimit"
	"github.com/sk3y04/provenance/internal/resolve"
	"github.com/sk3y04/provenance/internal/worker"
)

// ---------------------------------------------------------------------------
// Album extractor
//
// Resolution chain:
//
//	index listing site -> album page (/a/<id>) -> per-file page (/f/<id>)
//	-> CDN download URL -> optional client-side XOR decryption.
//
// The index site is NOT a file host: it is a directory that links out to
// albums served by a set of rotating album hosts. Each album page lists
// files as anchor links inside per-file blocks; each anchor points to a
// per-file page. The per-file page carries the real download URL behind
// either a signing endpoint or a POST-to-CDN-API call (optionally
// XOR-encrypted).
//
// MAINTENANCE NOTE (please read before editing the host tables below):
//
//	The service has rotated its album TLDs, its CDN/storage hosts, the numeric
//	suffix on its CDN API path (historically /api/_001, /api/_002, ...), and
//	its encryption scheme multiple times. When downloads suddenly fail with
//	"no such host", Cloudflare challenges on every host, or "all probed
//	endpoints returned non-JSON", this is almost always one of those values
//	going stale. The host tables are stored encoded (opaque hex constants) and
//	decoded at init; everything external is funnelled through those slices
//	plus the endpoint-probing logic in resolveFileViaAPI, so the usual fix is
//	to add the new host to one of the tables and re-encode (XOR every byte
//	with hostKey, NUL-separate entries, hex-encode). The live site currently
//	uses a signing endpoint discovered per page (jsCDN + signUrl), which is
//	why the API path is treated as a fallback.
// ---------------------------------------------------------------------------

var (
	albTransport = &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}

	albClient = &http.Client{Timeout: 30 * time.Second, Transport: albTransport, CheckRedirect: downloader.SafeRedirect}
)

const (
	albErrorPreviewLimit = 4 << 10
	albRetryBackoff      = 3 * time.Second
	albMaxRetryBackoff   = 20 * time.Second
	albMaxAttempts       = 4
	albPoolSize          = 3
)

// albUserAgent mimics a recent Chrome so album / per-file pages are served the
// full (non-challenge) HTML.
var albUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// hostKey is the fixed XOR byte used to encode/decode the host tables below.
const hostKey byte = 0x5a

// Encoded host tables: every byte XOR hostKey, entries NUL-separated, then
// hex-encoded. Decoded at init into the slices used by the URL classifier.
var (
	albAlbumHostsEncoded = "382f3431287439285a382f3431287429335a382f343128743c335a382f3431287429315a382f343128742a325a382f343128742d295a382f3431287438363b39315a382f34312874283f3e5a382f34312874373f3e333b5a382f3431287429332e3f5a382f34312874363b5a382f3431287433295a382f34312874292f5a382f34312874282f5a382f343128742e355a382f343128743b225a382f34312874393b2e"
	albCDNHostsEncoded   = "3d3f2e74382f3431282874292f5a382f3431282874292f5a393e3474382f34312874373f5a382f34312874373f5a382f343128743928"
	albIndexHostsEncoded = "383b36382f372974292e5a382f343128773b36382f3729743335"
)

var (
	// albAlbumHosts: hosts that serve /a/<id> album pages. Rotate often.
	// KEPT IN A SINGLE LIST so it is easy to update when hosts rotate.
	albAlbumHosts []string
	// albCDNHosts: hosts that serve per-file CDN API / storage. Used by the
	// historical POST-to-API flow (resolveFileViaAPI) and as fallbacks when a
	// host is behind a Cloudflare challenge. Same caveat as albAlbumHosts.
	albCDNHosts []string
	// albIndexHosts: listing sites that link out to album pages.
	albIndexHosts []string
)

func init() {
	albAlbumHosts = decodeHostTable(albAlbumHostsEncoded)
	albCDNHosts = decodeHostTable(albCDNHostsEncoded)
	albIndexHosts = decodeHostTable(albIndexHostsEncoded)
}

// decodeHostTable reverses the encoding used for the host tables.
func decodeHostTable(encoded string) []string {
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return nil
	}
	for i := range raw {
		raw[i] ^= hostKey
	}
	return strings.Split(string(raw), "\x00")
}

// albAPIPathSuffixes: the numeric suffix on the CDN API path changes over time,
// so resolveFileViaAPI probes each (domain, suffix) pair instead of
// hardcoding a single endpoint.
var albAPIPathSuffixes = []string{"/api/_001", "/api/_002", "/api/_003", "/api/_004", "/api/_01", "/api/01", "/api/1"}

// albXORKeyPrefix is the prefix of the per-hour XOR key used to decrypt API
// responses flagged encrypted:true (see albXORDecrypt). The full key is
// "SECRET_KEY_" + strconv timestamp/3600.
const albXORKeyPrefix = "SECRET_KEY_"

var (
	albOGTitleRe       = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:title["'][^>]+content=["']([^"']*)["']`)
	albH1Re            = regexp.MustCompile(`<h1[^>]*>([^<]*)</h1>`)
	albDataFileIDRe    = regexp.MustCompile(`data-file-id["'\s:=]+["']?([A-Za-z0-9_\-]+)`)
	albJSCDNRe         = regexp.MustCompile(`var\s+jsCDN\s*=\s*"((?:[^"\\]|\\.)*)"`)
	albSignURLRe       = regexp.MustCompile(`var\s+signUrl\s*=\s*"((?:[^"\\]|\\.)*)"`)
	albFileLinksRe     = regexp.MustCompile(`href=["'](/(f|v|i|d)/([A-Za-z0-9_\-]+))["']`)
	albBlockHiddenName = regexp.MustCompile(`<p[^>]*display[^>]*none[^>]*>([^<]+)</p>`)
	albBlockTheNameRe  = regexp.MustCompile(`theName[^>]*>\s*([^<]+)\s*<`)
	albBlockTitleRe    = regexp.MustCompile(`theItem[^>]*title=["']([^"']*)["']`)
)

// AlbumOptions mirrors IgOptions/RdOptions for the album extractor. No
// cookies/credentials are needed for public albums, so there is deliberately
// no CookiesFile field.
type AlbumOptions struct {
	Filter      manifest.FilterOptions
	SpeedLimit  int64
	Progress    downloader.ProgressReporter
	Limit       int
	RateLimiter *ratelimit.Manager
}

// albFile is one media file discovered on an album page.
type albFile struct {
	PageURL string // absolute per-file page URL (https://<album host>/f/<slug>)
	Slug    string // the /f/<slug> path segment
	Name    string // display filename (from <h1> or the album block)
	Ext     string
	Kind    string // "image" | "video"
}

// albFileInfo is the parsed content of a per-file page.
type albFileInfo struct {
	PageURL string
	Slug    string
	Name    string // <h1>
	FileID  string // numeric data-file-id (used by the legacy API flow)
	CDNUri  string // jsCDN storage URL (signing flow)
	SignURL string // signUrl endpoint (signing flow)
}

// albAlbum is a scraped album.
type albAlbum struct {
	URL   string
	Title string
	Files []albFile
}

// albURLKind classifies an input URL for the album extractor. It returns
// "index" for the album-listing sites, "album" for a direct /a/<id> page on a
// known album host, "file" for a /f|/v|/i|/d/ per-file page, or "" when the
// URL is not handled by this extractor.
func albURLKind(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(u.Host, "."))
	switch {
	case hostContainsAny(host, albIndexHosts):
		return "index"
	case albHostIsAlbum(host):
		p := strings.ToLower(u.Path)
		switch {
		case strings.Contains(p, "/a/"):
			return "album"
		case albFilePrefixRe.MatchString(p):
			return "file"
		}
	}
	return ""
}

// IsAlbumURL reports whether rawURL is handled by the album extractor: a direct
// album (/a/<id>) or per-file (/f|/v|/i|/d/) page on a known album host, or an
// album-listing page. The dispatcher uses it to route URLs.
func IsAlbumURL(rawURL string) bool {
	return albURLKind(rawURL) != ""
}

var albFilePrefixRe = regexp.MustCompile(`(^|/)(f|v|i|d)/`)

// hostContainsAny reports whether host contains any entry of list.
func hostContainsAny(host string, list []string) bool {
	for _, d := range list {
		if strings.Contains(host, d) {
			return true
		}
	}
	return false
}

// albHostIsAlbum reports whether host is a known album host.
func albHostIsAlbum(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range albAlbumHosts {
		if host == d {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Public API (mirror Instagram's Download/Scan/ScanResolved signatures)
// ---------------------------------------------------------------------------

// ScanAlbum discovers the files in an album without downloading them.
// cookiesFile is accepted for signature parity with the other extractors but
// is unused: public albums require no authentication.
func ScanAlbum(ctx context.Context, rawURL, outDir, cookiesFile string, opts AlbumOptions) (manifest.Manifest, error) {
	albumURL, err := resolveAlbumURL(ctx, rawURL, opts.RateLimiter)
	if err != nil {
		return manifest.Manifest{}, err
	}
	album, err := scrapeAlbum(ctx, albumURL, opts.RateLimiter)
	if err != nil {
		return manifest.Manifest{}, err
	}
	files := albApplyLimit(album.Files, opts.Limit)
	items := albFileItems(album.Title, albumURL, outDir, files)
	m := manifest.New(rawURL, "album", items)
	return m.Filter(opts.Filter)
}

// DownloadAlbum downloads every image and video in an album concurrently
// via the shared worker pool. cookiesFile is accepted for signature parity
// but is unused (public albums need no auth).
func DownloadAlbum(ctx context.Context, rawURL, outDir, cookiesFile string, opts AlbumOptions, dryRun bool) error {
	_ = cookiesFile
	albumURL, err := resolveAlbumURL(ctx, rawURL, opts.RateLimiter)
	if err != nil {
		return err
	}
	album, err := scrapeAlbum(ctx, albumURL, opts.RateLimiter)
	if err != nil {
		return err
	}
	title := album.Title
	if title == "" {
		title = albAlbumIDFromURL(albumURL)
	}
	fmt.Fprintf(os.Stderr, "[album] %q: %d file(s) at %s\n", sanitizeFilename(title), len(album.Files), albumURL)

	files := albApplyLimit(album.Files, opts.Limit)
	items := albFileItems(title, rawURL, outDir, files)
	items, err = manifest.FilterItems(items, opts.Filter)
	if err != nil {
		return err
	}
	allowed := make(map[string]manifest.Item, len(items))
	for _, it := range items {
		allowed[it.URL] = it
	}

	state := newAlbResolverState()
	pool := worker.NewPool(ctx, albPoolSize)

	failed := make(map[string]string)
	var failMu sync.Mutex
	recordFailure := func(id string, err error) {
		failMu.Lock()
		failed[id] = err.Error()
		failMu.Unlock()
		fmt.Fprintf(os.Stderr, "[provenance] album download failed: %s: %v\n", id, err)
	}

	for _, it := range items {
		it := it
		if dryRun {
			fmt.Printf("[dry-run] album: %s -> %s\n", it.URL, it.Destination)
			continue
		}
		pageURL := it.URL
		pool.SubmitWithHooks(func() error {
			dlURL, ref, rerr := state.resolveDownloadURL(ctx, pageURL, opts.RateLimiter)
			if rerr != nil {
				return rerr
			}
			dl := downloader.New()
			dl.SpeedLimit = opts.SpeedLimit
			dl.Progress = opts.Progress
			return dl.Download(ctx, dlURL, it.Destination, ref)
		}, nil, func(err error) {
			recordFailure(pageURL, err)
		})
	}

	pool.Wait()
	if len(failed) > 0 {
		return fmt.Errorf("album: %d file(s) failed to download (first: %s)", len(failed), firstFailedURL(failed))
	}
	return nil
}

// ScanAlbumResolved returns a resolve.Source describing the album's files.
func ScanAlbumResolved(ctx context.Context, rawURL, outDir, cookiesFile string, opts AlbumOptions) (resolve.Source, error) {
	_ = cookiesFile
	albumURL, err := resolveAlbumURL(ctx, rawURL, opts.RateLimiter)
	if err != nil {
		return resolve.Source{}, err
	}
	album, err := scrapeAlbum(ctx, albumURL, opts.RateLimiter)
	if err != nil {
		return resolve.Source{}, err
	}
	title := album.Title
	if title == "" {
		title = albAlbumIDFromURL(albumURL)
	}
	files := albApplyLimit(album.Files, opts.Limit)

	kind := resolve.KindFeed
	canonicalURL := albumURL
	src := resolve.NewSource(rawURL, canonicalURL, kind, "album")
	src.Title = title
	for _, f := range files {
		item := resolve.NewItem(albFileExternalID(f), f.PageURL)
		item.Title = firstNonEmpty(f.Name, f.Slug)
		asset := resolve.NewMediaAsset(f.PageURL, albResolveKind(f.Kind))
		asset.Extension = f.Ext
		asset.Filename = firstNonEmpty(f.Name, f.Slug)
		item.Media = append(item.Media, asset)
		src.Items = append(src.Items, item)
	}
	return src, nil
}

// ---------------------------------------------------------------------------
// Resolution: index listing -> album page
// ---------------------------------------------------------------------------

// resolveAlbumURL turns the raw input into a direct /a/<id> album URL. A
// direct album URL passes through untouched; an album-listing page is
// fetched and the first outbound album link it contains is returned.
func resolveAlbumURL(ctx context.Context, rawURL string, rl *ratelimit.Manager) (string, error) {
	switch albURLKind(rawURL) {
	case "album":
		return rawURL, nil
	case "index":
		page, err := albFetchHTML(ctx, rawURL, rawURL, rl)
		if err != nil {
			return "", fmt.Errorf("fetch index listing: %w", err)
		}
		if strings.TrimSpace(string(page)) == "" {
			return "", fmt.Errorf("index listing returned an empty page")
		}
		link := firstAlbumLink(string(page))
		if link == "" {
			return "", fmt.Errorf("index listing did not contain any album link (page structure may have changed)")
		}
		fmt.Fprintf(os.Stderr, "[album] resolved index listing -> %s\n", link)
		return link, nil
	default:
		return "", fmt.Errorf("unsupported album or index url: %s (expected an /a/<id> album page or an index listing)", rawURL)
	}
}

// firstAlbumLink scans HTML for the first anchor whose href points at a known
// album host (/a/<id>) and returns it.
func firstAlbumLink(html string) string {
	for _, m := range albAlbumHrefRe.FindAllStringSubmatch(html, -1) {
		ref, err := url.Parse(m[1])
		if err != nil || !albHostIsAlbum(strings.ToLower(ref.Host)) ||
			!strings.Contains(strings.ToLower(ref.Path), "/a/") {
			continue
		}
		ref.RawQuery = ""
		return ref.String()
	}
	return ""
}

var albAlbumHrefRe = regexp.MustCompile(`(?i)href=["'](https?://[^"']+)["']`)

// ---------------------------------------------------------------------------
// Album scraping
// ---------------------------------------------------------------------------

// scrapeAlbum fetches the album page and extracts the og:title plus the
// ordered list of per-file entries (link + display filename).
//
// Real structure (verified against the live site): each file lives in a
// `group/item theItem` block containing a hidden `<p style="display:none;">`
// (the filename), an img, an `<a href="/f/<slug>">` download link, and a
// `.theName` paragraph echoing the filename. One stray anchor
// (`href="/f/' + file.slug + '"`) is a server-side template fragment and is
// skipped by albFileLinksRe because it does not match a plain slug.
func scrapeAlbum(ctx context.Context, albumURL string, rl *ratelimit.Manager) (*albAlbum, error) {
	page, err := albFetchHTML(ctx, albumURL, albumURL, rl)
	if err != nil {
		return nil, fmt.Errorf("fetch album page: %w", err)
	}
	album := &albAlbum{URL: albumURL}
	if m := albOGTitleRe.FindStringSubmatch(string(page)); m != nil && strings.TrimSpace(m[1]) != "" {
		album.Title = strings.TrimSpace(m[1])
	}
	for _, f := range albFileLinks(string(page), albumURL) {
		album.Files = append(album.Files, f)
	}
	if len(album.Files) == 0 {
		return nil, fmt.Errorf("album page listed no files (structure may have changed): %s", albumURL)
	}
	return album, nil
}

// albFileLinks returns the per-file entries found in an album page, in document
// order, de-duplicated by slug.
func albFileLinks(html, albumURL string) []albFile {
	locs := albFileLinksRe.FindAllStringSubmatchIndex(html, -1)
	if len(locs) == 0 {
		return nil
	}
	albumHost := albHostFromURL(albumURL)

	seen := make(map[string]bool)
	var out []albFile
	for _, loc := range locs {
		// Offsets are byte positions into `html` (not into the matched span).
		hrefPath := html[loc[2]:loc[3]] // the /f/<slug> portion (group 1)
		slug := html[loc[6]:loc[7]]     // the file slug (group 3)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true

		pageURL := "https://" + albumHost + hrefPath
		// Look backwards from the link for the filename that this block shows.
		start := loc[0] - 1200
		if start < 0 {
			start = 0
		}
		name := albNameInWindow(html[start:loc[0]])
		out = append(out, albFile{
			PageURL: pageURL,
			Slug:    slug,
			Name:    name,
			Ext:     albExtForName(name),
			Kind:    albKindForName(name),
		})
	}
	return out
}

// albNameInWindow returns the filename shown in the block that precedes a file
// link, preferring the hidden <p>, then the .theName <p>, then the block's
// title attribute. It takes the *nearest* (last) match for each pattern so a
// large backward window cannot grab the previous block's name.
func albNameInWindow(window string) string {
	patterns := []*regexp.Regexp{albBlockHiddenName, albBlockTheNameRe, albBlockTitleRe}
	for _, re := range patterns {
		ms := re.FindAllStringSubmatchIndex(window, -1)
		if len(ms) == 0 {
			continue
		}
		m := ms[len(ms)-1] // nearest to the anchor
		if len(m) >= 4 {
			if name := strings.TrimSpace(window[m[2]:m[3]]); name != "" {
				return name
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Per-file page parsing + CDN download resolution
// ---------------------------------------------------------------------------

// resolveDownloadURL resolves a single per-file page to a directly downloadable
// CDN URL, caching per page and reusing any discovered-working CDN endpoint.
func (s *albResolverState) resolveDownloadURL(ctx context.Context, pageURL string, rl *ratelimit.Manager) (string, string, error) {
	s.mu.Lock()
	if u, ok := s.fileURLs[pageURL]; ok {
		s.mu.Unlock()
		return u, pageURL, nil
	}
	s.mu.Unlock()

	info, err := fetchFilePage(ctx, pageURL, rl)
	if err != nil {
		return "", "", err
	}
	dlURL, ref, err := s.resolveDownloadURLForInfo(ctx, info, rl)
	if err != nil {
		return "", "", err
	}
	s.cacheFileURL(pageURL, dlURL)
	if ref == "" {
		ref = pageURL
	}
	return dlURL, ref, nil
}

// resolveDownloadURLForInfo resolves an already-parsed per-file info to a
// directly downloadable CDN URL. It first tries the live signing flow (jsCDN +
// signUrl) and falls back to the legacy POST-to-CDN-API flow.
func (s *albResolverState) resolveDownloadURLForInfo(ctx context.Context, info *albFileInfo, rl *ratelimit.Manager) (string, string, error) {
	// Primary live path: the per-file page exposes a storage URL (jsCDN) and a
	// signing endpoint (signUrl); a signed GET yields the real file.
	if info.SignURL != "" && info.CDNUri != "" {
		if dlURL, ref, ok := albSigningDownloadURL(ctx, info, rl); ok {
			return dlURL, ref, nil
		}
	}

	// Fallback path: POST the file id to the CDN API (XOR-decrypting if the
	// response is encrypted). Probes every (domain, suffix) pair.
	return resolveFileViaAPI(ctx, info, s, rl)
}

// albParseFilePage parses a per-file page's h1, data-file-id, and (when present)
// the signing-flow variables (jsCDN + signUrl). It is pure so it can be unit
// tested without hitting the network.
func albParseFilePage(pageURL, html string) *albFileInfo {
	info := &albFileInfo{PageURL: pageURL}
	if m := albH1Re.FindStringSubmatch(html); m != nil && strings.TrimSpace(m[1]) != "" {
		info.Name = strings.TrimSpace(m[1])
	}
	if m := albDataFileIDRe.FindStringSubmatch(html); m != nil && m[1] != "" {
		info.FileID = m[1]
	}
	if m := albJSCDNRe.FindStringSubmatch(html); m != nil && m[1] != "" {
		info.CDNUri = albUnescapeJS(m[1])
	}
	if m := albSignURLRe.FindStringSubmatch(html); m != nil && m[1] != "" {
		info.SignURL = albUnescapeJS(m[1])
	}
	info.Slug = albSlugFromPageURL(pageURL)
	return info
}

// fetchFilePage fetches a per-file page and parses it.
func fetchFilePage(ctx context.Context, pageURL string, rl *ratelimit.Manager) (*albFileInfo, error) {
	page, err := albFetchHTML(ctx, pageURL, pageURL, rl)
	if err != nil {
		return nil, fmt.Errorf("fetch per-file page: %w", err)
	}
	return albParseFilePage(pageURL, string(page)), nil
}

// albSlugFromPageURL extracts the /f/<slug> or /v/<slug> path segment from a
// per-file page URL (used by the legacy API flow; the signing flow relies on
// jsCDN instead).
func albSlugFromPageURL(pageURL string) string {
	for _, prefix := range []string{"/f/", "/v/", "/i/", "/d/"} {
		if i := strings.LastIndex(strings.ToLower(pageURL), prefix); i >= 0 {
			slug := pageURL[i+len(prefix):]
			if j := strings.IndexAny(slug, "?#"); j >= 0 {
				slug = slug[:j]
			}
			return slug
		}
	}
	return ""
}

// albSigningDownloadURL performs the live signing flow: GET the sign endpoint
// with the storage path, then append the returned token/expiration to jsCDN.
// Returns ok=false when the sign response is missing a token or malformed.
func albSigningDownloadURL(ctx context.Context, info *albFileInfo, rl *ratelimit.Manager) (string, string, bool) {
	raw, err := url.Parse(info.CDNUri)
	if err != nil || raw.Path == "" {
		return "", "", false
	}
	signURL := info.SignURL + "?path=" + url.PathEscape(raw.Path)
	body, status, err := albFetchJSON(ctx, signURL, info.PageURL, "", rl, nil)
	if err != nil {
		return "", "", false
	}
	if status != http.StatusOK {
		fmt.Fprintf(os.Stderr, "[album] sign endpoint status %d: %s\n", status, sanitizeErrorBody(string(body)))
		return "", "", false
	}
	var sig struct {
		Token string `json:"token"`
		Ex    int64  `json:"ex"`
	}
	if err := json.Unmarshal(body, &sig); err != nil || sig.Token == "" {
		return "", "", false
	}
	out := *raw
	q := url.Values{}
	q.Set("token", sig.Token)
	if sig.Ex > 0 {
		q.Set("ex", strconv.FormatInt(sig.Ex, 10))
	}
	out.RawQuery = q.Encode()
	return out.String(), info.PageURL, true
}

// resolveFileViaAPI probes the legacy CDN API for a usable file URL, reusing
// any working endpoint discovered earlier in this run and falling back across
// the domain + suffix tables. Returns an error only after every option is
// exhausted.
func resolveFileViaAPI(ctx context.Context, info *albFileInfo, s *albResolverState, rl *ratelimit.Manager) (string, string, error) {
	if info.Slug == "" && info.FileID == "" {
		return "", "", fmt.Errorf("album: per-file page exposed no file id or slug")
	}

	// Reuse a working endpoint discovered for a previous file in this run.
	if cdn, suffix, ok := s.workingAPIEndpoint(); ok {
		u, ref, ok2, err := albAPIGet(ctx, cdn, suffix, info, s, rl)
		if err == nil && ok2 {
			return u, ref, nil
		}
	}

	var lastErr error
	for _, suffix := range albAPIPathSuffixes {
		for _, cdn := range albCDNHosts {
			key := cdn + suffix
			if s.apiIsTried(key) {
				continue
			}
			s.apiMarkTried(key)
			u, ref, ok, err := albAPIGet(ctx, cdn, suffix, info, s, rl)
			if err != nil {
				lastErr = err
				fmt.Fprintf(os.Stderr, "[album] API %s%s failed: %v\n", cdn, suffix, err)
				continue
			}
			if !ok {
				continue
			}
			s.apiMarkWorking(cdn, suffix)
			return u, ref, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no probed CDN endpoint returned a usable file url")
	}
	return "", "", fmt.Errorf("album: resolve via CDN API failed after probing all endpoints: %w", lastErr)
}

// albAPIResponse is the shape of the CDN API body. When Encrypted is true the
// URL is base64-encoded and XOR-decrypted with a per-hour key.
type albAPIResponse struct {
	URL       string `json:"url"`
	Encrypted bool   `json:"encrypted"`
	Timestamp int64  `json:"timestamp"`
}

// albAPIGet POSTs the file id to one (domain, suffix) endpoint and interprets
// the response. ok=false means "not usable right now, try another endpoint".
func albAPIGet(ctx context.Context, cdn, suffix string, info *albFileInfo, s *albResolverState, rl *ratelimit.Manager) (string, string, bool, error) {
	endpoint := "https://" + cdn + suffix
	ref := "https://" + cdn + "/file/" + firstNonEmpty(info.FileID, info.Slug)
	payload, err := json.Marshal(map[string]string{"id": firstNonEmpty(info.FileID, info.Slug)})
	if err != nil {
		return "", "", false, fmt.Errorf("marshal api body: %w", err)
	}
	body, status, err := albFetchJSON(ctx, endpoint, ref, "https://"+cdn, rl, payload)
	if err != nil {
		return "", "", false, err
	}
	if status == http.StatusForbidden || status == http.StatusBadGateway {
		// Cloudflare challenge (or upstream block) on this host.
		return "", "", false, fmt.Errorf("cloudflare challenge or forbidden on %s", cdn)
	}
	if status != http.StatusOK {
		return "", "", false, fmt.Errorf("api status %d: %s", status, sanitizeErrorBody(string(body)))
	}
	var api albAPIResponse
	if err := json.Unmarshal(body, &api); err != nil {
		return "", "", false, fmt.Errorf("non-json response: %s", sanitizeErrorBody(string(body)))
	}
	if api.Encrypted {
		api.URL = albXORDecrypt(api.URL, api.Timestamp)
	}
	if api.URL == "" {
		return "", "", false, fmt.Errorf("api returned an empty url")
	}
	if albIsMaintenanceVideo(api.URL) {
		return "", "", false, fmt.Errorf("maintenance-vid placeholder detected for this file")
	}
	return api.URL, ref, true, nil
}

// albIsMaintenanceVideo reports whether url points at the service's generic
// placeholder used when a file server is down for that file.
func albIsMaintenanceVideo(url string) bool {
	low := strings.ToLower(url)
	return strings.Contains(low, "maintenance-vid")
}

// ---------------------------------------------------------------------------
// XOR decryption (legacy API flow)
// ---------------------------------------------------------------------------

// albXORDecrypt decodes an API-provided encrypted URL. The encoded string is
// base64-decoded and XORed byte-wise against the repeating ASCII key
// "SECRET_KEY_" + (timestamp/3600) (bucketed hourly). It returns "" when the
// input cannot be decoded, so callers can treat it as "not usable".
func albXORDecrypt(encoded string, timestamp int64) string {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return ""
	}
	raw, err := albBase64Decode(encoded)
	if err != nil {
		return ""
	}
	key := albXORKeyPrefix + strconv.FormatInt(timestamp/3600, 10)
	if len(key) == 0 {
		return ""
	}
	out := make([]byte, len(raw))
	for i, b := range raw {
		out[i] = b ^ key[i%len(key)]
	}
	return string(out)
}

// albBase64Decode tries standard base64 first, then url-safe base64.
func albBase64Decode(s string) ([]byte, error) {
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	if raw, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	return nil, fmt.Errorf("base64 decode failed")
}

// ---------------------------------------------------------------------------
// Resolver state (per album run)
// ---------------------------------------------------------------------------

type albResolverState struct {
	mu         sync.Mutex
	fileURLs   map[string]string
	apiTried   map[string]bool
	apiWorking map[string]bool
}

func newAlbResolverState() *albResolverState {
	return &albResolverState{
		fileURLs:   make(map[string]string),
		apiTried:   make(map[string]bool),
		apiWorking: make(map[string]bool),
	}
}

func (s *albResolverState) cacheFileURL(pageURL, dlURL string) {
	s.mu.Lock()
	s.fileURLs[pageURL] = dlURL
	s.mu.Unlock()
}

func (s *albResolverState) workingAPIEndpoint() (cdn, suffix string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.apiWorking {
		if i := strings.Index(key, "/api"); i > 0 {
			return key[:i], key[i:], true
		}
	}
	return "", "", false
}

func (s *albResolverState) apiIsTried(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apiTried[key]
}

func (s *albResolverState) apiMarkTried(key string) {
	s.mu.Lock()
	s.apiTried[key] = true
	s.mu.Unlock()
}

func (s *albResolverState) apiMarkWorking(cdn, suffix string) {
	s.mu.Lock()
	s.apiWorking[cdn+suffix] = true
	s.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Item / classification helpers
// ---------------------------------------------------------------------------

func albFileItems(albumTitle, rawURL, outDir string, files []albFile) []manifest.Item {
	base := filepath.Join(outDir, "album", sanitizeFilename(firstNonEmpty(albumTitle, albAlbumIDFromURL(rawURL))))
	items := make([]manifest.Item, 0, len(files))
	for i, f := range files {
		name := firstNonEmpty(f.Name, f.Slug)
		if name == "" {
			name = fmt.Sprintf("album_file_%d", i+1)
		}
		items = append(items, manifest.Item{
			ID:          albFileExternalID(f),
			URL:         f.PageURL,
			Title:       name,
			Filename:    name,
			Extension:   f.Ext,
			Source:      "album",
			Kind:        f.Kind,
			Destination: filepath.Join(base, f.Kind, sanitizeFilename(name)),
		})
	}
	return items
}

func albFileExternalID(f albFile) string {
	if f.Slug != "" {
		return "album_" + f.Slug
	}
	return "album_file"
}

func albResolveKind(kind string) resolve.MediaKind {
	if kind == "video" {
		return resolve.MediaVideo
	}
	return resolve.MediaImage
}

// albKindForName maps a filename to "image" or "video" by extension, defaulting
// to images for anything unrecognised (most files carry an extension).
func albKindForName(name string) string {
	switch strings.ToLower(albExtForName(name)) {
	case "mp4", "webm", "mov", "avi", "mkv", "m4v", "mpg", "mpeg", "ts", "wmv", "flv", "gifv", "3gp", "ogv":
		return "video"
	default:
		return "image"
	}
}

func albExtForName(name string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
}

func albApplyLimit(files []albFile, limit int) []albFile {
	if limit > 0 && len(files) > limit {
		return files[:limit]
	}
	return files
}

func albHostFromURL(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host
	}
	return albAlbumHosts[0]
}

func albAlbumIDFromURL(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		if i := strings.LastIndex(u.Path, "/a/"); i >= 0 {
			id := u.Path[i+len("/a/"):]
			if j := strings.IndexAny(id, "?/"); j >= 0 {
				id = id[:j]
			}
			id = strings.TrimSpace(id)
			if id != "" {
				return sanitizeFilename(id)
			}
		}
	}
	return "album"
}

func albUnescapeJS(s string) string {
	return strings.ReplaceAll(s, `\/`, "/")
}

// ---------------------------------------------------------------------------
// HTTP helpers (retry/backoff + error-preview logging, mirroring the ig/rd
// extractors)
// ---------------------------------------------------------------------------

func albFetchHTML(ctx context.Context, rawURL, referer string, rl *ratelimit.Manager) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= albMaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		h := http.Header{}
		h.Set("User-Agent", albUserAgent)
		h.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		h.Set("Accept-Language", "en-US,en;q=0.9")
		if referer != "" {
			h.Set("Referer", referer)
		}
		req.Header = h
		if rl != nil {
			_ = rl.GetLimiter(albHostFromURL(rawURL)).Wait(ctx)
		}

		resp, err := albClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("http: %w", err)
			}
			lastErr = fmt.Errorf("http: %w", err)
			if attempt == albMaxAttempts {
				return nil, fmt.Errorf("album request failed after %d attempts: %w", attempt, lastErr)
			}
			time.Sleep(albRetryBackoff << min(attempt-1, 3))
			continue
		}
		if resp.StatusCode >= 400 {
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, albErrorPreviewLimit))
			_ = resp.Body.Close()
			body := string(preview)
			lastErr = fmt.Errorf("album status %d", resp.StatusCode)
			fmt.Fprintf(os.Stderr, "[album] response: %s\n", sanitizeErrorBody(body))
			if resp.StatusCode == http.StatusTooManyRequests {
				time.Sleep(albMaxRetryBackoff)
				continue
			}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && attempt == albMaxAttempts {
				return preview, lastErr
			}
			if resp.StatusCode >= 500 && attempt < albMaxAttempts {
				time.Sleep(albRetryBackoff << min(attempt-1, 3))
				continue
			}
			return preview, lastErr
		}
		full, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		return full, nil
	}
	return nil, lastErr
}

func albFetchJSON(ctx context.Context, rawURL, referer, origin string, rl *ratelimit.Manager, payload []byte) ([]byte, int, error) {
	var lastErr error
	for attempt := 1; attempt <= albMaxAttempts; attempt++ {
		method := http.MethodGet
		var reader io.Reader
		if payload != nil {
			method = http.MethodPost
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
		if err != nil {
			return nil, 0, fmt.Errorf("new request: %w", err)
		}
		h := http.Header{}
		h.Set("User-Agent", albUserAgent)
		h.Set("Accept", "application/json, text/plain, */*")
		h.Set("Content-Type", "application/json")
		if referer != "" {
			h.Set("Referer", referer)
		}
		if origin != "" {
			h.Set("Origin", origin)
		}
		req.Header = h
		if rl != nil {
			_ = rl.GetLimiter(albHostFromURL(rawURL)).Wait(ctx)
		}

		resp, err := albClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, fmt.Errorf("http: %w", err)
			}
			lastErr = fmt.Errorf("http: %w", err)
			if attempt == albMaxAttempts {
				return nil, 0, fmt.Errorf("album api request failed after %d attempts: %w", attempt, lastErr)
			}
			time.Sleep(albRetryBackoff << min(attempt-1, 3))
			continue
		}
		if resp.StatusCode >= 400 {
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, albErrorPreviewLimit))
			_ = resp.Body.Close()
			status := resp.StatusCode
			lastErr = fmt.Errorf("album api status %d", status)
			fmt.Fprintf(os.Stderr, "[album] api response: %s\n", sanitizeErrorBody(string(preview)))
			if status == http.StatusTooManyRequests {
				time.Sleep(albMaxRetryBackoff)
				continue
			}
			if status >= 400 && status < 500 && attempt == albMaxAttempts {
				return preview, status, lastErr
			}
			if status >= 500 && attempt < albMaxAttempts {
				time.Sleep(albRetryBackoff << min(attempt-1, 3))
				continue
			}
			return preview, status, lastErr
		}
		full, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, 0, fmt.Errorf("read body: %w", err)
		}
		return full, resp.StatusCode, nil
	}
	return nil, 0, lastErr
}
