package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sk3y04/provenance/internal/config"
)

func withHistoryFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.json")
	t.Setenv("PROVENANCE_HISTORY_FILE", path)
	return path
}

func TestAddListAndTrim(t *testing.T) {
	withHistoryFile(t)
	base := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		_, err := AddWithLimit(Run{
			ID:          string(rune('a' + i)),
			Title:       "run",
			URLs:        []string{"https://example.test/"},
			CompletedAt: base.Add(time.Duration(i) * time.Minute),
		}, 3)
		if err != nil {
			t.Fatal(err)
		}
	}
	runs, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("len=%d", len(runs))
	}
	if runs[0].ID != "e" || runs[1].ID != "d" || runs[2].ID != "c" {
		t.Fatalf("unexpected order: %#v", runs)
	}
}

func TestRunNormalizesAndTotalsFiles(t *testing.T) {
	withHistoryFile(t)
	run, err := AddWithLimit(Run{
		URLs: []string{" https://a ", "https://a", "https://b"},
		Options: config.Config{
			OutputDir:   "out",
			Concurrency: 7,
			Quality:     "720",
		},
		Files: []File{{Path: "a.mp4", Size: 10, Success: true}, {Path: "b.mp4", Size: 20, Success: true}, {Path: "bad.mp4", Size: 99, Success: false}},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if run.ID == "" || run.Title != "https://a" {
		t.Fatalf("bad id/title: %#v", run)
	}
	if got, want := run.TotalBytes, int64(30); got != want {
		t.Fatalf("total=%d want %d", got, want)
	}
	if len(run.URLs) != 2 || run.URLs[0] != "https://a" || run.URLs[1] != "https://b" {
		t.Fatalf("urls=%#v", run.URLs)
	}
	got, err := Get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	opts := got.Options
	if opts.OutputDir != "out" || opts.Concurrency != 7 || opts.Quality != "720" {
		t.Fatalf("options did not round-trip: %#v", opts)
	}
}

func TestDeleteAndClear(t *testing.T) {
	path := withHistoryFile(t)
	_, err := AddWithLimit(Run{ID: "x", CompletedAt: time.Now()}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := Delete("x"); err != nil {
		t.Fatal(err)
	}
	runs, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs=%#v", runs)
	}
	_, err = AddWithLimit(Run{ID: "y", CompletedAt: time.Now()}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("history file still exists or unexpected err: %v", err)
	}
}
