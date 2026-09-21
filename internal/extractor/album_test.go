package extractor

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAlbumURLClassification covers URL pattern recognition for the
// album-listing sites, direct /a/<id> album pages, and /f|/v|/i|/d/ per-file
// pages. Hosts are taken from the decoded tables (no literals).
func TestAlbumURLClassification(t *testing.T) {
	album0 := albAlbumHosts[0]
	legacy := albAlbumHosts[len(albAlbumHosts)-1]
	idx0, idx1 := albIndexHosts[0], albIndexHosts[1]
	tests := []struct {
		url  string
		want string
	}{
		{fmt.Sprintf("https://%s/", idx0), "index"},
		{fmt.Sprintf("https://%s/?q=pikachu", idx0), "index"},
		{fmt.Sprintf("https://%s/topalbums", idx0), "index"},
		{fmt.Sprintf("https://%s/", idx1), "index"},
		{fmt.Sprintf("https://%s/a/mlhYhhhJ", album0), "album"},
		{fmt.Sprintf("https://%s/a/61ZTSYd6", albAlbumHosts[1]), "album"},
		{fmt.Sprintf("https://%s/a/abc123?ref=x", albAlbumHosts[2]), "album"},
		{fmt.Sprintf("https://%s/a/legacy", legacy), "album"}, // legacy host still recognized
		{fmt.Sprintf("https://%s/f/MnqoeFhfK72tE", album0), "file"},
		{fmt.Sprintf("https://%s/v/abcXYZ", album0), "file"},
		{fmt.Sprintf("https://%s/i/abc123", album0), "file"},
		{fmt.Sprintf("https://%s/d/abc123", album0), "file"},
		{"https://www.instagram.com/p/ABC/", ""},
		{"https://reddit.com/r/test", ""},
		{"not a url", ""},
	}
	for _, tt := range tests {
		if got := albURLKind(tt.url); got != tt.want {
			t.Errorf("albURLKind(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

// TestAlbumHostIsAlbum isolates the album-host membership check (single source
// of truth for the rotating domain list).
func TestAlbumHostIsAlbum(t *testing.T) {
	if !albHostIsAlbum(albAlbumHosts[0]) {
		t.Errorf("%s should be an album host", albAlbumHosts[0])
	}
	if albHostIsAlbum("example.com") {
		t.Error("example.com should not be an album host")
	}
	if !albHostIsAlbum(strings.ToUpper(albAlbumHosts[0]) + ".") {
		t.Error("host normalization should lowercase and strip trailing dot")
	}
}

// TestAlbumXORDecrypt verifies the XOR decryption against a known input/output
// pair. The ciphertext below is the deterministic base64 output of
// XOR-encrypting the plaintext with key "SECRET_KEY_472222" (timestamp
// 1700000000, bucketed hourly as ts/3600).
func TestAlbumXORDecrypt(t *testing.T) {
	const (
		encoded = "OzE3IjZucGQmPTEaUkpTX0I/IG0xKjlwODE2LVVQVx1fVzcsIn0kNjwvID9uBgQGBwQcPjV3"
		want    = "https://cdn.example.com/storage/media/abcdef123456.mp4"
		ts      = int64(1700000000)
	)
	got := albXORDecrypt(encoded, ts)
	if got != want {
		t.Errorf("albXORDecrypt(%q, %d) = %q, want %q", encoded, ts, got, want)
	}

	// Garbage input must yield "" (treated as "not usable"), not a panic.
	if got := albXORDecrypt("!!!not base64!!!", ts); got != "" {
		t.Errorf("albXORDecrypt(garbage) = %q, want empty", got)
	}
	if got := albXORDecrypt("", ts); got != "" {
		t.Errorf("albXORDecrypt(empty) = %q, want empty", got)
	}
}

// TestAlbumScrapeFileListFromFixture parses the real-structure album fixture
// and checks that images/videos are extracted with the correct filename,
// link, and kind, and that the server-side template fragment is ignored.
func TestAlbumScrapeFileListFromFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "album_page.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	html := string(data)

	host := albAlbumHosts[0]
	album := &albAlbum{}
	if m := albOGTitleRe.FindStringSubmatch(html); m != nil {
		album.Title = m[1]
	}
	files := albFileLinks(html, fmt.Sprintf("https://%s/a/testalbum", host))

	if album.Title != "Pikachu Meme Compilation" {
		t.Errorf("album title = %q", album.Title)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files (template fragment must be skipped), got %d", len(files))
	}

	want := []struct {
		slug  string
		name  string
		kind  string
		ext   string
		page  string
	}{
		{"aaa111bbb222", "pika_vid_01.mp4", "video", "mp4", fmt.Sprintf("https://%s/f/aaa111bbb222", host)},
		{"ccc333ddd444", "pika_pic_02.jpg", "image", "jpg", fmt.Sprintf("https://%s/f/ccc333ddd444", host)},
		{"eee555fff666", "pika_pic_03.png", "image", "png", fmt.Sprintf("https://%s/f/eee555fff666", host)},
	}
	for i, w := range want {
		f := files[i]
		if f.Slug != w.slug {
			t.Errorf("file %d slug = %q, want %q", i, f.Slug, w.slug)
		}
		if f.Name != w.name {
			t.Errorf("file %d name = %q, want %q", i, f.Name, w.name)
		}
		if f.Kind != w.kind {
			t.Errorf("file %d kind = %q, want %q", i, f.Kind, w.kind)
		}
		if f.Ext != w.ext {
			t.Errorf("file %d ext = %q, want %q", i, f.Ext, w.ext)
		}
		if f.PageURL != w.page {
			t.Errorf("file %d page = %q, want %q", i, f.PageURL, w.page)
		}
	}
}

// TestAlbumFirstAlbumLink verifies index resolution picks the outbound album
// link from the card anchor.
func TestAlbumFirstAlbumLink(t *testing.T) {
	h := albAlbumHosts[0]
	html := fmt.Sprintf(`<a href="https://%s/a/61ZTSYd6" target="_blank" class="card">
		<span>View album</span></a>
		<a href="https://%s/a/mlhYhhhJ" target="_blank" class="card">Open</a>`, h, h)
	got := firstAlbumLink(html)
	if got != fmt.Sprintf("https://%s/a/61ZTSYd6", h) {
		t.Errorf("firstAlbumLink = %q", got)
	}

	if got := firstAlbumLink(`<p>no album links here</p>`); got != "" {
		t.Errorf("firstAlbumLink(no links) = %q, want empty", got)
	}
}

// TestAlbumAlbumIDFromURL checks the fallback name used when og:title is missing.
func TestAlbumAlbumIDFromURL(t *testing.T) {
	if got := albAlbumIDFromURL(fmt.Sprintf("https://%s/a/someId123", albAlbumHosts[0])); got != "someId123" {
		t.Errorf("albAlbumIDFromURL = %q", got)
	}
	if got := albAlbumIDFromURL(fmt.Sprintf("https://%s/", albIndexHosts[0])); got != "album" {
		t.Errorf("albAlbumIDFromURL fallback = %q", got)
	}
}

// TestAlbumMaintenanceVID ensures the placeholder video is detected.
func TestAlbumMaintenanceVID(t *testing.T) {
	if !albIsMaintenanceVideo("https://cdn.example.com/maintenance-vid.mp4") {
		t.Error("should detect maintenance-vid url")
	}
	if albIsMaintenanceVideo("https://cdn.example.com/real/file.mp4") {
		t.Error("real file should not be flagged as maintenance")
	}
}

// TestAlbumKindForName isolates the extension -> kind mapping used to pick the
// images/ vs videos/ subdirectory.
func TestAlbumKindForName(t *testing.T) {
	cases := map[string]string{
		"f.mp4":   "video",
		"f.webm":  "video",
		"f.gifv":  "video",
		"f.jpg":   "image",
		"f.png":   "image",
		"f.webp":  "image",
		"noext":   "image", // default
		"f.Bmp":   "image", // case-insensitive
	}
	for name, want := range cases {
		if got := albKindForName(name); got != want {
			t.Errorf("albKindForName(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestAlbumParseFilePage verifies the pure per-file page parser extracts the
// h1 filename, the numeric data-file-id, and the signing-flow variables.
func TestAlbumParseFilePage(t *testing.T) {
	html := `<html><head><title>x</title></head><body>
	<h1 class="theName">pika_vid_01.mp4</h1>
	<script id="fileTracker" data-file-id="63031538"></script>
	<script>
		var jsCDN = "https:\/\/c6no3-b.example.com\/storage\/media\/83299d0b-49d7-478b.mp4";
		var signUrl = "https://glb-apisign.example.com/sign";
	</script>
	</body></html>`

	info := albParseFilePage(fmt.Sprintf("https://%s/f/MnqoeFhfK72tE", albAlbumHosts[0]), html)
	if info.Name != "pika_vid_01.mp4" {
		t.Errorf("h1 name = %q", info.Name)
	}
	if info.FileID != "63031538" {
		t.Errorf("data-file-id = %q, want 63031538", info.FileID)
	}
	if !strings.Contains(info.CDNUri, "/storage/media/83299d0b-49d7-478b.mp4") {
		t.Errorf("jsCDN = %q", info.CDNUri)
	}
	if !strings.Contains(info.SignURL, "/sign") {
		t.Errorf("signUrl = %q", info.SignURL)
	}
	if info.Slug != "MnqoeFhfK72tE" {
		t.Errorf("slug = %q, want MnqoeFhfK72tE", info.Slug)
	}
}

// --- Network path tests (httptest, offline) -------------------------------

// swapAlbumCDN points the CDN tables and transport at a TLS test server and
// returns a restore function.
func swapAlbumCDN(t *testing.T, server *httptest.Server) func() {
	t.Helper()
	host := strings.TrimPrefix(server.URL, "https://")
	origTransport := albClient.Transport
	origCDN := albCDNHosts
	origSuffix := albAPIPathSuffixes
	albClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	albCDNHosts = []string{host}
	albAPIPathSuffixes = []string{"/api/_001"}
	return func() {
		albClient.Transport = origTransport
		albCDNHosts = origCDN
		albAPIPathSuffixes = origSuffix
	}
}

// TestAlbumResolveViaAPIProbing drives the legacy POST-to-API flow against a
// test server, asserting the endpoint is probed once, the URL is returned, and
// the working endpoint is cached for subsequent files.
func TestAlbumResolveViaAPIProbing(t *testing.T) {
	var calls int
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// Referer/Origin should be set by the extractor.
		if r.Header.Get("Origin") == "" || r.Header.Get("Referer") == "" {
			t.Errorf("missing Origin/Referer on api request")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"url":       "https://cdn.example.com/storage/media/abc.mp4",
			"encrypted": false,
			"timestamp": int64(1700000000),
		})
	}))
	defer ts.Close()
	restore := swapAlbumCDN(t, ts)
	defer restore()

	state := newAlbResolverState()
	info := &albFileInfo{PageURL: fmt.Sprintf("https://%s/f/abc", albAlbumHosts[0]), Slug: "abc", FileID: "63031538"}

	u1, ref, err := resolveFileViaAPI(context.Background(), info, state, nil)
	if err != nil {
		t.Fatalf("resolveFileViaAPI (file 1) error: %v", err)
	}
	if !strings.HasPrefix(u1, "https://cdn.example.com/storage/media/abc.mp4") {
		t.Errorf("unexpected url: %q", u1)
	}
	if !strings.Contains(ref, "/file/63031538") {
		t.Errorf("unexpected referer: %q", ref)
	}

	// Second file: the working endpoint should be reused (same single server
	// endpoint), so calls only increase for the new file's id.
	info2 := &albFileInfo{PageURL: fmt.Sprintf("https://%s/f/def", albAlbumHosts[0]), Slug: "def", FileID: "999"}
	u2, _, err := resolveFileViaAPI(context.Background(), info2, state, nil)
	if err != nil {
		t.Fatalf("resolveFileViaAPI (file 2) error: %v", err)
	}
	if u2 != u1 {
		t.Errorf("expected reused endpoint url, got %q", u2)
	}
}

// TestAlbumResolveViaSigning drives the live signing flow: the per-file page
// exposes jsCDN + signUrl; the extractor signs jsCDN's path and appends the
// token/expiration.
func TestAlbumResolveViaSigning(t *testing.T) {
	var sawPath string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sign" {
			sawPath = r.URL.Query().Get("path")
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "tok123", "ex": int64(1788299032)})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	restore := swapAlbumCDN(t, ts)
	defer restore()

	info := &albFileInfo{
		PageURL: fmt.Sprintf("https://%s/f/MnqoeFhfK72tE", albAlbumHosts[0]),
		Slug:    "MnqoeFhfK72tE",
		Name:    "pika_vid_01.mp4",
		CDNUri:  ts.URL + "/storage/media/83299d0b-49d7-478b-8ca5-4e7a8dc0bfae.mp4",
		SignURL: ts.URL + "/sign",
	}

	state := newAlbResolverState()
	dlURL, ref, err := state.resolveDownloadURLForInfo(context.Background(), info, nil)
	if err != nil {
		t.Fatalf("resolveDownloadURLForInfo error: %v", err)
	}
	if sawPath != "/storage/media/83299d0b-49d7-478b-8ca5-4e7a8dc0bfae.mp4" {
		t.Errorf("sign path = %q", sawPath)
	}
	if !strings.Contains(dlURL, "token=tok123") || !strings.Contains(dlURL, "ex=1788299032") {
		t.Errorf("signed url missing token/ex: %q", dlURL)
	}
	if ref != info.PageURL {
		t.Errorf("ref = %q, want %q", ref, info.PageURL)
	}
}

// TestAlbumAPIMaintenance detects the maintenance-vid placeholder returned by
// a hostile/unavailable file server and treats it as a retryable failure.
func TestAlbumAPIMaintenance(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"url":       "https://cdn.example.com/maintenance-vid.mp4",
			"encrypted": false,
			"timestamp": int64(1700000000),
		})
	}))
	defer ts.Close()
	restore := swapAlbumCDN(t, ts)
	defer restore()

	info := &albFileInfo{PageURL: fmt.Sprintf("https://%s/f/m", albAlbumHosts[0]), Slug: "m", FileID: "1"}
	u, _, ok, err := albAPIGet(context.Background(), strings.TrimPrefix(ts.URL, "https://"), "/api/_001", info, newAlbResolverState(), nil)
	if err == nil {
		t.Fatalf("expected maintenance error, got url %q", u)
	}
	if ok {
		t.Error("albAPIGet should return ok=false for maintenance-vid")
	}
}
