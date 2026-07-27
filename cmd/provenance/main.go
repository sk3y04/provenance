package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sk3y04/provenance/internal/app"
	"github.com/sk3y04/provenance/internal/archive"
	"github.com/sk3y04/provenance/internal/blobstore"
	"github.com/sk3y04/provenance/internal/catalog"
	"github.com/sk3y04/provenance/internal/citation"
	"github.com/sk3y04/provenance/internal/collection"
	"github.com/sk3y04/provenance/internal/config"
	"github.com/sk3y04/provenance/internal/diagnose"
	"github.com/sk3y04/provenance/internal/dispatcher"
	"github.com/sk3y04/provenance/internal/extractor"
	"github.com/sk3y04/provenance/internal/importers"
	"github.com/sk3y04/provenance/internal/manifest"
	"github.com/sk3y04/provenance/internal/ratelimit"
	"github.com/sk3y04/provenance/internal/session"
	"github.com/sk3y04/provenance/internal/tui"
	"github.com/sk3y04/provenance/internal/watch"
)

var (
	rateLimiter     *ratelimit.Manager
	flagOutput      string
	flagCookies     string
	flagConcurrency int
	flagQuality     string
	flagAudioOnly   bool
	flagDryRun      bool
	flagBatch       string
	flagNoArchive   bool
	flagSession     string
	flagRecord      bool

	flagIncludeExt         string
	flagExcludeExt         string
	flagMinSize            string
	flagMaxSize            string
	flagTitleMatch         string
	flagTitleExclude       string
	flagLimit              int
	flagCookiesFromBrowser string
	flagLayout             string
	flagOutputTemplate     string
	flagSpeedLimit         string
	flagIncludePosts       bool
	flagIncludeComments    bool
	flagMaxComments        int
	flagJSON               bool
	flagSave               string
	flagArchiveCollection  string
	flagVaultRoot          string
	flagCiteFormat         string
	flagCiteEntity         string
	flagSearchCollection   string
	flagSearchKind         string
	flagImportRef          string
	flagImportScope        string
	flagMaxPages           int
	flagScreenshot         bool
	flagEvery              string
	flagKeep               int
	flagChromePath         string
)

func main() {
	rateLimiter = ratelimit.New()

	root := &cobra.Command{
		Use:     "provenance",
		Version: "0.7.0",
		Short:   "provenance - universal media collector, source archive, and knowledge vault",
		Long: `provenance is a single-binary tool for collecting, preserving, searching, and citing
digital content — media, social posts, documentation, Git repos, and more.

It has four progressive levels:

  grab    — instant media download from any URL (video, image, audio, post)
  collect — named, repeatable sync for playlists, subreddits, profiles
  archive — immutable, content-addressed vault with SHA-256 verification
  import  — bring PDFs, Git repos, docs sites, and OpenAPI specs into the vault

Combined with full-text search (PostgreSQL), revision diffs, citations,
retention policies, garbage collection, and a Bubble Tea terminal UI.

Interactive TUI - run 'provenance tui' for a full-screen interface:

  • Session browser - resume, retry-failed, or delete sessions
  • Watch subscriptions - manage recurring downloads
  • New grab form - paste a URL, see a live scan preview
  • Scan & pick - browse items, multi-select, download subset
  • Live runner - per-URL counters, progress bars, throughput & ETA
  • Archive search - full-text search across vaulted content
  • History - recent runs with quick rerun and reveal-in-finder

CLI mode - fast one-shot commands for scripts and automation:

  provenance grab       <URL>        Download media from any source
  provenance collect    add|sync     Named repeatable source sync
  provenance archive    url|import   Preserve in content-addressed vault
  provenance vault      init|show    Manage the vault and PostgreSQL
  provenance search     <QUERY>      Full-text search archived content
  provenance manifest   show|verify  Capture manifests with SHA-256
  provenance scan       <URL>        Preview items without downloading
  provenance status     <SESSION>    Show session progress
  provenance resume     <SESSION>    Resume pending + failed URLs
  provenance retry-failed <SESSION>  Retry only failed URLs
  provenance sessions   list|export  Manage download sessions
  provenance watch      add|run      Recurring download subscriptions
  provenance install                 Pre-install yt-dlp + ffmpeg
  provenance tui                     Interactive terminal UI
  provenance completion              Generate shell completions

Powered by yt-dlp (1000+ sites), native clients for Twitter/X, Reddit,
Instagram, headless Chrome fallback (chromedp), and PostgreSQL full-text
search. No runtime dependencies beyond yt-dlp + ffmpeg.`,
	}

	root.AddCommand(downloadCmd())
	root.AddCommand(scanCmd())
	root.AddCommand(installCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(resumeCmd())
	root.AddCommand(retryFailedCmd())
	root.AddCommand(sessionsCmd())
	root.AddCommand(watchCmd())
	root.AddCommand(collectCmd())
	root.AddCommand(manifestCmd())
	root.AddCommand(archiveCmd())
	root.AddCommand(vaultCmd())
	root.AddCommand(searchCmd())
	root.AddCommand(tuiCmd())
	root.AddCommand(completionCmd())

	root.PersistentFlags().StringVar(&flagChromePath, "chrome-path", "", "Path to Chrome/Chromium executable (for browser fallback and import-web)")

	// Always make sure yt-dlp + ffmpeg are installed before any subcommand runs.
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if shouldSkipAutoInstall(cmd) {
			return nil
		}
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		// Best effort, silent.
		_ = extractor.EnsureInstalled(ctx)
		return nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if hint := diagnose.Hint(err); hint != "" {
			fmt.Fprintln(os.Stderr, "hint:", hint)
		}
		os.Exit(1)
	}
}

