package watch

import (
	"path/filepath"
	"testing"

	"github.com/sk3y04/provenance/internal/config"
)

func TestWatchStoreLifecycle(t *testing.T) {
	t.Setenv("PROVENANCE_WATCH_FILE", filepath.Join(t.TempDir(), "watch.json"))

	if err := Add("creator one", "https://example.com/a", config.Config{OutputDir: "downloads"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Add("creator two", "https://example.com/b", config.Config{Quality: "720"}); err != nil {
		t.Fatalf("Add second: %v", err)
	}
	subs, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(subs) != 2 || subs[0].Name != "creator-one" {
		t.Fatalf("subs = %+v", subs)
	}
	sub, err := Get("creator one")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sub.URL != "https://example.com/a" {
		t.Fatalf("URL = %q", sub.URL)
	}
	if err := MarkRun("creator one"); err != nil {
		t.Fatalf("MarkRun: %v", err)
	}
	sub, _ = Get("creator one")
	if sub.LastRunAt.IsZero() {
		t.Fatalf("LastRunAt was not set")
	}
	if err := Remove("creator two"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	subs, _ = List()
	if len(subs) != 1 {
		t.Fatalf("len(subs) = %d, want 1", len(subs))
	}
}
