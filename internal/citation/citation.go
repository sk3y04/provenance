// Package citation provides stable archive citation references and formatters.
package citation

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sk3y04/provenance/internal/archive"
)

type Reference struct {
	Collection string `json:"collection"`
	RevisionID string `json:"revision_id"`
	EntityID   string `json:"entity_id"`

	Title      string    `json:"title,omitempty"`
	URL        string    `json:"url,omitempty"`
	Author     string    `json:"author,omitempty"`
	CapturedAt time.Time `json:"captured_at,omitempty"`
	Sha256     string    `json:"sha256,omitempty"`
}

func Parse(s string) (*Reference, error) {
	if !strings.HasPrefix(s, "provenance://") {
		return nil, fmt.Errorf("invalid citation reference %q: expected provenance://<collection>@<revision>#<entity>", s)
	}
	rest := strings.TrimPrefix(s, "provenance://")
	atIdx := strings.LastIndex(rest, "@")
	if atIdx < 0 {
		return nil, fmt.Errorf("invalid citation reference %q: missing @ separator", s)
	}
	hashIdx := strings.Index(rest[atIdx+1:], "#")
	if hashIdx < 0 {
		return nil, fmt.Errorf("invalid citation reference %q: missing # separator", s)
	}
	return &Reference{
		Collection: rest[:atIdx],
		RevisionID: rest[atIdx+1 : atIdx+1+hashIdx],
		EntityID:   rest[atIdx+1+hashIdx+1:],
	}, nil
}

func Format(collection, revisionID, entityID string) string {
	return fmt.Sprintf("provenance://%s@%s#%s", collection, revisionID, entityID)
}

func (r *Reference) String() string {
	return Format(r.Collection, r.RevisionID, r.EntityID)
}

func (r *Reference) Markdown() string {
	title := r.Title
	if title == "" {
		title = r.URL
	}
	if title == "" {
		title = r.EntityID
	}
	return fmt.Sprintf("[%s](%s)", title, r.String())
}

func (r *Reference) Plain() string {
	if r.Title == "" {
		return r.String()
	}
	captured := ""
	if !r.CapturedAt.IsZero() {
		captured = fmt.Sprintf(", captured %s", r.CapturedAt.Format("2006-01-02"))
	}
	out := fmt.Sprintf("%s\n  %s%s\n  %s", r.Title, r.URL, captured, r.String())
	if r.Author != "" {
		out += fmt.Sprintf("\n  by %s", r.Author)
	}
	if r.Sha256 != "" {
		out += fmt.Sprintf("\n  SHA-256: %s", r.Sha256)
	}
	return out
}

func (r *Reference) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

func FromEntity(collection, revisionID string, ent *archive.Entity) *Reference {
	sha := ""
	if len(ent.Artifacts) > 0 {
		sha = ent.Artifacts[0]
	}
	return &Reference{
		Collection: collection,
		RevisionID: revisionID,
		EntityID:   ent.ExternalID,
		Title:      ent.Title,
		URL:        ent.URL,
		Author:     ent.Author,
		CapturedAt: ent.CapturedAt,
		Sha256:     sha,
	}
}

func Short(revisionID, entityID string) string {
	if len(revisionID) > 12 {
		revisionID = revisionID[:12]
	}
	return fmt.Sprintf("%s@%s", revisionID, entityID)
}