func shouldSkipAutoInstall(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "help", "install", "status", "sessions", "tui", "completion", "manifest", "search":
		return true
	}
	path := cmd.CommandPath()
	if strings.HasPrefix(path, "provenance sessions") {
		return true
	}
	if strings.HasPrefix(path, "provenance vault") && cmd.Name() != "init" {
		return true
	}
	switch path {
	case "provenance watch", "provenance watch add", "provenance watch list", "provenance watch remove":
		return true
	case "provenance collect list", "provenance collect show", "provenance collect remove":
		return true
	}
	return false
}

func downloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "grab [URL...]",
		Aliases: []string{"download"},
		Short:   "Download media from one or more URLs or --batch file",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			opts, err := buildOptions(flagDryRun)
			if err != nil {
				return err
			}
			if flagSession != "" {
				return runSessionDownload(ctx, flagSession, args, flagBatch, opts)
			}
			return app.Download(ctx, args, flagBatch, opts)
		},
	}
	addDownloadFlags(cmd)
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Print what would be downloaded without downloading")
	cmd.Flags().BoolVar(&flagNoArchive, "no-archive", false, "Disable archives entirely (re-attempt every URL and ignore yt-dlp download-archive)")
	cmd.Flags().StringVar(&flagSession, "session", "", "Save progress under a named resumable session")
	return cmd
}

func scanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan [URL...]",
		Short: "Preview downloadable items without downloading media",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagBatch == "" && len(args) == 0 {
				return fmt.Errorf("provide at least one URL or use --batch <file>")
			}
			opts, err := buildOptions(false)
			if err != nil {
				return err
			}
			urls := append([]string(nil), args...)
			if flagBatch != "" {
				batchURLs, err := readBatchURLs(flagBatch)
				if err != nil {
					return err
				}
				urls = append(urls, batchURLs...)
			}
			if flagJSON {
				all, err := app.ScanResolved(cmd.Context(), urls, opts)
				if err != nil {
					return err
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(all)
			}
			if flagSave != "" {
				all, err := app.Scan(cmd.Context(), urls, opts)
				if err != nil {
					return err
				}
				if err := saveScan(flagSave, all); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "[provenance] scan saved to %s\n", flagSave)
				return nil
			}
			all, err := app.Scan(cmd.Context(), urls, opts)
			if err != nil {
				return err
			}
			for i, m := range all {
				if i > 0 {
					fmt.Println()
				}
				manifest.PrintHuman(os.Stdout, m)
			}
			return nil
		},
	}
	addDownloadFlags(cmd)
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Print scan manifest as JSON")
	cmd.Flags().StringVar(&flagSave, "save", "", "Save scan manifest JSON to a file")
	return cmd
}

func addDownloadFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&flagOutput, "output", "o", "./downloads", "Output directory")
	cmd.Flags().StringVarP(&flagCookies, "cookies", "c", "", "Path to Netscape cookies.txt")
	cmd.Flags().StringVar(&flagCookiesFromBrowser, "cookies-from-browser", "", "Use yt-dlp browser cookies: chrome, edge, firefox, brave, etc.")
	cmd.Flags().IntVar(&flagConcurrency, "concurrency", 4, "Max parallel downloads")
	cmd.Flags().StringVar(&flagQuality, "quality", "best", "Video quality: best, 1080, 720, 480")
	cmd.Flags().BoolVar(&flagAudioOnly, "audio-only", false, "Extract audio only (mp3 320kbps)")
	cmd.Flags().StringVar(&flagBatch, "batch", "", "Path to a text file with one URL per line (# comments allowed)")
	cmd.Flags().StringVar(&flagIncludeExt, "include", "", "Comma-separated extensions to include, e.g. mp4,jpg,zip")
	cmd.Flags().StringVar(&flagExcludeExt, "exclude", "", "Comma-separated extensions to exclude, e.g. psd,zip")
	cmd.Flags().StringVar(&flagMinSize, "min-size", "", "Minimum known file size, e.g. 10MB")
	cmd.Flags().StringVar(&flagMaxSize, "max-size", "", "Maximum known file size, e.g. 2GB")
	cmd.Flags().StringVar(&flagTitleMatch, "title-match", "", "Only include titles/filenames/URLs matching this regex")
	cmd.Flags().StringVar(&flagTitleExclude, "title-exclude", "", "Exclude titles/filenames/URLs matching this regex")
	cmd.Flags().IntVar(&flagLimit, "limit", 0, "Twitter/Reddit/Instagram: limit number of posts")
	cmd.Flags().StringVar(&flagLayout, "layout", "", "Output layout preset: creator, site, flat, date")
	cmd.Flags().StringVar(&flagOutputTemplate, "filename-template", "", "Advanced yt-dlp output template, relative to output dir unless absolute")
	cmd.Flags().StringVar(&flagSpeedLimit, "speed-limit", "", "Limit download speed, e.g. 5MB or 500KB")
	cmd.Flags().BoolVar(&flagIncludePosts, "include-posts", false, "Download post text, links, and metadata as markdown files (Twitter/Reddit/Instagram)")
	cmd.Flags().BoolVar(&flagIncludeComments, "include-comments", false, "Download post comments/replies (Reddit)")
	cmd.Flags().IntVar(&flagMaxComments, "max-comments", 100, "Maximum number of comments to download per post")
}

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install yt-dlp and ffmpeg into the local cache",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			fmt.Println("Installing yt-dlp and ffmpeg...")
			if err := extractor.EnsureInstalled(ctx); err != nil {
				return fmt.Errorf("install: %w", err)
			}
			fmt.Println("Done.")
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status SESSION",
		Short: "Show progress for a saved download session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := session.Load(args[0])
			if err != nil {
				return err
			}
			printSessionStatus(s)
			return nil
		},
	}
}

func resumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume SESSION",
		Short: "Resume pending and failed URLs in a saved session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := session.Load(args[0])
			if err != nil {
				return err
			}
			if reset, err := s.ResetRunning(); err != nil {
				return err
			} else if reset > 0 {
				fmt.Fprintf(os.Stderr, "[provenance] reset %d interrupted URL(s) to pending\n", reset)
			}
			urls := s.URLsByStatus(session.StatusPending, session.StatusFailed)
			if len(urls) == 0 {
				fmt.Println("Nothing to resume; this session has no pending or failed URLs.")
				printSessionStatus(s)
				return nil
			}
			opts := dispatcher.Options{Config: s.Options, Reporter: s}
			fmt.Fprintf(os.Stderr, "[provenance] resuming session %q (%d URL(s))\n", s.Name, len(urls))
			return app.Download(cmd.Context(), urls, "", opts)
		},
	}
}

func retryFailedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retry-failed SESSION",
		Short: "Retry only failed URLs in a saved session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := session.Load(args[0])
			if err != nil {
				return err
			}
			urls := s.URLsByStatus(session.StatusFailed)
			if len(urls) == 0 {
				fmt.Println("Nothing to retry; this session has no failed URLs.")
				printSessionStatus(s)
				return nil
			}
			opts := dispatcher.Options{Config: s.Options, Reporter: s}
			fmt.Fprintf(os.Stderr, "[provenance] retrying failed URLs for session %q (%d URL(s))\n", s.Name, len(urls))
			return app.Download(cmd.Context(), urls, "", opts)
		},
	}
}

func sessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Manage saved download sessions",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List saved sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			infos, err := session.List()
			if err != nil {
				return err
			}
			if len(infos) == 0 {
				fmt.Println("No saved sessions.")
				return nil
			}
			fmt.Printf("%-24s %-19s %7s %7s %7s %7s\n", "Name", "Updated", "Total", "OK", "Failed", "Pending")
			for _, info := range infos {
				fmt.Printf("%-24s %-19s %7d %7d %7d %7d\n",
					info.Name,
					info.UpdatedAt.Format("2006-01-02 15:04"),
					info.Counts.Total,
					info.Counts.Succeeded,
					info.Counts.Failed,
					info.Counts.Pending)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "export SESSION FILE",
		Short: "Export a session JSON file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := session.Load(args[0])
			if err != nil {
				return err
			}
			data, err := os.ReadFile(s.Path())
			if err != nil {
				return err
			}
			return os.WriteFile(args[1], data, 0o644)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "clean SESSION",
		Short: "Delete a saved session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return session.Delete(args[0])
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "failed SESSION [FILE]",
		Short: "Print or save failed URLs from a session",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := session.Load(args[0])
			if err != nil {
				return err
			}
			var b strings.Builder
			for _, e := range s.EntriesByStatus(session.StatusFailed) {
				b.WriteString(e.URL)
				b.WriteByte('\n')
			}
			if len(args) == 2 {
				return os.WriteFile(args[1], []byte(b.String()), 0o644)
			}
			fmt.Print(b.String())
			return nil
		},
	})
	return cmd
}

func watchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Manage recurring download subscriptions",
	}
	add := &cobra.Command{
		Use:   "add NAME URL",
		Short: "Add or update a watch subscription",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := buildOptions(false)
			if err != nil {
				return err
			}
			if err := watch.Add(args[0], args[1], opts.Config); err != nil {
				return err
			}
			fmt.Printf("Added watch %q -> %s\n", args[0], args[1])
			return nil
		},
	}
	addDownloadFlags(add)
	cmd.AddCommand(add)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List watch subscriptions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			subs, err := watch.List()
			if err != nil {
				return err
			}
			if len(subs) == 0 {
				fmt.Println("No watch subscriptions.")
				return nil
			}
			fmt.Printf("%-24s %-19s %s\n", "Name", "Last run", "URL")
			for _, sub := range subs {
				last := "never"
				if !sub.LastRunAt.IsZero() {
					last = sub.LastRunAt.Format("2006-01-02 15:04")
				}
				fmt.Printf("%-24s %-19s %s\n", sub.Name, last, sub.URL)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "remove NAME",
		Short: "Remove a watch subscription",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return watch.Remove(args[0])
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "run [NAME]",
		Short: "Run one watch subscription or all subscriptions",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var subs []watch.Subscription
			if len(args) == 1 {
				sub, err := watch.Get(args[0])
				if err != nil {
					return err
				}
				subs = []watch.Subscription{sub}
			} else {
				var err error
				subs, err = watch.List()
				if err != nil {
					return err
				}
			}
			var firstErr error
			for _, sub := range subs {
				fmt.Fprintf(os.Stderr, "[provenance] watch %q: %s\n", sub.Name, sub.URL)
				sess, err := session.OpenOrCreate("watch-"+sub.Name, sub.Options)
				if err != nil {
					return err
				}
				opts := dispatcher.Options{Config: sub.Options, Reporter: sess}
				if err := app.Download(cmd.Context(), []string{sub.URL}, "", opts); err != nil && firstErr == nil {
					firstErr = err
				}
				if err := watch.MarkRun(sub.Name); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		},
	})
	return cmd
}

func collectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "collect",
		Short: "Manage named collection sync sources",
	}
	add := &cobra.Command{
		Use:   "add NAME URL",
		Short: "Add or update a collection source",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := buildOptions(false)
			if err != nil {
				return err
			}
			site, err := dispatcher.Classify(args[1])
			if err != nil {
				return err
			}
			if err := collection.Add(args[0], args[1], site.String(), opts.Config); err != nil {
				return err
			}
			fmt.Printf("Added collection %q -> %s\n", args[0], args[1])
			return nil
		},
	}
	addDownloadFlags(add)
	cmd.AddCommand(add)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List collection sources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cols, err := collection.List()
			if err != nil {
				return err
			}
			if len(cols) == 0 {
				fmt.Println("No collections.")
				return nil
			}
			fmt.Printf("%-24s %-19s %6s %6s %6s %s\n", "Name", "Last sync", "Total", "New", "Fail", "URL")
			for _, c := range cols {
				last := "never"
				total, newCnt, fail := "-", "-", "-"
				if !c.LastSync.IsZero() {
					last = c.LastSync.Format("2006-01-02 15:04")
				}
				if c.LastResult != nil {
					total = fmt.Sprintf("%d", c.LastResult.Total)
					newCnt = fmt.Sprintf("%d", c.LastResult.New)
					fail = fmt.Sprintf("%d", c.LastResult.Failed)
				}
				fmt.Printf("%-24s %-19s %6s %6s %6s %s\n", c.Name, last, total, newCnt, fail, c.URL)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "show NAME",
		Short: "Show collection details and last sync result",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := collection.Get(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Name:     %s\n", c.Name)
			fmt.Printf("URL:      %s\n", c.URL)
			fmt.Printf("Site:     %s\n", c.Site)
			fmt.Printf("Output:   %s\n", c.Options.OutputDir)
			if c.Options.CookiesFile != "" {
				fmt.Printf("Cookies:  %s\n", c.Options.CookiesFile)
			}
			fmt.Printf("Created:  %s\n", c.CreatedAt.Format("2006-01-02 15:04"))
			lastSync := "never"
			if !c.LastSync.IsZero() {
				lastSync = c.LastSync.Format("2006-01-02 15:04")
			}
			fmt.Printf("LastSync: %s\n", lastSync)
			fmt.Printf("Seen:     %d item(s)\n", len(c.SeenIDs))
			if c.LastResult != nil {
				fmt.Printf("\nLast result (%s):\n", c.LastResult.At.Format("2006-01-02 15:04"))
				fmt.Printf("  Total:   %d\n", c.LastResult.Total)
				fmt.Printf("  New:     %d\n", c.LastResult.New)
				fmt.Printf("  Skipped: %d\n", c.LastResult.Skipped)
				fmt.Printf("  Failed:  %d\n", c.LastResult.Failed)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "remove NAME",
		Short: "Remove a collection source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return collection.Remove(args[0])
		},
	})
	syncCmd := &cobra.Command{
		Use:   "sync [NAME]",
		Short: "Sync a collection source or all collections",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := collection.SyncOptions{
				DryRun:      flagDryRun,
				Record:      flagRecord,
				RateLimiter: rateLimiter,
			}
			doSync := func() error {
				if len(args) == 1 {
					result, err := collection.Sync(cmd.Context(), args[0], opts)
					if err != nil {
						return err
					}
					_ = result
					return nil
				}
				return collection.SyncAll(cmd.Context(), opts)
			}
			if flagEvery != "" {
				dur, err := time.ParseDuration(flagEvery)
				if err != nil {
					return fmt.Errorf("invalid --every duration: %w", err)
				}
				fmt.Fprintf(os.Stderr, "[provenance] scheduling sync every %s\n", dur)
				if err := doSync(); err != nil {
					fmt.Fprintf(os.Stderr, "[provenance] sync error: %v\n", err)
				}
				t := time.NewTicker(dur)
				defer t.Stop()
				for {
					select {
					case <-cmd.Context().Done():
						return nil
					case <-t.C:
						if err := doSync(); err != nil {
							fmt.Fprintf(os.Stderr, "[provenance] sync error: %v\n", err)
						}
					}
				}
			}
			return doSync()
		},
	}
	syncCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Show what would be synced without downloading")
	syncCmd.Flags().BoolVar(&flagRecord, "record", false, "Record capture manifest with SHA-256 verification data")
	syncCmd.Flags().StringVar(&flagEvery, "every", "", "Repeat sync at interval (e.g., 24h, 7d, 30m)")
	cmd.AddCommand(syncCmd)
	return cmd
}

func manifestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "Show and verify capture manifests",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show PATH",
		Short: "Display a capture manifest JSON file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read manifest: %w", err)
			}
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, data, "", "  "); err != nil {
				fmt.Println(string(data))
				return nil
			}
			fmt.Println(pretty.String())
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "verify DIR",
		Short: "Verify all files against recorded SHA-256 hashes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := manifest.VerifyCaptureDir(args[0])
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Println("No capture items found in", filepath.Join(args[0], manifest.ProvenanceDir, "items"))
				return nil
			}
			ok, fail, missing := 0, 0, 0
			for _, r := range results {
				if r.Missing {
					fmt.Printf("MISSING  %s\n", r.Path)
					missing++
				} else if r.OK {
					ok++
				} else {
					fmt.Printf("FAIL     %s\n", r.Path)
					fmt.Printf("  expected: %s\n", r.Expected)
					fmt.Printf("  actual:   %s\n", r.Actual)
					fail++
				}
			}
			fmt.Printf("\n%d ok, %d failed, %d missing\n", ok, fail, missing)
			return nil
		},
	})
	return cmd
}

func archiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "Preserve content in the durable vault",
	}
	cmd.PersistentFlags().StringVar(&flagArchiveCollection, "collection", "", "Archive collection name (required)")
	cmd.PersistentFlags().StringVar(&flagVaultRoot, "vault", "./provenance-vault", "Vault root directory")
	cmd.PersistentFlags().StringVar(&flagImportRef, "ref", "main", "Git ref (branch/tag) for import-git")
	cmd.PersistentFlags().StringVar(&flagImportScope, "scope", "", "URL scope prefix for import-docs")
	cmd.PersistentFlags().IntVar(&flagMaxPages, "max-pages", 10, "Maximum pages for import-docs and import-web")
	cmd.PersistentFlags().BoolVar(&flagScreenshot, "screenshot", false, "Capture page screenshots (import-web)")

	cmd.AddCommand(&cobra.Command{
		Use:   "url URL",
		Short: "Archive a single URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return archiveURL(cmd.Context(), args[0])
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "collection NAME",
		Short: "Archive a collection's captured content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return archiveCollection(cmd.Context(), args[0])
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "session NAME",
		Short: "Archive a session's downloaded content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return archiveSession(cmd.Context(), args[0])
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "import DIR",
		Short: "Import an existing directory with capture manifests",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return archiveImport(cmd.Context(), args[0])
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "import-pdf PATH",
		Short: "Import a PDF file into the vault",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagArchiveCollection == "" {
				return fmt.Errorf("--collection is required")
			}
			rev, err := importers.ImportPDF(flagVaultRoot, args[0], flagArchiveCollection)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "[provenance] imported PDF: revision %s (%d entities)\n", rev.ID, len(rev.Entities))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "import-git URL",
		Short: "Import a Git repository into the vault",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagArchiveCollection == "" {
				return fmt.Errorf("--collection is required")
			}
			rev, err := importers.ImportGit(flagVaultRoot, args[0], flagImportRef, flagArchiveCollection)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "[provenance] imported git repo: revision %s (%d entities)\n", rev.ID, len(rev.Entities))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "import-docs URL",
		Short: "Import a static documentation site into the vault",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagArchiveCollection == "" {
				return fmt.Errorf("--collection is required")
			}
			rev, err := importers.ImportDocs(flagVaultRoot, args[0], flagImportScope, flagMaxPages, flagArchiveCollection)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "[provenance] imported docs site: revision %s (%d entities)\n", rev.ID, len(rev.Entities))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "import-openapi PATH",
		Short: "Import an OpenAPI specification into the vault",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagArchiveCollection == "" {
				return fmt.Errorf("--collection is required")
			}
			rev, err := importers.ImportOpenAPI(flagVaultRoot, args[0], flagArchiveCollection)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "[provenance] imported OpenAPI spec: revision %s (%d entities)\n", rev.ID, len(rev.Entities))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "import-web URL",
		Short: "Archive a web page or site using headless Chrome",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagArchiveCollection == "" {
				return fmt.Errorf("--collection is required")
			}
			rev, err := importers.ImportWeb(flagVaultRoot, args[0], flagImportScope, flagMaxPages, flagScreenshot, flagArchiveCollection, flagChromePath)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "[provenance] imported web %d page(s): revision %s\n", len(rev.Entities), rev.ID)
			return nil
		},
	})
	return cmd
}

func archiveURL(ctx context.Context, rawURL string) error {
	if flagArchiveCollection == "" {
		return fmt.Errorf("--collection is required")
	}
	opts, err := buildOptions(false)
	if err != nil {
		return err
	}
	_, err = dispatcher.Classify(rawURL)
	if err != nil {
		return err
	}

	m, err := dispatcher.Scan(ctx, rawURL, opts)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[provenance] downloading and archiving %d item(s) from %s\n", len(m.Items), rawURL)

	sessName := fmt.Sprintf("archive-url-%d", time.Now().Unix())
	sess, err := session.OpenOrCreate(sessName, opts.Config)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	opts.Reporter = sess

	var urls []string
	for _, item := range m.Items {
		if item.URL != "" {
			urls = append(urls, item.URL)
		}
	}
	if err := app.Download(ctx, urls, "", opts); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	return archiveFromOutput(ctx, opts.OutputDir, archive.Source{
		URL:       rawURL,
		Kind:      archive.SourceURL,
		Reference: rawURL,
	})
}

