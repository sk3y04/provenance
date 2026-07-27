package archive

import (
	"fmt"
)

type DiffResult struct {
	RevisionA string       `json:"revision_a"`
	RevisionB string       `json:"revision_b"`
	Added     []EntityDiff `json:"added"`
	Removed   []EntityDiff `json:"removed"`
	Changed   []EntityDiff `json:"changed"`
	Unchanged int          `json:"unchanged"`
}

type EntityDiff struct {
	ExternalID string      `json:"external_id"`
	Title      string      `json:"title,omitempty"`
	Changes    []FieldDiff `json:"changes,omitempty"`
}

type FieldDiff struct {
	Field  string `json:"field"`
	OldVal string `json:"old_val"`
	NewVal string `json:"new_val"`
}

func DiffRevisions(old, new *Revision) *DiffResult {
	result := &DiffResult{
		RevisionA: old.ID,
		RevisionB: new.ID,
	}

	oldMap := make(map[string]*Entity)
	for i := range old.Entities {
		oldMap[old.Entities[i].ExternalID] = &old.Entities[i]
	}
	newMap := make(map[string]*Entity)
	for i := range new.Entities {
		newMap[new.Entities[i].ExternalID] = &new.Entities[i]
	}

	for id, newEnt := range newMap {
		oldEnt, exists := oldMap[id]
		if !exists {
			result.Added = append(result.Added, EntityDiff{
				ExternalID: id,
				Title:      newEnt.Title,
			})
			continue
		}
		diffs := diffEntity(oldEnt, newEnt)
		if len(diffs) > 0 {
			result.Changed = append(result.Changed, EntityDiff{
				ExternalID: id,
				Title:      newEnt.Title,
				Changes:    diffs,
			})
		} else {
			result.Unchanged++
		}
	}

	for id, oldEnt := range oldMap {
		if _, exists := newMap[id]; !exists {
			result.Removed = append(result.Removed, EntityDiff{
				ExternalID: id,
				Title:      oldEnt.Title,
			})
		}
	}

	return result
}

func diffEntity(old, new *Entity) []FieldDiff {
	var diffs []FieldDiff
	if old.Title != new.Title {
		diffs = append(diffs, FieldDiff{"title", old.Title, new.Title})
	}
	if old.Author != new.Author {
		diffs = append(diffs, FieldDiff{"author", old.Author, new.Author})
	}
	if old.Kind != new.Kind {
		diffs = append(diffs, FieldDiff{"kind", old.Kind, new.Kind})
	}

	oldText := ""
	newText := ""
	if old.Text != nil {
		oldText = old.Text.Content
	}
	if new.Text != nil {
		newText = new.Text.Content
	}
	if oldText != newText {
		diffs = append(diffs, FieldDiff{"text", fmt.Sprintf("%d chars", len(oldText)), fmt.Sprintf("%d chars", len(newText))})
	}

	oldArts := artifactSet(old.Artifacts)
	newArts := artifactSet(new.Artifacts)
	if fmt.Sprint(oldArts) != fmt.Sprint(newArts) {
		diffs = append(diffs, FieldDiff{"artifacts", fmt.Sprintf("%v", oldArts), fmt.Sprintf("%v", newArts)})
	}

	return diffs
}

func artifactSet(hashes []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, h := range hashes {
		if seen[h] {
			continue
		}
		seen[h] = true
		short := h
		if len(h) > 12 {
			short = h[:12]
		}
		result = append(result, short)
	}
	return result
}
