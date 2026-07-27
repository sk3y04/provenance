// Package collection provides named, repeatable source synchronisation.
// Collections persist source configuration, seen-item IDs, and sync results
// as JSON, reusing session, manifest, history, and dispatcher internally.
package collection

import (
	"encoding/json"
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

type Collection struct {
	Name       string          `json:"name"`
	URL        string          `json:"url"`
	Site       string          `json:"site"`
	Options    config.Config   `json:"options"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	LastSync   time.Time       `json:"last_sync,omitempty"`
	LastResult *SyncResult     `json:"last_result,omitempty"`
	SeenIDs    map[string]bool `json:"seen_ids"`
}

type SyncResult struct {
	At          time.Time `json:"at"`
	Total       int       `json:"total"`
	New         int       `json:"new"`
	Skipped     int       `json:"skipped"`
	Failed      int       `json:"failed"`
	SessionName string    `json:"session_name,omitempty"`
}

type Store struct {
	Collections map[string]Collection `json:"collections"`
}

func Add(name, rawURL, site string, opts config.Config) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	if err := validName(name); err != nil {
		return err
	}
	if strings.TrimSpace(rawURL) == "" {
		return fmt.Errorf("url must not be empty")
	}

	s, err := load()
	if err != nil {
		return err
	}

	existing, ok := s.Collections[name]
	now := time.Now()
	c := Collection{
		Name:      name,
		URL:       rawURL,
		Site:      site,
		Options:   opts,
		CreatedAt: now,
		UpdatedAt: now,
		SeenIDs:   make(map[string]bool),
	}
	if ok {
		c.CreatedAt = existing.CreatedAt
		c.LastSync = existing.LastSync
		c.LastResult = existing.LastResult
		for k, v := range existing.SeenIDs {
			c.SeenIDs[k] = v
		}
	}
	s.Collections[name] = c
	return save(s)
}

func List() ([]Collection, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	s, err := load()
	if err != nil {
		return nil, err
	}
	list := make([]Collection, 0, len(s.Collections))
	for _, c := range s.Collections {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

func Get(name string) (Collection, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	if err := validName(name); err != nil {
		return Collection{}, err
	}
	s, err := load()
	if err != nil {
		return Collection{}, err
	}
	c, ok := s.Collections[name]
	if !ok {
		return Collection{}, fmt.Errorf("collection %q not found", name)
	}
	if c.SeenIDs == nil {
		c.SeenIDs = make(map[string]bool)
	}
	return c, nil
}

func Remove(name string) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	if err := validName(name); err != nil {
		return err
	}
	s, err := load()
	if err != nil {
		return err
	}
	delete(s.Collections, name)
	return save(s)
}

func AddSeen(name string, ids ...string) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	if err := validName(name); err != nil {
		return err
	}
	s, err := load()
	if err != nil {
		return err
	}
	c, ok := s.Collections[name]
	if !ok {
		return fmt.Errorf("collection %q not found", name)
	}
	if c.SeenIDs == nil {
		c.SeenIDs = make(map[string]bool)
	}
	for _, id := range ids {
		c.SeenIDs[id] = true
	}
	c.UpdatedAt = time.Now()
	s.Collections[name] = c
	return save(s)
}

func RecordSync(name string, result SyncResult) error {
	storeMu.Lock()
	defer storeMu.Unlock()

	if err := validName(name); err != nil {
		return err
	}
	s, err := load()
	if err != nil {
		return err
	}
	c, ok := s.Collections[name]
	if !ok {
		return fmt.Errorf("collection %q not found", name)
	}
	now := time.Now()
	result.At = now
	c.LastSync = now
	c.LastResult = &result
	c.UpdatedAt = now
	s.Collections[name] = c
	return save(s)
}

func storePath() (string, error) {
	if p := os.Getenv("PROVENANCE_COLLECTION_FILE"); p != "" {
		return p, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		dir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, "provenance", "collections.json"), nil
}

func load() (Store, error) {
	path, err := storePath()
	if err != nil {
		return Store{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Store{Collections: make(map[string]Collection)}, nil
	}
	if err != nil {
		return Store{}, fmt.Errorf("read collections: %w", err)
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return Store{}, fmt.Errorf("unmarshal collections: %w", err)
	}
	if s.Collections == nil {
		s.Collections = make(map[string]Collection)
	}
	return s, nil
}

func save(s Store) error {
	path, err := storePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir collections: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal collections: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write collections: %w", err)
	}
	return os.Rename(tmp, path)
}

func validName(name string) error {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "..") {
		return fmt.Errorf("invalid collection name %q", name)
	}
	if strings.ContainsAny(name, "/\\:") {
		return fmt.Errorf("invalid collection name %q: contains path separator", name)
	}
	return nil
}
