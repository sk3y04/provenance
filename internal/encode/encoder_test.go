package encode

import (
	"context"
	"io"
	"strings"
	"testing"
)

func sampleJob() EncodeJob {
	return EncodeJob{
		Input:     "/in/movie.mp4",
		Output:    "/out/movie.av1-q30.mkv",
		LogPath:   "/out/movie.av1-q30.log",
		FPS:       24,
		GOPInt:    120,
		Device:    "/dev/dri/renderD128",
		Quality:   30,
		Preset:    "medium",
		Lookahead: 100,
		BFrames:   7,
	}
}

func findFlagVal(args []string, flag string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestBuildArgsCommonStructure(t *testing.T) {
	encoders := []Encoder{av1QSV{}, av1AMF{}, av1NVENC{}}
	for _, enc := range encoders {
		args := enc.BuildArgs(sampleJob())
		// Always starts with banner flags and never embeds the binary itself.
		if args[0] != "-hide_banner" || args[1] != "-nostdin" {
			t.Errorf("%s: unexpected header %#v", enc.Name(), args[:3])
		}
		// Stream mapping + metadata preservation.
		for _, want := range []string{"-map", "0:v?", "-map", "0:a?", "-map", "0:s?", "-map_metadata", "0"} {
			if !hasArg(args, want) {
				t.Errorf("%s: missing %q in %#v", enc.Name(), want, args)
			}
		}
		// Audio/subtitle copy.
		if !hasArg(args, "-c:a") || !hasArg(args, "copy") {
			t.Errorf("%s: expected -c:a copy", enc.Name())
		}
		if !hasArg(args, "-c:s") || !hasArg(args, "copy") {
			t.Errorf("%s: expected -c:s copy", enc.Name())
		}
		// GOP frame count.
		if v, ok := findFlagVal(args, "-g"); !ok || v != "120" {
			t.Errorf("%s: -g = %q (ok=%v), want 120", enc.Name(), v, ok)
		}
		// Output is the final argument and is Matroska.
		if got := args[len(args)-1]; got != sampleJob().Output {
			t.Errorf("%s: output = %q, want %q", enc.Name(), got, sampleJob().Output)
		}
		if !strings.HasSuffix(args[len(args)-1], ".mkv") {
			t.Errorf("%s: output must be .mkv, got %q", enc.Name(), args[len(args)-1])
		}
	}
}

func TestDeviceArgs(t *testing.T) {
	cases := []struct {
		enc    Encoder
		device string
		want   []string
	}{
		{av1QSV{}, "/dev/dri/renderD128", []string{"-qsv_device", "/dev/dri/renderD128"}},
		{av1AMF{}, "0", []string{"--device", "0"}},
		{av1NVENC{}, "1", []string{"-device", "1"}},
		{av1QSV{}, "", nil},
		{av1AMF{}, "", nil},
		{av1NVENC{}, "", nil},
	}
	for _, tc := range cases {
		if got := tc.enc.DeviceArgs(tc.device); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s.DeviceArgs(%q) = %#v, want %#v", tc.enc.Name(), tc.device, got, tc.want)
		}
	}
}

func TestQualityArgPerBackend(t *testing.T) {
	q := sampleJob()
	q.Quality = 30

	qsv := av1QSV{}.BuildArgs(q)
	if v, ok := findFlagVal(qsv, "-global_quality"); !ok || v != "30" {
		t.Errorf("qsv -global_quality = %q (ok=%v), want 30", v, ok)
	}

	amf := av1AMF{}.BuildArgs(q)
	if v, ok := findFlagVal(amf, "-cq_quality"); !ok || v != "30" {
		t.Errorf("amf -cq_quality = %q (ok=%v), want 30", v, ok)
	}

	nvenc := av1NVENC{}.BuildArgs(q)
	if v, ok := findFlagVal(nvenc, "-crf"); !ok || v != "19" {
		t.Errorf("nvenc -crf = %q (ok=%v), want 19", v, ok)
	}
}

func TestQualityClamping(t *testing.T) {
	q := sampleJob()

	q.Quality = 0 // below range
	if v, ok := findFlagVal(av1QSV{}.BuildArgs(q), "-global_quality"); !ok || v != "1" {
		t.Errorf("qsv clamped low = %q, want 1", v)
	}
	if v, ok := findFlagVal(av1NVENC{}.BuildArgs(q), "-crf"); !ok || v != "1" {
		t.Errorf("nvenc clamped low = %q, want 1", v)
	}

	q.Quality = 999 // above range
	if v, ok := findFlagVal(av1QSV{}.BuildArgs(q), "-global_quality"); !ok || v != "51" {
		t.Errorf("qsv clamped high = %q, want 51", v)
	}
	if v, ok := findFlagVal(av1NVENC{}.BuildArgs(q), "-crf"); !ok || v != "33" {
		t.Errorf("nvenc clamped high = %q, want 33", v)
	}
}

