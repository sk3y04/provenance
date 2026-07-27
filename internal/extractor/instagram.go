package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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

var igTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          10,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}

var igAPIClient = &http.Client{Timeout: 30 * time.Second, Transport: igTransport, CheckRedirect: downloader.SafeRedirect}

const (
	igAPIBase           = "https://i.instagram.com/api/v1"
	igAppID             = "936619743392459"
	igPageSize          = 12
	igMaxAttempts       = 4
	igErrorPreviewLimit = 4 << 10
	igRetryBackoff      = 3 * time.Second
	igMaxRetryBackoff   = 20 * time.Second
)

const igEncodingChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

var igUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func igAppIDFromEnv() string {
	if id := os.Getenv("INSTAGRAM_APP_ID"); id != "" {
		return id
	}
	return igAppID
}

func igShortcodeToID(shortcode string) int64 {
	s := shortcode
	if len(s) > 28 {
		s = s[:len(s)-28]
	}
	var id int64
	for _, c := range s {
		idx := strings.IndexRune(igEncodingChars, c)
		if idx < 0 {
			return 0
		}
		id = id*64 + int64(idx)
	}
	return id
}

type IgOptions struct {
	CookiesFile  string
	Filter       manifest.FilterOptions
	SpeedLimit   int64
	Progress     downloader.ProgressReporter
	Limit        int
	RateLimiter  *ratelimit.Manager
	IncludePosts bool
}

type IgTarget struct {
	Type      string // "profile", "post", "reel", "stories"
	Username  string
	Shortcode string // only for single post/reel
}

func ParseIgURL(rawURL string) (IgTarget, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return IgTarget{}, fmt.Errorf("parse url: %w", err)
	}
	host := strings.ToLower(u.Host)
	if !strings.Contains(host, "instagram") {
		return IgTarget{}, fmt.Errorf("not an instagram url: %s", rawURL)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return IgTarget{}, fmt.Errorf("not a valid instagram url: %s", rawURL)
	}

	for i, p := range parts {
		switch strings.ToLower(p) {
		case "p", "tv":
			if i+1 < len(parts) && parts[i+1] != "" {
				return IgTarget{Type: "post", Shortcode: parts[i+1]}, nil
			}
		case "reel", "reels":
			if i+1 < len(parts) && parts[i+1] != "" {
				return IgTarget{Type: "reel", Shortcode: parts[i+1]}, nil
			}
		case "stories":
			if i+1 < len(parts) && parts[i+1] != "" {
				return IgTarget{Type: "stories", Username: parts[i+1]}, nil
			}
		}
	}

	name := parts[0]
	if strings.HasPrefix(name, "?") || name == "explore" || name == "share" {
		return IgTarget{}, fmt.Errorf("unsupported instagram url type: %s", rawURL)
	}
	return IgTarget{Type: "profile", Username: name}, nil
}

func igClient(cookiesFile string) (*http.Client, string, error) {
	if cookiesFile == "" {
		return nil, "", fmt.Errorf("cookies file is required for Instagram (sessionid cookie needed; export from browser with an extension)")
	}
	cookies, err := loadNetscapeCookies(cookiesFile)
	if err != nil {
		return nil, "", fmt.Errorf("load cookies: %w", err)
	}
	var csrfToken string
	for _, ck := range cookies {
		if strings.EqualFold(ck.Name, "csrftoken") {
			csrfToken = ck.Value
			break
		}
	}
	return igAPIClient, csrfToken, nil
}

func igHeaders(cookiesFile, csrfToken string) (http.Header, error) {
	h := http.Header{}
	h.Set("User-Agent", igUserAgent)
	h.Set("X-IG-App-ID", igAppIDFromEnv())
	h.Set("X-ASBD-ID", "359341")
	h.Set("X-IG-WWW-Claim", "0")
	h.Set("Accept", "*/*")
	h.Set("Content-Type", "application/json")
	if csrfToken != "" {
		h.Set("X-CSRFToken", csrfToken)
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
		h.Set("Cookie", strings.Join(cv, "; "))
	}
	return h, nil
}

type igUserResult struct {
	Pk       json.Number `json:"pk"`
	ID       string      `json:"id"`
	Username string      `json:"username"`
	FullName string      `json:"full_name"`
}

type igProfileResponse struct {
	Data struct {
		User igUserResult `json:"user"`
	} `json:"data"`
}

type igMediaCandidate struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type igImageVersions struct {
	Candidates []igMediaCandidate `json:"candidates"`
}

type igVideoVersion struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Type   int    `json:"type"`
}

type igCaption struct {
	Text      string `json:"text"`
	CreatedAt int64  `json:"created_at"`
}

type igCarouselMedia struct {
	ImageVersions2 *igImageVersions `json:"image_versions2"`
	VideoVersions  []igVideoVersion `json:"video_versions"`
	MediaType      int              `json:"media_type"`
}

type igFeedMedia struct {
	ID             string            `json:"id"`
	Code           string            `json:"code"`
	TakenAt        int64             `json:"taken_at"`
	MediaType      int               `json:"media_type"`
	Caption        *igCaption        `json:"caption"`
	ImageVersions2 *igImageVersions  `json:"image_versions2"`
	VideoVersions  []igVideoVersion  `json:"video_versions"`
	CarouselMedia  []igCarouselMedia `json:"carousel_media"`
	User           igUserResult      `json:"user"`
}

type igFeedResponse struct {
	Items         []igFeedMedia `json:"items"`
	NextMaxID     string        `json:"next_max_id"`
	MoreAvailable bool          `json:"more_available"`
}

