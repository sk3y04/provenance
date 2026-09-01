package encode

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Encoder names (ffmpeg codec names).
const (
	EncoderQSV   = "av1_qsv"
	EncoderAMF   = "av1_amf"
	EncoderNVENC = "av1_nvenc"
)

// CLI shortcuts accepted by --encoder.
var encoderShortcuts = map[string]string{
	"qsv":       EncoderQSV,
	"av1_qsv":   EncoderQSV,
	"amf":       EncoderAMF,
	"av1_amf":   EncoderAMF,
	"nvenc":     EncoderNVENC,
	"av1_nvenc": EncoderNVENC,
}

// EncodeJob is a single transcode task: one input file mapped to one output
// file, with the per-file properties each backend needs. Device is the render
// node (QSV/AMF) or GPU index (NVENC) assigned by the fan-out.
type EncodeJob struct {
	Input     string
	Output    string
	LogPath   string
	FPS       float64
	GOPInt    int // keyframe interval in frames
	Device    string
	Quality   int // canonical QSV global_quality scale (1..51)
	Preset    string
	Lookahead int // look-ahead frames; <=0 disables the flag
	BFrames   int // B-frames per GOP; <=0 disables the flag
}

// Encoder selects an ffmpeg hardware AV1 backend and knows how to build its
// argument list and translate the shared Options onto the backend's native
// flag vocabulary (QSV global_quality / AMF CQ / NVENC CRF).
type Encoder interface {
	// Name is the ffmpeg codec name, e.g. "av1_qsv".
	Name() string
	// Available reports whether the installed ffmpeg build exposes this codec.
	Available(available map[string]bool) bool
	// DeviceArgs returns the ffmpeg flags that pin encoding to device (render
	// node for QSV/AMF, GPU index for NVENC). Empty when device is unset.
	DeviceArgs(device string) []string
	// BuildArgs returns every ffmpeg argument after the binary (no leading
	// "ffmpeg", no -i/output). The container is always Matroska.
	BuildArgs(job EncodeJob) []string
	// Run executes the encode, streaming ffmpeg stderr to log. In dry-run mode
	// it prints the constructed command and returns nil without touching the
	// filesystem.
	Run(ctx context.Context, ffmpeg string, runner CommandRunner, log io.Writer, job EncodeJob, dryRun bool) error
}

// av1Params captures the per-backend variation that av1Args cannot infer, so
// the shared builder remains the single source of truth for the parts of the
// ffmpeg invocation that are identical across every backend (input/device
// pinning, stream mapping, metadata copy, GOP size, audio/subtitle copy).
type av1Params struct {
	codec             string
	deviceArgs        []string
	qualityArg        []string // e.g. ["-global_quality", "30"]
	preset            string
	supportsLookahead bool
	supportsBFrames   bool
	extraBefore       []string // inserted after preset (e.g. QSV extbrc/adaptive, NVENC rc)
	extraAfter        []string // inserted after GOP size (reserved for future backends)
}

// av1Args assembles the complete ffmpeg argument slice for a job. All three
// backends funnel through here so the container, mapping, and stream handling
// stay consistent.
func av1Args(job EncodeJob, p av1Params) []string {
	args := make([]string, 0, 40)
	args = append(args, "-hide_banner", "-nostdin", "-y")
	args = append(args, p.deviceArgs...)
	args = append(args, "-i", job.Input)
	args = append(args, "-map", "0:v?", "-map", "0:a?", "-map", "0:s?", "-map_metadata", "0")
	args = append(args, "-c:v", p.codec)
	args = append(args, p.qualityArg...)
	args = append(args, "-preset", p.preset)
	if p.supportsLookahead && job.Lookahead > 0 {
		args = append(args, "-look_ahead", "1", "-look_ahead_depth", strconv.Itoa(job.Lookahead))
	}
	args = append(args, p.extraBefore...)
	if p.supportsBFrames && job.BFrames > 0 {
		args = append(args, "-bf", strconv.Itoa(job.BFrames))
	}
	args = append(args, "-g", strconv.Itoa(job.GOPInt))
	args = append(args, p.extraAfter...)
	args = append(args, "-c:a", "copy", "-c:s", "copy")
	args = append(args, job.Output)
	return args
}