func archiveCollection(ctx context.Context, name string) error {
	if flagArchiveCollection == "" {
		return fmt.Errorf("--collection is required")
	}
	c, err := collection.Get(name)
	if err != nil {
		return err
	}
	return archiveFromOutput(ctx, c.Options.OutputDir, archive.Source{
		URL:       c.URL,
		Kind:      archive.SourceCollection,
		Reference: name,
	})
}

func archiveSession(ctx context.Context, name string) error {
	if flagArchiveCollection == "" {
		return fmt.Errorf("--collection is required")
	}
	s, err := session.Load(name)
	if err != nil {
		return err
	}
	return archiveFromOutput(ctx, s.Options.OutputDir, archive.Source{
		URL:       name,
		Kind:      archive.SourceSession,
		Reference: name,
	})
}

func archiveImport(ctx context.Context, dir string) error {
	if flagArchiveCollection == "" {
		return fmt.Errorf("--collection is required")
	}
	return archiveFromOutput(ctx, dir, archive.Source{
		URL:       dir,
		Kind:      archive.SourceImport,
		Reference: dir,
	})
}

func archiveFromOutput(ctx context.Context, outputDir string, src archive.Source) error {
	bs := blobstore.New(flagVaultRoot)
	ingestOpts := archive.IngestOptions{
		VaultRoot:      flagVaultRoot,
		CollectionName: flagArchiveCollection,
		Source:         src,
		Tool:           "provenance",
	}

	runs, err := manifest.ListRunManifests(outputDir)
	if err != nil {
		return fmt.Errorf("list runs: %w", err)
	}
	if len(runs) == 0 {
		return fmt.Errorf("no capture manifests found in %s/.provenance/runs/ — run collect sync --record first", outputDir)
	}

	totalItems := 0
	for _, runPath := range runs {
		cm, err := manifest.ReadCaptureManifest(runPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[provenance] skipping %s: %v\n", runPath, err)
			continue
		}
		rev, err := archive.IngestCaptureManifest(bs, ingestOpts, cm)
		if err != nil {
			return fmt.Errorf("ingest %s: %w", runPath, err)
		}
		if rev == nil || len(rev.Entities) == 0 {
			continue
		}
		if err := archive.WriteRevision(flagVaultRoot, rev); err != nil {
			return fmt.Errorf("write revision: %w", err)
		}
		if catalog.HasStore() {
			if err := catalog.Store().InsertRevision(ctx, rev, flagArchiveCollection); err != nil {
				fmt.Fprintf(os.Stderr, "[provenance] warning: PG write failed: %v\n", err)
			}
		}
		totalItems += len(rev.Entities)
		fmt.Fprintf(os.Stderr, "[provenance] archived revision %s (%d entities)\n", rev.ID, len(rev.Entities))
	}

	now := time.Now()
	if err := catalog.UpsertCollection(flagVaultRoot, archive.ArchiveCollection{
		Name:      flagArchiveCollection,
		VaultRoot: flagVaultRoot,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return fmt.Errorf("save collection: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[provenance] archived %d total item(s) to collection %q\n", totalItems, flagArchiveCollection)
	return nil
}

func ensureVaultStore() error {
	if catalog.HasStore() {
		return nil
	}
	connStr := os.Getenv("PROVENANCE_DATABASE_URL")
	if connStr == "" {
		return fmt.Errorf("PROVENANCE_DATABASE_URL not set")
	}
	store, err := catalog.NewPgStore(context.Background(), connStr)
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w\nhint: run 'provenance vault init' first", err)
	}
	catalog.SetStore(store)
	return nil
}

func vaultCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Manage the durable vault",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize the vault and PostgreSQL schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := catalog.NewPgStore(cmd.Context(), os.Getenv("PROVENANCE_DATABASE_URL"))
			if err != nil {
				return fmt.Errorf("connect to PostgreSQL: %w\nhint: set PROVENANCE_DATABASE_URL=postgres://user:pass@localhost/dbname", err)
			}
			defer func() { _ = store.Close() }()
			if err := store.Init(cmd.Context()); err != nil {
				return fmt.Errorf("init schema: %w", err)
			}
			catalog.SetStore(store)
			fmt.Println("Vault initialized.")
			return nil
		},
	})
	cmd.AddCommand(revisionShowCmd())
	cmd.AddCommand(citeCmd())
	cmd.AddCommand(diffCmd())
	cmd.AddCommand(gcCmd())
	cmd.AddCommand(retentionCmd())
	cmd.AddCommand(backupCmd())
	return cmd
}

func revisionShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show ID",
		Short: "Show revision details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureVaultStore(); err != nil {
				return err
			}
			rev, err := catalog.Store().GetRevision(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Printf("ID:         %s\n", rev.ID)
			fmt.Printf("Captured:   %s\n", rev.CapturedAt.Format("2006-01-02 15:04 UTC"))
			fmt.Printf("Source:     %s (%s)\n", rev.Source.Reference, rev.Source.Kind)
			fmt.Printf("Tool:       %s %s\n", rev.Tool, rev.ToolVersion)
			fmt.Printf("Entities:   %d\n", len(rev.Entities))
			fmt.Printf("Artifacts:  %d\n", len(rev.Artifacts))
			fmt.Println()
			for i, ent := range rev.Entities {
				if i >= 20 {
					fmt.Printf("  ... and %d more entities\n", len(rev.Entities)-i)
					break
				}
				fmt.Printf("  %s — %s (%s)\n", trimShort(ent.Title, 50), ent.ExternalID, ent.Kind)
			}
			return nil
		},
	}
}

func citeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cite REVISION_ID",
		Short: "Generate a citation for a revision entity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureVaultStore(); err != nil {
				return err
			}
			rev, err := catalog.Store().GetRevision(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			collectionName := flagSearchCollection
			if collectionName == "" {
				collectionName = "default"
			}

			entityID := flagCiteEntity
			if entityID == "" && len(rev.Entities) > 0 {
				entityID = rev.Entities[0].ExternalID
			}

			for _, ent := range rev.Entities {
				if entityID != "" && ent.ExternalID != entityID {
					continue
				}
				ref := citation.FromEntity(collectionName, args[0], &ent)
				switch flagCiteFormat {
				case "markdown":
					fmt.Println(ref.Markdown())
				case "json":
					data, _ := ref.ToJSON()
					fmt.Println(string(data))
				case "plain":
					fmt.Println(ref.Plain())
				default:
					fmt.Println(ref.Plain())
				}
				break
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flagSearchCollection, "collection", "", "Archive collection name")
	cmd.Flags().StringVar(&flagCiteFormat, "format", "plain", "Citation format: provenance, markdown, plain, json")
	cmd.Flags().StringVar(&flagCiteEntity, "entity", "", "Entity external ID")
	return cmd
}

func diffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff ID_A ID_B",
		Short: "Diff two revisions",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			old, err := archive.ReadRevision(flagVaultRoot, args[0])
			if err != nil {
				return fmt.Errorf("read revision A: %w", err)
			}
			new, err := archive.ReadRevision(flagVaultRoot, args[1])
			if err != nil {
				return fmt.Errorf("read revision B: %w", err)
			}
			diff := archive.DiffRevisions(old, new)
			fmt.Printf("Diff %s -> %s\n\n", args[0][:12], args[1][:12])
			fmt.Printf("Added:    %d\n", len(diff.Added))
			fmt.Printf("Removed:  %d\n", len(diff.Removed))
			fmt.Printf("Changed:  %d\n", len(diff.Changed))
			fmt.Printf("Unchanged:%d\n", diff.Unchanged)
			for _, c := range diff.Changed {
				fmt.Printf("\n  %s\n", c.ExternalID)
				for _, f := range c.Changes {
					fmt.Printf("    %s: %s -> %s\n", f.Field, f.OldVal, f.NewVal)
				}
			}
			for _, a := range diff.Added {
				fmt.Printf("\n  + %s\n", a.ExternalID)
			}
			for _, r := range diff.Removed {
				fmt.Printf("\n  - %s\n", r.ExternalID)
			}
			return nil
		},
	}
}

func gcCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gc",
		Short: "Garbage-collect orphaned blobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			orphans, err := catalog.GarbageCollect(flagVaultRoot, flagDryRun)
			if err != nil {
				return err
			}
			if flagDryRun {
				fmt.Printf("Would remove %d orphaned blob(s):\n", len(orphans))
				for _, h := range orphans {
					fmt.Printf("  %s\n", h[:12])
				}
			} else {
				fmt.Printf("Removed %d orphaned blob(s)\n", len(orphans))
			}
			return nil
		},
	}
}

func retentionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retention",
		Short: "Manage revision retention",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "prune",
		Short: "Prune old revisions keeping the last N",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagSearchCollection == "" || flagKeep == 0 {
				return fmt.Errorf("--collection and --keep are required")
			}
			removed, err := catalog.PruneRevisions(flagVaultRoot, flagSearchCollection, flagKeep)
			if err != nil {
				return err
			}
			fmt.Printf("Pruned %d revision(s), kept %d\n", len(removed), flagKeep)
			return nil
		},
	})
	cmd.Flags().IntVar(&flagKeep, "keep", 0, "Number of revisions to keep")
	cmd.Flags().StringVar(&flagSearchCollection, "collection", "", "Archive collection name")
	return cmd
}

func backupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup",
		Short: "Backup the vault",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dest := filepath.Join(filepath.Dir(flagVaultRoot), filepath.Base(flagVaultRoot)+"-backup.tar")
			fmt.Fprintf(os.Stderr, "[provenance] backup: %s -> %s (use tar/cp -a for full backup)\n", flagVaultRoot, dest)
			fmt.Fprintf(os.Stderr, "[provenance] hint: backup the vault directory with: cp -a %s %s\n", flagVaultRoot, dest)
			return nil
		},
	}
}

func searchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search QUERY",
		Short: "Full-text search across archived content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureVaultStore(); err != nil {
				return err
			}
			opts := catalog.SearchOptions{
				CollectionName: flagSearchCollection,
				Kind:           flagSearchKind,
				Limit:          20,
			}
			result, err := catalog.Store().Search(cmd.Context(), args[0], opts)
			if err != nil {
				return err
			}
			if len(result.Hits) == 0 {
				fmt.Println("No results.")
				return nil
			}
			fmt.Printf("%d result(s) for %q\n\n", result.Total, args[0])
			for i, h := range result.Hits {
				fmt.Printf("%d. %s\n", i+1, h.Title)
				if h.Headline != "" {
					fmt.Printf("   %s\n", h.Headline)
				}
				fmt.Printf("   %s — %s — %s @ %s\n", h.URL, h.CollectionName, h.RevisionID[:12], h.CapturedAt.Format("2006-01-02"))
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flagSearchCollection, "collection", "", "Filter by archive collection name")
	cmd.Flags().StringVar(&flagSearchKind, "kind", "", "Filter by entity kind (video, image, post, etc.)")
	return cmd
}

