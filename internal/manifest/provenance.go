package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const ProvenanceDir = ".provenance"

func CollectionPath(outputDir string) string {
	return filepath.Join(outputDir, ProvenanceDir, "collection.json")
}

func RunPath(outputDir string) string {
	ts := time.Now().UTC().Format("2006-01-02T150405Z")
	return filepath.Join(outputDir, ProvenanceDir, "runs", ts+".json")
}

func ItemPath(outputDir, externalID string) string {
	name := sanitizeProvenanceName(externalID) + ".json"
	return filepath.Join(outputDir, ProvenanceDir, "items", name)
}

func WriteCaptureManifest(dir, filePath string, cm *CaptureManifest) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("mkdir provenance: %w", err)
	}
	return writeJSON(filePath, cm)
}

func WriteCaptureItem(dir, externalID string, item *CaptureItem) error {
	filePath := ItemPath(dir, externalID)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("mkdir provenance items: %w", err)
	}
	return writeJSON(filePath, item)
}

func WriteCollectionConfig(dir string, data []byte) error {
	filePath := CollectionPath(dir)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("mkdir provenance: %w", err)
	}
	return writeRaw(filePath, data)
}

func ReadCaptureManifest(filePath string) (*CaptureManifest, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var cm CaptureManifest
	if err := json.Unmarshal(data, &cm); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}
	return &cm, nil
}

func ListRunManifests(outputDir string) ([]string, error) {
	runsDir := filepath.Join(outputDir, ProvenanceDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			paths = append(paths, filepath.Join(runsDir, e.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func VerifyCaptureDir(outputDir string) ([]VerificationResult, error) {
	itemsDir := filepath.Join(outputDir, ProvenanceDir, "items")
	entries, err := os.ReadDir(itemsDir)
	if err != nil {
		return nil, fmt.Errorf("read items dir: %w", err)
	}
	var results []VerificationResult
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		itemPath := filepath.Join(itemsDir, e.Name())
		data, err := os.ReadFile(itemPath)
		if err != nil {
			results = append(results, VerificationResult{
				Path:    itemPath,
				Missing: true,
			})
			continue
		}
		var item CaptureItem
		if err := json.Unmarshal(data, &item); err != nil {
			continue
		}
		if item.DownloadedPath == "" || item.Sha256 == "" {
			continue
		}
		fullPath := item.DownloadedPath
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(outputDir, fullPath)
		}
		fi, err := os.Stat(fullPath)
		if err != nil {
			results = append(results, VerificationResult{
				Path:     fullPath,
				Expected: item.Sha256,
				Missing:  true,
			})
			continue
		}
		actual, err := FileSha256(fullPath)
		if err != nil {
			continue
		}
		results = append(results, VerificationResult{
			Path:     fullPath,
			Expected: item.Sha256,
			Actual:   actual,
			OK:       item.Sha256 == actual,
			ByteSize: fi.Size(),
		})
	}
	return results, nil
}

func FileSha256(path string) (string, error) {
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

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return writeRaw(path, data)
}

func writeRaw(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return os.Rename(tmp, path)
}

func sanitizeProvenanceName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			return '_'
		}
		return r
	}, s)
	if s == "" {
		s = "unknown"
	}
	return s
}
