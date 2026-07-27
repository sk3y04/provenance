package importers

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sk3y04/provenance/internal/archive"
	"github.com/sk3y04/provenance/internal/blobstore"
)

type openAPISpec struct {
	OpenAPI string `yaml:"openapi" json:"openapi"`
	Info    struct {
		Title       string `yaml:"title" json:"title"`
		Description string `yaml:"description" json:"description"`
		Version     string `yaml:"version" json:"version"`
	} `yaml:"info" json:"info"`
	Servers []struct {
		URL         string `yaml:"url" json:"url"`
		Description string `yaml:"description" json:"description"`
	} `yaml:"servers" json:"servers"`
	Paths map[string]interface{} `yaml:"paths" json:"paths"`
}

func ImportOpenAPI(vaultRoot, specPath, collectionName string) (*archive.Revision, error) {
	if _, err := os.Stat(specPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("spec not found: %s", specPath)
	}

	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}

	var spec openAPISpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse openapi spec: %w", err)
	}

	bs := blobstore.New(vaultRoot)
	rev := newRevision(specPath, "provenance-import-openapi", "1.0")

	blobHash, byteSize, err := storeFile(bs, specPath)
	if err != nil {
		return nil, fmt.Errorf("store spec blob: %w", err)
	}
	rev.Artifacts = append(rev.Artifacts, archive.Artifact{
		Sha256: blobHash,
		Path:   specPath,
		Size:   byteSize,
		Kind:   archive.ArtifactText,
	})

	title := spec.Info.Title
	if title == "" {
		title = specPath
	}

	entity := archive.Entity{
		ExternalID: title,
		URL:        specPath,
		Title:      title,
		Kind:       "api_spec",
		Extractor:  "openapi-importer",
		CapturedAt: rev.CapturedAt,
		Artifacts:  []string{blobHash},
	}

	if spec.Info.Description != "" {
		entity.Text = &archive.Document{
			ExternalID: title + "-description",
			Content:    spec.Info.Description,
			Format:     archive.DocMarkdown,
		}
	}

	for path, methods := range spec.Paths {
		methodsMap, ok := methods.(map[string]interface{})
		if !ok {
			continue
		}
		for method := range methodsMap {
			if method == "parameters" || method == "servers" || method == "summary" || method == "description" {
				continue
			}
			methodUpper := strings.ToUpper(method)
			endpointID := fmt.Sprintf("%s-%s", methodUpper, path)
			endpointDoc := archive.Document{
				ExternalID: endpointID,
				Content:    fmt.Sprintf("%s %s\n%s: %s", methodUpper, path, title, spec.Info.Version),
				Format:     archive.DocPlain,
			}
			entity.Documents = append(entity.Documents, endpointDoc)
		}
	}

	rev.Entities = append(rev.Entities, entity)

	if err := persistRevision(vaultRoot, rev); err != nil {
		return nil, fmt.Errorf("write revision: %w", err)
	}

	return rev, nil
}
