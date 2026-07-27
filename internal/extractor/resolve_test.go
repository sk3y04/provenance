package extractor

import (
	"testing"
	"time"

	"github.com/sk3y04/provenance/internal/resolve"
)

func TestYtdlpInfoToSource(t *testing.T) {
	info := ytdlpInfo{
		ID:         "test-id",
		Title:      "Test Video",
		WebpageURL: "https://youtube.com/watch?v=test-id",
		URL:        "https://example.com/video.mp4",
		Extractor:  "youtube",
		Uploader:   "TestCreator",
		Ext:        "mp4",
		Filesize:   123456,
		UploadDate: "20240115",
	}

	src, items := YtdlpInfoToSource(info, "https://youtube.com/watch?v=test-id")

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	it := items[0]
	if it.ExternalID != "test-id" {
		t.Errorf("expected ExternalID test-id, got %q", it.ExternalID)
	}
	if it.URL != "https://youtube.com/watch?v=test-id" {
		t.Errorf("unexpected URL: %q", it.URL)
	}
	if it.Title != "Test Video" {
		t.Errorf("unexpected title: %q", it.Title)
	}
	if it.Author != "TestCreator" {
		t.Errorf("unexpected author: %q", it.Author)
	}
	if len(it.Media) != 1 {
		t.Fatalf("expected 1 media asset, got %d", len(it.Media))
	}
	asset := it.Media[0]
	if asset.URL != "https://example.com/video.mp4" {
		t.Errorf("unexpected media URL: %q", asset.URL)
	}
	if asset.Kind != resolve.MediaVideo {
		t.Errorf("expected kind video, got %q", asset.Kind)
	}
	if asset.Size != 123456 {
		t.Errorf("expected size 123456, got %d", asset.Size)
	}
	if asset.Extension != "mp4" {
		t.Errorf("expected extension mp4, got %q", asset.Extension)
	}
	if it.PublishedAt == nil {
		t.Error("expected non-nil PublishedAt")
	} else if it.PublishedAt.Format("20060102") != "20240115" {
		t.Errorf("unexpected date: %s", it.PublishedAt.Format("20060102"))
	}
	if len(it.RawMetadata) == 0 {
		t.Error("expected non-empty RawMetadata")
	}

	if src.Kind != resolve.KindSingle {
		t.Errorf("expected KindSingle, got %q", src.Kind)
	}
	if src.CanonicalURL != "https://youtube.com/watch?v=test-id" {
		t.Errorf("unexpected canonical URL: %q", src.CanonicalURL)
	}
	if src.Extractor != "youtube" {
		t.Errorf("expected extractor youtube, got %q", src.Extractor)
	}
}

func TestTwTweetToItem(t *testing.T) {
	published := "Mon Jan 15 10:00:00 +0000 2024"
	tweet := twTweetResult{
		RestID: "1234567890123456789",
		Legacy: twLegacy{
			FullText:  "Hello world",
			CreatedAt: published,
			ExtendedEntities: &twExtendedEntities{
				Media: []twMedia{{
					Type:          "photo",
					MediaURLHTTPS: "https://pbs.twimg.com/media/abc.jpg",
					Sizes: struct {
						Large  twMediaSize `json:"large"`
						Medium twMediaSize `json:"medium"`
						Small  twMediaSize `json:"small"`
						Thumb  twMediaSize `json:"thumb"`
						Orig   twMediaSize `json:"orig"`
					}{
						Orig: twMediaSize{URL: "https://pbs.twimg.com/media/abc.jpg", W: 1200, H: 800, Size: 50000},
					},
				}},
			},
		},
	}

	item := TwTweetToItem(tweet, "testuser")

	if item.ExternalID != "1234567890123456789" {
		t.Errorf("unexpected ExternalID: %q", item.ExternalID)
	}
	if item.URL != "https://x.com/testuser/status/1234567890123456789" {
		t.Errorf("unexpected URL: %q", item.URL)
	}
	if item.Title != "Hello world" {
		t.Errorf("unexpected title: %q", item.Title)
	}
	if item.Author != "testuser" {
		t.Errorf("unexpected author: %q", item.Author)
	}
	if item.Text == nil {
		t.Error("expected non-nil Text")
	} else if item.Text.Body != "Hello world" {
		t.Errorf("unexpected text body: %q", item.Text.Body)
	}
	if item.Text.Format != resolve.FormatPlain {
		t.Errorf("expected plain format, got %q", item.Text.Format)
	}
	if len(item.Media) != 1 {
		t.Fatalf("expected 1 media asset, got %d", len(item.Media))
	}
	if item.Media[0].Kind != resolve.MediaImage {
		t.Errorf("expected image kind, got %q", item.Media[0].Kind)
	}
	if item.Media[0].Size != 50000 {
		t.Errorf("expected size 50000, got %d", item.Media[0].Size)
	}
}

