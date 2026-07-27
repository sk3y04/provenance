// Package watch manages recurring download subscriptions for Twitter, Reddit, and other sources.
package watch

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

type Subscription struct {
	Name      string                    `json:"name"`
	URL       string                    `json:"url"`
	Options   config.Config             `json:"options"`
	CreatedAt time.Time                 `json:"created_at"`
	UpdatedAt time.Time                 `json:"updated_at"`
	LastRunAt time.Time                 `json:"last_run_at,omitempty"`
	Metadata  map[string]map[string]int `json:"metadata,omitempty"`
}

type Store struct {
	Subscriptions map[string]Subscription `json:"subscriptions"`
}

func Add(name, rawURL string, opts config.Config) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	name, err := safeName(name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(rawURL) == "" {
		return fmt.Errorf("watch URL cannot be empty")
	}
	s, err := load()
	if err != nil {
		return err
	}
	now := time.Now()
	created := now
	if existing, ok := s.Subscriptions[name]; ok {
		created = existing.CreatedAt
	}
	s.Subscriptions[name] = Subscription{Name: name, URL: strings.TrimSpace(rawURL), Options: opts, CreatedAt: created, UpdatedAt: now}
	return save(s)
}

func List() ([]Subscription, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	s, err := load()
	if err != nil {
		return nil, err
	}
	out := make([]Subscription, 0, len(s.Subscriptions))
	for _, sub := range s.Subscriptions {
		out = append(out, sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func Get(name string) (Subscription, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	name, err := safeName(name)
	if err != nil {
		return Subscription{}, err
	}
	s, err := load()
	if err != nil {
		return Subscription{}, err
	}
	sub, ok := s.Subscriptions[name]
	if !ok {
		return Subscription{}, fmt.Errorf("watch subscription %q not found", name)
	}
	return sub, nil
}

func Remove(name string) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	name, err := safeName(name)
	if err != nil {
		return err
	}
	s, err := load()
	if err != nil {
		return err
	}
	delete(s.Subscriptions, name)
	return save(s)
}

func MarkRun(name string) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	name, err := safeName(name)
	if err != nil {
		return err
	}
	s, err := load()
	if err != nil {
		return err
	}
	sub, ok := s.Subscriptions[name]
	if !ok {
		return fmt.Errorf("watch subscription %q not found", name)
	}
	now := time.Now()
	sub.LastRunAt = now
	sub.UpdatedAt = now
	s.Subscriptions[name] = sub
	return save(s)
}

func load() (Store, error) {
	path, err := storePath()
	if err != nil {
		return Store{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Store{Subscriptions: map[string]Subscription{}}, nil
		}
		return Store{}, err
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return Store{}, err
	}
	if s.Subscriptions == nil {
		s.Subscriptions = map[string]Subscription{}
	}
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
	if p := strings.TrimSpace(os.Getenv("PROVENANCE_WATCH_FILE")); p != "" {
		return p, nil
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "provenance", "watch.json"), nil
}

func safeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("watch name cannot be empty")
	}
	if name == "." || name == ".." || strings.Contains(name, "..") || strings.ContainsAny(name, `/\\:`) {
		return "", fmt.Errorf("watch name %q is not allowed", name)
	}
	return strings.NewReplacer(" ", "-", "\t", "-", "\n", "-", "\r", "-").Replace(name), nil
}
