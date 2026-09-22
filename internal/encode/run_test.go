package encode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeRunner is an in-memory CommandRunner used to exercise the fan-out,
// logging, skipping, and result accounting without touching a GPU.
type fakeRunner struct {
	mu         sync.Mutex
	calls      []fakeCall
	probeOut   string // stdout for `ffmpeg -encoders`
	ffprobeOut string // stdout for ffprobe r_frame_rate
	encodeErr  error  // returned for encoding runs (nil = success)
	logText    string // written to the log/stderr writer for encoding runs
}

type fakeCall struct {
	bin  string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, bin string, _ []string, stdout, stderr io.Writer, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{bin: bin, args: append([]string(nil), args...)})

	if strings.HasSuffix(bin, "ffprobe") {
		_, _ = io.WriteString(stdout, f.ffprobeOut)
		return nil
	}
	if hasArg(args, "-encoders") {
		_, _ = io.WriteString(stdout, f.probeOut)
		return nil
	}
	// encoding run: tee log text, then honor the configured error.
	if f.logText != "" && stderr != nil {
		_, _ = io.WriteString(stderr, f.logText)
	}
	return f.encodeErr
}

func (f *fakeRunner) encodeCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasSuffix(c.bin, "ffmpeg") && !hasArg(c.args, "-encoders") {
			n++
		}
	}
	return n
}

// qsvAvailable builds a probeOut exposing av1_qsv.
func qsvAvailable() string {
	return " V.....    av1_qsv              Intel QSV AV1 encoder\n" +
		" V.....    hevc_qsv             Intel QSV HEVC encoder\n"
}

func allEncoders() string {
	return " V.....    av1_qsv              Intel QSV AV1 encoder\n" +
		" V.....    av1_amf              AMF AV1 encoder\n" +
		" V.....    av1_nvenc              NVIDIA NVENC AV1 encoder\n"
}

// stubBinaries puts fake ffmpeg/ffprobe paths on the stubbed PATH lookup so
// Run's preflight passes without a real FFmpeg installation. It restores the
// real lookPath when the test ends.
func stubBinaries(t *testing.T) {
	t.Helper()
	prev := lookPath
	lookPath = func(name string) (string, error) {
		if name == "ffmpeg" || name == "ffprobe" {
			return "/usr/bin/" + name, nil
		}
		return "", fmt.Errorf("%s not found on PATH", name)
	}
	t.Cleanup(func() { lookPath = prev })
}

