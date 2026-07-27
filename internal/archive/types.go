// Package archive provides the domain model for the opt-in durable vault.
// It defines ArchiveCollection, Source, Revision, Entity, Artifact, Document,
// and Relation types, plus ingest logic that consumes capture manifests
// and stores files in a content-addressed blob store.
package archive

import (
	"encoding/json"
	"time"
)

type ArchiveCollection struct {
	Name        string    `json:"name"`
	VaultRoot   string    `json:"vault_root"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SourceKind string

const (
	SourceURL        SourceKind = "url"
	SourceCollection SourceKind = "collection"
	SourceSession    SourceKind = "session"
	SourceImport     SourceKind = "import"
)

type Source struct {
	URL       string     `json:"url"`
	Kind      SourceKind `json:"kind"`
	Reference string     `json:"reference"`
}

type ArtifactKind string

const (
	ArtifactImage  ArtifactKind = "image"
	ArtifactVideo  ArtifactKind = "video"
	ArtifactAudio  ArtifactKind = "audio"
	ArtifactText   ArtifactKind = "text"
	ArtifactBinary ArtifactKind = "binary"
	ArtifactHTML   ArtifactKind = "html"
)

type Artifact struct {
	Sha256   string       `json:"sha256"`
	Path     string       `json:"path"`
	Size     int64        `json:"size"`
	MimeType string       `json:"mime_type,omitempty"`
	Kind     ArtifactKind `json:"kind"`
}

type DocumentFormat string

const (
	DocPlain    DocumentFormat = "plain"
	DocMarkdown DocumentFormat = "markdown"
	DocHTML     DocumentFormat = "html"
)

type Document struct {
	ExternalID string         `json:"external_id"`
	Content    string         `json:"content"`
	Format     DocumentFormat `json:"format"`
}

type RelationKind string

const (
	RelContains    RelationKind = "contains"
	RelAttachedTo  RelationKind = "attached_to"
	RelReplyTo     RelationKind = "reply_to"
	RelDerivedFrom RelationKind = "derived_from"
	RelBelongsTo   RelationKind = "belongs_to"
)

type Relation struct {
	From string       `json:"from"`
	To   string       `json:"to"`
	Kind RelationKind `json:"kind"`
}

type Entity struct {
	ExternalID   string          `json:"external_id"`
	URL          string          `json:"url"`
	CanonicalURL string          `json:"canonical_url,omitempty"`
	Title        string          `json:"title,omitempty"`
	Author       string          `json:"author,omitempty"`
	PublishedAt  *time.Time      `json:"published_at,omitempty"`
	CapturedAt   time.Time       `json:"captured_at"`
	Kind         string          `json:"kind,omitempty"`
	Extractor    string          `json:"extractor,omitempty"`
	Text         *Document       `json:"text,omitempty"`
	Artifacts    []string        `json:"artifacts"`
	Documents    []Document      `json:"documents,omitempty"`
	Relations    []Relation      `json:"relations,omitempty"`
	RawMetadata  json.RawMessage `json:"raw_metadata,omitempty"`
}

type Revision struct {
	ID          string     `json:"id"`
	CapturedAt  time.Time  `json:"captured_at"`
	Source      Source     `json:"source"`
	Tool        string     `json:"tool"`
	ToolVersion string     `json:"tool_version"`
	Entities    []Entity   `json:"entities"`
	Artifacts   []Artifact `json:"artifacts"`
	Documents   []Document `json:"documents"`
	Relations   []Relation `json:"relations"`
}
