package dispatcher

import (
	"errors"
	"testing"
)

func TestSiteString(t *testing.T) {
	tests := map[Site]string{
		SiteUnknown:   "unknown",
		SitePatreon:   "patreon",
		SiteTwitter:   "twitter",
		SiteReddit:    "reddit",
		SiteInstagram: "instagram",
		SiteGeneric:   "generic",
		Site(99):      "unknown",
	}
	for site, want := range tests {
		if got := site.String(); got != want {
			t.Errorf("Site(%d).String() = %q, want %q", site, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		url  string
		want Site
	}{
		{"https://www.patreon.com/creator", SitePatreon},
		{"https://x.com/user/status/123", SiteTwitter},
		{"https://twitter.com/user", SiteTwitter},
		{"https://www.reddit.com/r/golang", SiteReddit},
		{"https://old.reddit.com/user/foo", SiteReddit},
		{"https://www.instagram.com/p/ABC/", SiteInstagram},
		{"https://instagram.com/username/", SiteInstagram},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", SiteGeneric},
		{"https://example.com/file.mp4", SiteGeneric},
	}
	for _, tt := range tests {
		got, err := Classify(tt.url)
		if err != nil {
			t.Errorf("Classify(%q) unexpected error: %v", tt.url, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Classify(%q) = %s, want %s", tt.url, got, tt.want)
		}
	}
}

func TestClassifyInvalidURL(t *testing.T) {
	_, err := Classify("not a valid :// url")
	if err == nil {
		t.Error("Classify on invalid URL should return error")
	}
}

func TestShouldBrowserFallback(t *testing.T) {
	tests := []struct {
		haystack string
		want     bool
	}{
		{"", false},
		{"some random error about ffmpeg", false},
		{"unsupported url: cannot download", true},
		{"no video formats found for this page", true},
		{"unable to extract video data", true},
		{"no suitable extractor found", true},
		{"error: is not a valid url", true},
		{"this is a valid URL but something else failed", false},
	}
	for _, tt := range tests {
		if got := shouldBrowserFallback(tt.haystack); got != tt.want {
			t.Errorf("shouldBrowserFallback(%q) = %v, want %v", tt.haystack, got, tt.want)
		}
	}
}

func TestIsPermanentYtdlpFailure(t *testing.T) {
	tests := []struct {
		err    error
		stderr string
		want   bool
	}{
		{nil, "", false},
		{errors.New("http error 410: gone"), "", true},
		{errors.New("http error 404: not found"), "", true},
		{errors.New("requested format is not available"), "", true},
		{errors.New("unsupported url"), "", true},
		{errors.New("this video is unavailable"), "", true},
		{errors.New("video unavailable"), "", true},
		{errors.New("private video"), "", true},
		{errors.New("members-only"), "", true},
		{errors.New("connection timed out"), "", false},
		{errors.New("download failed"), "ERROR: private video\n", true},
		{errors.New("download failed"), "some transient network error", false},
	}
	for _, tt := range tests {
		if got := isPermanentYtdlpFailure(tt.err, tt.stderr); got != tt.want {
			t.Errorf("isPermanentYtdlpFailure(%q, %q) = %v, want %v", tt.err, tt.stderr, got, tt.want)
		}
	}
}

func TestArchiveEntry(t *testing.T) {
	tests := []struct {
		line       string
		wantURL    string
		wantStatus string
	}{
		{"ok:https://example.com/video.mp4", "https://example.com/video.mp4", archiveStatusOK},
		{"perm-fail:https://example.com/bad.mp4", "https://example.com/bad.mp4", archiveStatusPermFail},
		{"https://example.com/bare.mp4", "https://example.com/bare.mp4", archiveStatusOK},
		{"http://example.com/bare.mp4", "http://example.com/bare.mp4", archiveStatusOK},
		{"# this is a comment", "", ""},
		{"", "", ""},
		{"garbage line", "", ""},
	}
	for _, tt := range tests {
		gotURL, gotStatus := archiveEntry(tt.line)
		if gotURL != tt.wantURL || gotStatus != tt.wantStatus {
			t.Errorf("archiveEntry(%q) = (%q, %q), want (%q, %q)", tt.line, gotURL, gotStatus, tt.wantURL, tt.wantStatus)
		}
	}
}

func TestDedupeLinks(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{[]string{"a", "b", "a", "c"}, []string{"a", "b", "c"}},
		{[]string{"  dup  ", "dup", "unique"}, []string{"dup", "unique"}},
		{[]string{"", "a", "", "b", ""}, []string{"a", "b"}},
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{nil, nil},
	}
	for _, tt := range tests {
		got := dedupeLinks(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("dedupeLinks(%v) = %v (len=%d), want %v (len=%d)", tt.in, got, len(got), tt.want, len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("dedupeLinks(%v)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := map[string]string{
		"hello/world":     "hello_world",
		"file:name":       "file_name",
		"a*b?c<d>e|f":     "a_b_c_d_e_f",
		`has"quotes`:      "has_quotes",
		"normal_file.txt": "normal_file.txt",
		`c:\path\to\file`: "c__path_to_file",
	}
	for in, want := range tests {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
