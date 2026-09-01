package encode

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sk3y04/provenance/internal/worker"
)

// Options holds every flag for `provenance encode`, populated directly from
// cobra flags in cmd/provenance/main.go.
//
// These live in a dedicated struct rather than on config.Config on purpose:
// config.Config is the serializable download configuration shared by the
// dispatcher/session/watch machinery (and embedded in capture manifests),
// whereas encode is a self-contained, batch-oriented feature. This mirrors how
// collection uses a dedicated SyncOptions struct and how archive carries its
// persistent flags separately from the generic download Config — the encoder
// has no download/session semantics to preserve, so it gets its own option bag.
type Options struct {
	Dir        string   // source directory (ignored when File is set)
	File       string   // single input file
	Recursive  bool     // recurse into subdirectories
	Ext        []string // input extensions to match (no leading dot)
	OutDir     string   // empty -> <sourceDir>/av1_qN
	Suffix     string   // inserted before .mkv (empty -> av1-qN)
	Overwrite  bool     // re-encode even if the output already exists
	Encoder    string   // qsv|amf|nvenc|auto
	Devices    []string // render-node paths (QSV/AMF) or GPU indices (NVENC)
	Quality    int      // canonical QSV global_quality scale 1..51
	Preset     string   // speed/efficiency hint, mapped per backend
	GOPSeconds float64  // keyframe interval in seconds
	Lookahead  int      // look-ahead frames; QSV max 100
	BFrames    int      // B-frames per GOP
	DryRun     bool     // print commands without encoding
}

// Result summarizes a completed (or dry-run) batch.
type Result struct {
	Total     int
	Skipped   int
	Succeeded int
	Failed    int
	OutDir    string
	Encoder   string
	Devices   int
}

// OK reports whether the batch produced no failures and touched at least one
// file. A run with zero discovered files is not OK.
func (r Result) OK() bool { return r.Failed == 0 && r.Total > 0 }

// defaultDevice is the default render node when the user does not pass
// --devices. QSV is the primary target, so the first Intel render node is the
// sensible default.
const defaultDevice = "/dev/dri/renderD128"

// Run discovers inputs, resolves the encoder, and fans the batch out over the
// requested devices using the worker pool. It returns a non-nil error (so the
// CLI exits non-zero) when preflight fails or any encode fails.
func Run(ctx context.Context, opts Options, runner CommandRunner) (Result, error) {
	if runner == nil {
		runner = NewRunner()
	}

	exts := extSet(opts.Ext)

	// Determine the source directory (dir, or the directory of --file).
	sourceDir := opts.Dir
	if sourceDir == "" && opts.File != "" {
		sourceDir = filepath.Dir(opts.File)
	}

	quality := opts.Quality
	if quality == 0 {
		quality = 30
	}
	gopSeconds := opts.GOPSeconds
	if gopSeconds <= 0 {
		gopSeconds = 10
	}

	outDir := opts.OutDir
	if strings.TrimSpace(outDir) == "" {
		outDir = filepath.Join(sourceDir, fmt.Sprintf("av1_q%d", quality))
	}
	suffix := opts.Suffix
	if strings.TrimSpace(suffix) == "" {
		suffix = fmt.Sprintf("av1-q%d", quality)
	}
	suffix = strings.TrimSpace(suffix)

	devices := opts.Devices
	if len(devices) == 0 {
		devices = []string{defaultDevice}
	}

	// Look-ahead defaults to 100 (QSV's max) when unset, matching the CLI flag
	// default; <=0 disables the flag inside av1Args.
	lookahead := opts.Lookahead
	if lookahead <= 0 {
		lookahead = 100
	}

	// Preflight: ffmpeg + ffprobe must exist (EnsureInstalled has already run
	// best-effort in main's PersistentPreRunE; resolveBinaries just confirms).
	ffmpeg, ffprobe, err := resolveBinaries()
	if err != nil {
		return Result{}, err
	}

	enc, err := ResolveEncoder(ctx, opts.Encoder, ffmpeg, runner)
	if err != nil {
		return Result{}, err
	}

	// Discover inputs.
	var inputs []string
	if opts.File != "" {
		abs, err := filepath.Abs(opts.File)
		if err != nil {
			return Result{}, fmt.Errorf("resolve --file: %w", err)
		}
		inputs = []string{abs}
	} else {
		abs, err := filepath.Abs(sourceDir)
		if err != nil {
			return Result{}, fmt.Errorf("resolve --dir: %w", err)
		}
		inputs, err = collectFiles(abs, opts.Recursive, exts, outDir)
		if err != nil {
			return Result{}, fmt.Errorf("scan %s: %w", abs, err)
		}
	}
	if len(inputs) == 0 {
		return Result{}, fmt.Errorf("no matching video files found in %s (extensions: %s)", sourceDir, strings.Join(defaultExts(), ","))
	}

	if !opts.DryRun {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return Result{}, fmt.Errorf("create output dir: %w", err)
		}
	}

	// Build jobs, skipping outputs that already exist unless --overwrite.
	reporter := newReporter()
	reporter.total = len(inputs)

	for i, in := range inputs {
		base := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))
		outPath := filepath.Join(outDir, fmt.Sprintf("%s.%s.mkv", base, suffix))
		logPath := filepath.Join(outDir, fmt.Sprintf("%s.%s.log", base, suffix))

		if !opts.Overwrite {
			if _, statErr := os.Stat(outPath); statErr == nil {
				reporter.incSkipped()
				continue
			}
		}

		fps, _ := ffmpegFPS(ctx, ffprobe, in, runner)
		job := EncodeJob{
			Input:     in,
			Output:    outPath,
			LogPath:   logPath,
			FPS:       fps,
			GOPInt:    GOPFrames(fps, gopSeconds),
			Device:    devices[i%len(devices)],
			Quality:   quality,
			Preset:    opts.Preset,
			Lookahead: clampInt(lookahead, 0, 100),
			BFrames:   opts.BFrames,
		}
		reporter.jobs = append(reporter.jobs, job)
	}

	reporter.printHeader(len(reporter.jobs), enc.Name(), quality, opts.Preset, gopSeconds, suffix, devices, opts.DryRun)

	// Fan out: one worker pool per device, each pulling its assigned jobs. This
	// yields exactly one worker per device, reusing the shared worker.Pool.
	pools := make([]*worker.Pool, len(devices))
	for i := range pools {
		pools[i] = worker.NewPool(ctx, 1)
	}

	for i, job := range reporter.jobs {
		job := job
		p := pools[i%len(devices)]
		p.SubmitWithHooks(
			func() error {
				if runErr := runOne(ctx, ffmpeg, runner, enc, job, opts.DryRun); runErr != nil {
					// Encode is not retryable: mark permanent so the pool does
					// not re-run (which could overwrite in progress output).
					return worker.Permanent(runErr)
				}
				return nil
			},
			func() {
				reporter.incSucceeded()
				reporter.printDone(devices[i%len(devices)], job, reporter.completed())
			},
			func(runErr error) {
				reporter.fail(job, runErr)
			},
		)
	}
	for _, p := range pools {
		p.Wait()
	}

	res := Result{
		Total:     reporter.total,
		Skipped:   int(reporter.skipped),
		Succeeded: int(reporter.succeeded),
		Failed:    int(reporter.failed),
		OutDir:    outDir,
		Encoder:   enc.Name(),
		Devices:   len(devices),
	}
	reporter.printSummary(res, time.Since(reporter.start))

	if res.Failed > 0 {
		return res, fmt.Errorf("%d encode(s) failed (see %s)", res.Failed, outDir)
	}
	return res, nil
}

