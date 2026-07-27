package manifest

import (
	"encoding/json"
	"time"
)

const CaptureFormatVersion = "provenance/capture+1"

type CaptureOptions struct {
	OutputDir          string `json:"output_dir"`
	Concurrency        int    `json:"concurrency"`
	Quality            string `json:"quality,omitempty"`
	AudioOnly          bool   `json:"audio_only,omitempty"`
	IncludePosts       bool   `json:"include_posts,omitempty"`
	CookiesFile        string `json:"cookies_file,omitempty"`
	CookiesFromBrowser string `json:"cookies_from_browser,omitempty"`
	OutputLayout       string `json:"output_layout,omitempty"`
	OutputTemplate     string `json:"output_template,omitempty"`
	Limit              int    `json:"limit,omitempty"`
	IncludeComments    bool   `json:"include_comments,omitempty"`
	CommentLimit       int    `json:"comment_limit,omitempty"`
}

type CaptureManifest struct {
	Format       string         `json:"format"`
	SourceURL    string         `json:"source_url"`
	Site         string         `json:"site"`
	DownloadedAt time.Time      `json:"downloaded_at"`
	OutputDir    string         `json:"output_dir"`
	Tool         string         `json:"tool"`
	ToolVersion  string         `json:"tool_version"`
	Options      CaptureOptions `json:"options"`
	Items        []CaptureItem  `json:"items"`
}

type CaptureItem struct {
	ExternalID     string          `json:"external_id"`
	URL            string          `json:"url"`
	CanonicalURL   string          `json:"canonical_url,omitempty"`
	Title          string          `json:"title,omitempty"`
	Author         string          `json:"author,omitempty"`
	PublishedAt    *time.Time      `json:"published_at,omitempty"`
	Kind           string          `json:"kind,omitempty"`
	Extractor      string          `json:"extractor"`
	DownloadedPath string          `json:"downloaded_path"`
	ByteSize       int64           `json:"byte_size"`
	Sha256         string          `json:"sha256"`
	CapturedAt     time.Time       `json:"captured_at"`
	RawMetadata    json.RawMessage `json:"raw_metadata,omitempty"`
	Text           *TextCapture    `json:"text,omitempty"`
	SessionName    string          `json:"session_name,omitempty"`
	Status         string          `json:"status"`
	Error          string          `json:"error,omitempty"`
}

type TextCapture struct {
	Body   string `json:"body"`
	Format string `json:"format"`
}

type VerificationResult struct {
	Path     string `json:"path"`
	Expected string `json:"expected_sha256"`
	Actual   string `json:"actual_sha256"`
	OK       bool   `json:"ok"`
	Missing  bool   `json:"missing,omitempty"`
	ByteSize int64  `json:"byte_size,omitempty"`
}
