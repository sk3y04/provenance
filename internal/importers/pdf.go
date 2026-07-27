package importers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ledongthuc/pdf"

	"github.com/sk3y04/provenance/internal/archive"
	"github.com/sk3y04/provenance/internal/blobstore"
)

func ImportPDF(vaultRoot, pdfPath, collectionName string) (*archive.Revision, error) {
	bs := blobstore.New(vaultRoot)

	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("pdf not found: %s", pdfPath)
	}

	rev := newRevision(pdfPath, "provenance-import-pdf", "1.0")

	blobHash, byteSize, err := storeFile(bs, pdfPath)
	if err != nil {
		return nil, fmt.Errorf("store pdf blob: %w", err)
	}

	rev.Artifacts = append(rev.Artifacts, archive.Artifact{
		Sha256: blobHash,
		Path:   filepath.Base(pdfPath),
		Size:   byteSize,
		Kind:   archive.ArtifactBinary,
	})

	pdfFile, reader, err := pdf.Open(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("open pdf: %w", err)
	}
	defer func() { _ = pdfFile.Close() }()

	totalPage := reader.NumPage()
	entity := archive.Entity{
		ExternalID: filepath.Base(pdfPath),
		URL:        pdfPath,
		Title:      filepath.Base(pdfPath),
		Kind:       "pdf",
		Extractor:  "pdf-importer",
		CapturedAt: rev.CapturedAt,
		Artifacts:  []string{blobHash},
	}

	for pageNum := 1; pageNum <= totalPage; pageNum++ {
		page := reader.Page(pageNum)
		text, err := page.GetPlainText(nil)
		if err != nil || text == "" {
			continue
		}

		pageID := fmt.Sprintf("%s:p%d", filepath.Base(pdfPath), pageNum)
		entity.Documents = append(entity.Documents, archive.Document{
			ExternalID: pageID,
			Content:    text,
			Format:     archive.DocPlain,
		})

		entity.Relations = append(entity.Relations, archive.Relation{
			From: filepath.Base(pdfPath),
			To:   pageID,
			Kind: archive.RelContains,
		})
	}

	rev.Entities = append(rev.Entities, entity)

	if err := persistRevision(vaultRoot, rev); err != nil {
		return nil, fmt.Errorf("write revision: %w", err)
	}

	return rev, nil
}
