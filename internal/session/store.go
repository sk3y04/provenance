// Package session provides persistent, resumable download sessions with per-URL state tracking.
package session

import (
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

const maxSaveRetries = 3

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusSkipped   Status = "skipped"
)

type Entry struct {
	URL       string    `json:"url"`
	Source    string    `json:"source,omitempty"`
	Status    Status    `json:"status"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var ErrConcurrentModification = errors.New("session modified by another process; reload and retry")

type Session struct {
	Name      string            `json:"name"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Version   int64             `json:"version"`
	Options   config.Config     `json:"options"`
	Entries   map[string]*Entry `json:"entries"`

	mu   sync.Mutex
	path string
}

type Counts struct {
	Pending   int
	Running   int
	Succeeded int
	Failed    int
	Skipped   int
	Total     int
}

type Info struct {
	Name      string
	Path      string
	CreatedAt time.Time
	UpdatedAt time.Time
	Counts    Counts
}

func List() ([]Info, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	infos := make([]Info, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		s, err := Load(name)
		if err != nil {
			continue
		}
		infos = append(infos, Info{Name: s.Name, Path: s.Path(), CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Counts: s.Counts()})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].UpdatedAt.After(infos[j].UpdatedAt) })
	return infos, nil
}

func Delete(name string) error {
	path, err := pathFor(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func OpenOrCreate(name string, opts config.Config) (*Session, error) {
	if s, err := Load(name); err == nil {
		s.mu.Lock()
		s.Options = opts
		s.UpdatedAt = time.Now()
		err = s.saveLocked()
		s.mu.Unlock()
		return s, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	path, err := pathFor(name)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	s := &Session{
		Name:      strings.TrimSpace(name),
		CreatedAt: now,
		UpdatedAt: now,
		Options:   opts,
		Entries:   map[string]*Entry{},
		path:      path,
	}
	return s, s.Save()
}

func Load(name string) (*Session, error) {
	path, err := pathFor(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	if s.Entries == nil {
		s.Entries = map[string]*Entry{}
	}
	s.path = path
	return &s, nil
}

func (s *Session) Path() string { return s.path }

func (s *Session) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Session) AddURLs(urls []string, source string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	added := 0
	for _, raw := range urls {
		if s.addURLLocked(raw, source) {
			added++
		}
	}
	if added > 0 {
		s.UpdatedAt = time.Now()
		return added, s.saveLocked()
	}
	return 0, nil
}

func (s *Session) Queue(url, source string) {
	s.mu.Lock()
	if s.addURLLocked(url, source) {
		if err := s.saveLocked(); err != nil {
			fmt.Fprintf(os.Stderr, "[provenance] session save warning: %v\n", err)
		}
	}
	s.mu.Unlock()
}

func (s *Session) Start(url string) {
	s.transition(url, StatusRunning, "", true)
}

func (s *Session) Success(url string) {
	s.transition(url, StatusSucceeded, "", false)
}

func (s *Session) Failure(url string, err error) {
	msg := "failed"
	if err != nil {
		msg = err.Error()
	}
	s.transition(url, StatusFailed, msg, false)
}

func (s *Session) Skip(url, reason string) {
	s.transition(url, StatusSkipped, reason, false)
}

func (s *Session) ResetRunning() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := 0
	now := time.Now()
	for _, e := range s.Entries {
		if e.Status == StatusRunning {
			e.Status = StatusPending
			e.UpdatedAt = now
			changed++
		}
	}
	if changed > 0 {
		s.UpdatedAt = now
		if err := s.saveLocked(); err != nil {
			if errors.Is(err, ErrConcurrentModification) {
				fmt.Fprintf(os.Stderr, "[provenance] session save conflict, reloading and retrying...\n")
				if rerr := s.reloadFromDiskLocked(); rerr == nil {
					changed = 0
					for _, e := range s.Entries {
						if e.Status == StatusRunning {
							e.Status = StatusPending
							e.UpdatedAt = time.Now()
							changed++
						}
					}
					if changed > 0 {
						s.UpdatedAt = time.Now()
						_ = s.saveLocked()
					}
				}
			}
			return changed, err
		}
	}
	return changed, nil
}

func (s *Session) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c Counts
	for _, e := range s.Entries {
		c.Total++
		switch e.Status {
		case StatusPending:
			c.Pending++
		case StatusRunning:
			c.Running++
		case StatusSucceeded:
			c.Succeeded++
		case StatusFailed:
			c.Failed++
		case StatusSkipped:
			c.Skipped++
		}
	}
	return c
}

func (s *Session) EntriesByStatus(statuses ...Status) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := map[Status]struct{}{}
	for _, st := range statuses {
		wanted[st] = struct{}{}
	}
	out := make([]Entry, 0)
	for _, e := range s.Entries {
		if _, ok := wanted[e.Status]; ok {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.Before(out[j].UpdatedAt)
	})
	return out
}

