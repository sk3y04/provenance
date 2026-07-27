// Package importers provides technical knowledge importers for PDFs, Git repos,
// static documentation sites, and OpenAPI specifications into the archive vault.
package importers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/sk3y04/provenance/internal/archive"
	"github.com/sk3y04/provenance/internal/blobstore"
)

func newRevision(srcURL string, tool, toolVersion string) *archive.Revision {
	return &archive.Revision{
		CapturedAt: time.Now().UTC(),
		Source: archive.Source{
			URL:       srcURL,
			Kind:      archive.SourceImport,
			Reference: srcURL,
		},
		Tool:        tool,
		ToolVersion: toolVersion,
	}
}

func storeFile(bs *blobstore.Store, path string) (string, int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("stat: %w", err)
	}
	hash, err := bs.Put(path)
	if err != nil && err != blobstore.ErrExists {
		return "", 0, fmt.Errorf("blob put: %w", err)
	}
	return hash, fi.Size(), nil
}

func persistRevision(vaultRoot string, rev *archive.Revision) error {
	revID, err := computeID(rev)
	if err != nil {
		return fmt.Errorf("compute revision id: %w", err)
	}
	rev.ID = revID
	return archive.WriteRevision(vaultRoot, rev)
}

func computeID(rev *archive.Revision) (string, error) {
	data, err := json.Marshal(rev)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

func safeTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	for i := maxLen; i > maxLen-4 && i > 0; i-- {
		if s[i]&0xC0 != 0x80 {
			return s[:i]
		}
	}
	return s[:maxLen]
}