// runOne encodes a single job, streaming ffmpeg stderr to the per-file log.
func runOne(ctx context.Context, ffmpeg string, runner CommandRunner, enc Encoder, job EncodeJob, dryRun bool) error {
	if dryRun {
		// Dry-run prints the constructed command and never touches the
		// filesystem, so there is no log to open.
		return enc.Run(ctx, ffmpeg, runner, io.Discard, job, true)
	}
	log, err := os.Create(job.LogPath)
	if err != nil {
		return fmt.Errorf("create log %s: %w", job.LogPath, err)
	}
	defer func() { _ = log.Close() }()

	if err := enc.Run(ctx, ffmpeg, runner, log, job, false); err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	return nil
}

// extSet joins the configured extensions into a lookup set.
func extSet(exts []string) map[string]bool {
	if len(exts) == 0 {
		return parseExts(strings.Join(defaultExts(), ","))
	}
	return parseExts(strings.Join(exts, ","))
}

func defaultExts() []string {
	return []string{"mp4", "mkv", "mov", "avi", "ts", "webm"}
}

// reporter serializes [provenance] stderr lines and counts outcomes. It is
// safe for concurrent use because pool hooks run in worker goroutines.
type reporter struct {
	mu        sync.Mutex
	start     time.Time
	total     int
	jobs      []EncodeJob
	succeeded int64
	failed    int64
	skipped   int64
	firstErr  error
}

func newReporter() *reporter {
	return &reporter{start: time.Now()}
}

func (r *reporter) incSucceeded() { atomic.AddInt64(&r.succeeded, 1) }
func (r *reporter) incFailed()    { atomic.AddInt64(&r.failed, 1) }
func (r *reporter) incSkipped()   { atomic.AddInt64(&r.skipped, 1) }

func (r *reporter) completed() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int(atomic.LoadInt64(&r.succeeded) + atomic.LoadInt64(&r.failed))
}

// fail records the first failure (guarded) and prints a per-file error line.
func (r *reporter) fail(job EncodeJob, runErr error) {
	r.incFailed()
	r.mu.Lock()
	if r.firstErr == nil {
		r.firstErr = fmt.Errorf("encode %s: %w", filepath.Base(job.Input), runErr)
	}
	r.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(os.Stderr, "[provenance] %s: FAILED %s -> %v (see %s)\n",
		job.Device, filepath.Base(job.Input), runErr, job.LogPath)
}

func (r *reporter) linef(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func (r *reporter) printHeader(count int, codec string, quality int, preset string, gopSeconds float64, suffix string, devices []string, dryRun bool) {
	r.linef("[provenance] encode: %d input(s), %d device worker(s): %s",
		count, len(devices), strings.Join(devices, ", "))
	r.linef("[provenance] encoder: %s | quality=%d | preset=%q | suffix=%q | gop=%gs | dry-run=%v",
		codec, quality, preset, suffix, gopSeconds, dryRun)
}

func (r *reporter) printDone(device string, job EncodeJob, done int) {
	r.linef("[provenance] %s: done %d/%d  %s (fps=%.2f gop=%d)",
		device, done, r.total, filepath.Base(job.Output), job.FPS, job.GOPInt)
}

func (r *reporter) printSummary(res Result, dur time.Duration) {
	r.linef("[provenance] done in %s — success: %d, skipped: %d, failed: %d, output: %s",
		dur.Round(time.Second), res.Succeeded, res.Skipped, res.Failed, res.OutDir)
}