func TestNvencCRFTranslation(t *testing.T) {
	cases := []struct{ q, want int }{
		{1, 1}, {20, 13}, {30, 19}, {40, 26}, {51, 33},
	}
	for _, tc := range cases {
		if got := nvencCRF(tc.q); got != tc.want {
			t.Errorf("nvencCRF(%d) = %d, want %d", tc.q, got, tc.want)
		}
	}
}

func TestPresetMapping(t *testing.T) {
	cases := []struct {
		enc  Encoder
		in   string
		def  string
		want string
	}{
		{av1QSV{}, "veryslow", "medium", "veryslow"},
		{av1QSV{}, "", "medium", "medium"},
		{av1QSV{}, "bogus", "medium", "medium"},
		{av1AMF{}, "ultrafast", "balanced", "ultrafast"},
		{av1AMF{}, "veryfast", "balanced", "ultrafast"},
		{av1AMF{}, "slow", "balanced", "quality"},
		{av1AMF{}, "bogus", "balanced", "balanced"},
		{av1NVENC{}, "veryfast", "hq", "fast"},
		{av1NVENC{}, "slow", "hq", "slow_hq"},
		{av1NVENC{}, "bogus", "hq", "hq"},
	}
	for _, tc := range cases {
		job := sampleJob()
		job.Preset = tc.in
		var got string
		var ok bool
		switch tc.enc.(type) {
		case av1QSV:
			got, ok = findFlagVal(tc.enc.BuildArgs(job), "-preset")
		case av1AMF:
			got, ok = findFlagVal(tc.enc.BuildArgs(job), "-preset")
		default:
			got, ok = findFlagVal(tc.enc.BuildArgs(job), "-preset")
		}
		if !ok || got != tc.want {
			t.Errorf("%s.preset(%q) = %q, want %q", tc.enc.Name(), tc.in, got, tc.want)
		}
	}
}

func TestBFramesBackendSupport(t *testing.T) {
	q := sampleJob()
	q.BFrames = 7
	if !hasArg(av1QSV{}.BuildArgs(q), "-bf") {
		t.Error("qsv should emit -bf when bframes set")
	}
	if !hasArg(av1NVENC{}.BuildArgs(q), "-bf") {
		t.Error("nvenc should emit -bf when bframes set")
	}
	if hasArg(av1AMF{}.BuildArgs(q), "-bf") {
		t.Error("amf should NOT emit -bf (no portable bframes flag)")
	}
}

func TestLookaheadOnlyWhenPositive(t *testing.T) {
	q := sampleJob()
	q.Lookahead = 0
	if hasArg(av1QSV{}.BuildArgs(q), "-look_ahead") {
		t.Error("qsv should omit lookahead when Lookahead <= 0")
	}
	q.Lookahead = 100
	if !hasArg(av1QSV{}.BuildArgs(q), "-look_ahead_depth") {
		if v, ok := findFlagVal(av1QSV{}.BuildArgs(q), "-look_ahead_depth"); !ok || v != "100" {
			t.Errorf("qsv -look_ahead_depth = %q (ok=%v), want 100", v, ok)
		}
	}
}

func TestAvailable(t *testing.T) {
	avail := map[string]bool{EncoderQSV: true}
	if !(av1QSV{}).Available(avail) {
		t.Error("qsv should be available")
	}
	if (av1AMF{}).Available(avail) {
		t.Error("amf should not be available")
	}
	if (av1NVENC{}).Available(avail) {
		t.Error("nvenc should not be available")
	}
}

func TestDryRunDoesNotInvokeRunner(t *testing.T) {
	fake := &fakeRunner{logText: "some ffmpeg output"}
	job := sampleJob()
	if err := (av1QSV{}).Run(context.Background(), "ffmpeg", fake, io.Discard, job, true); err != nil {
		t.Fatalf("dry-run Run: %v", err)
	}
	// In dry-run mode no ffmpeg process is attempted, so the runner is never called.
	if len(fake.calls) != 0 {
		t.Fatalf("dry-run should not invoke ffmpeg, got %d calls", len(fake.calls))
	}
}
