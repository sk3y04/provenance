package encode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Integration tests in this file exercise the real ffmpeg/ffprobe binaries
// end to end. They are skipped (not failed) when FFmpeg is absent from PATH,
// since it is an external prerequisite; CI installs FFmpeg so they run there.

// haveFFmpeg reports whether both ffmpeg and ffprobe are available on PATH.
func haveFFmpeg() bool {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return false
	}
	_, err := exec.LookPath("ffprobe")
	return err == nil
}

// TestIntegrationFFprobeFPS generates a small 25 fps test video with the real
// ffmpeg, reads its frame rate back through the real ffprobe, and checks the
// parsing against the probe output. This verifies the probe's argument
// construction and error behavior against a real installation.
func TestIntegrationFFprobeFPS(t *testing.T) {
	if !haveFFmpeg() {
		t.Skip("ffmpeg/ffprobe not on PATH; skipping integration test (external prerequisite)")
	}

	ctx := context.Background()
	runner := NewRunner()

	dir := t.TempDir()
	in := filepath.Join(dir, "src.mp4")
	if err := runner.Run(ctx, "ffmpeg", nil, nil, nil,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=64x64:rate=25",
		"-pix_fmt", "yuv420p", in,
	); err != nil {
		t.Fatalf("generate test video: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(in) })

	var out strings.Builder
	if err := runner.Run(ctx, "ffprobe", nil, &out, nil,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=r_frame_rate",
		"-of", "default=noprint_wrappers=1:nokey=1",
		in,
	); err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	ratio := strings.TrimSpace(out.String())
	fps, err := parseFPS(ratio)
	if err != nil {
		t.Fatalf("parseFPS(%q): %v", ratio, err)
	}
	if fps != 25 {
		t.Fatalf("ffprobe reported fps %v for ratio %q, want 25", fps, ratio)
	}
}
