package extractor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

const (
	rdAPIBase           = "https://old.reddit.com"
	rdAPIFallback       = "https://www.reddit.com"
	rdPageSize          = 100
	rdMaxAttempts       = 4
	rdErrorPreviewLimit = 4 << 10
	rdRetryBackoff      = 3 * time.Second
	rdMaxRetryBackoff   = 20 * time.Second
)

var rdUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func rdOAuthToken() string {
	if t := os.Getenv("REDDIT_OAUTH_TOKEN"); t != "" {
		return t
	}
	clientID := os.Getenv("REDDIT_CLIENT_ID")
	clientSecret := os.Getenv("REDDIT_CLIENT_SECRET")
	if clientID != "" && clientSecret != "" {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(clientID+":"+clientSecret))
	}
	return ""
}

func rdClient(cookiesFile string) (*http.Client, error) {
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: downloader.SafeRedirect,
	}, nil
}

type rdGalleryItem struct {
	MediaID string `json:"media_id"`
	Caption string `json:"caption"`
}

type rdGalleryData struct {
	Items []rdGalleryItem `json:"items"`
}

type rdMediaMetadataSource struct {
	U   string `json:"u"`
	Gif string `json:"gif"`
}

type rdMediaMetadata struct {
	S rdMediaMetadataSource `json:"s"`
	E string                `json:"e"`
}