func TestRdPostToItem(t *testing.T) {
	post := rdPostData{
		ID:                  "abc123",
		Permalink:           "/r/test/comments/abc123/title/",
		Title:               "Test Post",
		Author:              "testauthor",
		SelfText:            "Post body text",
		CreatedUTC:          1705316400,
		URLOverriddenByDest: "https://i.redd.it/abc.jpg",
	}

	item := RdPostToItem(post)

	if item.ExternalID != "abc123" {
		t.Errorf("unexpected ExternalID: %q", item.ExternalID)
	}
	if item.URL != "https://reddit.com/r/test/comments/abc123/title/" {
		t.Errorf("unexpected URL: %q", item.URL)
	}
	if item.Title != "Test Post" {
		t.Errorf("unexpected title: %q", item.Title)
	}
	if item.Author != "testauthor" {
		t.Errorf("unexpected author: %q", item.Author)
	}
	if item.Text == nil {
		t.Error("expected non-nil Text")
	}
	if item.PublishedAt == nil {
		t.Error("expected non-nil PublishedAt")
	} else {
		expected := time.Unix(1705316400, 0)
		if !item.PublishedAt.Equal(expected) {
			t.Errorf("unexpected PublishedAt: %v", item.PublishedAt)
		}
	}
	if len(item.Media) < 1 {
		t.Errorf("expected at least 1 media asset")
	}
}

func TestIgPostToItem(t *testing.T) {
	post := igFeedMedia{
		ID:        "12345",
		Code:      "SHORTCODE",
		TakenAt:   1705316400,
		MediaType: 1,
		Caption:   &igCaption{Text: "My post"},
		User: igUserResult{
			Username: "testuser",
		},
		ImageVersions2: &igImageVersions{
			Candidates: []igMediaCandidate{
				{URL: "https://instagram.com/media.jpg", Width: 1080, Height: 1080},
			},
		},
	}

	item := IgPostToItem(post, "testuser", "post")

	if item.ExternalID != "SHORTCODE" {
		t.Errorf("unexpected ExternalID: %q", item.ExternalID)
	}
	if item.URL != "https://instagram.com/p/SHORTCODE/" {
		t.Errorf("unexpected URL: %q", item.URL)
	}
	if item.Title != "My post" {
		t.Errorf("unexpected title: %q", item.Title)
	}
	if item.Author != "testuser" {
		t.Errorf("unexpected author: %q", item.Author)
	}
	if item.Text == nil {
		t.Error("expected non-nil Text")
	}
	if len(item.Media) != 1 {
		t.Fatalf("expected 1 media asset, got %d", len(item.Media))
	}
	if item.Media[0].Kind != resolve.MediaImage {
		t.Errorf("expected image kind, got %q", item.Media[0].Kind)
	}
	if item.Media[0].Extension != "jpg" {
		t.Errorf("expected jpg extension, got %q", item.Media[0].Extension)
	}
}
