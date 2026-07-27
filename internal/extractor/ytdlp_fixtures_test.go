package extractor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sk3y04/provenance/internal/manifest"
)

func loadTestFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func unmarshalYtdlpInfo(t *testing.T, data []byte) ytdlpInfo {
	t.Helper()
	var info ytdlpInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshal ytdlp info: %v", err)
	}
	return info
}

func itemFromYtdlp(info ytdlpInfo) manifest.Item {
	return manifest.Item{
		ID:        info.ID,
		URL:       firstNonEmpty(info.WebpageURL, info.URL),
		Title:     info.Title,
		Extension: info.Ext,
		Size:      info.Filesize,
		Source:    firstNonEmpty(info.Extractor, "yt-dlp"),
		Creator:   info.Uploader,
		Kind:      "video",
	}
}

func TestYtdlpSingleVideoFixture(t *testing.T) {
	data := loadTestFixture(t, "ytdlp_single_video.json")
	info := unmarshalYtdlpInfo(t, data)

	if info.ID != "dQw4w9WgXcQ" {
		t.Errorf("expected ID dQw4w9WgXcQ, got %q", info.ID)
	}
	if info.Title != "Rick Astley - Never Gonna Give You Up" {
		t.Errorf("unexpected title: %q", info.Title)
	}
	if info.Ext != "mp4" {
		t.Errorf("expected mp4 ext, got %q", info.Ext)
	}
	if info.Extractor != "youtube" {
		t.Errorf("expected youtube extractor, got %q", info.Extractor)
	}
	if info.Uploader != "Rick Astley" {
		t.Errorf("expected Rick Astley uploader, got %q", info.Uploader)
	}
	if info.Filesize != 9830400 {
		t.Errorf("expected filesize 9830400, got %d", info.Filesize)
	}

	item := itemFromYtdlp(info)
	if item.Extension != "mp4" {
		t.Errorf("item: expected mp4 extension, got %q", item.Extension)
	}
	if item.Source != "youtube" {
		t.Errorf("item: expected youtube source, got %q", item.Source)
	}
	if item.Creator != "Rick Astley" {
		t.Errorf("item: expected Rick Astley creator, got %q", item.Creator)
	}
}

func TestYtdlpPlaylistFixture(t *testing.T) {
	data := loadTestFixture(t, "ytdlp_playlist.json")
	rawLines := strings.Split(strings.TrimSpace(string(data)), "\n")

	var items []manifest.Item
	for i, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		info := unmarshalYtdlpInfo(t, []byte(line))
		items = append(items, itemFromYtdlp(info))
		_ = i
	}

	if len(items) != 3 {
		t.Fatalf("expected 3 playlist items, got %d", len(items))
	}

	ids := []string{"vid1", "vid2", "vid3"}
	for i, want := range ids {
		if items[i].ID != want {
			t.Errorf("item %d: expected ID %q, got %q", i, want, items[i].ID)
		}
	}

	extensions := map[string]int{}
	for _, it := range items {
		extensions[it.Extension]++
	}
	if extensions["mp4"] != 2 {
		t.Errorf("expected 2 mp4 items, got %d", extensions["mp4"])
	}
	if extensions["webm"] != 1 {
		t.Errorf("expected 1 webm item, got %d", extensions["webm"])
	}

	m := manifest.New("https://youtube.com/playlist?list=PL123", "yt-dlp", items)
	if s := m.Summary(); s.Count != 3 {
		t.Errorf("expected 3 items in manifest, got %d", s.Count)
	}
}
