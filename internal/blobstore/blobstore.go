// Package blobstore provides SHA-256 content-addressed filesystem storage.
// Files are stored under <root>/blobs/sha256/<first-two-hex>/<full-hex>
// and are deduplicated: identical content (same hash) is stored only once.
package blobstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var ErrExists = errors.New("blob already exists")

type Store struct {
	Root string
}

func New(root string) *Store {
	return &Store{Root: root}
}

func (s *Store) blobPath(hash string) string {
	if len(hash) < 4 {
		return filepath.Join(s.Root, "blobs", "sha256", "xx", hash)
	}
	return filepath.Join(s.Root, "blobs", "sha256", hash[:2], hash)
}

func (s *Store) Put(srcPath string) (string, error) {
	hash, err := fileHash(srcPath)
	if err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}

	dst := s.blobPath(hash)
	if _, err := os.Stat(dst); err == nil {
		return hash, ErrExists
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("mkdir blob: %w", err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("open src: %w", err)
	}
	defer func() { _ = src.Close() }()

	tmp := dst + ".tmp"
	dstf, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			if _, statErr := os.Stat(dst); statErr == nil {
				return hash, ErrExists
			}
			_ = os.Remove(tmp)
			dstf, err = os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
		}
		if err != nil {
			return "", fmt.Errorf("create blob: %w", err)
		}
	}

	if _, err := io.Copy(dstf, src); err != nil {
		_ = dstf.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("copy blob: %w", err)
	}
	_ = dstf.Close()

	if err := os.Rename(tmp, dst); err != nil {
		return "", fmt.Errorf("rename blob: %w", err)
	}

	return hash, nil
}

func (s *Store) Get(hash string) (*os.File, error) {
	path := s.blobPath(hash)
	return os.Open(path)
}

func (s *Store) Exists(hash string) bool {
	_, err := os.Stat(s.blobPath(hash))
	return err == nil
}

func (s *Store) Remove(hash string) error {
	return os.Remove(s.blobPath(hash))
}

func (s *Store) List() ([]string, error) {
	dir := filepath.Join(s.Root, "blobs", "sha256")
	var hashes []string
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 2 {
			continue
		}
		subPath := filepath.Join(dir, entry.Name())
		subEntries, err := os.ReadDir(subPath)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if sub.IsDir() {
				continue
			}
			hashes = append(hashes, sub.Name())
		}
	}
	return hashes, nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
