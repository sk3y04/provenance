package blobstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPutGetExists(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	tmp := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmp, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}

	hash, err := s.Put(tmp)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}

	if !s.Exists(hash) {
		t.Fatal("expected blob to exist")
	}

	f, err := s.Get(hash)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer f.Close()

	data := make([]byte, 11)
	n, err := f.Read(data)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data[:n]) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data[:n]))
	}

	blobPath := s.blobPath(hash)
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		t.Errorf("expected blob file at %s", blobPath)
	}
}

func TestPutDeduplication(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	tmp := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmp, []byte("dedup content"), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}

	hash1, err := s.Put(tmp)
	if err != nil {
		t.Fatalf("first put: %v", err)
	}

	hash2, err := s.Put(tmp)
	if err != ErrExists {
		t.Fatalf("expected ErrExists, got %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("expected same hash, got %q vs %q", hash1, hash2)
	}
}

func TestRemove(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	tmp := filepath.Join(t.TempDir(), "test.txt")
	_ = os.WriteFile(tmp, []byte("remove me"), 0o644)

	hash, _ := s.Put(tmp)
	if !s.Exists(hash) {
		t.Fatal("expected blob to exist before remove")
	}

	if err := s.Remove(hash); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if s.Exists(hash) {
		t.Fatal("expected blob to not exist after remove")
	}
}
