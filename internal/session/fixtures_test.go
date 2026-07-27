package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sk3y04/provenance/internal/config"
)

func TestSessionFixtureResume(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROVENANCE_SESSION_DIR", dir)

	src := filepath.Join("testdata", "resume_session.json")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dst := filepath.Join(dir, "test-session.json")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	s, err := Load("test-session")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}

	counts := s.Counts()
	if counts.Total != 3 {
		t.Errorf("expected 3 total, got %d", counts.Total)
	}
	if counts.Succeeded != 1 {
		t.Errorf("expected 1 succeeded, got %d", counts.Succeeded)
	}
	if counts.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", counts.Failed)
	}
	if counts.Pending != 1 {
		t.Errorf("expected 1 pending, got %d", counts.Pending)
	}

	pending := s.URLsByStatus(StatusPending)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending URL, got %d", len(pending))
	}
	if pending[0] != "https://example.com/pending" {
		t.Errorf("unexpected pending URL: %q", pending[0])
	}

	failed := s.URLsByStatus(StatusFailed)
	if len(failed) != 1 {
		t.Fatalf("expected 1 failed URL, got %d", len(failed))
	}
	if failed[0] != "https://example.com/failed" {
		t.Errorf("unexpected failed URL: %q", failed[0])
	}

	resumeURLs := s.URLsByStatus(StatusPending, StatusFailed)
	if len(resumeURLs) != 2 {
		t.Fatalf("expected 2 URLs for resume, got %d", len(resumeURLs))
	}
}

func TestSessionFixtureRetryFailed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROVENANCE_SESSION_DIR", dir)

	src := filepath.Join("testdata", "retry_failed_session.json")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dst := filepath.Join(dir, "retry-session.json")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	s, err := Load("retry-session")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}

	failed := s.URLsByStatus(StatusFailed)
	if len(failed) != 2 {
		t.Fatalf("expected 2 failed URLs, got %d", len(failed))
	}

	failedSet := make(map[string]bool)
	for _, u := range failed {
		failedSet[u] = true
	}
	if !failedSet["https://example.com/failed1"] {
		t.Error("expected failed1 in failed URLs")
	}
	if !failedSet["https://example.com/failed2"] {
		t.Error("expected failed2 in failed URLs")
	}

	entries := s.EntriesByStatus(StatusFailed)
	for _, e := range entries {
		if e.Status != StatusFailed {
			t.Errorf("expected status failed, got %q for %s", e.Status, e.URL)
		}
		if e.LastError == "" {
			t.Errorf("expected non-empty error for %s", e.URL)
		}
	}
}

func TestSessionFixtureInterruptedResume(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROVENANCE_SESSION_DIR", dir)

	s, err := OpenOrCreate("interrupted-test", config.Config{
		OutputDir:   "downloads",
		Concurrency: 4,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := s.AddURLs([]string{
		"https://example.com/a",
		"https://example.com/b",
		"https://example.com/c",
	}, "argument"); err != nil {
		t.Fatalf("add URLs: %v", err)
	}

	s.Start("https://example.com/a")
	s.Success("https://example.com/a")
	s.Start("https://example.com/b")

	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	s2, err := Load("interrupted-test")
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}

	reset, err := s2.ResetRunning()
	if err != nil {
		t.Fatalf("reset running: %v", err)
	}
	if reset != 1 {
		t.Errorf("expected 1 reset URL, got %d", reset)
	}

	counts := s2.Counts()
	if counts.Pending != 2 {
		t.Errorf("expected 2 pending after reset, got %d", counts.Pending)
	}
	if counts.Running != 0 {
		t.Errorf("expected 0 running after reset, got %d", counts.Running)
	}

	resumeURLs := s2.URLsByStatus(StatusPending)
	if len(resumeURLs) != 2 {
		t.Errorf("expected 2 URLs for resume, got %d", len(resumeURLs))
	}
}

func TestSessionFixtureWatchSkip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROVENANCE_SESSION_DIR", dir)

	s, err := OpenOrCreate("watch-skip-test", config.Config{
		OutputDir: "downloads",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	urls := []string{
		"https://example.com/new1",
		"https://example.com/already1",
		"https://example.com/new2",
	}
	if _, err := s.AddURLs(urls, "argument"); err != nil {
		t.Fatalf("add URLs: %v", err)
	}

	s.Start("https://example.com/already1")
	s.Success("https://example.com/already1")

	counts := s.Counts()
	if counts.Succeeded != 1 {
		t.Errorf("expected 1 succeeded, got %d", counts.Succeeded)
	}
	if counts.Pending != 2 {
		t.Errorf("expected 2 pending, got %d", counts.Pending)
	}
}
