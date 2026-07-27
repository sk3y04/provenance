package importers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sk3y04/provenance/internal/archive"
)

func ImportGit(vaultRoot, repoURL, ref, collectionName string) (*archive.Revision, error) {
	tmpDir, err := os.MkdirTemp("", "provenance-git-")
	if err != nil {
		return nil, fmt.Errorf("mkdir temp: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cloneCmd := exec.Command("git", "clone", "--depth", "1", "--branch", ref, repoURL, tmpDir)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("clone %s: %w\n%s", repoURL, err, string(out))
	}

	commitCmd := exec.Command("git", "-C", tmpDir, "rev-parse", "HEAD")
	commitOut, err := commitCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("get commit: %w", err)
	}
	commitSHA := strings.TrimSpace(string(commitOut))

	rev := newRevision(repoURL, "provenance-import-git", "1.0")
	rev.Source.Reference = ref + "@" + commitSHA

	entity := archive.Entity{
		ExternalID:  commitSHA,
		URL:         repoURL,
		Title:       fmt.Sprintf("%s @ %s", repoURL, ref),
		Kind:        "git_repo",
		Extractor:   "git-importer",
		CapturedAt:  rev.CapturedAt,
		RawMetadata: nil,
	}

	docFiles := []string{".md", ".adoc", ".txt", ".rst", ".markdown"}
	err = filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		if filepath.Dir(path) == filepath.Join(tmpDir, ".git") {
			return nil
		}
		for _, ext := range docFiles {
			if strings.HasSuffix(strings.ToLower(info.Name()), ext) || strings.EqualFold(info.Name(), "readme") || strings.EqualFold(info.Name(), "license") || strings.EqualFold(info.Name(), "changelog") {
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				rel, _ := filepath.Rel(tmpDir, path)
				content := string(data)
				if len(content) > 50000 {
					content = safeTruncate(content, 50000)
				}
				entity.Documents = append(entity.Documents, archive.Document{
					ExternalID: rel,
					Content:    content,
					Format:     archive.DocMarkdown,
				})
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk repo: %w", err)
	}

	rev.Entities = append(rev.Entities, entity)

	if err := persistRevision(vaultRoot, rev); err != nil {
		return nil, fmt.Errorf("write revision: %w", err)
	}

	return rev, nil
}
