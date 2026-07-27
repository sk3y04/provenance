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
	"regexp"
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

var twitterTransport = &http.Transport{
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

var (
	twitterRefreshClient = &http.Client{Timeout: 15 * time.Second, Transport: twitterTransport, CheckRedirect: downloader.SafeRedirect}
	twitterAPIClient     = &http.Client{Timeout: 30 * time.Second, Transport: twitterTransport, CheckRedirect: downloader.SafeRedirect}
)

const (
	twAPIBase           = "https://x.com/i/api/graphql"
	twPageSize          = 20
	twMaxAttempts       = 4
	twErrorPreviewLimit = 4 << 10
	twRetryBackoff      = 2 * time.Second
	twMaxRetryBackoff   = 15 * time.Second
)

var twUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func twBearerToken() string {
	if t := os.Getenv("TWITTER_BEARER_TOKEN"); t != "" {
		return "Bearer " + t
	}
	twQueryIDCache.mu.Lock()
	defer twQueryIDCache.mu.Unlock()
	if twQueryIDCache.guestBearer != "" {
		return twQueryIDCache.guestBearer
	}
	// Public guest token from Twitter's web client JS. Same for all
	// unauthenticated visitors; not a secret. Refreshed at runtime via
	// twRefreshQueryIDs. Hardcoded fallback for offline / first-run.
	return "Bearer AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"
}

var twQueryIDCache struct {
	mu               sync.RWMutex
	userByScreenName string
	userMedia        string
	userTweets       string
	guestBearer      string
}

func twUserByScreenNameQueryID() string {
	if v := os.Getenv("TWITTER_QUERY_USER_BY_SCREEN_NAME"); v != "" {
		return v
	}
	twQueryIDCache.mu.RLock()
	if twQueryIDCache.userByScreenName != "" {
		v := twQueryIDCache.userByScreenName
		twQueryIDCache.mu.RUnlock()
		return v
	}
	twQueryIDCache.mu.RUnlock()
	return "2qvSHpkWTMS9i0zJAwDNiA"
}

func twUserTweetsQueryID() string {
	if v := os.Getenv("TWITTER_QUERY_USER_TWEETS"); v != "" {
		return v
	}
	twQueryIDCache.mu.RLock()
	if twQueryIDCache.userTweets != "" {
		v := twQueryIDCache.userTweets
		twQueryIDCache.mu.RUnlock()
		return v
	}
	twQueryIDCache.mu.RUnlock()
	return "6r5OLCC_wFH4CpRyXKuAmQ"
}

func twRefreshQueryIDs(ctx context.Context, cookiesFile string) {
	twRefreshQueryIDsForce(ctx, cookiesFile, false)
}

func twRefreshQueryIDsForce(ctx context.Context, cookiesFile string, force bool) {
	twQueryIDCache.mu.Lock()
	if !force && twQueryIDCache.userByScreenName != "" && twQueryIDCache.userMedia != "" && twQueryIDCache.userTweets != "" {
		twQueryIDCache.mu.Unlock()
		return
	}
	twQueryIDCache.mu.Unlock()

	client := twitterRefreshClient
	req, err := http.NewRequestWithContext(ctx, "GET", "https://x.com/home", nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", twUserAgent)
	if cookiesFile != "" {
		cookies, err := loadNetscapeCookies(cookiesFile)
		if err == nil {
			cv := make([]string, 0, len(cookies))
			for _, ck := range cookies {
				cv = append(cv, ck.Name+"="+ck.Value)
			}
			req.Header.Set("Cookie", strings.Join(cv, "; "))
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	re := regexp.MustCompile(`https://abs\.twimg\.com/responsive-web/client-web/main\.[a-f0-9]+\.js`)
	jsURL := re.FindString(string(body))
	if jsURL == "" {
		return
	}
	req2, err := http.NewRequestWithContext(ctx, "GET", jsURL, nil)
	if err != nil {
		return
	}
	req2.Header.Set("User-Agent", twUserAgent)
	resp2, err := client.Do(req2)
	if err != nil {
		return
	}
	defer func() { _ = resp2.Body.Close() }()
	jsBody, _ := io.ReadAll(io.LimitReader(resp2.Body, 4<<20))

	twQueryIDCache.mu.Lock()
	defer twQueryIDCache.mu.Unlock()
	re2 := regexp.MustCompile(`queryId:"([^"]+)",operationName:"UserByScreenName"`)
	if m := re2.FindStringSubmatch(string(jsBody)); len(m) > 1 {
		twQueryIDCache.userByScreenName = m[1]
	}
	re3 := regexp.MustCompile(`queryId:"([^"]+)",operationName:"UserMedia"`)
	if m := re3.FindStringSubmatch(string(jsBody)); len(m) > 1 {
		twQueryIDCache.userMedia = m[1]
	}
	re4 := regexp.MustCompile(`queryId:"([^"]+)",operationName:"UserTweets"`)
	if m := re4.FindStringSubmatch(string(jsBody)); len(m) > 1 {
		twQueryIDCache.userTweets = m[1]
	}
	// Guest bearer token - also public, extracted from the web client JS.
	re5 := regexp.MustCompile(`"(AAAAA[^"]{50,})"`)
	if m := re5.FindStringSubmatch(string(jsBody)); len(m) > 1 {
		twQueryIDCache.guestBearer = "Bearer " + m[1]
	}
}

func twInvalidateQueryIDs() {
	twQueryIDCache.mu.Lock()
	defer twQueryIDCache.mu.Unlock()
	twQueryIDCache.userByScreenName = ""
	twQueryIDCache.userMedia = ""
	twQueryIDCache.userTweets = ""
}

var twFeatures = map[string]interface{}{
	"hidden_profile_likes_enabled":                                      true,
	"hidden_profile_subscriptions_enabled":                              true,
	"profile_label_improvements_pcf_label_in_post_enabled":              true,
	"responsive_web_graphql_exclude_directive_enabled":                  true,
	"responsive_web_graphql_skip_user_profile_image_extensions_enabled": false,
	"responsive_web_graphql_timeline_navigation_enabled":                true,
	"rweb_tipjar_consumption_enabled":                                   true,
	"verified_phone_label_enabled":                                      false,
	"highlights_tweets_tab_ui_enabled":                                  true,
	"creator_subscriptions_tweet_preview_api_enabled":                   true,
	"responsive_web_twitter_article_tweet_consumption_enabled":          false,
	"tweet_awards_web_tipping_enabled":                                  false,
	"longform_notetweets_consumption_enabled":                           true,
	"longform_notetweets_rich_text_read_enabled":                        true,
	"longform_notetweets_inline_media_enabled":                          true,
	"responsive_web_media_download_video_enabled":                       false,
	"responsive_web_enhance_cards_enabled":                              false,
}

func twClient(cookiesFile string) (*http.Client, string, error) {
	if cookiesFile == "" {
		return nil, "", fmt.Errorf("cookies file is required for Twitter/X")
	}
	cookies, err := loadNetscapeCookies(cookiesFile)
	if err != nil {
		return nil, "", fmt.Errorf("load cookies: %w", err)
	}
	var csrfToken string
	for _, ck := range cookies {
		if strings.EqualFold(ck.Name, "ct0") {
			csrfToken = ck.Value
			break
		}
	}
	return twitterAPIClient, csrfToken, nil
}

type TwOptions struct {
	CookiesFile  string
	Filter       manifest.FilterOptions
	SpeedLimit   int64
	Progress     downloader.ProgressReporter
	Limit        int
	RateLimiter  *ratelimit.Manager
	IncludePosts bool
}

func ParseTwURL(rawURL string) (username string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	host := strings.ToLower(u.Host)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", fmt.Errorf("not a twitter profile url: %s", rawURL)
	}
	_ = host
	return sanitizeFilename(parts[0]), nil
}

func twHeaders(cookiesFile, csrfToken string) (http.Header, error) {
	h := http.Header{}
	h.Set("User-Agent", twUserAgent)
	h.Set("Authorization", twBearerToken())
	h.Set("x-csrf-token", csrfToken)
	h.Set("Content-Type", "application/json")
	h.Set("Referer", "https://x.com/")
	h.Set("Origin", "https://x.com")

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

type twUserResult struct {
	RestID string `json:"rest_id"`
	Legacy struct {
		ScreenName string `json:"screen_name"`
	} `json:"legacy"`
}

type twUserData struct {
	User struct {
		Result twUserResult `json:"result"`
	} `json:"user"`
}

func twIsStaleQueryError(statusCode int, body string) bool {
	if statusCode == http.StatusNotFound {
		return true
	}
	if statusCode == http.StatusBadRequest && strings.Contains(body, "query") {
		return true
	}
	return false
}

func isRateLimitError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "status 429")
}

func resolveTwUserID(ctx context.Context, client *http.Client, cookiesFile, csrfToken, username string, rl *ratelimit.Manager) (string, error) {
	fmt.Fprintf(os.Stderr, "[twitter] resolving user @%s ...\n", username)
	for refresh := 0; refresh < 2; refresh++ {
		twRefreshQueryIDs(ctx, cookiesFile)
		queryID := twUserByScreenNameQueryID()
		endpoint := fmt.Sprintf("%s/%s/UserByScreenName", twAPIBase, queryID)

		variables, _ := json.Marshal(map[string]interface{}{
			"screen_name":                username,
			"withSafetyModeUserFields":   true,
			"withSuperFollowsUserFields": false,
			"includePromotedContent":     false,
		})
		features, _ := json.Marshal(twFeatures)

		params := url.Values{}
		params.Set("variables", string(variables))
		params.Set("features", string(features))
		fullURL := endpoint + "?" + params.Encode()

		var lastErr error
		staleQuery := false
		for attempt := 1; attempt <= twMaxAttempts; attempt++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
			if err != nil {
				return "", fmt.Errorf("new request: %w", err)
			}
			headers, err := twHeaders(cookiesFile, csrfToken)
			if err != nil {
				return "", err
			}
			req.Header = headers

			if u, _ := url.Parse(fullURL); u != nil && rl != nil {
				_ = rl.GetLimiter(u.Host).Wait(ctx)
			}

			resp, err := client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return "", fmt.Errorf("http: %w", err)
				}
				lastErr = fmt.Errorf("http: %w", err)
				if attempt == twMaxAttempts {
					return "", fmt.Errorf("resolve twitter user after %d attempts: %w", attempt, lastErr)
				}
				time.Sleep(twRetryBackoff << min(attempt-1, 3))
				continue
			}

			if resp.StatusCode >= 400 {
				preview, _ := io.ReadAll(io.LimitReader(resp.Body, twErrorPreviewLimit))
				_ = resp.Body.Close()
				body := string(preview)
				if twIsStaleQueryError(resp.StatusCode, body) && refresh == 0 {
					staleQuery = true
					fmt.Fprintf(os.Stderr, "[twitter] query ID stale (status %d), refreshing...\n", resp.StatusCode)
					break
				}
				if resp.StatusCode == http.StatusTooManyRequests {
					delay := twRetryBackoff << min(attempt-1, 3)
					if ra := resp.Header.Get("Retry-After"); ra != "" {
						if sec, err := strconv.Atoi(ra); err == nil && sec > 0 {
							delay = time.Duration(sec) * time.Second
						}
					}
					fmt.Fprintf(os.Stderr, "[twitter] rate limited (429), attempt %d, waiting %v...\n", attempt, delay)
					time.Sleep(delay)
					continue
				}
				return "", fmt.Errorf("twitter user lookup status %d: %s", resp.StatusCode, body)
			}

			var result struct {
				Data twUserData `json:"data"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				_ = resp.Body.Close()
				if refresh == 0 {
					staleQuery = true
					break
				}
				return "", fmt.Errorf("decode: %w", err)
			}
			_ = resp.Body.Close()
			if result.Data.User.Result.RestID == "" {
				return "", fmt.Errorf("twitter: user not found: %s", username)
			}
			return result.Data.User.Result.RestID, nil
		}
		if staleQuery {
			twInvalidateQueryIDs()
			continue
		}
		return "", lastErr
	}
	return "", fmt.Errorf("resolve twitter user: failed after query id refresh")
}

type twMediaSize struct {
	W    int    `json:"w"`
	H    int    `json:"h"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

type twVideoVariant struct {
	Bitrate     int    `json:"bitrate"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
}

type twVideoInfo struct {
	Variants []twVideoVariant `json:"variants"`
}

type twMedia struct {
	MediaURLHTTPS string `json:"media_url_https"`
	Type          string `json:"type"`
	Sizes         struct {
		Large  twMediaSize `json:"large"`
		Medium twMediaSize `json:"medium"`
		Small  twMediaSize `json:"small"`
		Thumb  twMediaSize `json:"thumb"`
		Orig   twMediaSize `json:"orig"`
	} `json:"sizes"`
	VideoInfo *twVideoInfo `json:"video_info,omitempty"`
}

type twURLEntity struct {
	URL         string `json:"url"`
	ExpandedURL string `json:"expanded_url"`
	DisplayURL  string `json:"display_url"`
}

type twEntities struct {
	URLs []twURLEntity `json:"urls"`
}

type twExtendedEntities struct {
	Media []twMedia `json:"media"`
}

type twLegacy struct {
	ExtendedEntities *twExtendedEntities `json:"extended_entities,omitempty"`
	FullText         string              `json:"full_text"`
	CreatedAt        string              `json:"created_at"`
	Entities         *twEntities         `json:"entities,omitempty"`
}

type twTweetResult struct {
	RestID string   `json:"rest_id"`
	Legacy twLegacy `json:"legacy"`
}

type twTweetResultWrapper struct {
	Tweet twTweetResult
}

func (w *twTweetResultWrapper) UnmarshalJSON(data []byte) error {
	// First try TweetWithVisibilityResults: {"__typename":"TweetWithVisibilityResults","tweet":{...}}
	var wrapped struct {
		Tweet twTweetResult `json:"tweet"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Tweet.RestID != "" {
		w.Tweet = wrapped.Tweet
		return nil
	}
	// Fall back to plain Tweet: {"__typename":"Tweet","rest_id":"...","legacy":{...}}
	return json.Unmarshal(data, &w.Tweet)
}

type twTweetContent struct {
	ItemContent struct {
		TweetResults struct {
			Result twTweetResultWrapper `json:"result"`
		} `json:"tweet_results"`
	} `json:"itemContent"`
	Value      string `json:"value"`
	CursorType string `json:"cursorType"`
	Items      []struct {
		EntryID string `json:"entryId"`
		Item    struct {
			ItemContent struct {
				TweetResults struct {
					Result twTweetResultWrapper `json:"result"`
				} `json:"tweet_results"`
			} `json:"itemContent"`
		} `json:"item"`
	} `json:"items"`
	EntryType string `json:"entryType"`
}

type twEntry struct {
	EntryID string         `json:"entryId"`
	Content twTweetContent `json:"content"`
}

type twInstruction struct {
	Type        string    `json:"type"`
	Entries     []twEntry `json:"entries,omitempty"`
	Entry       *twEntry  `json:"entry,omitempty"`
	ModuleItems []struct {
		EntryID string `json:"entryId"`
		Item    struct {
			ItemContent struct {
				TweetResults struct {
					Result twTweetResultWrapper `json:"result"`
				} `json:"tweet_results"`
			} `json:"itemContent"`
		} `json:"item"`
	} `json:"moduleItems,omitempty"`
}

type twTimeline struct {
	Instructions []twInstruction `json:"instructions"`
}

type twUserResultWrapper struct {
	Result struct {
		Timeline struct {
			Timeline twTimeline `json:"timeline"`
		} `json:"timeline"`
	} `json:"result"`
}

func fetchTwMediaPage(ctx context.Context, client *http.Client, cookiesFile, csrfToken, userID, cursor string, rl *ratelimit.Manager) ([]twTweetResult, string, error) {
	for refresh := 0; refresh < 2; refresh++ {
		twRefreshQueryIDs(ctx, cookiesFile)
		queryID := twUserTweetsQueryID()
		endpoint := fmt.Sprintf("%s/%s/UserTweets", twAPIBase, queryID)

		vars := map[string]interface{}{
			"userId":                                 userID,
			"count":                                  twPageSize,
			"includePromotedContent":                 false,
			"withQuickPromoteEligibilityTweetFields": true,
			"withVoice":                              true,
			"withV2Timeline":                         true,
		}
		if cursor != "" {
			vars["cursor"] = cursor
		}
		variables, _ := json.Marshal(vars)
		features, _ := json.Marshal(twFeatures)

		params := url.Values{}
		params.Set("variables", string(variables))
		params.Set("features", string(features))
		fullURL := endpoint + "?" + params.Encode()

		var lastErr error
		staleQuery := false
		for attempt := 1; attempt <= twMaxAttempts; attempt++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
			if err != nil {
				return nil, "", fmt.Errorf("new request: %w", err)
			}
			headers, err := twHeaders(cookiesFile, csrfToken)
			if err != nil {
				return nil, "", err
			}
			req.Header = headers

			if u, _ := url.Parse(fullURL); u != nil && rl != nil {
				_ = rl.GetLimiter(u.Host).Wait(ctx)
			}

			resp, err := client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return nil, "", fmt.Errorf("http: %w", err)
				}
				lastErr = fmt.Errorf("http: %w", err)
				if attempt == twMaxAttempts {
					return nil, "", fmt.Errorf("twitter request failed after %d attempts: %w", attempt, lastErr)
				}
				time.Sleep(twRetryBackoff << min(attempt-1, 3))
				continue
			}

			if resp.StatusCode >= 400 {
				preview, _ := io.ReadAll(io.LimitReader(resp.Body, twErrorPreviewLimit))
				_ = resp.Body.Close()
				body := string(preview)
				staleCode := resp.StatusCode
				if twIsStaleQueryError(staleCode, body) && refresh == 0 {
					staleQuery = true
					fmt.Fprintf(os.Stderr, "[twitter] query ID stale (status %d), refreshing...\n", staleCode)
					break
				}
				lastErr = fmt.Errorf("twitter status %d", staleCode)
				fmt.Fprintf(os.Stderr, "[twitter] response: %s\n", sanitizeErrorBody(body))
				if resp.StatusCode == http.StatusTooManyRequests {
					delay := twMaxRetryBackoff
					if ra := resp.Header.Get("Retry-After"); ra != "" {
						if sec, err := strconv.Atoi(ra); err == nil && sec > 0 {
							delay = time.Duration(sec) * time.Second
						}
					}
					fmt.Fprintf(os.Stderr, "[twitter] rate limited (429), waiting %v...\n", delay)
					time.Sleep(delay)
					continue
				}
				if resp.StatusCode >= 400 && resp.StatusCode < 500 && attempt == twMaxAttempts {
					return nil, "", lastErr
				}
				if resp.StatusCode >= 500 && attempt < twMaxAttempts {
					time.Sleep(twRetryBackoff << min(attempt-1, 3))
					continue
				}
				return nil, "", lastErr
			}

			var result struct {
				Data struct {
					User twUserResultWrapper `json:"user"`
				} `json:"data"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				_ = resp.Body.Close()
				if refresh == 0 {
					staleQuery = true
					break
				}
				return nil, "", fmt.Errorf("decode: %w", err)
			}
			_ = resp.Body.Close()

			var tweets []twTweetResult
			var nextCursor string
			for _, inst := range result.Data.User.Result.Timeline.Timeline.Instructions {
				switch inst.Type {
				case "TimelineAddEntries":
					for _, entry := range inst.Entries {
						if strings.HasPrefix(entry.EntryID, "cursor-bottom-") {
							if entry.Content.Value != "" {
								nextCursor = entry.Content.Value
							} else {
								nextCursor = entry.EntryID
							}
							continue
						}
						if strings.HasPrefix(entry.EntryID, "cursor-") {
							continue
						}
						if entry.Content.EntryType == "TimelineTimelineModule" {
							for _, item := range entry.Content.Items {
								t := item.Item.ItemContent.TweetResults.Result.Tweet
								if t.RestID != "" {
									tweets = append(tweets, t)
								}
							}
							continue
						}
						t := entry.Content.ItemContent.TweetResults.Result.Tweet
						if t.RestID != "" {
							tweets = append(tweets, t)
						}
					}
				case "TimelineAddToModule":
					for _, item := range inst.ModuleItems {
						t := item.Item.ItemContent.TweetResults.Result.Tweet
						if t.RestID != "" {
							tweets = append(tweets, t)
						}
					}
				}
			}
			return tweets, nextCursor, nil
		}
		if staleQuery {
			twInvalidateQueryIDs()
			continue
		}
		return nil, "", lastErr
	}
	return nil, "", fmt.Errorf("fetch twitter media: failed after query id refresh")
}

func FetchAllTwTweets(ctx context.Context, client *http.Client, cookiesFile, csrfToken, userID string, limit int, rl *ratelimit.Manager) ([]twTweetResult, error) {
	var all []twTweetResult
	cursor := ""
	pages := 0
	for {
		page, nextCursor, err := fetchTwMediaPage(ctx, client, cookiesFile, csrfToken, userID, cursor, rl)
		if err != nil {
			if len(all) > 0 && isRateLimitError(err) {
				fmt.Fprintf(os.Stderr, "[twitter] rate limited mid-scan, returning %d items from %d pages\n", len(all), pages)
				return all, nil
			}
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		pages++
		all = append(all, page...)
		fmt.Fprintf(os.Stderr, "[twitter] page %d: %d tweets (%d with media, %d total)\n", pages, len(page), tweetMediaCount(page), len(all))
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

func tweetMediaCount(tweets []twTweetResult) int {
	n := 0
	for _, t := range tweets {
		if t.Legacy.ExtendedEntities != nil {
			n++
		}
	}
	return n
}

func twMediaBestURLs(m twMedia) []string {
	switch m.Type {
	case "photo", "animated_gif":
		if m.Sizes.Orig.URL != "" {
			return []string{m.Sizes.Orig.URL}
		}
		if m.MediaURLHTTPS != "" {
			return []string{m.MediaURLHTTPS + "?name=orig"}
		}
		return nil
	case "video":
		if m.VideoInfo == nil {
			return nil
		}
		variants := make([]twVideoVariant, 0, len(m.VideoInfo.Variants))
		for _, v := range m.VideoInfo.Variants {
			if v.ContentType == "application/x-mpegURL" {
				continue
			}
			variants = append(variants, v)
		}
		sort.Slice(variants, func(i, j int) bool {
			return variants[i].Bitrate > variants[j].Bitrate
		})
		if len(variants) > 0 {
			return []string{variants[0].URL}
		}
	}
	return nil
}

func twMediaExt(m twMedia) string {
	switch m.Type {
	case "photo":
		return "jpg"
	case "video", "animated_gif":
		return "mp4"
	}
	return ""
}

func parseTwTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{time.RubyDate, "Mon Jan 2 15:04:05 -0700 2006", "Mon Jan 02 15:04:05 -0700 2006", time.RFC3339}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func twPostMarkdown(t twTweetResult, username string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "@%s", username)
	if !parseTwTime(t.Legacy.CreatedAt).IsZero() {
		fmt.Fprintf(&b, " · %s", parseTwTime(t.Legacy.CreatedAt).UTC().Format("2006-01-02 15:04 UTC"))
	}
	b.WriteString("\n\n")
	text := t.Legacy.FullText
	if t.Legacy.Entities != nil {
		for _, u := range t.Legacy.Entities.URLs {
			text = strings.ReplaceAll(text, u.URL, u.ExpandedURL)
		}
	}
	b.WriteString(text)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "[Source](https://x.com/%s/status/%s)\n", username, t.RestID)
	return []byte(b.String())
}

func twTweetItems(rawURL, outDir string, tweets []twTweetResult, includePosts bool) []manifest.Item {
	username := ""
	if u, err := ParseTwURL(rawURL); err == nil {
		username = u
	}
	items := make([]manifest.Item, 0)
	for _, t := range tweets {
		published := parseTwTime(t.Legacy.CreatedAt)
		base := filepath.Join(outDir, "twitter", username)

		if includePosts && t.RestID != "" {
			postName := t.RestID + ".md"
			items = append(items, manifest.Item{
				ID:          t.RestID + "_post",
				URL:         "post://twitter/" + t.RestID,
				Title:       firstNonEmpty(t.Legacy.FullText, "post_"+t.RestID),
				Filename:    postName,
				Extension:   "md",
				Source:      "twitter",
				Creator:     username,
				PostID:      t.RestID,
				PublishedAt: published,
				Destination: filepath.Join(base, "posts", postName),
				Kind:        "post",
			})
		}

		if t.Legacy.ExtendedEntities == nil {
			continue
		}
		for i, m := range t.Legacy.ExtendedEntities.Media {
			urls := twMediaBestURLs(m)
			for _, mediaURL := range urls {
				if mediaURL == "" {
					continue
				}
				ext := twMediaExt(m)
				name := fmt.Sprintf("%s_%d.%s", t.RestID, i, ext)
				if ext == "" {
					name = fmt.Sprintf("%s_%d", t.RestID, i)
					if u, _ := url.Parse(mediaURL); u != nil {
						name = sanitizeFilename(filepath.Base(u.Path))
					}
				}
				kind := m.Type
				if kind == "" {
					kind = "attachment"
				}
				subdir := "images"
				if m.Type == "video" || m.Type == "animated_gif" {
					subdir = "videos"
				}
				items = append(items, manifest.Item{
					ID:          t.RestID + "_" + strconv.Itoa(i),
					URL:         mediaURL,
					Title:       firstNonEmpty(t.Legacy.FullText, kind+"_"+strconv.Itoa(i)),
					Filename:    name,
					Extension:   ext,
					Source:      "twitter",
					Creator:     username,
					PostID:      t.RestID,
					PublishedAt: published,
					Destination: filepath.Join(base, subdir, name),
					Kind:        kind,
				})
			}
		}
	}
	return items
}

func ScanTwitter(ctx context.Context, rawURL, outDir string, cookiesFile string, opts TwOptions) (manifest.Manifest, error) {
	username, err := ParseTwURL(rawURL)
	if err != nil {
		return manifest.Manifest{}, err
	}
	client, csrfToken, err := twClient(cookiesFile)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("create client: %w", err)
	}
	userID, err := resolveTwUserID(ctx, client, cookiesFile, csrfToken, username, opts.RateLimiter)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("resolve user id: %w", err)
	}
	tweets, err := FetchAllTwTweets(ctx, client, cookiesFile, csrfToken, userID, opts.Limit, opts.RateLimiter)
	if err != nil {
		return manifest.Manifest{}, err
	}
	m := manifest.New(rawURL, "twitter", twTweetItems(rawURL, outDir, tweets, opts.IncludePosts))
	return m.Filter(opts.Filter)
}

func DownloadTwitter(ctx context.Context, rawURL, outDir string, cookiesFile string, opts TwOptions, dryRun bool) error {
	username, err := ParseTwURL(rawURL)
	if err != nil {
		return err
	}
	client, csrfToken, err := twClient(cookiesFile)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	userID, err := resolveTwUserID(ctx, client, cookiesFile, csrfToken, username, opts.RateLimiter)
	if err != nil {
		return fmt.Errorf("resolve user id: %w", err)
	}
	tweets, err := FetchAllTwTweets(ctx, client, cookiesFile, csrfToken, userID, opts.Limit, opts.RateLimiter)
	if err != nil {
		return err
	}
	items := twTweetItems(rawURL, outDir, tweets, opts.IncludePosts)
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

	pool := worker.NewPool(ctx, 4)
	dl := downloader.New()
	dl.SpeedLimit = opts.SpeedLimit
	dl.Progress = opts.Progress

	failed := make(map[string]string)
	var failMu sync.Mutex
	recordFailure := func(url string, err error) {
		failMu.Lock()
		failed[url] = err.Error()
		failMu.Unlock()
		fmt.Fprintf(os.Stderr, "[provenance] twitter download failed: %s: %v\n", url, err)
	}

	for _, t := range tweets {
		t := t
		base := filepath.Join(outDir, "twitter", username)

		if postItem, ok := postsByID[t.RestID]; ok {
			postDest := postItem.Destination
			postContent := twPostMarkdown(t, username)
			if dryRun {
				fmt.Printf("[dry-run] twitter post: %s -> %s\n", t.RestID, postDest)
			} else {
				pool.SubmitWithHooks(func() error {
					return writeFile(postDest, postContent)
				}, func() {
					fmt.Fprintf(os.Stderr, "[provenance] twitter post saved: %s\n", postDest)
				}, func(err error) {
					recordFailure("post:"+t.RestID, err)
				})
			}
		}

		if t.Legacy.ExtendedEntities == nil {
			continue
		}
		for i, m := range t.Legacy.ExtendedEntities.Media {
			m := m
			urls := twMediaBestURLs(m)
			for _, mediaURL := range urls {
				mediaURL := mediaURL
				if mediaURL == "" {
					continue
				}
				item, ok := allowed[mediaURL]
				if !ok {
					continue
				}
				dest := item.Destination
				if dest == "" {
					ext := twMediaExt(m)
					name := fmt.Sprintf("%s_%d.%s", t.RestID, i, ext)
					subdir := "images"
					if m.Type == "video" || m.Type == "animated_gif" {
						subdir = "videos"
					}
					dest = filepath.Join(base, subdir, sanitizeFilename(name))
				}
				if dryRun {
					fmt.Printf("[dry-run] twitter: %s -> %s\n", mediaURL, dest)
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
	}

	pool.Wait()
	if len(failed) > 0 {
		return fmt.Errorf("twitter: %d file(s) failed to download (first: %s)", len(failed), firstFailedURL(failed))
	}
	return nil
}

func TwTweetToItem(t twTweetResult, username string) resolve.Item {
	published := parseTwTime(t.Legacy.CreatedAt)
	canonicalURL := fmt.Sprintf("https://x.com/%s/status/%s", username, t.RestID)
	item := resolve.NewItem(t.RestID, canonicalURL)
	item.Title = firstNonEmpty(t.Legacy.FullText, t.RestID)
	item.Author = username
	if !published.IsZero() {
		item.PublishedAt = &published
	}
	if t.Legacy.FullText != "" {
		item.Text = &resolve.TextContent{Body: t.Legacy.FullText, Format: resolve.FormatPlain}
	}
	if raw, err := json.Marshal(t); err == nil {
		item.RawMetadata = raw
	}
	if t.Legacy.ExtendedEntities == nil {
		return item
	}
	for _, m := range t.Legacy.ExtendedEntities.Media {
		urls := twMediaBestURLs(m)
		for _, mediaURL := range urls {
			ext := twMediaExt(m)
			kind := resolve.MediaImage
			if m.Type == "video" || m.Type == "animated_gif" {
				kind = resolve.MediaVideo
			}
			asset := resolve.NewMediaAsset(mediaURL, kind)
			asset.Extension = ext
			if m.Type != "video" && m.Type != "animated_gif" {
				asset.Size = m.Sizes.Orig.Size
			}
			item.Media = append(item.Media, asset)
		}
	}
	return item
}

func ScanTwitterResolved(ctx context.Context, rawURL, outDir, cookiesFile string, opts TwOptions) (resolve.Source, error) {
	username, err := ParseTwURL(rawURL)
	if err != nil {
		return resolve.Source{}, err
	}
	client, csrfToken, err := twClient(cookiesFile)
	if err != nil {
		return resolve.Source{}, fmt.Errorf("create client: %w", err)
	}
	userID, err := resolveTwUserID(ctx, client, csrfToken, cookiesFile, username, opts.RateLimiter)
	if err != nil {
		return resolve.Source{}, fmt.Errorf("resolve user id: %w", err)
	}
	tweets, err := FetchAllTwTweets(ctx, client, cookiesFile, csrfToken, userID, opts.Limit, opts.RateLimiter)
	if err != nil {
		return resolve.Source{}, err
	}
	canonicalURL := fmt.Sprintf("https://x.com/%s", username)
	src := resolve.NewSource(rawURL, canonicalURL, resolve.KindFeed, "twitter")
	src.Author = username
	for _, t := range tweets {
		src.Items = append(src.Items, TwTweetToItem(t, username))
	}
	return src, nil
}
