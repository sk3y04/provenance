package catalog

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sk3y04/provenance/internal/blobstore"
)

func GarbageCollect(vaultRoot string, dryRun bool) ([]string, error) {
	bs := blobstore.New(vaultRoot)
	diskBlobs, err := bs.List()
	if err != nil {
		return nil, fmt.Errorf("list blobs: %w", err)
	}

	diskSet := make(map[string]bool)
	for _, h := range diskBlobs {
		diskSet[h] = true
	}

	refBlobs, err := referencedBlobsFromFilesystem(vaultRoot)
	if err != nil {
		return nil, fmt.Errorf("list referenced: %w", err)
	}

	var orphans []string
	for h := range diskSet {
		if !refBlobs[h] {
			orphans = append(orphans, h)
		}
	}

	if dryRun {
		return orphans, nil
	}

	for _, h := range orphans {
		_ = bs.Remove(h)
	}

	return orphans, nil
}

func referencedBlobsFromFilesystem(vaultRoot string) (map[string]bool, error) {
	ref := make(map[string]bool)
	revDir := filepath.Join(vaultRoot, "revisions")
	entries, err := os.ReadDir(revDir)
	if os.IsNotExist(err) {
		return ref, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(revDir, entry.Name(), "manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		for _, line := range extractSHA256s(data) {
			ref[line] = true
		}
	}
	return ref, nil
}

func extractSHA256s(data []byte) []string {
	var result []string
	str := string(data)
	for i := 0; i < len(str)-64; i++ {
		candidate := str[i : i+64]
		if isHex(candidate) {
			result = append(result, candidate)
			i += 63
		}
	}
	return result
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
