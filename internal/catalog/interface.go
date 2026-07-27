package catalog

import (
	"context"
	"time"

	"github.com/sk3y04/provenance/internal/archive"
)

type CatalogStore interface {
	Init(ctx context.Context) error
	Close() error

	CollectionStore
	RevisionStore
	EntityStore
	SearchStore
}

type CollectionStore interface {
	UpsertCollection(ctx context.Context, col archive.ArchiveCollection) error
	GetCollection(ctx context.Context, name string) (*archive.ArchiveCollection, error)
	ListCollections(ctx context.Context) ([]archive.ArchiveCollection, error)
}

type RevisionStore interface {
	InsertRevision(ctx context.Context, rev *archive.Revision, collectionName string) error
	GetRevision(ctx context.Context, id string) (*archive.Revision, error)
	ListRevisions(ctx context.Context, collectionName string) ([]archive.Revision, error)
}

type EntityStore interface {
	GetEntity(ctx context.Context, revisionID, externalID string) (*archive.Entity, error)
}

type SearchOptions struct {
	CollectionName string
	Kind           string
	Limit          int
	Offset         int
}

type SearchHit struct {
	Title          string    `json:"title"`
	Headline       string    `json:"headline"`
	URL            string    `json:"url"`
	CollectionName string    `json:"collection_name"`
	RevisionID     string    `json:"revision_id"`
	EntityID       string    `json:"entity_id"`
	CapturedAt     time.Time `json:"captured_at"`
	Author         string    `json:"author,omitempty"`
	Kind           string    `json:"kind,omitempty"`
	Rank           float64   `json:"rank"`
}

type SearchResult struct {
	Total int         `json:"total"`
	Hits  []SearchHit `json:"hits"`
}

type SearchStore interface {
	Search(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error)
}

var defaultStore CatalogStore

func SetStore(s CatalogStore) {
	defaultStore = s
}

func Store() CatalogStore {
	return defaultStore
}

func HasStore() bool {
	return defaultStore != nil
}
