package encode

import (
	"context"
	"strings"
	"testing"
)

func TestParseExts(t *testing.T) {
	got := parseExts(" MP4,.MKV ,mov , , .avi ")
	want := map[string]bool{".mp4": true, ".mkv": true, ".mov": true, ".avi": true}
	if len(got) != len(want) {
		t.Fatalf("parseExts = %v (len %d), want len %d", got, len(got), len(want))
	}
	for k := range want {
		if !got[k] {
			t.Errorf("parseExts missing %q in %v", k, got)
		}
	}
	if parseExts("")[".mp4"] {
		t.Error("empty ext string should yield an empty set")
	}
}

func TestParseFPS(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"30000/1001", 29.97},
		{"60000/1001", 59.94},
		{"24", 24},
		{"25", 25},
		{"30", 30},
	}
	for _, tc := range cases {
		got, err := parseFPS(tc.in)
		if err != nil {
			t.Fatalf("parseFPS(%q): %v", tc.in, err)
		}
		if abs(got-tc.want) > 0.01 {
			t.Errorf("parseFPS(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestGOPFrames(t *testing.T) {
	cases := []struct {
		fps, gop float64
		want     int
	}{
		{24, 10, 240},
		{29.97, 10, 300},
		{25, 4, 100},
		{24, 0.5, 12},
		{0, 10, 250}, // zero fps falls back to 25
		{24, -1, 1},  // non-positive seconds floors at 1
	}
	for _, tc := range cases {
		if got := GOPFrames(tc.fps, tc.gop); got != tc.want {
			t.Errorf("GOPFrames(%v,%v) = %d, want %d", tc.fps, tc.gop, got, tc.want)
		}
	}
}

func TestAvailableEncoders(t *testing.T) {
	fake := &fakeRunner{probeOut: allEncoders()}
	avail, err := availableEncoders(context.Background(), "ffmpeg", fake)
	if err != nil {
		t.Fatalf("availableEncoders: %v", err)
	}
	if !avail[EncoderQSV] || !avail[EncoderAMF] || !avail[EncoderNVENC] {
		t.Fatalf("expected all three codecs, got %v", avail)
	}
}

func TestAvailableEncodersPartial(t *testing.T) {
	fake := &fakeRunner{probeOut: qsvAvailable()}
	avail, err := availableEncoders(context.Background(), "ffmpeg", fake)
	if err != nil {
		t.Fatalf("availableEncoders: %v", err)
	}
	if avail[EncoderQSV] != true || avail[EncoderAMF] || avail[EncoderNVENC] {
		t.Fatalf("expected only qsv, got %v", avail)
	}
}

func TestResolveEncoderAuto(t *testing.T) {
	// auto picks the first available in QSV > AMF > NVENC order.
	fake := &fakeRunner{probeOut: allEncoders()}
	enc, err := ResolveEncoder(context.Background(), "auto", "ffmpeg", fake)
	if err != nil {
		t.Fatalf("ResolveEncoder(auto): %v", err)
	}
	if enc.Name() != EncoderQSV {
		t.Fatalf("auto should prefer qsv, got %s", enc.Name())
	}

	// With only nvenc present, auto selects nvenc.
	fake2 := &fakeRunner{probeOut: " V.....    av1_nvenc              NVIDIA NVENC AV1 encoder\n"}
	enc2, err := ResolveEncoder(context.Background(), "auto", "ffmpeg", fake2)
	if err != nil {
		t.Fatalf("ResolveEncoder(auto, nvenc-only): %v", err)
	}
	if enc2.Name() != EncoderNVENC {
		t.Fatalf("auto should select nvenc, got %s", enc2.Name())
	}
}

func TestResolveEncoderExplicitAndError(t *testing.T) {
	// Explicit alias resolves to the backend.
	fake := &fakeRunner{probeOut: allEncoders()}
	enc, err := ResolveEncoder(context.Background(), "av1_amf", "ffmpeg", fake)
	if err != nil {
		t.Fatalf("ResolveEncoder(av1_amf): %v", err)
	}
	if enc.Name() != EncoderAMF {
		t.Fatalf("ResolveEncoder(av1_amf) = %s, want amf", enc.Name())
	}

	// Explicit codec absent from build -> clear "not supported" error.
	fakeOnlyQSV := &fakeRunner{probeOut: qsvAvailable()}
	_, err = ResolveEncoder(context.Background(), "nvenc", "ffmpeg", fakeOnlyQSV)
	if err == nil || !strings.Contains(err.Error(), "not supported by this ffmpeg build") {
		t.Fatalf("expected unsupported error, got %v", err)
	}

	// Unknown codec name.
	_, err = ResolveEncoder(context.Background(), "theora", "ffmpeg", fakeOnlyQSV)
	if err == nil || !strings.Contains(err.Error(), "unknown --encoder") {
		t.Fatalf("expected unknown-encoder error, got %v", err)
	}

	// No hardware AV1 encoder at all with auto.
	fakeNone := &fakeRunner{probeOut: " V.....    libvpx-vp9             VP9 encoder\n"}
	_, err = ResolveEncoder(context.Background(), "auto", "ffmpeg", fakeNone)
	if err == nil {
		t.Fatal("expected auto to fail with no AV1 encoders")
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
