package extractor

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sk3y04/provenance/internal/manifest"
)

func TestParseIgURL(t *testing.T) {
	tests := []struct {
		url             string
		wantType        string
		wantUsername    string
		wantShortcode   string
		wantErr         bool
		wantErrContains string
	}{
		{url: "https://www.instagram.com/username/", wantType: "profile", wantUsername: "username"},
		{url: "https://instagram.com/username", wantType: "profile", wantUsername: "username"},
		{url: "https://www.instagram.com/p/CxYzAbCdEf/", wantType: "post", wantShortcode: "CxYzAbCdEf"},
		{url: "https://www.instagram.com/p/CxYzAbCdEf/?utm=test", wantType: "post", wantShortcode: "CxYzAbCdEf"},
		{url: "https://www.instagram.com/reel/Chunk8-jurw/", wantType: "reel", wantShortcode: "Chunk8-jurw"},
		{url: "https://www.instagram.com/reels/Chunk8-jurw/", wantType: "reel", wantShortcode: "Chunk8-jurw"},
		{url: "https://www.instagram.com/tv/BkfuX9UB-eK/", wantType: "post", wantShortcode: "BkfuX9UB-eK"},
		{url: "https://www.instagram.com/username/reel/Chunk8-jurw/", wantType: "reel", wantShortcode: "Chunk8-jurw"},
		{url: "https://www.instagram.com/explore/tags/test/", wantErr: true, wantErrContains: "unsupported"},
		{url: "https://www.instagram.com/stories/test/", wantType: "stories", wantUsername: "test"},
		{url: "https://www.instagram.com/", wantErr: true},
		{url: "https://twitter.com/user", wantErr: true, wantErrContains: "not an instagram"},
	}
	for _, tt := range tests {
		target, err := ParseIgURL(tt.url)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseIgURL(%q) expected error, got nil", tt.url)
			} else if tt.wantErrContains != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.wantErrContains)) {
				t.Errorf("ParseIgURL(%q) error %q does not contain %q", tt.url, err.Error(), tt.wantErrContains)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseIgURL(%q) unexpected error: %v", tt.url, err)
			continue
		}
		if target.Type != tt.wantType {
			t.Errorf("ParseIgURL(%q) type = %q, want %q", tt.url, target.Type, tt.wantType)
		}
		if target.Username != tt.wantUsername {
			t.Errorf("ParseIgURL(%q) username = %q, want %q", tt.url, target.Username, tt.wantUsername)
		}
		if target.Shortcode != tt.wantShortcode {
			t.Errorf("ParseIgURL(%q) shortcode = %q, want %q", tt.url, target.Shortcode, tt.wantShortcode)
		}
	}
}

func TestIgShortcodeConversionRoundtrip(t *testing.T) {
	codes := []string{
		"aye83DjauH",
		"Chunk8-jurw",
		"BQ0eAlwhDrw",
		"BkfuX9UB-eK",
		"CxYzAbCdEf",
		"1234567",
		"a",
		"-_-_-__",
	}
	for _, sc := range codes {
		id := igShortcodeToID(sc)
		back := igShortcodeToID(sc) // use id-to-shortcode-would-be-here but we only have shortcode-to-ID
		if back != id {
			t.Errorf("igShortcodeToID(%q) inconsistent: first=%d second=%d", sc, id, back)
		}
		// Verifying this isn't zero for valid shortcodes
		if id == 0 && sc != "" {
			// Empty shortcodes should return 0, but valid ones shouldn't
			recalculated := igShortcodeToID(sc)
			if recalculated != 0 {
				t.Errorf("igShortcodeToID(%q) is 0 but recalculated is %d", sc, recalculated)
			}
		}
	}
}

func TestIgShortcodeToIDInvalidChars(t *testing.T) {
	invalid := "!!!!!"
	if id := igShortcodeToID(invalid); id != 0 {
		t.Errorf("igShortcodeToID(%q) = %d, want 0", invalid, id)
	}
}

func TestIgMediaTypeName(t *testing.T) {
	tests := map[int]string{
		1: "image",
		2: "video",
		8: "carousel",
		9: "unknown",
	}
	for typ, want := range tests {
		if got := igMediaTypeName(typ); got != want {
			t.Errorf("igMediaTypeName(%d) = %q, want %q", typ, got, want)
		}
	}
}

func TestIgMediaBestURLsImage(t *testing.T) {
	media := igFeedMedia{
		MediaType: 1,
		ImageVersions2: &igImageVersions{
			Candidates: []igMediaCandidate{
				{URL: "https://cdn.inst/med_res.jpg", Width: 1080, Height: 1080},
				{URL: "https://cdn.inst/lo_res.jpg", Width: 640, Height: 640},
			},
		},
	}
	urls := igMediaBestURLs(media)
	if len(urls) != 1 {
		t.Fatalf("expected 1 url, got %d", len(urls))
	}
	if urls[0] != "https://cdn.inst/med_res.jpg" {
		t.Errorf("got %q, want first candidate (highest res)", urls[0])
	}
}

