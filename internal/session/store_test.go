package session

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/sk3y04/provenance/internal/config"
)

func TestSessionLifecycle(t *testing.T) {
	t.Setenv("PROVENANCE_SESSION_DIR", t.TempDir())

	s, err := OpenOrCreate("my videos", config.Config{
		OutputDir:   "downloads",
		Concurrency: 6,
		Quality:     "720",
	})
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}

	added, err := s.AddURLs([]string{
		"https://example.com/a",
		"https://example.com/a",
		"  https://example.com/b  ",
		"",
	}, "argument")
	if err != nil {
		t.Fatalf("AddURLs: %v", err)
	}
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}

	s.Start("https://example.com/a")
	s.Success("https://example.com/a")
	s.Start("https://example.com/b")
	s.Failure("https://example.com/b", errors.New("network failed"))
	s.Queue("https://example.com/c", "discovered")
	s.Start("https://example.com/c")

	reset, err := s.ResetRunning()
	if err != nil {
		t.Fatalf("ResetRunning: %v", err)
	}
	if reset != 1 {
		t.Fatalf("reset = %d, want 1", reset)
	}

	loaded, err := Load("my videos")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	counts := loaded.Counts()
	if counts.Total != 3 || counts.Succeeded != 1 || counts.Failed != 1 || counts.Pending != 1 {
		t.Fatalf("counts = %+v, want total=3 succeeded=1 failed=1 pending=1", counts)
	}

	failed := loaded.EntriesByStatus(StatusFailed)
	if len(failed) != 1 || failed[0].URL != "https://example.com/b" || !strings.Contains(failed[0].LastError, "network failed") {
		t.Fatalf("failed entries = %+v", failed)
	}

	if _, err := os.Stat(loaded.Path()); err != nil {
		t.Fatalf("session file was not written: %v", err)
	}
}

func TestInvalidSessionNames(t *testing.T) {
	t.Setenv("PROVENANCE_SESSION_DIR", t.TempDir())

	bad := []string{"", "..", "../x", `bad\name`, "bad:name"}
	for _, name := range bad {
		if _, err := OpenOrCreate(name, config.Config{}); err == nil {
			t.Fatalf("OpenOrCreate(%q) succeeded, want error", name)
		}
	}
}
