package encode

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// parseExts turns a comma-separated extension list into a set of lowercased,
// dot-prefixed extensions (".mp4"). Blank entries are ignored.
func parseExts(s string) map[string]bool {
	m := make(map[string]bool)
	for _, e := range strings.Split(s, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		m[e] = true
	}
	return m
}

// collectFiles walks dir and returns absolute paths of files whose extension is
// in exts. It never descends into the encoder's own output directories
// (named av1_qN or equal to outDir) so re-runs do not re-encode their output.
func collectFiles(dir string, recursive bool, exts map[string]bool, outDir string) ([]string, error) {
	var results []string
	var walkErr error

	walk := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			// Skip this tool's own output directories to avoid re-encoding.
			if strings.HasPrefix(base, "av1_q") && path != dir {
				return filepath.SkipDir
			}
			if outDir != "" && pathIsInside(path, outDir) {
				return filepath.SkipDir
			}
			if !recursive && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if exts[strings.ToLower(filepath.Ext(path))] {
			results = append(results, path)
		}
		return nil
	}

	if err := filepath.WalkDir(dir, walk); err != nil {
		return nil, err
	}
	return results, walkErr
}

// pathIsInside reports whether sub is sub itself or nested under dir.
func pathIsInside(sub, dir string) bool {
	sub = filepath.Clean(sub)
	dir = filepath.Clean(dir)
	if sub == dir {
		return true
	}
	return strings.HasPrefix(sub, dir+string(os.PathSeparator))
}

// lookPath resolves a binary on PATH. It is a package variable rather than a
// CommandRunner method because PATH lookup is not command execution; tests
// override it to exercise the preflight without a real ffmpeg installation.
var lookPath = exec.LookPath

// resolveBinaries locates ffmpeg and ffprobe on PATH, reusing the same
// LookPath-based approach internal/extractor uses for ffmpeg discovery.
// FFmpeg is an external prerequisite (provenance never installs it), so the
// returned errors carry user-facing, per-platform installation guidance.
func resolveBinaries() (ffmpeg, ffprobe string, err error) {
	ffmpeg, err = lookPath("ffmpeg")
	if err != nil {
		return "", "", fmt.Errorf("ffmpeg not found on PATH\n%s", ffmpegPrereqError())
	}
	ffprobe, err = lookPath("ffprobe")
	if err != nil {
		return "", "", fmt.Errorf("ffprobe not found on PATH (needed for per-file FPS detection)\n%s", ffmpegPrereqError())
	}
	return ffmpeg, ffprobe, nil
}

// ffmpegPrereqError is the user-facing guidance explaining that FFmpeg is an
// external prerequisite and how to install it on each platform.
func ffmpegPrereqError() string {
	return "FFmpeg is an external prerequisite for 'provenance encode' (provenance does not install it). Install it with your platform's package manager:\n" +
		"  Linux:   sudo apt install ffmpeg   (or dnf install ffmpeg / pacman -S ffmpeg)\n" +
		"  macOS:   brew install ffmpeg\n" +
		"  Windows: winget install Gyan.FFmpeg   (or choco install ffmpeg)"
}

// availableEncoders probes `ffmpeg -encoders` and reports which of the known
// hardware AV1 codecs the build exposes.
func availableEncoders(ctx context.Context, ffmpeg string, runner CommandRunner) (map[string]bool, error) {
	var out strings.Builder
	if err := runner.Run(ctx, ffmpeg, nil, &out, io.Discard, "-encoders"); err != nil {
		return nil, err
	}
	avail := make(map[string]bool)
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Fields(line)
		// ffmpeg -encoders lines look like: " V.....    av1_qsv   <description>"
		// where the codec name is the second whitespace-separated field.
		if len(f) < 2 {
			continue
		}
		switch strings.ToLower(f[1]) {
		case EncoderQSV, EncoderAMF, EncoderNVENC:
			avail[strings.ToLower(f[1])] = true
		}
	}
	return avail, nil
}