type igMediaInfoResponse struct {
	Items []igFeedMedia `json:"items"`
}

func resolveIgUserID(ctx context.Context, client *http.Client, cookiesFile, csrfToken, username string, rl *ratelimit.Manager) (string, error) {
	fmt.Fprintf(os.Stderr, "[instagram] resolving user @%s ...\n", username)

	params := url.Values{}
	params.Set("username", username)
	endpoint := fmt.Sprintf("%s/users/web_profile_info/?%s", igAPIBase, params.Encode())
	referer := fmt.Sprintf("https://www.instagram.com/%s/", username)

	var lastErr error
	for attempt := 1; attempt <= igMaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return "", fmt.Errorf("new request: %w", err)
		}
		headers, err := igHeaders(cookiesFile, csrfToken)
		if err != nil {
			return "", err
		}
		headers.Set("Referer", referer)
		req.Header = headers

		if rl != nil {
			_ = rl.GetLimiter("i.instagram.com").Wait(ctx)
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return "", fmt.Errorf("http: %w", err)
			}
			lastErr = fmt.Errorf("http: %w", err)
			if attempt == igMaxAttempts {
				break
			}
			time.Sleep(igRetryBackoff << min(attempt-1, 3))
			continue
		}

		if resp.StatusCode >= 400 {
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, igErrorPreviewLimit))
			_ = resp.Body.Close()
			body := string(preview)
			lastErr = fmt.Errorf("instagram status %d", resp.StatusCode)
			fmt.Fprintf(os.Stderr, "[instagram] response: %s\n", sanitizeErrorBody(body))
			if resp.StatusCode == http.StatusTooManyRequests {
				delay := igMaxRetryBackoff
				if ra := resp.Header.Get("Retry-After"); ra != "" {
					if sec, err := strconv.Atoi(ra); err == nil && sec > 0 {
						delay = time.Duration(sec) * time.Second
					}
				}
				fmt.Fprintf(os.Stderr, "[instagram] rate limited (429), waiting %v...\n", delay)
				time.Sleep(delay)
				continue
			}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && attempt == igMaxAttempts {
				break
			}
			if resp.StatusCode >= 500 && attempt < igMaxAttempts {
				time.Sleep(igRetryBackoff << min(attempt-1, 3))
				continue
			}
			break
		}

		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("read body: %w", err)
		}

		var result igProfileResponse
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", fmt.Errorf("decode: %w", err)
		}
		userID := result.Data.User.Pk.String()
		if userID == "" {
			userID = result.Data.User.ID
		}
		if userID == "" {
			fmt.Fprintf(os.Stderr, "[instagram] web_profile_info returned empty user id, response: %s\n", sanitizeErrorBody(string(respBody[:min(len(respBody), 512)])))
			lastErr = fmt.Errorf("instagram: user not found: %s", username)
			break
		}
		return userID, nil
	}

	fmt.Fprintf(os.Stderr, "[instagram] api lookup failed (%v), trying web scrape ...\n", lastErr)

	if userID, err := resolveIgUserIDFromWeb(ctx, cookiesFile, username); err == nil {
		fmt.Fprintf(os.Stderr, "[instagram] resolved user @%s -> %s via web scrape\n", username, userID)
		return userID, nil
	} else {
		fmt.Fprintf(os.Stderr, "[instagram] web scrape failed: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "[instagram] trying www api host ...\n")

	if userID, err := resolveIgUserIDWWW(ctx, client, cookiesFile, csrfToken, username); err == nil {
		fmt.Fprintf(os.Stderr, "[instagram] resolved user @%s -> %s via www api\n", username, userID)
		return userID, nil
	} else {
		fmt.Fprintf(os.Stderr, "[instagram] www api failed: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "[instagram] trying json endpoint ...\n")

	if userID, err := resolveIgUserIDJSON(ctx, cookiesFile, csrfToken, username); err == nil {
		fmt.Fprintf(os.Stderr, "[instagram] resolved user @%s -> %s via json endpoint\n", username, userID)
		return userID, nil
	} else {
		fmt.Fprintf(os.Stderr, "[instagram] json endpoint failed: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "[instagram] trying users/search ...\n")

	if userID, err := resolveIgUserIDSearch(ctx, client, cookiesFile, csrfToken, username); err == nil {
		fmt.Fprintf(os.Stderr, "[instagram] resolved user @%s -> %s via users/search\n", username, userID)
		return userID, nil
	} else {
		fmt.Fprintf(os.Stderr, "[instagram] users/search failed: %v\n", err)
	}

	return "", fmt.Errorf("resolve user id: all methods failed; api: %w", lastErr)
}

func resolveIgUserIDWWW(ctx context.Context, client *http.Client, cookiesFile, csrfToken, username string) (string, error) {
	wwwBase := "https://www.instagram.com/api/v1"
	params := url.Values{}
	params.Set("username", username)
	endpoint := fmt.Sprintf("%s/users/web_profile_info/?%s", wwwBase, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	headers, err := igHeaders(cookiesFile, csrfToken)
	if err != nil {
		return "", err
	}
	headers.Set("Referer", fmt.Sprintf("https://www.instagram.com/%s/", username))
	req.Header = headers

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("www api request: %w", err)
	}

	if resp.StatusCode >= 400 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, igErrorPreviewLimit))
		_ = resp.Body.Close()
		return "", fmt.Errorf("www api status %d: %s", resp.StatusCode, sanitizeErrorBody(string(preview)))
	}

	var result igProfileResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		_ = resp.Body.Close()
		return "", fmt.Errorf("www api decode: %w", err)
	}
	_ = resp.Body.Close()
	if result.Data.User.Pk.String() == "" && result.Data.User.ID == "" {
		return "", fmt.Errorf("www api returned empty user id")
	}
	userID := result.Data.User.Pk.String()
	if userID == "" {
		userID = result.Data.User.ID
	}
	return userID, nil
}

func resolveIgUserIDFromWeb(ctx context.Context, cookiesFile, username string) (string, error) {
	profileURL := fmt.Sprintf("https://www.instagram.com/%s/", username)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, profileURL, nil)
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}

	h := http.Header{}
	h.Set("User-Agent", igUserAgent)
	h.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	h.Set("Accept-Language", "en-US,en;q=0.9")
	h.Set("Sec-Fetch-Dest", "document")
	h.Set("Sec-Fetch-Mode", "navigate")
	h.Set("Sec-Fetch-Site", "none")
	h.Set("Sec-Fetch-User", "?1")
	h.Set("Upgrade-Insecure-Requests", "1")
	if cookiesFile != "" {
		cookies, err := loadNetscapeCookies(cookiesFile)
		if err != nil {
			return "", fmt.Errorf("load cookies: %w", err)
		}
		cv := make([]string, 0, len(cookies))
		for _, ck := range cookies {
			cv = append(cv, ck.Name+"="+ck.Value)
		}
		h.Set("Cookie", strings.Join(cv, "; "))
	}
	req.Header = h

	resp, err := igAPIClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch profile page: %w", err)
	}

	if resp.StatusCode >= 400 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, igErrorPreviewLimit))
		_ = resp.Body.Close()
		return "", fmt.Errorf("profile page status %d: %s", resp.StatusCode, sanitizeErrorBody(string(preview)))
	}

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("read profile page: %w", err)
	}

	html := string(body)
	fmt.Fprintf(os.Stderr, "[instagram] web scrape: status %d, body length %d\n", resp.StatusCode, len(body))

	if userID := extractIgUserID(html); userID != "" {
		return userID, nil
	}

	if strings.Contains(html, `"native_client_redesign"`) || strings.Contains(html, `"entry_data"`) {
		fmt.Fprintf(os.Stderr, "[instagram] web scrape: page structure looks like profile but no ID patterns matched, preview:\n%s\n", sanitizeErrorBody(html[:min(len(html), 1024)]))
	} else {
		fmt.Fprintf(os.Stderr, "[instagram] web scrape: page does not look like a profile page (possibly login/challenge wall), preview:\n%s\n", sanitizeErrorBody(html[:min(len(html), 1024)]))
	}

	return "", fmt.Errorf("could not find user id in profile page html")
}