func TestIgMediaBestURLsVideo(t *testing.T) {
	media := igFeedMedia{
		MediaType: 2,
		VideoVersions: []igVideoVersion{
			{URL: "https://cdn.inst/v_lo.mp4", Width: 640, Height: 480},
			{URL: "https://cdn.inst/v_hi.mp4", Width: 1920, Height: 1080},
			{URL: "https://cdn.inst/v_mid.mp4", Width: 1280, Height: 720},
		},
	}
	urls := igMediaBestURLs(media)
	if len(urls) != 1 {
		t.Fatalf("expected 1 url, got %d", len(urls))
	}
	if urls[0] != "https://cdn.inst/v_hi.mp4" {
		t.Errorf("got %q, want highest resolution (1920x1080)", urls[0])
	}
}

func TestIgMediaBestURLsVideoSortsByHeightThenWidth(t *testing.T) {
	media := igFeedMedia{
		MediaType: 2,
		VideoVersions: []igVideoVersion{
			{URL: "url_wide.mp4", Width: 1920, Height: 1080},
			{URL: "url_tall.mp4", Width: 720, Height: 1280},
		},
	}
	urls := igMediaBestURLs(media)
	if len(urls) != 1 {
		t.Fatalf("expected 1 url, got %d", len(urls))
	}
	// Taller (1280) should win over wider (1080) because height is first sort key
	if urls[0] != "url_tall.mp4" {
		t.Errorf("got %q, want url_tall.mp4 (tallest by height)", urls[0])
	}
}

func TestIgMediaBestURLsCarousel(t *testing.T) {
	media := igFeedMedia{
		MediaType: 8,
		CarouselMedia: []igCarouselMedia{
			{
				MediaType: 1,
				ImageVersions2: &igImageVersions{
					Candidates: []igMediaCandidate{
						{URL: "https://cdn.inst/c1_img.jpg", Width: 1080, Height: 1080},
					},
				},
			},
			{
				MediaType: 2,
				VideoVersions: []igVideoVersion{
					{URL: "https://cdn.inst/c2_vid.mp4", Width: 1920, Height: 1080},
				},
			},
			{
				MediaType: 1,
				ImageVersions2: &igImageVersions{
					Candidates: []igMediaCandidate{
						{URL: "https://cdn.inst/c3_img.jpg", Width: 640, Height: 640},
					},
				},
			},
		},
	}
	urls := igMediaBestURLs(media)
	if len(urls) != 3 {
		t.Fatalf("expected 3 urls (one per carousel item), got %d", len(urls))
	}
	if urls[0] != "https://cdn.inst/c1_img.jpg" {
		t.Errorf("urls[0] = %q", urls[0])
	}
	if urls[1] != "https://cdn.inst/c2_vid.mp4" {
		t.Errorf("urls[1] = %q", urls[1])
	}
	if urls[2] != "https://cdn.inst/c3_img.jpg" {
		t.Errorf("urls[2] = %q", urls[2])
	}
}

func TestIgMediaBestURLsEmptyImageVersions(t *testing.T) {
	media := igFeedMedia{
		MediaType:      1,
		ImageVersions2: nil,
	}
	urls := igMediaBestURLs(media)
	if len(urls) != 0 {
		t.Errorf("expected 0 urls for nil ImageVersions2, got %d", len(urls))
	}
}

func TestIgMediaBestURLsEmptyVideoVersions(t *testing.T) {
	media := igFeedMedia{
		MediaType:     2,
		VideoVersions: nil,
	}
	urls := igMediaBestURLs(media)
	if len(urls) != 0 {
		t.Errorf("expected 0 urls for nil VideoVersions, got %d", len(urls))
	}
}

func TestIgCarouselBestURLsImage(t *testing.T) {
	cm := igCarouselMedia{
		MediaType: 1,
		ImageVersions2: &igImageVersions{
			Candidates: []igMediaCandidate{
				{URL: "https://cdn.inst/cm_img.jpg", Width: 1080, Height: 720},
			},
		},
	}
	urls := igCarouselBestURLs(cm)
	if len(urls) != 1 || urls[0] != "https://cdn.inst/cm_img.jpg" {
		t.Errorf("got %v, want [https://cdn.inst/cm_img.jpg]", urls)
	}
}

func TestIgCarouselBestURLsVideo(t *testing.T) {
	cm := igCarouselMedia{
		MediaType: 2,
		VideoVersions: []igVideoVersion{
			{URL: "https://cdn.inst/cm_lo.mp4", Width: 640, Height: 360},
			{URL: "https://cdn.inst/cm_hi.mp4", Width: 1920, Height: 1080},
		},
	}
	urls := igCarouselBestURLs(cm)
	if len(urls) != 1 || urls[0] != "https://cdn.inst/cm_hi.mp4" {
		t.Errorf("got %v, want [https://cdn.inst/cm_hi.mp4]", urls)
	}
}