// newEncoderFor returns the Encoder for a resolved codec name, or nil for an
// unknown name.
func newEncoderFor(codec string) Encoder {
	switch codec {
	case EncoderQSV:
		return av1QSV{}
	case EncoderAMF:
		return av1AMF{}
	case EncoderNVENC:
		return av1NVENC{}
	default:
		return nil
	}
}

// availableList renders a stable, human-readable list of present codecs.
func availableList(avail map[string]bool) string {
	present := make([]string, 0, len(avail))
	for _, c := range []string{EncoderQSV, EncoderAMF, EncoderNVENC} {
		if avail[c] {
			present = append(present, c)
		}
	}
	if len(present) == 0 {
		return "(none)"
	}
	sort.Strings(present)
	return strings.Join(present, ", ")
}

// ResolveEncoder selects the encoder for the --encoder value, resolving "auto"
// to the first available hardware AV1 codec (QSV preferred) and validating
// explicit selections against the installed ffmpeg build. The returned error
// for an unsupported explicit encoder carries the phrase "not supported by
// this ffmpeg build" so diagnose.Hint can offer guidance.
func ResolveEncoder(ctx context.Context, selected, ffmpeg string, runner CommandRunner) (Encoder, error) {
	key := strings.ToLower(strings.TrimSpace(selected))
	codec := key
	if v, ok := encoderShortcuts[key]; ok {
		codec = v
	}

	avail, err := availableEncoders(ctx, ffmpeg, runner)
	if err != nil {
		return nil, fmt.Errorf("probe ffmpeg encoders: %w", err)
	}

	if key == "auto" || key == "" {
		for _, c := range []string{EncoderQSV, EncoderAMF, EncoderNVENC} {
			if avail[c] {
				return newEncoderFor(c), nil
			}
		}
		return nil, fmt.Errorf("auto: no hardware AV1 encoder was found in this ffmpeg build (available: %s)", availableList(avail))
	}

	enc := newEncoderFor(codec)
	if enc == nil {
		return nil, fmt.Errorf("unknown --encoder %q (want qsv, amf, nvenc, or auto)", selected)
	}
	if !avail[enc.Name()] {
		return nil, fmt.Errorf("encoder %q not supported by this ffmpeg build (available: %s)", enc.Name(), availableList(avail))
	}
	return enc, nil
}

// parseFPS converts an ffprobe r_frame_rate value like "30000/1001" or "30"
// into a float fps.
func parseFPS(ratio string) (float64, error) {
	ratio = strings.TrimSpace(ratio)
	parts := strings.Split(ratio, "/")
	if len(parts) == 2 {
		num, err1 := parseRatioNum(parts[0])
		den, err2 := parseRatioNum(parts[1])
		if err1 == nil && err2 == nil && den != 0 {
			return num / den, nil
		}
	}
	return parseRatioNum(ratio)
}

// parseRatioNum parses a plain integer or decimal frame-rate number.
func parseRatioNum(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty fps value")
	}
	return strconv.ParseFloat(s, 64)
}

// ffmpegFPS runs ffprobe to read the first video stream's frame rate,
// returning 25 as a sane fallback when the probe fails or the file has no
// video stream.
func ffmpegFPS(ctx context.Context, ffprobe, input string, runner CommandRunner) (float64, error) {
	var out strings.Builder
	err := runner.Run(ctx, ffprobe, nil, &out, io.Discard,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=r_frame_rate",
		"-of", "default=noprint_wrappers=1:nokey=1",
		input,
	)
	if err != nil {
		return 25, nil
	}
	ratio := strings.TrimSpace(out.String())
	if ratio == "" {
		return 25, nil
	}
	fps, err := parseFPS(ratio)
	if err != nil {
		return 25, nil
	}
	return fps, nil
}

// GOPFrames converts a keyframe interval in seconds into a frame count using
// the file's detected fps, floored at 1.
func GOPFrames(fps, gopSeconds float64) int {
	f := fps
	if f <= 0 {
		f = 25
	}
	n := int(f*gopSeconds + 0.5)
	if n < 1 {
		n = 1
	}
	return n
}