// av1QSV is the Intel QuickSync backend — the primary target. Quality maps
// directly onto QSV global_quality (1..51); lookahead, extbrc, and b-frames
// are all supported.
type av1QSV struct{}

// NewQSVEncoder returns the Intel QuickSync AV1 encoder.
func NewQSVEncoder() Encoder { return av1QSV{} }

// Name implements Encoder.
func (av1QSV) Name() string { return EncoderQSV }

// Available implements Encoder.
func (av1QSV) Available(available map[string]bool) bool { return available[EncoderQSV] }

// DeviceArgs implements Encoder.
func (av1QSV) DeviceArgs(device string) []string {
	if device == "" {
		return nil
	}
	return []string{"-qsv_device", device}
}

// BuildArgs implements Encoder.
func (av1QSV) BuildArgs(job EncodeJob) []string {
	return av1Args(job, av1Params{
		codec:             EncoderQSV,
		deviceArgs:        av1QSV{}.DeviceArgs(job.Device),
		qualityArg:        []string{"-global_quality", strconv.Itoa(clampInt(job.Quality, 1, 51))},
		preset:            qsvPreset(job.Preset, "medium"),
		supportsLookahead: true,
		supportsBFrames:   true,
		extraBefore:       []string{"-extbrc", "1", "-adaptive_i", "1", "-adaptive_b", "1", "-b_strategy", "1"},
	})
}

// Run implements Encoder.
func (e av1QSV) Run(ctx context.Context, ffmpeg string, runner CommandRunner, log io.Writer, job EncodeJob, dryRun bool) error {
	if dryRun {
		fmt.Fprintf(os.Stderr, "[provenance] dry-run (%s): ffmpeg %s\n", e.Name(), formatCmdline(e.BuildArgs(job)))
		return nil
	}
	return runner.Run(ctx, ffmpeg, nil, io.Discard, log, e.BuildArgs(job)...)
}

// av1AMF is the AMD Radeon backend. Quality maps onto AMF CQ quality (same
// 1..51 range as QSV); rate control is set to CQ. AMD's amf encoders expose no
// portable b-frames flag, so b-frames are dropped gracefully.
type av1AMF struct{}

// NewAMFFncoder returns the AMD AMF AV1 encoder.
func NewAMFFncoder() Encoder { return av1AMF{} }

// Name implements Encoder.
func (av1AMF) Name() string { return EncoderAMF }

// Available implements Encoder.
func (av1AMF) Available(available map[string]bool) bool { return available[EncoderAMF] }

// DeviceArgs implements Encoder.
func (av1AMF) DeviceArgs(device string) []string {
	if device == "" {
		return nil
	}
	// AMF selects the OpenCL device by index or path via --device.
	return []string{"--device", device}
}

// BuildArgs implements Encoder.
func (av1AMF) BuildArgs(job EncodeJob) []string {
	return av1Args(job, av1Params{
		codec:             EncoderAMF,
		deviceArgs:        av1AMF{}.DeviceArgs(job.Device),
		qualityArg:        []string{"-cq_quality", strconv.Itoa(clampInt(job.Quality, 1, 51))},
		preset:            amfPreset(job.Preset, "balanced"),
		supportsLookahead: true,
		supportsBFrames:   false,
		extraBefore:       []string{"-rc", "cq"},
	})
}

// Run implements Encoder.
func (e av1AMF) Run(ctx context.Context, ffmpeg string, runner CommandRunner, log io.Writer, job EncodeJob, dryRun bool) error {
	if dryRun {
		fmt.Fprintf(os.Stderr, "[provenance] dry-run (%s): ffmpeg %s\n", e.Name(), formatCmdline(e.BuildArgs(job)))
		return nil
	}
	return runner.Run(ctx, ffmpeg, nil, io.Discard, log, e.BuildArgs(job)...)
}

// av1NVENC is the NVIDIA backend. Quality maps onto NVENC AV1 CRF (0..33) with
// proportional translation from the canonical QSV scale. Rate control is set to
// CRF and the GPU is selected by index via -device.
type av1NVENC struct{}