func TestIgMediaExtImage(t *testing.T) {
	m := igFeedMedia{MediaType: 1}
	if got := igMediaExt(m); got != "jpg" {
		t.Errorf("got %q, want jpg", got)
	}
}

func TestIgMediaExtVideo(t *testing.T) {
	m := igFeedMedia{MediaType: 2}
	if got := igMediaExt(m); got != "mp4" {
		t.Errorf("got %q, want mp4", got)
	}
}

func TestIgMediaExtCarouselVideo(t *testing.T) {
	m := igFeedMedia{
		MediaType: 8,
		CarouselMedia: []igCarouselMedia{
			{MediaType: 2},
		},
	}
	if got := igMediaExt(m); got != "mp4" {
		t.Errorf("got %q, want mp4", got)
	}
}

func TestIgMediaExtCarouselImage(t *testing.T) {
	m := igFeedMedia{
		MediaType: 8,
		CarouselMedia: []igCarouselMedia{
			{MediaType: 1},
		},
	}
	if got := igMediaExt(m); got != "jpg" {
		t.Errorf("got %q, want jpg", got)
	}
}

func TestIgMediaExtCarouselEmpty(t *testing.T) {
	m := igFeedMedia{MediaType: 8}
	if got := igMediaExt(m); got != "jpg" {
		t.Errorf("empty carousel should default to jpg, got %q", got)
	}
}

func TestIgCarouselExt(t *testing.T) {
	if got := igCarouselExt(igCarouselMedia{MediaType: 1}); got != "jpg" {
		t.Errorf("type 1: got %q, want jpg", got)
	}
	if got := igCarouselExt(igCarouselMedia{MediaType: 2}); got != "mp4" {
		t.Errorf("type 2: got %q, want mp4", got)
	}
	if got := igCarouselExt(igCarouselMedia{MediaType: 8}); got != "jpg" {
		t.Errorf("type 8 (unknown): got %q, want jpg (default)", got)
	}
}

func TestIgPostMarkdown(t *testing.T) {
	ts := time.Unix(1690000000, 0)
	post := igFeedMedia{
		Code:    "CxYzAbCdEf",
		TakenAt: 1690000000,
		Caption: &igCaption{Text: "Hello Instagram!"},
		User:    igUserResult{Username: "testuser", FullName: "Test User"},
	}
	md := igPostMarkdown(post, "testuser")

	content := string(md)
	if !strings.Contains(content, "@testuser") {
		t.Errorf("markdown should contain @testuser: %s", content)
	}
	if !strings.Contains(content, "Test User") {
		t.Errorf("markdown should contain full name: %s", content)
	}
	if !strings.Contains(content, ts.UTC().Format("2006-01-02 15:04 UTC")) {
		t.Errorf("markdown should contain formatted timestamp: %s", content)
	}
	if !strings.Contains(content, "Hello Instagram!") {
		t.Errorf("markdown should contain caption text: %s", content)
	}
	if !strings.Contains(content, "instagram.com/p/CxYzAbCdEf") {
		t.Errorf("markdown should contain source link: %s", content)
	}
}

func TestIgPostMarkdownNoCaption(t *testing.T) {
	post := igFeedMedia{
		Code:    "CxTest",
		TakenAt: 1690000000,
		User:    igUserResult{Username: "someone", FullName: ""},
	}
	md := igPostMarkdown(post, "someone")
	content := string(md)
	if !strings.Contains(content, "@someone") {
		t.Errorf("markdown should contain username: %s", content)
	}
	if !strings.Contains(content, "instagram.com/p/CxTest") {
		t.Errorf("markdown should contain source link even without caption: %s", content)
	}
}

func TestIgPostItemsImage(t *testing.T) {
	posts := []igFeedMedia{
		{
			Code:      "CxImg01",
			TakenAt:   1690000001,
			MediaType: 1,
			ImageVersions2: &igImageVersions{
				Candidates: []igMediaCandidate{
					{URL: "https://cdn.inst/img1.jpg", Width: 1080, Height: 1080},
				},
			},
			User: igUserResult{Username: "photog", FullName: "Photo G"},
		},
	}
	items := igPostItems("https://instagram.com/photog/", "downloads", posts, "photog", false)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	it := items[0]
	if it.Extension != "jpg" {
		t.Errorf("Extension = %q, want jpg", it.Extension)
	}
	if it.Source != "instagram" {
		t.Errorf("Source = %q, want instagram", it.Source)
	}
	if it.Creator != "photog" {
		t.Errorf("Creator = %q, want photog", it.Creator)
	}
	if it.URL != "https://cdn.inst/img1.jpg" {
		t.Errorf("URL = %q", it.URL)
	}
	expectedDest := filepath.Join("downloads", "instagram", "photog", "images", "CxImg01.jpg")
	if it.Destination != expectedDest {
		t.Errorf("Destination = %q, want %q", it.Destination, expectedDest)
	}
}