func resolveIgUserIDJSON(ctx context.Context, cookiesFile, csrfToken, username string) (string, error) {
	jsonURL := fmt.Sprintf("https://www.instagram.com/%s/?__a=1&__d=1", username)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jsonURL, nil)
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}

	headers, err := igHeaders(cookiesFile, csrfToken)
	if err != nil {
		return "", err
	}
	headers.Set("Accept", "application/json, text/plain, */*")
	headers.Set("X-Requested-With", "XMLHttpRequest")
	headers.Set("Referer", fmt.Sprintf("https://www.instagram.com/%s/", username))
	req.Header = headers

	resp, err := igAPIClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("json endpoint request: %w", err)
	}

	if resp.StatusCode != 200 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, igErrorPreviewLimit))
		_ = resp.Body.Close()
		return "", fmt.Errorf("json endpoint status %d: %s", resp.StatusCode, sanitizeErrorBody(string(preview)))
	}

	var result struct {
		GraphQL struct {
			User struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"user"`
		} `json:"graphql"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		_ = resp.Body.Close()
		return "", fmt.Errorf("json endpoint decode: %w", err)
	}
	_ = resp.Body.Close()
	if result.GraphQL.User.ID != "" {
		return result.GraphQL.User.ID, nil
	}

	return "", fmt.Errorf("json endpoint returned empty user id")
}

func resolveIgUserIDSearch(ctx context.Context, client *http.Client, cookiesFile, csrfToken, username string) (string, error) {
	params := url.Values{}
	params.Set("q", username)
	params.Set("count", "1")
	endpoint := fmt.Sprintf("%s/users/search/?%s", igAPIBase, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	headers, err := igHeaders(cookiesFile, csrfToken)
	if err != nil {
		return "", err
	}
	headers.Set("Referer", fmt.Sprintf("https://www.instagram.com/%s/", username))
	req.Header = headers

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request: %w", err)
	}

	if resp.StatusCode >= 400 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, igErrorPreviewLimit))
		_ = resp.Body.Close()
		return "", fmt.Errorf("search status %d: %s", resp.StatusCode, sanitizeErrorBody(string(preview)))
	}

	respBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("search read body: %w", err)
	}

	var result struct {
		Users []struct {
			Pk       json.Number `json:"pk"`
			Username string      `json:"username"`
		} `json:"users"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("search decode: %w", err)
	}

	for _, u := range result.Users {
		if strings.EqualFold(u.Username, username) {
			return u.Pk.String(), nil
		}
	}
	if len(result.Users) > 0 {
		return result.Users[0].Pk.String(), nil
	}

	return "", fmt.Errorf("search returned no results for %s", username)
}

func extractIgUserID(html string) string {
	if idx := strings.Index(html, `"profilePage_`); idx >= 0 {
		rest := html[idx+len(`"profilePage_"`):]
		var id string
		for _, c := range rest {
			if c >= '0' && c <= '9' {
				id += string(c)
			} else {
				break
			}
		}
		if id != "" {
			return id
		}
	}

	if idx := strings.Index(html, `instagram://user?id=`); idx >= 0 {
		rest := html[idx+len(`instagram://user?id=`):]
		var id string
		for _, c := range rest {
			if c >= '0' && c <= '9' {
				id += string(c)
			} else {
				break
			}
		}
		if id != "" {
			return id
		}
	}

	if idx := strings.Index(html, `"user_id":"`); idx >= 0 {
		rest := html[idx+len(`"user_id":"`):]
		var id string
		for _, c := range rest {
			if c >= '0' && c <= '9' {
				id += string(c)
			} else {
				break
			}
		}
		if id != "" {
			return id
		}
	}

	return ""
}

