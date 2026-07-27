// Package config holds the serializable download configuration shared across
// the dispatcher, session, watch, and history packages.
package config

import "github.com/sk3y04/provenance/internal/manifest"

type Config struct {
	OutputDir          string                 `json:"output_dir"`
	CookiesFile        string                 `json:"cookies_file,omitempty"`
	Concurrency        int                    `json:"concurrency"`
	Quality            string                 `json:"quality"`
	AudioOnly          bool                   `json:"audio_only"`
	NoArchive          bool                   `json:"no_archive"`
	Filter             manifest.FilterOptions `json:"filter,omitempty"`
	PostLimit          int                    `json:"post_limit,omitempty"`
	IncludePosts       bool                   `json:"include_posts,omitempty"`
	IncludeComments    bool                   `json:"include_comments,omitempty"`
	CommentLimit       int                    `json:"comment_limit,omitempty"`
	CookiesFromBrowser string                 `json:"cookies_from_browser,omitempty"`
	OutputLayout       string                 `json:"output_layout,omitempty"`
	OutputTemplate     string                 `json:"output_template,omitempty"`
	SpeedLimit         int64                  `json:"speed_limit,omitempty"`
	ChromePath         string                 `json:"chrome_path,omitempty"`
}

func CaptureOptionsFromConfig(cfg Config) manifest.CaptureOptions {
	return manifest.CaptureOptions{
		OutputDir:          cfg.OutputDir,
		Concurrency:        cfg.Concurrency,
		Quality:            cfg.Quality,
		AudioOnly:          cfg.AudioOnly,
		IncludePosts:       cfg.IncludePosts,
		CookiesFile:        cfg.CookiesFile,
		CookiesFromBrowser: cfg.CookiesFromBrowser,
		OutputLayout:       cfg.OutputLayout,
		OutputTemplate:     cfg.OutputTemplate,
		Limit:              cfg.PostLimit,
		IncludeComments:    cfg.IncludeComments,
		CommentLimit:       cfg.CommentLimit,
	}
}