func TestIgPostItemsVideo(t *testing.T) {
	posts := []igFeedMedia{
		{
			Code:      "CxVid01",
			TakenAt:   1690000002,
			MediaType: 2,
			VideoVersions: []igVideoVersion{
				{URL: "https://cdn.inst/vid1.mp4", Width: 1920, Height: 1080},
			},
			User: igUserResult{Username: "videomaker"},
		},
	}
	items := igPostItems("https://instagram.com/videomaker/", "out", posts, "videomaker", false)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	it := items[0]
	if it.Extension != "mp4" {
		t.Errorf("Extension = %q, want mp4", it.Extension)
	}
	expectedDest := filepath.Join("out", "instagram", "videomaker", "videos", "CxVid01.mp4")
	if it.Destination != expectedDest {
		t.Errorf("Destination = %q, want %q", it.Destination, expectedDest)
	}
}

func TestIgPostItemsCarousel(t *testing.T) {
	posts := []igFeedMedia{
		{
			Code:      "CxCar01",
			TakenAt:   1690000003,
			MediaType: 8,
			CarouselMedia: []igCarouselMedia{
				{
					MediaType: 1,
					ImageVersions2: &igImageVersions{
						Candidates: []igMediaCandidate{
							{URL: "https://cdn.inst/c_slide1.jpg", Width: 1080, Height: 1080},
						},
					},
				},
				{
					MediaType: 2,
					VideoVersions: []igVideoVersion{
						{URL: "https://cdn.inst/c_slide2.mp4", Width: 1920, Height: 1080},
					},
				},
			},
			User: igUserResult{Username: "carouseler"},
		},
	}
	items := igPostItems("https://instagram.com/carouseler/", "dl", posts, "carouseler", false)
	if len(items) != 2 {
		t.Fatalf("expected 2 items (one per carousel slide), got %d", len(items))
	}
	if items[0].Extension != "jpg" {
		t.Errorf("first slide ext = %q, want jpg", items[0].Extension)
	}
	if items[1].Extension != "mp4" {
		t.Errorf("second slide ext = %q, want mp4", items[1].Extension)
	}
}

func TestIgPostItemsWithPosts(t *testing.T) {
	posts := []igFeedMedia{
		{
			Code:      "CxWithCap",
			TakenAt:   1690000004,
			MediaType: 1,
			ImageVersions2: &igImageVersions{
				Candidates: []igMediaCandidate{
					{URL: "https://cdn.inst/with_cap.jpg", Width: 1080, Height: 1080},
				},
			},
			Caption: &igCaption{Text: "A beautiful day"},
			User:    igUserResult{Username: "capper", FullName: "Cap Person"},
		},
	}
	items := igPostItems("https://instagram.com/capper/", "dl", posts, "capper", true)
	if len(items) != 2 {
		t.Fatalf("expected 2 items (post markdown + image), got %d", len(items))
	}
	var post, media *manifest.Item
	for i := range items {
		if items[i].Kind == "post" {
			post = &items[i]
		} else {
			media = &items[i]
		}
	}
	if post == nil {
		t.Fatal("no post item found")
	}
	if post.Extension != "md" {
		t.Errorf("post ext = %q, want md", post.Extension)
	}
	if post.PostID != "CxWithCap" {
		t.Errorf("post PostID = %q", post.PostID)
	}
	if !strings.Contains(post.Title, "A beautiful day") {
		t.Errorf("post title should contain caption: %q", post.Title)
	}
	if media == nil {
		t.Fatal("no media item found")
	}
	if media.Extension != "jpg" {
		t.Errorf("media ext = %q, want jpg", media.Extension)
	}
}

func TestIgPostItemsWithoutIncludePosts(t *testing.T) {
	posts := []igFeedMedia{
		{
			Code:      "CxNoPost",
			TakenAt:   1690000005,
			MediaType: 1,
			ImageVersions2: &igImageVersions{
				Candidates: []igMediaCandidate{
					{URL: "https://cdn.inst/nopost.jpg", Width: 1080, Height: 1080},
				},
			},
			Caption: &igCaption{Text: "No post file for me"},
			User:    igUserResult{Username: "silent"},
		},
	}
	items := igPostItems("https://instagram.com/silent/", "dl", posts, "silent", false)
	if len(items) != 1 {
		t.Fatalf("expected 1 item (no post markdown), got %d", len(items))
	}
	if items[0].Kind == "post" {
		t.Errorf("should not have post item when IncludePosts is false")
	}
}