func fetchIgMediaPage(ctx context.Context, client *http.Client, cookiesFile, csrfToken, userID, cursor string, rl *ratelimit.Manager) ([]igFeedMedia, string, error) {
	params := url.Values{}
	params.Set("count", strconv.Itoa(igPageSize))
	if cursor != "" {
		params.Set("max_id", cursor)
	}
	endpoint := fmt.Sprintf("%s/feed/user/%s/?%s", igAPIBase, userID, params.Encode())

	var lastErr error
	for attempt := 1; attempt <= igMaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, "", fmt.Errorf("new request: %w", err)
		}
		headers, err := igHeaders(cookiesFile, csrfToken)
		if err != nil {
			return nil, "", err
		}
		req.Header = headers

		if rl != nil {
			_ = rl.GetLimiter("i.instagram.com").Wait(ctx)
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", fmt.Errorf("http: %w", err)
			}
			lastErr = fmt.Errorf("http: %w", err)
			if attempt == igMaxAttempts {
				return nil, "", fmt.Errorf("instagram request failed after %d attempts: %w", attempt, lastErr)
			}
			time.Sleep(igRetryBackoff << min(attempt-1, 3))
			continue
		}

		if resp.StatusCode >= 400 {
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, igErrorPreviewLimit))
			_ = resp.Body.Close()
			body := string(preview)
			lastErr = fmt.Errorf("instagram status %d", resp.StatusCode)
			fmt.Fprintf(os.Stderr, "[instagram] response: %s\n", sanitizeErrorBody(body))
			if resp.StatusCode == http.StatusTooManyRequests {
				delay := igMaxRetryBackoff
				if ra := resp.Header.Get("Retry-After"); ra != "" {
					if sec, err := strconv.Atoi(ra); err == nil && sec > 0 {
						delay = time.Duration(sec) * time.Second
					}
				}
				fmt.Fprintf(os.Stderr, "[instagram] rate limited (429), waiting %v...\n", delay)
				time.Sleep(delay)
				continue
			}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && attempt == igMaxAttempts {
				return nil, "", lastErr
			}
			if resp.StatusCode >= 500 && attempt < igMaxAttempts {
				time.Sleep(igRetryBackoff << min(attempt-1, 3))
				continue
			}
			return nil, "", lastErr
		}

		var result igFeedResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			_ = resp.Body.Close()
			return nil, "", fmt.Errorf("decode: %w", err)
		}
		_ = resp.Body.Close()
		return result.Items, result.NextMaxID, nil
	}
	return nil, "", lastErr
}

func FetchAllIgPosts(ctx context.Context, client *http.Client, cookiesFile, csrfToken, userID string, limit int, rl *ratelimit.Manager) ([]igFeedMedia, error) {
	var all []igFeedMedia
	cursor := ""
	pages := 0
	for {
		page, nextCursor, err := fetchIgMediaPage(ctx, client, cookiesFile, csrfToken, userID, cursor, rl)
		if err != nil {
			if len(all) > 0 && strings.Contains(err.Error(), "429") {
				fmt.Fprintf(os.Stderr, "[instagram] rate limited mid-scan, returning %d items from %d pages\n", len(all), pages)
				return all, nil
			}
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		pages++
		all = append(all, page...)
		fmt.Fprintf(os.Stderr, "[instagram] page %d: %d posts (%d total)\n", pages, len(page), len(all))
		if limit > 0 && len(all) >= limit {
			all = all[:limit]
			break
		}
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return all, nil
}

func fetchSingleIgPost(ctx context.Context, client *http.Client, cookiesFile, csrfToken, shortcode string, rl *ratelimit.Manager) (*igFeedMedia, error) {
	mediaID := igShortcodeToID(shortcode)
	if mediaID == 0 {
		return nil, fmt.Errorf("invalid instagram shortcode: %s", shortcode)
	}
	endpoint := fmt.Sprintf("%s/media/%d/info/", igAPIBase, mediaID)

	var lastErr error
	for attempt := 1; attempt <= igMaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		headers, err := igHeaders(cookiesFile, csrfToken)
		if err != nil {
			return nil, err
		}
		req.Header = headers

		if rl != nil {
			_ = rl.GetLimiter("i.instagram.com").Wait(ctx)
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("http: %w", err)
			}
			lastErr = fmt.Errorf("http: %w", err)
			if attempt == igMaxAttempts {
				return nil, fmt.Errorf("instagram request failed after %d attempts: %w", attempt, lastErr)
			}
			time.Sleep(igRetryBackoff << min(attempt-1, 3))
			continue
		}

		if resp.StatusCode >= 400 {
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, igErrorPreviewLimit))
			_ = resp.Body.Close()
			body := string(preview)
			lastErr = fmt.Errorf("instagram status %d", resp.StatusCode)
			fmt.Fprintf(os.Stderr, "[instagram] response: %s\n", sanitizeErrorBody(body))
			if resp.StatusCode == http.StatusTooManyRequests {
				delay := igMaxRetryBackoff
				if ra := resp.Header.Get("Retry-After"); ra != "" {
					if sec, err := strconv.Atoi(ra); err == nil && sec > 0 {
						delay = time.Duration(sec) * time.Second
					}
				}
				fmt.Fprintf(os.Stderr, "[instagram] rate limited (429), waiting %v...\n", delay)
				time.Sleep(delay)
				continue
			}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && attempt == igMaxAttempts {
				return nil, lastErr
			}
			if resp.StatusCode >= 500 && attempt < igMaxAttempts {
				time.Sleep(igRetryBackoff << min(attempt-1, 3))
				continue
			}
			return nil, lastErr
		}

		var result igMediaInfoResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("decode: %w", err)
		}
		_ = resp.Body.Close()
		if len(result.Items) == 0 {
			return nil, fmt.Errorf("instagram: post not found: %s", shortcode)
		}
		return &result.Items[0], nil
	}
	return nil, lastErr
}

