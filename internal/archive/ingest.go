package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sk3y04/provenance/internal/blobstore"
	"github.com/sk3y04/provenance/internal/manifest"
)

type IngestOptions struct {
	VaultRoot      string
	CollectionName string
	Source         Source
	Tool           string
	ToolVersion    string
}

func IngestCaptureManifest(bs *blobstore.Store, opts IngestOptions, cm *manifest.CaptureManifest) (*Revision, error) {
	outputDir := cm.OutputDir
	rev := &Revision{
		CapturedAt:  time.Now().UTC(),
		Source:      opts.Source,
		Tool:        opts.Tool,
		ToolVersion: opts.ToolVersion,
	}

	artMap := make(map[string]Artifact)
	for _, item := range cm.Items {
		if item.Sha256 == "" || item.DownloadedPath == "" {
			continue
		}

		fullPath := item.DownloadedPath
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(outputDir, fullPath)
		}

		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			continue
		}

		actual, err := manifest.FileSha256(fullPath)
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", fullPath, err)
		}
		if !strings.EqualFold(actual, item.Sha256) {
			return nil, fmt.Errorf("hash mismatch for %s: recorded %s, actual %s", fullPath, item.Sha256, actual)
		}

		blobHash, err := bs.Put(fullPath)
		if err != nil && err != blobstore.ErrExists {
			return nil, fmt.Errorf("store blob %s: %w", fullPath, err)
		}

		if _, exists := artMap[blobHash]; !exists {
			artMap[blobHash] = Artifact{
				Sha256: blobHash,
				Path:   filepath.Base(item.DownloadedPath),
				Size:   item.ByteSize,
				Kind:   artifactKindFromItem(item.Kind, item.Extractor),
			}
		}

		extID := item.ExternalID
		if extID == "" {
			extID = item.URL
		}

		ent := Entity{
			ExternalID:   extID,
			URL:          item.URL,
			CanonicalURL: item.CanonicalURL,
			Title:        item.Title,
			Author:       item.Author,
			PublishedAt:  item.PublishedAt,
			CapturedAt:   item.CapturedAt,
			Kind:         item.Kind,
			Extractor:    item.Extractor,
			Artifacts:    []string{blobHash},
			RawMetadata:  item.RawMetadata,
		}

		if item.Text != nil && item.Text.Body != "" {
			ent.Text = &Document{
				ExternalID: extID,
				Content:    item.Text.Body,
				Format:     DocumentFormat(item.Text.Format),
			}
		}

		rev.Entities = append(rev.Entities, ent)
	}

	for _, a := range artMap {
		rev.Artifacts = append(rev.Artifacts, a)
	}
	sort.Slice(rev.Artifacts, func(i, j int) bool { return rev.Artifacts[i].Sha256 < rev.Artifacts[j].Sha256 })
	sort.Slice(rev.Entities, func(i, j int) bool { return rev.Entities[i].ExternalID < rev.Entities[j].ExternalID })
	sort.Slice(rev.Documents, func(i, j int) bool { return rev.Documents[i].ExternalID < rev.Documents[j].ExternalID })
	sort.Slice(rev.Relations, func(i, j int) bool {
		return rev.Relations[i].From+rev.Relations[i].To < rev.Relations[j].From+rev.Relations[j].To
	})

	revID, err := computeRevisionID(rev)
	if err != nil {
		return nil, fmt.Errorf("compute revision id: %w", err)
	}
	rev.ID = revID

	return rev, nil
}

func WriteRevision(vaultRoot string, rev *Revision) error {
	dir := filepath.Join(vaultRoot, "revisions", rev.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir revision: %w", err)
	}

	if err := writeJSON(filepath.Join(dir, "manifest.json"), rev); err != nil {
		return err
	}
	if err := writeJSONLines(filepath.Join(dir, "entities.jsonl"), rev.Entities); err != nil {
		return err
	}
	if err := writeJSONLines(filepath.Join(dir, "artifacts.jsonl"), rev.Artifacts); err != nil {
		return err
	}
	if err := writeJSONLines(filepath.Join(dir, "documents.jsonl"), rev.Documents); err != nil {
		return err
	}
	if err := writeJSONLines(filepath.Join(dir, "relations.jsonl"), rev.Relations); err != nil {
		return err
	}

	return nil
}

func ReadRevision(vaultRoot, id string) (*Revision, error) {
	dir := filepath.Join(vaultRoot, "revisions", id)
	path := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read revision %s: %w", id, err)
	}
	var rev Revision
	if err := json.Unmarshal(data, &rev); err != nil {
		return nil, fmt.Errorf("unmarshal revision %s: %w", id, err)
	}
	return &rev, nil
}

func computeRevisionID(rev *Revision) (string, error) {
	data, err := json.Marshal(rev)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

func artifactKindFromItem(kind, extractor string) ArtifactKind {
	k := strings.ToLower(kind)
	switch {
	case strings.Contains(k, "video") || strings.Contains(k, "animated_gif"):
		return ArtifactVideo
	case strings.Contains(k, "image") || strings.Contains(k, "photo") || strings.Contains(k, "gallery"):
		return ArtifactImage
	case strings.Contains(k, "audio") || strings.Contains(k, "mp3"):
		return ArtifactAudio
	case strings.Contains(k, "post") || strings.Contains(k, "text") || strings.Contains(k, "markdown"):
		return ArtifactText
	default:
		return ArtifactBinary
	}
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return os.Rename(tmp, path)
}

func writeJSONLines(path string, items interface{}) error {
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	var lines []string
	for _, item := range arr {
		lines = append(lines, string(item))
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return fmt.Errorf("write jsonl: %w", err)
	}
	return os.Rename(tmp, path)
}