type rdRedditVideo struct {
	FallbackURL string `json:"fallback_url"`
	DASHURL     string `json:"dash_url"`
	HLSURL      string `json:"hls_url"`
	Duration    int    `json:"duration"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

type rdPreviewImage struct {
	Source struct {
		URL    string `json:"url"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"source"`
}

type rdPreview struct {
	Images []rdPreviewImage `json:"images"`
}

type rdPostData struct {
	ID         string  `json:"id"`
	Subreddit  string  `json:"subreddit"`
	Author     string  `json:"author"`
	Title      string  `json:"title"`
	SelfText   string  `json:"selftext"`
	Permalink  string  `json:"permalink"`
	URL        string  `json:"url"`
	Domain     string  `json:"domain"`
	CreatedUTC float64 `json:"created_utc"`
	PostHint   string  `json:"post_hint"`
	IsVideo    bool    `json:"is_video"`
	IsGallery  bool    `json:"is_gallery"`
	Media      *struct {
		RedditVideo *rdRedditVideo `json:"reddit_video"`
	} `json:"media"`
	GalleryData         *rdGalleryData             `json:"gallery_data"`
	MediaMetadata       map[string]rdMediaMetadata `json:"media_metadata"`
	URLOverriddenByDest string                     `json:"url_overridden_by_dest"`
	Preview             *rdPreview                 `json:"preview"`
	CrosspostParentList []rdPostData               `json:"crosspost_parent_list"`
	Name                string                     `json:"name"`
}

type rdPost struct {
	Kind string     `json:"kind"`
	Data rdPostData `json:"data"`
}

type rdResponse struct {
	Kind string `json:"kind"`
	Data struct {
		After    string   `json:"after"`
		Children []rdPost `json:"children"`
	} `json:"data"`
}

type RdOptions struct {
	CookiesFile     string
	Filter          manifest.FilterOptions
	SpeedLimit      int64
	Progress        downloader.ProgressReporter
	Limit           int
	RateLimiter     *ratelimit.Manager
	IncludePosts    bool
	IncludeComments bool
	CommentLimit    int
}

type RdTarget struct {
	Name        string
	IsSubreddit bool
}

func (t RdTarget) apiPath() string {
	if t.IsSubreddit {
		return "/r/" + t.Name + "/new.json"
	}
	return "/user/" + t.Name + "/submitted.json"
}

func ParseRdURL(rawURL string) (RdTarget, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return RdTarget{}, fmt.Errorf("parse url: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if i+1 < len(parts) {
			if strings.EqualFold(p, "user") {
				return RdTarget{Name: sanitizeFilename(parts[i+1]), IsSubreddit: false}, nil
			}
			if strings.EqualFold(p, "r") {
				return RdTarget{Name: sanitizeFilename(parts[i+1]), IsSubreddit: true}, nil
			}
		}
	}
	return RdTarget{}, fmt.Errorf("not a reddit user profile or subreddit url: %s (expected /user/<name> or /r/<name>)", rawURL)
}

func rdHeaders(cookiesFile, referer string) (http.Header, error) {
	h := http.Header{}
	h.Set("User-Agent", rdUserAgent)
	h.Set("Accept", "application/json, text/plain, */*")
	h.Set("Accept-Language", "en-US,en;q=0.9")
	h.Set("Sec-Fetch-Dest", "empty")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("Sec-Fetch-Site", "same-origin")
	if referer != "" {
		h.Set("Referer", referer)
	}

	if token := rdOAuthToken(); token != "" {
		h.Set("Authorization", token)
	}

	if cookiesFile != "" {
		cookies, err := loadNetscapeCookies(cookiesFile)
		if err != nil {
			return nil, fmt.Errorf("load cookies: %w", err)
		}
		cv := make([]string, 0, len(cookies))
		for _, ck := range cookies {
			cv = append(cv, ck.Name+"="+ck.Value)
		}
		if len(cv) > 0 {
			h.Set("Cookie", strings.Join(cv, "; "))
		}
	}
	return h, nil
}

func fetchRdPage(ctx context.Context, client *http.Client, cookiesFile string, target RdTarget, after string, rl *ratelimit.Manager) ([]rdPost, string, error) {
	u, _ := url.Parse(rdAPIBase)
	u.Path = target.apiPath()
	params := url.Values{}
	params.Set("limit", strconv.Itoa(rdPageSize))
	params.Set("raw_json", "1")
	if after != "" {
		params.Set("after", after)
	}
	u.RawQuery = params.Encode()
	fullURL := u.String()

	var lastErr error
	for attempt := 1; attempt <= rdMaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return nil, "", fmt.Errorf("new request: %w", err)
		}
		headers, err := rdHeaders(cookiesFile, "https://www.reddit.com/")
		if err != nil {
			return nil, "", err
		}
		req.Header = headers

		if rl != nil {
			_ = rl.GetLimiter("www.reddit.com").Wait(ctx)
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", fmt.Errorf("http: %w", err)
			}
			lastErr = fmt.Errorf("http: %w", err)
			if attempt == rdMaxAttempts {
				return nil, "", fmt.Errorf("reddit request failed after %d attempts: %w", attempt, lastErr)
			}
			time.Sleep(rdRetryBackoff << min(attempt-1, 3))
			continue
		}

		if resp.StatusCode >= 400 {
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, rdErrorPreviewLimit))
			_ = resp.Body.Close()
			body := string(preview)
			lastErr = fmt.Errorf("reddit status %d", resp.StatusCode)
			fmt.Fprintf(os.Stderr, "[reddit] response: %s\n", sanitizeErrorBody(body))
			if resp.StatusCode == http.StatusTooManyRequests {
				time.Sleep(rdMaxRetryBackoff)
				continue
			}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && attempt == rdMaxAttempts {
				return nil, "", lastErr
			}
			if resp.StatusCode >= 500 && attempt < rdMaxAttempts {
				time.Sleep(rdRetryBackoff << min(attempt-1, 3))
				continue
			}
			return nil, "", lastErr
		}

		var result rdResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			_ = resp.Body.Close()
			return nil, "", fmt.Errorf("decode: %w", err)
		}
		_ = resp.Body.Close()
		return result.Data.Children, result.Data.After, nil
	}
	return nil, "", lastErr
}

func FetchAllRdPosts(ctx context.Context, client *http.Client, cookiesFile string, target RdTarget, rl *ratelimit.Manager) ([]rdPost, error) {
	var all []rdPost
	after := ""
	for {
		page, nextAfter, err := fetchRdPage(ctx, client, cookiesFile, target, after, rl)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		if nextAfter == "" || nextAfter == after {
			break
		}
		after = nextAfter
	}
	return all, nil
}

func rdIsImageURL(u string) bool {
	low := strings.ToLower(u)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp"} {
		if strings.Contains(low, ext) {
			return true
		}
	}
	return false
}

func rdExtFromURL(u string) string {
	low := strings.ToLower(u)
	u2, _ := url.Parse(u)
	path := low
	if u2 != nil {
		path = strings.ToLower(u2.Path)
	}
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".mp4", ".webm", ".gifv"} {
		if strings.Contains(path, ext) {
			return strings.TrimPrefix(ext, ".")
		}
	}
	return ""
}

type rdMediaItem struct {
	URL  string
	Ext  string
	Kind string
}

func rdPostMedia(d rdPostData) []rdMediaItem {
	var out []rdMediaItem

	if d.GalleryData != nil && d.MediaMetadata != nil {
		for _, gi := range d.GalleryData.Items {
			mm, ok := d.MediaMetadata[gi.MediaID]
			if !ok {
				continue
			}
			src := mm.S.Gif
			if src == "" {
				src = mm.S.U
			}
			if strings.HasPrefix(src, "http") {
				out = append(out, rdMediaItem{URL: src, Ext: mm.E, Kind: "gallery"})
			}
		}
		return out
	}

	if d.IsVideo && d.Media != nil && d.Media.RedditVideo != nil {
		postURL := d.URL
		if postURL == "" || !strings.Contains(postURL, "reddit") {
			postURL = fmt.Sprintf("https://www.reddit.com/r/%s/comments/%s/", d.Subreddit, d.ID)
		}
		return []rdMediaItem{{URL: postURL, Ext: "mp4", Kind: "external-video"}}
	}

	directURL := d.URLOverriddenByDest
	if directURL == "" {
		directURL = d.URL
	}
	if directURL == "" {
		return nil
	}

	if rdIsImageURL(directURL) {
		return []rdMediaItem{{URL: directURL, Ext: rdExtFromURL(directURL), Kind: "image"}}
	}

	if strings.Contains(directURL, "imgur.com") && !rdIsImageURL(directURL) {
		if strings.Contains(directURL, "/a/") || strings.Contains(directURL, "/gallery/") {
			return nil
		}
		if !strings.HasSuffix(directURL, ".jpg") && !strings.HasSuffix(directURL, ".png") &&
			!strings.HasSuffix(directURL, ".gif") && !strings.HasSuffix(directURL, ".mp4") &&
			!strings.HasSuffix(directURL, ".gifv") {
			return []rdMediaItem{{URL: directURL, Ext: "jpg", Kind: "external"}}
		}
	}

	if strings.Contains(directURL, "redgifs.com") || strings.Contains(directURL, "gfycat.com") ||
		strings.Contains(directURL, "youtube.com") || strings.Contains(directURL, "youtu.be") ||
		strings.Contains(directURL, "vimeo.com") || strings.Contains(directURL, "streamable.com") {
		return []rdMediaItem{{URL: directURL, Ext: "mp4", Kind: "external-video"}}
	}

	if strings.HasPrefix(directURL, "http") && rdIsImageURL(directURL) {
		return []rdMediaItem{{URL: directURL, Ext: rdExtFromURL(directURL), Kind: "image"}}
	}

	return nil
}

func rdPostMarkdown(d rdPostData) []byte {
	var b strings.Builder
	b.WriteString("# " + d.Title + "\n\n")
	b.WriteString("r/" + d.Subreddit)
	if d.Author != "" {
		b.WriteString(" · u/" + d.Author)
	}
	if !time.Unix(int64(d.CreatedUTC), 0).IsZero() {
		b.WriteString(" · " + time.Unix(int64(d.CreatedUTC), 0).UTC().Format("2006-01-02 15:04 UTC"))
	}
	b.WriteString("\n\n")
	if d.SelfText != "" {
		b.WriteString(d.SelfText + "\n\n")
	}
	if d.URL != "" && !strings.HasPrefix(d.URL, "https://www.reddit.com") && !strings.Contains(d.URL, "reddit.com") {
		b.WriteString("[Link](" + d.URL + ")\n\n")
	}
	sourceURL := d.Permalink
	if sourceURL == "" {
		sourceURL = fmt.Sprintf("/r/%s/comments/%s/", d.Subreddit, d.ID)
	}
	sourceURL = "https://www.reddit.com" + sourceURL
	b.WriteString("[Source](" + sourceURL + ")\n")
	return []byte(b.String())
}

func rdPostItems(rawURL, outDir string, posts []rdPost, includePosts bool) []manifest.Item {
	creator := ""
	if target, err := ParseRdURL(rawURL); err == nil {
		creator = target.Name
	}
	items := make([]manifest.Item, 0)
	for _, p := range posts {
		d := p.Data
		published := time.Unix(int64(d.CreatedUTC), 0)
		base := filepath.Join(outDir, "reddit", creator)

		if includePosts && d.ID != "" {
			postName := d.ID + ".md"
			items = append(items, manifest.Item{
				ID:          d.Name + "_post",
				URL:         "post://reddit/" + d.ID,
				Title:       firstNonEmpty(d.Title, "post_"+d.ID),
				Filename:    postName,
				Extension:   "md",
				Source:      "reddit",
				Creator:     creator,
				PostID:      d.ID,
				PublishedAt: published,
				Destination: filepath.Join(base, "posts", postName),
				Kind:        "post",
			})
		}

		mediaItems := rdPostMedia(d)
		base = filepath.Join(outDir, "reddit", creator)
		for i, mi := range mediaItems {
			if mi.URL == "" {
				continue
			}
			var fname, dest string
			if mi.Kind != "external-video" {
				fname = fmt.Sprintf("%s_%d.%s", d.ID, i, mi.Ext)
				if mi.Ext == "" {
					fname = fmt.Sprintf("%s_%d", d.ID, i)
					if u, _ := url.Parse(mi.URL); u != nil {
						fname = sanitizeFilename(filepath.Base(u.Path))
					}
				}
				dest = filepath.Join(base, fname)
			}
			items = append(items, manifest.Item{
				ID:          d.Name + "_" + strconv.Itoa(i),
				URL:         mi.URL,
				Title:       firstNonEmpty(d.Title, mi.Kind+"_"+strconv.Itoa(i)),
				Filename:    fname,
				Extension:   mi.Ext,
				Source:      "reddit",
				Creator:     creator,
				PostID:      d.ID,
				PublishedAt: published,
				Destination: dest,
				Kind:        mi.Kind,
			})
		}

		for _, cp := range d.CrosspostParentList {
			for i, mi := range rdPostMedia(cp) {
				if mi.URL == "" {
					continue
				}
				var xname, xdest string
				if mi.Kind != "external-video" {
					xname = fmt.Sprintf("%s_xpost_%d.%s", d.ID, i, mi.Ext)
					if mi.Ext == "" {
						xname = fmt.Sprintf("%s_xpost_%d", d.ID, i)
					}
					xdest = filepath.Join(base, xname)
				}
				items = append(items, manifest.Item{
					ID:          d.Name + "_xpost_" + strconv.Itoa(i),
					URL:         mi.URL,
					Title:       firstNonEmpty(d.Title, mi.Kind+"_"+strconv.Itoa(i)),
					Filename:    xname,
					Extension:   mi.Ext,
					Source:      "reddit",
					Creator:     creator,
					PostID:      d.ID,
					PublishedAt: published,
					Destination: xdest,
					Kind:        mi.Kind,
				})
			}
		}
	}
	return items
}

func ScanReddit(ctx context.Context, rawURL, outDir string, cookiesFile string, opts RdOptions) (manifest.Manifest, error) {
	target, err := ParseRdURL(rawURL)
	if err != nil {
		return manifest.Manifest{}, err
	}
	client, err := rdClient(cookiesFile)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("create client: %w", err)
	}
	posts, err := FetchAllRdPosts(ctx, client, cookiesFile, target, opts.RateLimiter)
	if err != nil {
		return manifest.Manifest{}, err
	}
	if opts.Limit > 0 && len(posts) > opts.Limit {
		posts = posts[:opts.Limit]
	}
	m := manifest.New(rawURL, "reddit", rdPostItems(rawURL, outDir, posts, opts.IncludePosts))
	return m.Filter(opts.Filter)
}

func DownloadReddit(ctx context.Context, rawURL, outDir string, cookiesFile string, opts RdOptions, dryRun bool) error {
	target, err := ParseRdURL(rawURL)
	if err != nil {
		return err
	}
	client, err := rdClient(cookiesFile)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	posts, err := FetchAllRdPosts(ctx, client, cookiesFile, target, opts.RateLimiter)
	if err != nil {
		return err
	}
	if opts.Limit > 0 && len(posts) > opts.Limit {
		posts = posts[:opts.Limit]
	}
	items := rdPostItems(rawURL, outDir, posts, opts.IncludePosts)
	items, err = manifest.FilterItems(items, opts.Filter)
	if err != nil {
		return err
	}
	allowed := map[string]manifest.Item{}
	postsByID := map[string]manifest.Item{}
	for _, it := range items {
		allowed[it.URL] = it
		if it.Kind == "post" {
			postsByID[it.PostID] = it
		}
	}

	pool := worker.NewPool(ctx, 3)

	failed := make(map[string]string)
	var failMu sync.Mutex
	recordFailure := func(url string, err error) {
		failMu.Lock()
		failed[url] = err.Error()
		failMu.Unlock()
		fmt.Fprintf(os.Stderr, "[provenance] reddit download failed: %s: %v\n", url, err)
	}

	for _, p := range posts {
		d := p.Data
		base := filepath.Join(outDir, "reddit", target.Name)

		if postItem, ok := postsByID[d.ID]; ok {
			postDest := postItem.Destination
			postContent := rdPostMarkdown(d)
			if dryRun {
				fmt.Printf("[dry-run] reddit post: %s -> %s\n", d.ID, postDest)
			} else {
				pool.SubmitWithHooks(func() error {
					return writeFile(postDest, postContent)
				}, func() {
					fmt.Fprintf(os.Stderr, "[provenance] reddit post saved: %s\n", postDest)
				}, func(err error) {
					recordFailure("post:"+d.ID, err)
				})
			}
		}

		handleMedia := func(data rdPostData, prefix string) {
			mediaItems := rdPostMedia(data)
			for i, mi := range mediaItems {
				mi := mi
				if mi.URL == "" {
					continue
				}
				item, ok := allowed[mi.URL]
				if !ok {
					continue
				}
				dest := item.Destination
				if dest == "" {
					name := fmt.Sprintf("%s_%s_%d.%s", d.ID, prefix, i, mi.Ext)
					if mi.Ext == "" {
						name = fmt.Sprintf("%s_%s_%d", d.ID, prefix, i)
					}
					dest = filepath.Join(base, sanitizeFilename(name))
				}
				if dryRun {
					fmt.Printf("[dry-run] reddit: %s -> %s\n", mi.URL, dest)
					continue
				}
				furl := mi.URL
				if mi.Kind == "external-video" {
					pool.SubmitWithHooks(func() error {
						return RunYtdlp(ctx, furl, YtdlpOptions{
							OutputDir:   base,
							CookiesFile: cookiesFile,
							SpeedLimit:  opts.SpeedLimit,
							Progress:    opts.Progress,
						})
					}, nil, func(err error) {
						recordFailure(furl, err)
					})
					continue
				}
				pool.SubmitWithHooks(func() error {
					dl := downloader.New()
					dl.SpeedLimit = opts.SpeedLimit
					dl.Progress = opts.Progress
					return dl.Download(ctx, furl, dest, "")
				}, nil, func(err error) {
					recordFailure(furl, err)
				})
			}
		}

		handleMedia(d, "0")

		for ci, cp := range d.CrosspostParentList {
			handleMedia(cp, fmt.Sprintf("xpost%d", ci))
		}
	}

	pool.Wait()
	if len(failed) > 0 {
		return fmt.Errorf("reddit: %d file(s) failed to download (first: %s)", len(failed), firstFailedURL(failed))
	}
	return nil
}

func RdPostToItem(d rdPostData) resolve.Item {
	canonicalURL := "https://reddit.com" + d.Permalink
	item := resolve.NewItem(d.ID, canonicalURL)
	item.Title = d.Title
	item.Author = d.Author
	if d.CreatedUTC > 0 {
		t := time.Unix(int64(d.CreatedUTC), 0)
		item.PublishedAt = &t
	}
	if d.SelfText != "" {
		item.Text = &resolve.TextContent{Body: d.SelfText, Format: resolve.FormatPlain}
	} else if d.Title != "" {
		item.Text = &resolve.TextContent{Body: d.Title, Format: resolve.FormatPlain}
	}
	if raw, err := json.Marshal(d); err == nil {
		item.RawMetadata = raw
	}
	mediaItems := rdPostMedia(d)
	for _, mi := range mediaItems {
		kind := resolve.MediaImage
		if mi.Kind == "external-video" {
			kind = resolve.MediaVideo
		}
		asset := resolve.NewMediaAsset(mi.URL, kind)
		asset.Extension = mi.Ext
		item.Media = append(item.Media, asset)
	}
	for _, cp := range d.CrosspostParentList {
		for _, mi := range rdPostMedia(cp) {
			kind := resolve.MediaImage
			if mi.Kind == "external-video" {
				kind = resolve.MediaVideo
			}
			asset := resolve.NewMediaAsset(mi.URL, kind)
			asset.Extension = mi.Ext
			item.Media = append(item.Media, asset)
		}
	}
	return item
}

func ScanRedditResolved(ctx context.Context, rawURL, outDir, cookiesFile string, opts RdOptions) (resolve.Source, error) {
	target, err := ParseRdURL(rawURL)
	if err != nil {
		return resolve.Source{}, err
	}
	client, err := rdClient(cookiesFile)
	if err != nil {
		return resolve.Source{}, fmt.Errorf("create client: %w", err)
	}
	posts, err := FetchAllRdPosts(ctx, client, cookiesFile, target, opts.RateLimiter)
	if err != nil {
		return resolve.Source{}, err
	}
	if opts.Limit > 0 && len(posts) > opts.Limit {
		posts = posts[:opts.Limit]
	}
	kind := resolve.KindFeed
	var canonicalURL string
	if target.IsSubreddit {
		canonicalURL = fmt.Sprintf("https://www.reddit.com/r/%s/", target.Name)
	} else {
		canonicalURL = fmt.Sprintf("https://www.reddit.com/user/%s/", target.Name)
	}
	src := resolve.NewSource(rawURL, canonicalURL, kind, "reddit")
	src.Author = target.Name
	for _, p := range posts {
		src.Items = append(src.Items, RdPostToItem(p.Data))
	}
	return src, nil
}

var _ = fetchRdComments // future: wire into DownloadReddit for --include-comments
var _ = walkRdComments

type rdComment struct {
	ID        string      `json:"id"`
	ParentID  string      `json:"parent_id"`
	Author    string      `json:"author"`
	Body      string      `json:"body"`
	CreatedAt float64     `json:"created_utc"`
	Depth     int         `json:"depth"`
	Replies   []rdComment `json:"replies"`
}

func fetchRdComments(ctx context.Context, client *http.Client, cookiesFile string, permalink string, maxComments int, rl *ratelimit.Manager) ([]rdComment, error) {
	u := "https://www.reddit.com" + permalink + ".json"
	if maxComments <= 0 {
		maxComments = 100
	}

	var lastErr error
	for attempt := 1; attempt <= rdMaxAttempts; attempt++ {
		if rl != nil {
			_ = rl.GetLimiter("www.reddit.com").Wait(ctx)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		headers, err := rdHeaders(cookiesFile, "https://www.reddit.com/")
		if err != nil {
			return nil, err
		}
		req.Header = headers

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("http: %w", err)
			}
			lastErr = fmt.Errorf("http: %w", err)
			if attempt == rdMaxAttempts {
				return nil, fmt.Errorf("reddit comments request failed after %d attempts: %w", attempt, lastErr)
			}
			time.Sleep(rdRetryBackoff << min(attempt-1, 3))
			continue
		}

		if resp.StatusCode >= 400 {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("reddit comments status %d", resp.StatusCode)
			if resp.StatusCode == http.StatusTooManyRequests {
				time.Sleep(rdMaxRetryBackoff)
			}
			if attempt == rdMaxAttempts {
				return nil, fmt.Errorf("reddit comments request failed after %d attempts: %w", attempt, lastErr)
			}
			time.Sleep(rdRetryBackoff << min(attempt-1, 3))
			continue
		}

		raw, err := decodeRdComments(resp)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			if attempt == rdMaxAttempts {
				return nil, err
			}
			time.Sleep(rdRetryBackoff << min(attempt-1, 3))
			continue
		}

		var comments []rdComment
		for _, listing := range raw {
			for _, child := range listing.Data.Children {
				walkRdComments(&comments, child.Data.ID, child.Data.ParentID, child.Data.Author, child.Data.Body, child.Data.Created, child.Data.Replies, 0, &maxComments)
			}
		}
		return comments, nil
	}
	return nil, lastErr
}

type rdCommentsRaw struct {
	Data struct {
		Children []struct {
			Data struct {
				ID       string          `json:"id"`
				ParentID string          `json:"parent_id"`
				Author   string          `json:"author"`
				Body     string          `json:"body"`
				Created  float64         `json:"created_utc"`
				Replies  json.RawMessage `json:"replies"`
			} `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

func decodeRdComments(resp *http.Response) ([]rdCommentsRaw, error) {
	var raw []rdCommentsRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode comments: %w", err)
	}
	return raw, nil
}

func walkRdComments(out *[]rdComment, id, parentID, author, body string, created float64, repliesRaw json.RawMessage, depth int, max *int) {
	if *max <= 0 {
		return
	}
	*max--
	*out = append(*out, rdComment{
		ID:        id,
		ParentID:  parentID,
		Author:    author,
		Body:      body,
		CreatedAt: created,
		Depth:     depth,
	})

	if len(repliesRaw) == 0 || string(repliesRaw) == `""` {
		return
	}
	var replies struct {
		Data struct {
			Children []struct {
				Data struct {
					ID       string          `json:"id"`
					ParentID string          `json:"parent_id"`
					Author   string          `json:"author"`
					Body     string          `json:"body"`
					Created  float64         `json:"created_utc"`
					Replies  json.RawMessage `json:"replies"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(repliesRaw, &replies); err != nil {
		return
	}
	for _, child := range replies.Data.Children {
		walkRdComments(out, child.Data.ID, child.Data.ParentID, child.Data.Author, child.Data.Body, child.Data.Created, child.Data.Replies, depth+1, max)
	}
}