func trimShort(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Launch the interactive terminal UI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.Run(cmd.Context())
		},
	}
}

func completionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "completion [bash|zsh|fish|powershell]",
		Short:                 "Generate shell completion script",
		Long:                  "Generate a completion script for the chosen shell. Source the output in your shell init.",
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(os.Stdout, true)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			}
			return fmt.Errorf("unknown shell %q", args[0])
		},
	}
	return cmd
}

func runSessionDownload(ctx context.Context, name string, args []string, batchPath string, opts dispatcher.Options) error {
	if opts.DryRun {
		return fmt.Errorf("--session cannot be combined with --dry-run because dry runs do not create resumable work")
	}
	if batchPath == "" && len(args) == 0 {
		return fmt.Errorf("provide at least one URL or use --batch <file>")
	}
	s, err := session.OpenOrCreate(name, opts.Config)
	if err != nil {
		return err
	}
	if len(args) > 0 {
		if _, err := s.AddURLs(args, "argument"); err != nil {
			return err
		}
	}
	if batchPath != "" {
		urls, err := readBatchURLs(batchPath)
		if err != nil {
			return err
		}
		if _, err := s.AddURLs(urls, "batch:"+batchPath); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "[provenance] session %q saved at %s\n", s.Name, s.Path())
	opts.Reporter = s
	err = app.Download(ctx, args, batchPath, opts)
	printSessionStatus(s)
	return err
}

func buildOptions(dryRun bool) (dispatcher.Options, error) {
	minSize, err := manifest.ParseSize(flagMinSize)
	if err != nil {
		return dispatcher.Options{}, fmt.Errorf("--min-size: %w", err)
	}
	maxSize, err := manifest.ParseSize(flagMaxSize)
	if err != nil {
		return dispatcher.Options{}, fmt.Errorf("--max-size: %w", err)
	}
	speedLimit, err := manifest.ParseSize(flagSpeedLimit)
	if err != nil {
		return dispatcher.Options{}, fmt.Errorf("--speed-limit: %w", err)
	}

	cookiesFile := ""
	if flagCookies != "" {
		abs, err := filepath.Abs(flagCookies)
		if err != nil {
			return dispatcher.Options{}, fmt.Errorf("resolve cookies path: %w", err)
		}
		cookiesFile = abs
	}

	return dispatcher.Options{
		Config: config.Config{
			OutputDir:   flagOutput,
			CookiesFile: cookiesFile,
			Concurrency: flagConcurrency,
			Quality:     flagQuality,
			AudioOnly:   flagAudioOnly,
			NoArchive:   flagNoArchive,
			Filter: manifest.FilterOptions{
				IncludeExt:  manifest.ParseCSV(flagIncludeExt),
				ExcludeExt:  manifest.ParseCSV(flagExcludeExt),
				MinSize:     minSize,
				MaxSize:     maxSize,
				TitleMatch:  flagTitleMatch,
				TitleReject: flagTitleExclude,
			},
			PostLimit:          flagLimit,
			IncludePosts:       flagIncludePosts,
			IncludeComments:    flagIncludeComments,
			CommentLimit:       flagMaxComments,
			CookiesFromBrowser: flagCookiesFromBrowser,
			OutputLayout:       flagLayout,
			OutputTemplate:     flagOutputTemplate,
			SpeedLimit:         speedLimit,
			ChromePath:         flagChromePath,
		},
		DryRun:      dryRun,
		RateLimiter: rateLimiter,
	}, nil
}

func saveScan(path string, manifests []manifest.Manifest) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(manifests)
}

func readBatchURLs(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open batch file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var urls []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		urls = append(urls, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read batch file: %w", err)
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("batch file %q contained no URLs", path)
	}
	return urls, nil
}

func printSessionStatus(s *session.Session) {
	counts := s.Counts()
	fmt.Printf("Session: %s\n", s.Name)
	fmt.Printf("File:    %s\n", s.Path())
	fmt.Printf("Updated: %s\n", s.UpdatedAt.Format("2006-01-02 15:04:05"))
	fmt.Println()
	fmt.Printf("Total:      %d\n", counts.Total)
	fmt.Printf("Succeeded:  %d\n", counts.Succeeded)
	fmt.Printf("Skipped:    %d\n", counts.Skipped)
	fmt.Printf("Pending:    %d\n", counts.Pending)
	fmt.Printf("Running:    %d\n", counts.Running)
	fmt.Printf("Failed:     %d\n", counts.Failed)

	failed := s.EntriesByStatus(session.StatusFailed)
	if len(failed) > 0 {
		fmt.Println()
		fmt.Println("Failed URLs:")
		for i, e := range failed {
			if i >= 20 {
				fmt.Printf("  ... and %d more\n", len(failed)-i)
				break
			}
			fmt.Printf("  - %s\n", e.URL)
			if e.LastError != "" {
				fmt.Printf("    %s\n", e.LastError)
			}
		}
		fmt.Println()
		fmt.Printf("Retry with: provenance retry-failed %q\n", s.Name)
	}
}