func fetchIgStories(ctx context.Context, client *http.Client, cookiesFile, csrfToken, userID string, rl *ratelimit.Manager) ([]igFeedMedia, error) {
	params := url.Values{}
	params.Set("reel_ids", userID)
	endpoint := fmt.Sprintf("%s/feed/reels_media/?%s", igAPIBase, params.Encode())

	var lastErr error
	for attempt := 1; attempt <= igMaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		headers, err := igHeaders(cookiesFile, csrfToken)
		if err != nil {
			return nil, err
		}
		req.Header = headers

		if rl != nil {
			_ = rl.GetLimiter("i.instagram.com").Wait(ctx)
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("http: %w", err)
			}
			lastErr = fmt.Errorf("http: %w", err)
			if attempt == igMaxAttempts {
				return nil, fmt.Errorf("instagram stories request failed after %d attempts: %w", attempt, lastErr)
			}
			time.Sleep(igRetryBackoff << min(attempt-1, 3))
			continue
		}

		if resp.StatusCode >= 400 {
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, igErrorPreviewLimit))
			_ = resp.Body.Close()
			body := string(preview)
			lastErr = fmt.Errorf("instagram stories status %d", resp.StatusCode)
			fmt.Fprintf(os.Stderr, "[instagram] response: %s\n", sanitizeErrorBody(body))
			if resp.StatusCode == http.StatusTooManyRequests {
				delay := igMaxRetryBackoff
				if ra := resp.Header.Get("Retry-After"); ra != "" {
					if sec, err := strconv.Atoi(ra); err == nil && sec > 0 {
						delay = time.Duration(sec) * time.Second
					}
				}
				fmt.Fprintf(os.Stderr, "[instagram] rate limited (429), waiting %v...\n", delay)
				time.Sleep(delay)
				continue
			}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && attempt == igMaxAttempts {
				return nil, lastErr
			}
			if resp.StatusCode >= 500 && attempt < igMaxAttempts {
				time.Sleep(igRetryBackoff << min(attempt-1, 3))
				continue
			}
			return nil, lastErr
		}

		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}

		var result struct {
			Reels map[string]struct {
				Items []igFeedMedia `json:"items"`
			} `json:"reels"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}

		for _, reel := range result.Reels {
			if len(reel.Items) > 0 {
				fmt.Fprintf(os.Stderr, "[instagram] found %d stories\n", len(reel.Items))
				return reel.Items, nil
			}
		}

		return nil, fmt.Errorf("instagram: no stories found for user %s", userID)
	}
	return nil, lastErr
}

func igMediaTypeName(mediaType int) string {
	switch mediaType {
	case 1:
		return "image"
	case 2:
		return "video"
	case 8:
		return "carousel"
	}
	return "unknown"
}

func igMediaBestURLs(media igFeedMedia) []string {
	switch media.MediaType {
	case 1: // image
		if media.ImageVersions2 != nil && len(media.ImageVersions2.Candidates) > 0 {
			return []string{media.ImageVersions2.Candidates[0].URL}
		}
	case 2: // video
		if len(media.VideoVersions) > 0 {
			variants := append([]igVideoVersion(nil), media.VideoVersions...)
			sort.Slice(variants, func(i, j int) bool {
				if variants[i].Height != variants[j].Height {
					return variants[i].Height > variants[j].Height
				}
				return variants[i].Width > variants[j].Width
			})
			return []string{variants[0].URL}
		}
	case 8: // carousel
		var urls []string
		for _, cm := range media.CarouselMedia {
			switch cm.MediaType {
			case 1:
				if cm.ImageVersions2 != nil && len(cm.ImageVersions2.Candidates) > 0 {
					urls = append(urls, cm.ImageVersions2.Candidates[0].URL)
				}
			case 2:
				if len(cm.VideoVersions) > 0 {
					vs := append([]igVideoVersion(nil), cm.VideoVersions...)
					sort.Slice(vs, func(i, j int) bool {
						if vs[i].Height != vs[j].Height {
							return vs[i].Height > vs[j].Height
						}
						return vs[i].Width > vs[j].Width
					})
					urls = append(urls, vs[0].URL)
				}
			}
		}
		return urls
	}
	return nil
}

func igMediaExt(media igFeedMedia) string {
	switch media.MediaType {
	case 1:
		return "jpg"
	case 2:
		return "mp4"
	case 8:
		// carousel - check first item's type
		if len(media.CarouselMedia) > 0 && media.CarouselMedia[0].MediaType == 2 {
			return "mp4"
		}
		return "jpg"
	}
	return ""
}

func igPostMarkdown(post igFeedMedia, username string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "@%s", username)
	if !strings.EqualFold(post.User.FullName, "") {
		fmt.Fprintf(&b, " · %s", post.User.FullName)
	}
	if post.TakenAt > 0 {
		t := time.Unix(post.TakenAt, 0).UTC()
		fmt.Fprintf(&b, " · %s", t.Format("2006-01-02 15:04 UTC"))
	}
	b.WriteString("\n\n")
	if post.Caption != nil && post.Caption.Text != "" {
		b.WriteString(post.Caption.Text)
		b.WriteString("\n\n")
	}
	if post.Code != "" {
		fmt.Fprintf(&b, "[Source](https://www.instagram.com/p/%s/)\n", post.Code)
	}
	return []byte(b.String())
}

func igPostItems(rawURL, outDir string, posts []igFeedMedia, username string, includePosts bool) []manifest.Item {
	items := make([]manifest.Item, 0)
	for _, p := range posts {
		p := p
		base := filepath.Join(outDir, "instagram", username)

		if includePosts && p.Code != "" {
			postName := p.Code + ".md"
			title := ""
			if p.Caption != nil {
				title = p.Caption.Text
			}
			items = append(items, manifest.Item{
				ID:          p.Code + "_post",
				URL:         "post://instagram/" + p.Code,
				Title:       firstNonEmpty(title, "post_"+p.Code),
				Filename:    postName,
				Extension:   "md",
				Source:      "instagram",
				Creator:     username,
				PostID:      p.Code,
				PublishedAt: time.Unix(p.TakenAt, 0),
				Destination: filepath.Join(base, postName),
				Kind:        "post",
			})
		}

		if p.MediaType == 8 {
			for ci, cm := range p.CarouselMedia {
				urls := igCarouselBestURLs(cm)
				for _, mediaURL := range urls {
					ext := igCarouselExt(cm)
					name := fmt.Sprintf("%s_carousel_%d.%s", p.Code, ci, ext)
					items = append(items, manifest.Item{
						ID:          fmt.Sprintf("%s_cm%d", p.Code, ci),
						URL:         mediaURL,
						Title:       name,
						Filename:    name,
						Extension:   ext,
						Source:      "instagram",
						Creator:     username,
						PostID:      p.Code,
						PublishedAt: time.Unix(p.TakenAt, 0),
						Destination: filepath.Join(base, "images", sanitizeFilename(name)),
					})
				}
			}
			continue
		}

		urls := igMediaBestURLs(p)
		ext := igMediaExt(p)
		typeName := igMediaTypeName(p.MediaType)
		for i, mediaURL := range urls {
			var subdir string
			switch p.MediaType {
			case 2:
				subdir = "videos"
			case 1:
				subdir = "images"
			default:
				subdir = typeName
			}
			var name string
			if len(urls) > 1 {
				name = fmt.Sprintf("%s_%d.%s", p.Code, i, ext)
			} else {
				name = fmt.Sprintf("%s.%s", p.Code, ext)
			}
			items = append(items, manifest.Item{
				ID:          fmt.Sprintf("%s_%d", p.Code, i),
				URL:         mediaURL,
				Title:       name,
				Filename:    name,
				Extension:   ext,
				Source:      "instagram",
				Creator:     username,
				PostID:      p.Code,
				PublishedAt: time.Unix(p.TakenAt, 0),
				Destination: filepath.Join(base, subdir, sanitizeFilename(name)),
			})
		}
	}
	return items
}

func igCarouselBestURLs(cm igCarouselMedia) []string {
	switch cm.MediaType {
	case 1:
		if cm.ImageVersions2 != nil && len(cm.ImageVersions2.Candidates) > 0 {
			return []string{cm.ImageVersions2.Candidates[0].URL}
		}
	case 2:
		if len(cm.VideoVersions) > 0 {
			vs := append([]igVideoVersion(nil), cm.VideoVersions...)
			sort.Slice(vs, func(i, j int) bool {
				if vs[i].Height != vs[j].Height {
					return vs[i].Height > vs[j].Height
				}
				return vs[i].Width > vs[j].Width
			})
			return []string{vs[0].URL}
		}
	}
	return nil
}

func igCarouselExt(cm igCarouselMedia) string {
	switch cm.MediaType {
	case 1:
		return "jpg"
	case 2:
		return "mp4"
	}
	return "jpg"
}

func ScanInstagram(ctx context.Context, rawURL, outDir string, cookiesFile string, opts IgOptions) (manifest.Manifest, error) {
	target, err := ParseIgURL(rawURL)
	if err != nil {
		return manifest.Manifest{}, err
	}
	client, csrfToken, err := igClient(cookiesFile)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("create client: %w", err)
	}

	var posts []igFeedMedia
	var username string

	switch target.Type {
	case "profile":
		userID, err := resolveIgUserID(ctx, client, cookiesFile, csrfToken, target.Username, opts.RateLimiter)
		if err != nil {
			return manifest.Manifest{}, fmt.Errorf("resolve user id: %w", err)
		}
		posts, err = FetchAllIgPosts(ctx, client, cookiesFile, csrfToken, userID, opts.Limit, opts.RateLimiter)
		if err != nil {
			return manifest.Manifest{}, err
		}
		username = target.Username

	case "stories":
		userID, err := resolveIgUserID(ctx, client, cookiesFile, csrfToken, target.Username, opts.RateLimiter)
		if err != nil {
			return manifest.Manifest{}, fmt.Errorf("resolve user id: %w", err)
		}
		posts, err = fetchIgStories(ctx, client, cookiesFile, csrfToken, userID, opts.RateLimiter)
		if err != nil {
			return manifest.Manifest{}, err
		}
		username = target.Username

	case "post", "reel":
		post, err := fetchSingleIgPost(ctx, client, cookiesFile, csrfToken, target.Shortcode, opts.RateLimiter)
		if err != nil {
			return manifest.Manifest{}, err
		}
		posts = []igFeedMedia{*post}
		username = post.User.Username
		if username == "" {
			username = target.Username
		}
		if username == "" {
			username = sanitizeFilename(target.Shortcode)
		}
	}

	if opts.Limit > 0 && len(posts) > opts.Limit {
		posts = posts[:opts.Limit]
	}
	m := manifest.New(rawURL, "instagram", igPostItems(rawURL, outDir, posts, username, opts.IncludePosts))
	return m.Filter(opts.Filter)
}

func DownloadInstagram(ctx context.Context, rawURL, outDir string, cookiesFile string, opts IgOptions, dryRun bool) error {
	target, err := ParseIgURL(rawURL)
	if err != nil {
		return err
	}
	client, csrfToken, err := igClient(cookiesFile)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	var posts []igFeedMedia
	var username string

	switch target.Type {
	case "profile":
		userID, err := resolveIgUserID(ctx, client, cookiesFile, csrfToken, target.Username, opts.RateLimiter)
		if err != nil {
			return fmt.Errorf("resolve user id: %w", err)
		}
		posts, err = FetchAllIgPosts(ctx, client, cookiesFile, csrfToken, userID, opts.Limit, opts.RateLimiter)
		if err != nil {
			return err
		}
		username = target.Username

	case "stories":
		userID, err := resolveIgUserID(ctx, client, cookiesFile, csrfToken, target.Username, opts.RateLimiter)
		if err != nil {
			return fmt.Errorf("resolve user id: %w", err)
		}
		posts, err = fetchIgStories(ctx, client, cookiesFile, csrfToken, userID, opts.RateLimiter)
		if err != nil {
			return err
		}
		username = target.Username

	case "post", "reel":
		post, err := fetchSingleIgPost(ctx, client, cookiesFile, csrfToken, target.Shortcode, opts.RateLimiter)
		if err != nil {
			return err
		}
		posts = []igFeedMedia{*post}
		username = post.User.Username
		if username == "" {
			username = target.Username
		}
		if username == "" {
			username = sanitizeFilename(target.Shortcode)
		}
	}

	if opts.Limit > 0 && len(posts) > opts.Limit {
		posts = posts[:opts.Limit]
	}

	items := igPostItems(rawURL, outDir, posts, username, opts.IncludePosts)
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
	recordFailure := func(id string, err error) {
		failMu.Lock()
		failed[id] = err.Error()
		failMu.Unlock()
		fmt.Fprintf(os.Stderr, "[provenance] instagram download failed: %s: %v\n", id, err)
	}

	for _, p := range posts {
		p := p
		base := filepath.Join(outDir, "instagram", username)

		if postItem, ok := postsByID[p.Code]; ok {
			postDest := postItem.Destination
			postContent := igPostMarkdown(p, username)
			if dryRun {
				fmt.Printf("[dry-run] instagram post: %s -> %s\n", p.Code, postDest)
			} else {
				pool.SubmitWithHooks(func() error {
					return writeFile(postDest, postContent)
				}, func() {
					fmt.Fprintf(os.Stderr, "[provenance] instagram post saved: %s\n", postDest)
				}, func(err error) {
					recordFailure("post:"+p.Code, err)
				})
			}
		}

		handleMedia := func(media igFeedMedia, allowMap map[string]manifest.Item, prefix string) {
			if media.MediaType == 8 {
				for ci, cm := range media.CarouselMedia {
					urls := igCarouselBestURLs(cm)
					ext := igCarouselExt(cm)
					for _, mediaURL := range urls {
						mediaURL := mediaURL
						if mediaURL == "" {
							continue
						}
						item, ok := allowMap[mediaURL]
						if !ok {
							continue
						}
						dest := item.Destination
						if dest == "" {
							name := fmt.Sprintf("%s_carousel_%d.%s", p.Code, ci, ext)
							dest = filepath.Join(base, "images", sanitizeFilename(name))
						}
						if dryRun {
							fmt.Printf("[dry-run] instagram: %s -> %s\n", mediaURL, dest)
							continue
						}
						furl := mediaURL
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
				return
			}

			urls := igMediaBestURLs(media)
			ext := igMediaExt(media)
			var subdir string
			switch media.MediaType {
			case 2:
				subdir = "videos"
			default:
				subdir = "images"
			}
			for i, mediaURL := range urls {
				mediaURL := mediaURL
				if mediaURL == "" {
					continue
				}
				item, ok := allowMap[mediaURL]
				if !ok {
					continue
				}
				dest := item.Destination
				if dest == "" {
					var name string
					if len(urls) > 1 {
						name = fmt.Sprintf("%s_%d.%s", p.Code, i, ext)
					} else {
						name = fmt.Sprintf("%s.%s", p.Code, ext)
					}
					dest = filepath.Join(base, subdir, sanitizeFilename(name))
				}
				if dryRun {
					fmt.Printf("[dry-run] instagram: %s -> %s\n", mediaURL, dest)
					continue
				}
				furl := mediaURL
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

		handleMedia(p, allowed, "0")
	}

	pool.Wait()
	if len(failed) > 0 {
		return fmt.Errorf("instagram: %d file(s) failed to download (first: %s)", len(failed), firstFailedURL(failed))
	}
	return nil
}

func IgPostToItem(p igFeedMedia, username, targetType string) resolve.Item {
	var canonicalURL string
	switch targetType {
	case "reel":
		canonicalURL = fmt.Sprintf("https://instagram.com/reel/%s/", p.Code)
	case "tv":
		canonicalURL = fmt.Sprintf("https://instagram.com/tv/%s/", p.Code)
	default:
		canonicalURL = fmt.Sprintf("https://instagram.com/p/%s/", p.Code)
	}
	item := resolve.NewItem(p.Code, canonicalURL)
	item.Author = username
	if p.Caption != nil {
		item.Title = p.Caption.Text
	}
	if p.TakenAt > 0 {
		t := time.Unix(p.TakenAt, 0)
		item.PublishedAt = &t
	}
	if p.Caption != nil && p.Caption.Text != "" {
		item.Text = &resolve.TextContent{Body: p.Caption.Text, Format: resolve.FormatPlain}
	}
	if raw, err := json.Marshal(p); err == nil {
		item.RawMetadata = raw
	}

	if p.MediaType == 8 {
		for _, cm := range p.CarouselMedia {
			urls := igCarouselBestURLs(cm)
			ext := igCarouselExt(cm)
			kind := resolve.MediaImage
			if cm.MediaType == 2 {
				kind = resolve.MediaVideo
			}
			for _, mediaURL := range urls {
				asset := resolve.NewMediaAsset(mediaURL, kind)
				asset.Extension = ext
				item.Media = append(item.Media, asset)
			}
		}
		return item
	}

	urls := igMediaBestURLs(p)
	ext := igMediaExt(p)
	kind := resolve.MediaImage
	if p.MediaType == 2 {
		kind = resolve.MediaVideo
	}
	for _, mediaURL := range urls {
		asset := resolve.NewMediaAsset(mediaURL, kind)
		asset.Extension = ext
		item.Media = append(item.Media, asset)
	}
	return item
}

func ScanInstagramResolved(ctx context.Context, rawURL, outDir, cookiesFile string, opts IgOptions) (resolve.Source, error) {
	target, err := ParseIgURL(rawURL)
	if err != nil {
		return resolve.Source{}, err
	}
	client, csrfToken, err := igClient(cookiesFile)
	if err != nil {
		return resolve.Source{}, fmt.Errorf("create client: %w", err)
	}

	var posts []igFeedMedia
	var username string

	switch target.Type {
	case "profile":
		userID, err := resolveIgUserID(ctx, client, cookiesFile, csrfToken, target.Username, opts.RateLimiter)
		if err != nil {
			return resolve.Source{}, fmt.Errorf("resolve user id: %w", err)
		}
		posts, err = FetchAllIgPosts(ctx, client, cookiesFile, csrfToken, userID, opts.Limit, opts.RateLimiter)
		if err != nil {
			return resolve.Source{}, err
		}
		username = target.Username

	case "stories":
		userID, err := resolveIgUserID(ctx, client, cookiesFile, csrfToken, target.Username, opts.RateLimiter)
		if err != nil {
			return resolve.Source{}, fmt.Errorf("resolve user id: %w", err)
		}
		posts, err = fetchIgStories(ctx, client, cookiesFile, csrfToken, userID, opts.RateLimiter)
		if err != nil {
			return resolve.Source{}, err
		}
		username = target.Username

	case "post", "reel":
		post, err := fetchSingleIgPost(ctx, client, cookiesFile, csrfToken, target.Shortcode, opts.RateLimiter)
		if err != nil {
			return resolve.Source{}, err
		}
		posts = []igFeedMedia{*post}
		username = post.User.Username
		if username == "" {
			username = target.Username
		}
		if username == "" {
			username = sanitizeFilename(target.Shortcode)
		}
	}

	if opts.Limit > 0 && len(posts) > opts.Limit {
		posts = posts[:opts.Limit]
	}
	kind := resolve.KindFeed
	canonicalURL := fmt.Sprintf("https://www.instagram.com/%s/", username)
	if target.Type == "post" || target.Type == "reel" || target.Type == "tv" {
		kind = resolve.KindSingle
		switch target.Type {
		case "reel":
			canonicalURL = fmt.Sprintf("https://www.instagram.com/reel/%s/", target.Shortcode)
		case "tv":
			canonicalURL = fmt.Sprintf("https://www.instagram.com/tv/%s/", target.Shortcode)
		default:
			canonicalURL = fmt.Sprintf("https://www.instagram.com/p/%s/", target.Shortcode)
		}
	}
	if target.Type == "stories" {
		kind = resolve.KindSingle
		canonicalURL = fmt.Sprintf("https://www.instagram.com/stories/%s/", username)
	}
	src := resolve.NewSource(rawURL, canonicalURL, kind, "instagram")
	src.Author = username
	for _, p := range posts {
		src.Items = append(src.Items, IgPostToItem(p, username, target.Type))
	}
	return src, nil
}