func (s *Session) URLsByStatus(statuses ...Status) []string {
	entries := s.EntriesByStatus(statuses...)
	urls := make([]string, 0, len(entries))
	for _, e := range entries {
		urls = append(urls, e.URL)
	}
	return urls
}

func (s *Session) transition(url string, status Status, lastError string, countAttempt bool) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addURLLocked(url, "discovered")
	e := s.Entries[url]
	if countAttempt {
		e.Attempts++
	}
	e.Status = status
	e.LastError = lastError
	e.UpdatedAt = time.Now()
	s.UpdatedAt = e.UpdatedAt
	s.saveWithRetryLocked()
}

func (s *Session) saveWithRetryLocked() {
	for retry := 0; retry < maxSaveRetries; retry++ {
		err := s.saveLocked()
		if err == nil {
			return
		}
		if !errors.Is(err, ErrConcurrentModification) {
			fmt.Fprintf(os.Stderr, "[provenance] session save warning: %v\n", err)
			return
		}
		if err := s.reloadFromDiskLocked(); err != nil {
			fmt.Fprintf(os.Stderr, "[provenance] session reload warning: %v\n", err)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "[provenance] session save: max retries exceeded (concurrent modification)\n")
}

func (s *Session) reloadFromDiskLocked() error {
	if s.path == "" {
		return fmt.Errorf("cannot reload session with empty path")
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var reloaded Session
	if err := json.Unmarshal(data, &reloaded); err != nil {
		return err
	}
	if reloaded.Entries == nil {
		reloaded.Entries = map[string]*Entry{}
	}
	s.Version = reloaded.Version
	s.Entries = reloaded.Entries
	s.UpdatedAt = reloaded.UpdatedAt
	return nil
}

func (s *Session) addURLLocked(raw, source string) bool {
	url := strings.TrimSpace(raw)
	if url == "" {
		return false
	}
	if s.Entries == nil {
		s.Entries = map[string]*Entry{}
	}
	if _, ok := s.Entries[url]; ok {
		return false
	}
	now := time.Now()
	s.Entries[url] = &Entry{
		URL:       url,
		Source:    source,
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.UpdatedAt = now
	return true
}

func (s *Session) saveLocked() error {
	if s.path == "" {
		path, err := pathFor(s.Name)
		if err != nil {
			return err
		}
		s.path = path
	}
	if s.Version > 0 {
		data, err := os.ReadFile(s.path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			var onDisk Session
			if err := json.Unmarshal(data, &onDisk); err == nil && onDisk.Version != s.Version {
				return ErrConcurrentModification
			}
		}
	}
	if s.Version == 0 {
		s.Version = 1
	} else {
		s.Version++
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func pathFor(name string) (string, error) {
	safe, err := safeName(name)
	if err != nil {
		return "", err
	}
	dir, err := sessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, safe+".json"), nil
}

func sessionsDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("PROVENANCE_SESSION_DIR")); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			return "", err
		}
		base = wd
	}
	return filepath.Join(base, "provenance", "sessions"), nil
}

func safeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("session name cannot be empty")
	}
	if name == "." || name == ".." || strings.Contains(name, "..") {
		return "", fmt.Errorf("session name %q is not allowed", name)
	}
	if strings.ContainsAny(name, `/\\:`) {
		return "", fmt.Errorf("session name %q cannot contain path separators", name)
	}
	repl := strings.NewReplacer(" ", "-", "\t", "-", "\n", "-", "\r", "-")
	return repl.Replace(name), nil
}
