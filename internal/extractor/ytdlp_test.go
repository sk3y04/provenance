package extractor

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type progressEvent struct {
	kind           string
	url, dest      string
	written, total int64
	err            error
}

type fakeProgressReporter struct {
	events []progressEvent
}

func (r *fakeProgressReporter) OnStart(url, dest string, total int64) {
	r.events = append(r.events, progressEvent{kind: "start", url: url, dest: dest, total: total})
}
func (r *fakeProgressReporter) OnProgress(url string, written, total int64) {
	r.events = append(r.events, progressEvent{kind: "progress", url: url, written: written, total: total})
}
func (r *fakeProgressReporter) OnDone(url string, err error) {
	r.events = append(r.events, progressEvent{kind: "done", url: url, err: err})
}

func TestYtdlpProgressWriterParsesMarkedJSON(t *testing.T) {
	rep := &fakeProgressReporter{}
	var fallback, capture bytes.Buffer
	w := newYtdlpProgressWriter("https://example.test/video", rep, &fallback, &capture)

	line := `noise ` + ytdlpProgressMarker + `{"status":"downloading","filename":"out.mp4","downloaded_bytes":512,"total_bytes":"1024"}` + "\n"
	if _, err := w.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	if fallback.Len() != 0 || capture.Len() != 0 {
		t.Fatalf("progress line leaked to fallback/capture: fallback=%q capture=%q", fallback.String(), capture.String())
	}
	if len(rep.events) != 2 {
		t.Fatalf("expected start+progress, got %#v", rep.events)
	}
	if rep.events[0].kind != "start" || rep.events[0].url != "out.mp4" || rep.events[0].dest != "out.mp4" || rep.events[0].total != 1024 {
		t.Fatalf("bad start event: %#v", rep.events[0])
	}
	if rep.events[1].kind != "progress" || rep.events[1].written != 512 || rep.events[1].total != 1024 {
		t.Fatalf("bad progress event: %#v", rep.events[1])
	}
}

func TestYtdlpProgressWriterForwardsNormalStderr(t *testing.T) {
	rep := &fakeProgressReporter{}
	var fallback, capture bytes.Buffer
	w := newYtdlpProgressWriter("src", rep, &fallback, &capture)

	_, _ = w.Write([]byte("ERROR: unsupported URL\n"))
	if got := fallback.String(); got != "ERROR: unsupported URL\n" {
		t.Fatalf("fallback = %q", got)
	}
	if got := capture.String(); got != "ERROR: unsupported URL\n" {
		t.Fatalf("capture = %q", got)
	}
	if len(rep.events) != 0 {
		t.Fatalf("unexpected events: %#v", rep.events)
	}
}

func TestYtdlpProgressWriterFinishAllMarksActiveRowsFailed(t *testing.T) {
	rep := &fakeProgressReporter{}
	w := newYtdlpProgressWriter("src", rep, nil, nil)
	_, _ = w.Write([]byte(ytdlpProgressMarker + `{"status":"downloading","filename":"file.mp4","downloaded_bytes":1,"total_bytes":10}` + "\n"))
	boom := errors.New("boom")
	w.finishAll(boom)
	last := rep.events[len(rep.events)-1]
	if last.kind != "done" || !errors.Is(last.err, boom) {
		t.Fatalf("last event = %#v", last)
	}
}

func TestFlexibleInt64AcceptsNumbersStringsAndNA(t *testing.T) {
	cases := map[string]int64{
		`123`:    123,
		`"456"`:  456,
		`1.9`:    1,
		`"2.5"`:  2,
		`null`:   0,
		`"NA"`:   0,
		`"N/A"`:  0,
		`"nope"`: 0,
	}
	for raw, want := range cases {
		var got flexibleInt64
		if err := got.UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if int64(got) != want {
			t.Fatalf("%s: got %d want %d", raw, got, want)
		}
	}
}

func TestMetadataPathArgsDefaultsToMetadataSubdir(t *testing.T) {
	args := metadataPathArgs("downloads", "")
	if len(args) == 0 || len(args)%2 != 0 {
		t.Fatalf("expected pairs of args, got %#v", args)
	}
	want := "infojson:" + filepath.Join("downloads", "_metadata")
	if args[0] != "--paths" || args[1] != want {
		t.Fatalf("first pair = %q,%q want --paths %q", args[0], args[1], want)
	}
	for i := 0; i < len(args); i += 2 {
		if args[i] != "--paths" {
			t.Fatalf("arg %d = %q, want --paths", i, args[i])
		}
		if !strings.Contains(args[i+1], ":"+filepath.Join("downloads", "_metadata")) {
			t.Fatalf("arg %d value = %q, missing target dir", i+1, args[i+1])
		}
	}
}

func TestMetadataPathArgsRespectsCustomAndDot(t *testing.T) {
	if got := metadataPathArgs("out", "."); got != nil {
		t.Fatalf("expected nil for opt-out, got %#v", got)
	}
	args := metadataPathArgs("out", "meta")
	if len(args) < 2 || args[1] != "infojson:"+filepath.Join("out", "meta") {
		t.Fatalf("unexpected args: %#v", args)
	}
	abs := filepath.Join(t.TempDir(), "side")
	args = metadataPathArgs("out", abs)
	if args[1] != "infojson:"+abs {
		t.Fatalf("absolute meta dir not preserved: %q", args[1])
	}
}
