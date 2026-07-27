// Package history records completed download runs with file, timing, and configuration metadata.
package history

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sk3y04/provenance/internal/config"
)

var storeMu sync.Mutex

const DefaultLimit = 25

type File struct {
	URL     string `json:"url,omitempty"`
	Path    string `json:"path"`
	Size    int64  `json:"size,omitempty"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type Run struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	URLs        []string      `json:"urls"`
	Options     config.Config `json:"options"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt time.Time     `json:"completed_at"`
	Duration    time.Duration `json:"duration"`
	Succeeded   int           `json:"succeeded"`
	Failed      int           `json:"failed"`
	Skipped     int           `json:"skipped"`
	TotalBytes  int64         `json:"total_bytes"`
	Files       []File        `json:"files,omitempty"`
	Error       string        `json:"error,omitempty"`
}

type Store struct {
	Runs []Run `json:"runs"`
}

func List() ([]Run, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	s, err := load()
	if err != nil {
		return nil, err
	}
	out := append([]Run(nil), s.Runs...)
	sortRuns(out)
	return out, nil
}

func Add(run Run) (Run, error) {
	return AddWithLimit(run, DefaultLimit)
}

func AddWithLimit(run Run, limit int) (Run, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if limit <= 0 {
		limit = DefaultLimit
	}
	run = normalizeRun(run)
	s, err := load()
	if err != nil {
		return Run{}, err
	}
	// Replace same ID if caller intentionally reuses one.
	filtered := s.Runs[:0]
	for _, existing := range s.Runs {
		if existing.ID != run.ID {
			filtered = append(filtered, existing)
		}
	}
	s.Runs = append(filtered, run)
	sortRuns(s.Runs)
	if len(s.Runs) > limit {
		s.Runs = s.Runs[:limit]
	}
	return run, save(s)
}

func Get(id string) (Run, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" {
		return Run{}, fmt.Errorf("history id cannot be empty")
	}
	s, err := load()
	if err != nil {
		return Run{}, err
	}
	for _, run := range s.Runs {
		if run.ID == id {
			return run, nil
		}
	}
	return Run{}, os.ErrNotExist
}

func Delete(id string) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("history id cannot be empty")
	}
	s, err := load()
	if err != nil {
		return err
	}
	out := s.Runs[:0]
	for _, run := range s.Runs {
		if run.ID != id {
			out = append(out, run)
		}
	}
	s.Runs = out
	return save(s)
}

func Clear() error {
	storeMu.Lock()
	defer storeMu.Unlock()
	path, err := storePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func normalizeRun(run Run) Run {
	if strings.TrimSpace(run.ID) == "" {
		run.ID = newID()
	}
	if run.CompletedAt.IsZero() {
		run.CompletedAt = time.Now()
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = run.CompletedAt.Add(-run.Duration)
		if run.StartedAt.IsZero() {
			run.StartedAt = run.CompletedAt
		}
	}
	if run.Duration == 0 && !run.StartedAt.IsZero() && !run.CompletedAt.IsZero() {
		run.Duration = run.CompletedAt.Sub(run.StartedAt)
	}
	seen := map[string]struct{}{}
	urls := run.URLs[:0]
	for _, u := range run.URLs {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		urls = append(urls, u)
	}
	run.URLs = append([]string(nil), urls...)
	if strings.TrimSpace(run.Title) == "" {
		if len(run.URLs) > 0 {
			run.Title = run.URLs[0]
		} else {
			run.Title = "download"
		}
	} else {
		run.Title = strings.TrimSpace(run.Title)
	}
	if run.TotalBytes == 0 {
		for _, f := range run.Files {
			if f.Success && f.Size > 0 {
				run.TotalBytes += f.Size
			}
		}
	}
	return run
}

func load() (Store, error) {
	path, err := storePath()
	if err != nil {
		return Store{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Store{}, nil
		}
		return Store{}, err
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return Store{}, fmt.Errorf("decode history: %w", err)
	}
	sortRuns(s.Runs)
	return s, nil
}

func save(s Store) error {
	path, err := storePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func storePath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("PROVENANCE_HISTORY_FILE")); p != "" {
		return p, nil
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "provenance", "history.json"), nil
}

func sortRuns(runs []Run) {
	sort.SliceStable(runs, func(i, j int) bool {
		return runs[i].CompletedAt.After(runs[j].CompletedAt)
	})
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%s-%d", time.Now().Format("20060102-150405"), time.Now().UnixNano())
}
