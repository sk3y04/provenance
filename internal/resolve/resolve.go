// Package resolve defines the shared result types that all extractors map into.
// These are used by scan --json and will later feed collection and archive pipelines.
package resolve

import (
	"encoding/json"
	"time"
)

type SourceKind string

const (
	KindFeed     SourceKind = "feed"
	KindSingle   SourceKind = "single"
	KindPlaylist SourceKind = "playlist"
)

type Source struct {
	URL          string     `json:"url"`
	CanonicalURL string     `json:"canonical_url"`
	Kind         SourceKind `json:"kind"`
	Extractor    string     `json:"extractor"`
	Title        string     `json:"title,omitempty"`
	Author       string     `json:"author,omitempty"`
	Items        []Item     `json:"items"`
}

type Item struct {
	ExternalID  string          `json:"external_id"`
	URL         string          `json:"url"`
	Title       string          `json:"title,omitempty"`
	Author      string          `json:"author,omitempty"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
	Media       []MediaAsset    `json:"media,omitempty"`
	Text        *TextContent    `json:"text,omitempty"`
	RawMetadata json.RawMessage `json:"raw_metadata,omitempty"`
}

type MediaAsset struct {
	URL       string    `json:"url"`
	Filename  string    `json:"filename,omitempty"`
	Extension string    `json:"extension,omitempty"`
	Size      int64     `json:"size,omitempty"`
	Kind      MediaKind `json:"kind,omitempty"`
}

type MediaKind string

const (
	MediaImage MediaKind = "image"
	MediaVideo MediaKind = "video"
	MediaAudio MediaKind = "audio"
)

type TextContent struct {
	Body   string     `json:"body"`
	Format TextFormat `json:"format"`
}

type TextFormat string

const (
	FormatPlain    TextFormat = "plain"
	FormatMarkdown TextFormat = "markdown"
	FormatHTML     TextFormat = "html"
)

func NewSource(url, canonicalURL string, kind SourceKind, extractor string) Source {
	if canonicalURL == "" {
		canonicalURL = url
	}
	return Source{
		URL:          url,
		CanonicalURL: canonicalURL,
		Kind:         kind,
		Extractor:    extractor,
	}
}

func NewItem(externalID, url string) Item {
	return Item{
		ExternalID: externalID,
		URL:        url,
	}
}

func NewMediaAsset(url string, kind MediaKind) MediaAsset {
	return MediaAsset{
		URL:  url,
		Kind: kind,
	}
}

func NewTextContent(body string, format TextFormat) *TextContent {
	return &TextContent{Body: body, Format: format}
}