func mkfile(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunFanOutOverDevices(t *testing.T) {
	stubBinaries(t)
	dir := t.TempDir()
	mkfile(t, dir, "a.mp4")
	mkfile(t, dir, "b.mp4")

	fake := &fakeRunner{probeOut: qsvAvailable(), ffprobeOut: "24"}
	opts := Options{
		Dir:        dir,
		Encoder:    "qsv",
		Devices:    []string{"/dev/dri/renderD128", "/dev/dri/renderD129"},
		Quality:    30,
		Preset:     "medium",
		GOPSeconds: 10,
	}
	res, err := Run(context.Background(), opts, fake)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Total != 2 || res.Succeeded != 2 || res.Failed != 0 || res.Skipped != 0 {
		t.Fatalf("unexpected result: %#v", res)
	}
	if res.Encoder != EncoderQSV || res.Devices != 2 {
		t.Fatalf("unexpected encoder/devices: %s/%d", res.Encoder, res.Devices)
	}
	if fake.encodeCallCount() != 2 {
		t.Fatalf("expected 2 encoding calls, got %d", fake.encodeCallCount())
	}

	// Verify round-robin device assignment. Each worker finishes on its own
	// schedule, so pair the input (-i) with its device (-qsv_device) per call
	// rather than trusting call-recording order.
	fake.mu.Lock()
	defer fake.mu.Unlock()
	deviceForInput := map[string]string{}
	for _, c := range fake.calls {
		if !strings.HasSuffix(c.bin, "ffmpeg") || hasArg(c.args, "-encoders") {
			continue
		}
		var in, dev string
		for i := 0; i+1 < len(c.args); i++ {
			switch c.args[i] {
			case "-i":
				in = c.args[i+1]
			case "-qsv_device":
				dev = c.args[i+1]
			}
		}
		if in != "" {
			deviceForInput[filepath.Base(in)] = dev
		}
	}
	want := map[string]string{
		"a.mp4": "/dev/dri/renderD128",
		"b.mp4": "/dev/dri/renderD129",
	}
	if len(deviceForInput) != len(want) {
		t.Fatalf("expected %d encoding calls, got %d (%v)", len(want), len(deviceForInput), deviceForInput)
	}
	for in, wdev := range want {
		if deviceForInput[in] != wdev {
			t.Fatalf("input %s assigned to %s, want %s", in, deviceForInput[in], wdev)
		}
	}
}

func TestRunSkipExisting(t *testing.T) {
	stubBinaries(t)
	dir := t.TempDir()
	mkfile(t, dir, "a.mp4")
	mkfile(t, dir, "b.mp4")

	// Pre-create one output so it is skipped (overwrite is false by default).
	outDir := filepath.Join(dir, "av1_q30")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "a.av1-q30.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeRunner{probeOut: qsvAvailable(), ffprobeOut: "24"}
	res, err := Run(context.Background(), Options{Dir: dir, Encoder: "qsv", Quality: 30}, fake)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Skipped != 1 || res.Succeeded != 1 {
		t.Fatalf("expected 1 skipped + 1 succeeded, got %#v", res)
	}
	if fake.encodeCallCount() != 1 {
		t.Fatalf("expected 1 encoding call, got %d", fake.encodeCallCount())
	}
}

func TestRunOverwriteReencodes(t *testing.T) {
	stubBinaries(t)
	dir := t.TempDir()
	mkfile(t, dir, "a.mp4")
	outDir := filepath.Join(dir, "av1_q30")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "a.av1-q30.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeRunner{probeOut: qsvAvailable(), ffprobeOut: "24"}
	res, err := Run(context.Background(), Options{Dir: dir, Encoder: "qsv", Quality: 30, Overwrite: true}, fake)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Skipped != 0 || res.Succeeded != 1 {
		t.Fatalf("expected overwrite to re-encode, got %#v", res)
	}
}

func TestRunReportsFailureAndNonZero(t *testing.T) {
	stubBinaries(t)
	dir := t.TempDir()
	mkfile(t, dir, "a.mp4")

	fake := &fakeRunner{probeOut: qsvAvailable(), ffprobeOut: "24", encodeErr: errors.New("device busy")}
	res, err := Run(context.Background(), Options{Dir: dir, Encoder: "qsv", Quality: 30}, fake)
	if err == nil {
		t.Fatal("expected a non-nil error when an encode fails")
	}
	if res.Failed != 1 || res.Succeeded != 0 {
		t.Fatalf("unexpected result on failure: %#v", res)
	}
}

func TestRunSingleFile(t *testing.T) {
	stubBinaries(t)
	dir := t.TempDir()
	mkfile(t, dir, "solo.mov")

	fake := &fakeRunner{probeOut: qsvAvailable(), ffprobeOut: "30"}
	res, err := Run(context.Background(), Options{File: filepath.Join(dir, "solo.mov"), Encoder: "qsv", Quality: 22}, fake)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Total != 1 || res.Succeeded != 1 {
		t.Fatalf("unexpected result: %#v", res)
	}
}

func TestRunDryRunInvokesNoEncoder(t *testing.T) {
	stubBinaries(t)
	dir := t.TempDir()
	mkfile(t, dir, "a.mp4")
	mkfile(t, dir, "b.mp4")

	fake := &fakeRunner{probeOut: qsvAvailable(), ffprobeOut: "24"}
	res, err := Run(context.Background(), Options{Dir: dir, Encoder: "qsv", Quality: 30, DryRun: true}, fake)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Total != 2 || res.Succeeded != 2 {
		t.Fatalf("dry-run should report all as encodable, got %#v", res)
	}
	if fake.encodeCallCount() != 0 {
		t.Fatalf("dry-run must not invoke ffmpeg, got %d calls", fake.encodeCallCount())
	}
}

func TestRunNoFilesErrors(t *testing.T) {
	stubBinaries(t)
	dir := t.TempDir()
	fake := &fakeRunner{probeOut: qsvAvailable(), ffprobeOut: "24"}
	_, err := Run(context.Background(), Options{Dir: dir, Encoder: "qsv"}, fake)
	if err == nil {
		t.Fatal("expected an error when no matching files exist")
	}
}

func TestRunDefaultExtsAndOutDir(t *testing.T) {
	stubBinaries(t)
	dir := t.TempDir()
	mkfile(t, dir, "keep.mp4")
	mkfile(t, dir, "skip.txt")
	mkfile(t, dir, "web.webm")

	fake := &fakeRunner{probeOut: qsvAvailable(), ffprobeOut: "24"}
	res, err := Run(context.Background(), Options{Dir: dir, Encoder: "qsv", Quality: 30}, fake)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Total != 2 {
		t.Fatalf("default ext set should match mp4+webm only, got total=%d", res.Total)
	}
	if filepath.Base(res.OutDir) != "av1_q30" {
		t.Fatalf("default out dir should be av1_q30, got %q", res.OutDir)
	}
}

func TestCollectFilesRecursiveFiltersAndSkipsOutput(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "a.mp4")
	mkfile(t, root, "b.mkv")
	mkfile(t, root, "c.txt")
	mkfile(t, root, "sub/d.mov")
	// A nested output dir that must never be re-scanned.
	outDir := filepath.Join(root, "av1_q30")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mkfile(t, outDir, "already.av1-q30.mkv")

	exts := parseExts("mp4,mkv,mov")

	// Non-recursive: sub/ is not descended.
	nonRec, err := collectFiles(root, false, exts, outDir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(nonRec)
	if got := strings.Join(baenames(nonRec), ","); got != "a.mp4,b.mkv" {
		t.Fatalf("non-recursive = %v", got)
	}

	// Recursive: sub/ descended, av1_q30 skipped.
	rec, err := collectFiles(root, true, exts, outDir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(rec)
	if got := strings.Join(baenames(rec), ","); got != "a.mp4,b.mkv,d.mov" {
		t.Fatalf("recursive = %v", got)
	}
}

func baenames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}
