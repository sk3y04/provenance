// Package diagnose provides human-readable error hints for common download failures.
package diagnose

import "strings"

func Hint(err error) string {
	if err == nil {
		return ""
	}
	h := strings.ToLower(err.Error())
	switch {
	case containsAny(h, "http error 401", "http error 403", "forbidden", "unauthorized", "private video", "members-only", "login required"):
		return "This looks like an authentication problem. Try --cookies cookies.txt or --cookies-from-browser chrome/edge/firefox."
	case containsAny(h, "too many requests", "http error 429", "rate limit", "temporarily blocked"):
		return "This looks like rate limiting. Try a lower --concurrency, resume later, or use a saved --session."
	case containsAny(h, "ffmpeg", "postprocessing"):
		return "This looks related to ffmpeg/post-processing. Run provenance install or install ffmpeg on PATH."
	case containsAny(h, "no space left", "disk full"):
		return "Your destination disk appears to be full. Free space or choose another --output directory."
	case containsAny(h, "unsupported url", "no suitable extractor", "unable to extract"):
		return "The site may not be supported by yt-dlp. provenance will use browser fallback for eligible pages; otherwise try updating yt-dlp or using --cookies."
	case containsAny(h, "http error 404", "http error 410", "not found", "gone"):
		return "The media appears to be deleted or unavailable. If using sessions, retry later with provenance retry-failed <session>."
	case containsAny(h, "no such host", "dns lookup", "lookup ", "name resolution"):
		return "DNS lookup failed for the API host. The site may have rotated to a new TLD - update provenance or check your DNS / VPN."
	case containsAny(h, "timeout", "context deadline exceeded", "connection reset", "connection refused"):
		return "This looks like a network problem. Resume the session later or lower --concurrency."
	}
	return ""
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