// NewNVENCEncoder returns the NVIDIA NVENC AV1 encoder.
func NewNVENCEncoder() Encoder { return av1NVENC{} }

// Name implements Encoder.
func (av1NVENC) Name() string { return EncoderNVENC }

// Available implements Encoder.
func (av1NVENC) Available(available map[string]bool) bool { return available[EncoderNVENC] }

// DeviceArgs implements Encoder.
func (av1NVENC) DeviceArgs(device string) []string {
	if device == "" {
		return nil
	}
	// NVENC selects the CUDA device by index via -device (or -gpu on older
	// builds). Index values are used on Windows; render-node paths on Linux.
	return []string{"-device", device}
}

// BuildArgs implements Encoder.
func (av1NVENC) BuildArgs(job EncodeJob) []string {
	return av1Args(job, av1Params{
		codec:             EncoderNVENC,
		deviceArgs:        av1NVENC{}.DeviceArgs(job.Device),
		qualityArg:        []string{"-crf", strconv.Itoa(nvencCRF(job.Quality))},
		preset:            nvencPreset(job.Preset, "hq"),
		supportsLookahead: true,
		supportsBFrames:   true,
		extraBefore:       []string{"-rc", "crf"},
	})
}

// Run implements Encoder.
func (av1NVENC) Run(ctx context.Context, ffmpeg string, runner CommandRunner, log io.Writer, job EncodeJob, dryRun bool) error {
	if dryRun {
		fmt.Fprintf(os.Stderr, "[provenance] dry-run (%s): ffmpeg %s\n", av1NVENC{}.Name(), formatCmdline(av1NVENC{}.BuildArgs(job)))
		return nil
	}
	return runner.Run(ctx, ffmpeg, nil, io.Discard, log, av1NVENC{}.BuildArgs(job)...)
}

// clampInt v into [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// qsvPreset maps the canonical preset name onto QSV's vocabulary, falling back
// to def for unknown inputs.
func qsvPreset(input, def string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "ultrafast":
		return "ultrafast"
	case "superfast":
		return "superfast"
	case "veryfast", "very_fast":
		return "veryfast"
	case "fast":
		return "fast"
	case "medium", "":
		return "medium"
	case "slow":
		return "slow"
	case "veryslow", "very_slow":
		return "veryslow"
	case "default":
		return "default"
	default:
		return def
	}
}

// amfPreset maps the canonical preset name onto AMF's quality/speed
// vocabulary. Speed-oriented inputs map toward "ultrafast", efficiency-oriented
// inputs toward "maximum".
func amfPreset(input, def string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "ultrafast", "superfast", "veryfast", "fast", "speed":
		return "ultrafast"
	case "medium", "balanced", "normal":
		return "balanced"
	case "slow", "hq", "quality":
		return "quality"
	case "veryslow", "maximum", "lossless":
		return "maximum"
	default:
		return def
	}
}

// nvencPreset maps the canonical preset name onto NVENC's vocabulary
// (default, hp, hq, slow_hq, fast, hd, studio).
func nvencPreset(input, def string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "ultrafast", "superfast", "veryfast", "fast", "p1", "p2", "p3":
		return "fast"
	case "medium", "p4", "p5":
		return "hq"
	case "slow", "slow_hq", "p6":
		return "slow_hq"
	case "veryslow", "maximum", "p7", "studio":
		return "studio"
	default:
		return def
	}
}

// nvencCRF translates the canonical QSV global_quality scale (1..51) onto
// NVENC's CRF range (1..33), proportionally.
func nvencCRF(quality int) int {
	crf := int((float64(clampInt(quality, 1, 51))-1.0)/50.0*33.0 + 0.5)
	return clampInt(crf, 1, 33)
}

// formatCmdline renders an argument slice as a shell-escaped string for
// --dry-run display. It never invokes a shell.
func formatCmdline(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if needsQuoting(a) {
			out[i] = "\"" + strings.ReplaceAll(a, "\"", "\\\"") + "\""
		} else {
			out[i] = a
		}
	}
	return strings.Join(out, " ")
}

func needsQuoting(s string) bool {
	return s == "" || strings.ContainsAny(s, " \t\"'")
}
