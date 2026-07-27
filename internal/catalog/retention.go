package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/sk3y04/provenance/internal/archive"
)

func PruneRevisions(vaultRoot, collectionName string, keep int) ([]string, error) {
	revs, err := ListRevisions(vaultRoot)
	if err != nil {
		return nil, err
	}

	type revWithTime struct {
		id string
		at time.Time
	}

	var matching []revWithTime
	for _, id := range revs {
		rev, err := archive.ReadRevision(vaultRoot, id)
		if err != nil {
			continue
		}
		if rev.Source.Kind == archive.SourceCollection && rev.Source.Reference == collectionName {
			matching = append(matching, revWithTime{id: id, at: rev.CapturedAt})
		}
	}

	sort.Slice(matching, func(i, j int) bool { return matching[i].at.Before(matching[j].at) })

	if len(matching) <= keep {
		return nil, nil
	}

	var removed []string
	for i := 0; i < len(matching)-keep; i++ {
		revDir := filepath.Join(vaultRoot, "revisions", matching[i].id)
		if err := os.RemoveAll(revDir); err != nil {
			return removed, fmt.Errorf("remove revision %s: %w", matching[i].id, err)
		}
		removed = append(removed, matching[i].id)
	}

	return removed, nil
}
