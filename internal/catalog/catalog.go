// Package catalog provides persistence interfaces and a JSON adapter
// for archive collection metadata.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/sk3y04/provenance/internal/archive"
)

var mu sync.Mutex

func LoadCollections(vaultRoot string) ([]archive.ArchiveCollection, error) {
	mu.Lock()
	defer mu.Unlock()

	path := filepath.Join(vaultRoot, "collections.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read collections: %w", err)
	}
	var cols []archive.ArchiveCollection
	if err := json.Unmarshal(data, &cols); err != nil {
		return nil, fmt.Errorf("unmarshal collections: %w", err)
	}
	return cols, nil
}

func SaveCollections(vaultRoot string, cols []archive.ArchiveCollection) error {
	mu.Lock()
	defer mu.Unlock()

	path := filepath.Join(vaultRoot, "collections.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(cols, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return os.Rename(tmp, path)
}

func UpsertCollection(vaultRoot string, col archive.ArchiveCollection) error {
	mu.Lock()
	defer mu.Unlock()
	cols, err := loadLocked(vaultRoot)
	if err != nil {
		return err
	}
	found := false
	for i, c := range cols {
		if c.Name == col.Name {
			cols[i] = col
			found = true
			break
		}
	}
	if !found {
		cols = append(cols, col)
	}
	return saveLocked(vaultRoot, cols)
}

func GetCollection(vaultRoot, name string) (*archive.ArchiveCollection, error) {
	cols, err := LoadCollections(vaultRoot)
	if err != nil {
		return nil, err
	}
	for _, c := range cols {
		if c.Name == name {
			return &c, nil
		}
	}
	return nil, fmt.Errorf("archive collection %q not found", name)
}

func ListRevisions(vaultRoot string) ([]string, error) {
	dir := filepath.Join(vaultRoot, "revisions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

func loadLocked(vaultRoot string) ([]archive.ArchiveCollection, error) {
	path := filepath.Join(vaultRoot, "collections.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read collections: %w", err)
	}
	var cols []archive.ArchiveCollection
	if err := json.Unmarshal(data, &cols); err != nil {
		return nil, fmt.Errorf("unmarshal collections: %w", err)
	}
	return cols, nil
}

func saveLocked(vaultRoot string, cols []archive.ArchiveCollection) error {
	path := filepath.Join(vaultRoot, "collections.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(cols, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return os.Rename(tmp, path)
}
