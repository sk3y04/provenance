package watch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sk3y04/provenance/internal/config"
)

func TestWatchFixtureLoad(t *testing.T) {
	src := filepath.Join("testdata", "watch.json")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	file := filepath.Join(t.TempDir(), "watch.json")
	t.Setenv("PROVENANCE_WATCH_FILE", file)
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatalf("write watch: %v", err)
	}

	subs, err := List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}

	sub := subs[0]
	if sub.Name != "test-watch" {
		t.Errorf("expected test-watch, got %q", sub.Name)
	}
	if sub.URL != "https://example.com/source" {
		t.Errorf("unexpected URL: %q", sub.URL)
	}

	got, err := Get("test-watch")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "test-watch" {
		t.Errorf("expected test-watch from Get, got %q", got.Name)
	}
}

func TestWatchSkipAlreadyHandled(t *testing.T) {
	file := filepath.Join(t.TempDir(), "watch.json")
	t.Setenv("PROVENANCE_WATCH_FILE", file)

	if err := Add("skip-watch", "https://example.com/source", config.Config{
		OutputDir: "downloads",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := MarkRun("skip-watch"); err != nil {
		t.Fatalf("mark run: %v", err)
	}

	sub, err := Get("skip-watch")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sub.LastRunAt.IsZero() {
		t.Error("expected non-zero LastRunAt after MarkRun")
	}

	if err := MarkRun("skip-watch"); err != nil {
		t.Fatalf("mark run again: %v", err)
	}

	sub2, err := Get("skip-watch")
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if !sub2.LastRunAt.After(sub.LastRunAt) {
		t.Error("expected LastRunAt to advance after second MarkRun")
	}
}
