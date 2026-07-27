package tui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gen2brain/beeep"

	"github.com/sk3y04/provenance/internal/dispatcher"
	"github.com/sk3y04/provenance/internal/history"
	"github.com/sk3y04/provenance/internal/session"
)

// ---------------------------------------------------------------------------
// Runner: live download view
// ---------------------------------------------------------------------------

func (m *model) updateRunner(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace", "h", "q":
		if m.runner.done {
			m.view = viewMain
			m.runner = runnerState{}
			return m, m.loadSessionsCmd()
		}
	}
	return m, nil
}

// startRunner sets up the runner view and kicks off the download in a
// goroutine that streams events back to the TUI.
func (m *model) startRunner(title string, urls []string, opts dispatcher.Options, sess *session.Session) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.view = viewRunner
	bar := progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage())
	bar.Width = 30
	m.runner = runnerState{
		title:        title,
		urls:         append([]string(nil), urls...),
		options:      opts.Config,
		startAt:      time.Now(),
		cancel:       cancel,
		maxLines:     200,
		files:        map[string]*fileProgress{},
		bar:          bar,
		lastSampleAt: time.Now(),
	}

	prog := m.program
	rep := &teaReporter{prog: prog, sess: sess}
	opts.Reporter = rep
	opts.FileProgress = &fileReporter{prog: prog}

	// Hijack both stdout and stderr so dispatcher / yt-dlp output shows up
	// in the runner log instead of trashing the TUI alt-screen.
	restore := redirectStdStreams(prog)

	go func() {
		defer cancel()
		defer restore()
		var firstErr error
		for _, u := range urls {
			if err := dispatcher.Dispatch(ctx, u, opts); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		prog.Send(runnerDoneMsg{err: firstErr})
	}()

	return tickEvery(time.Second)
}

// sampleThroughput updates speedBps based on bytes written across all
// in-flight + recently completed files since the previous tick.
func (m *model) sampleThroughput() {
	var total int64
	for _, fp := range m.runner.files {
		total += fp.written
	}
	now := time.Now()
	dt := now.Sub(m.runner.lastSampleAt).Seconds()
	if dt <= 0 {
		return
	}
	delta := total - m.runner.lastSampleBytes
	if delta < 0 {
		delta = 0
	}
	// Smooth with a simple EMA so the number doesn't jitter wildly.
	instant := float64(delta) / dt
	if m.runner.speedBps == 0 {
		m.runner.speedBps = instant
	} else {
		m.runner.speedBps = 0.6*m.runner.speedBps + 0.4*instant
	}
	m.runner.lastSampleAt = now
	m.runner.lastSampleBytes = total
}

// pruneFinishedFiles drops finished file rows older than ~3s once we have
// more than `keep` rows total, so the live list stays bounded during big runs.
func (m *model) pruneFinishedFiles(keep int) {
	if len(m.runner.fileOrder) <= keep {
		return
	}
	cutoff := time.Now().Add(-3 * time.Second)
	kept := make([]string, 0, len(m.runner.fileOrder))
	for _, u := range m.runner.fileOrder {
		fp, ok := m.runner.files[u]
		if !ok {
			continue
		}
		if fp.done && !fp.doneAt.IsZero() && fp.doneAt.Before(cutoff) {
			delete(m.runner.files, u)
			continue
		}
		kept = append(kept, u)
	}
	m.runner.fileOrder = kept
}

func (m *model) appendLog(line string) {
	if line == "" {
		return
	}
	if strings.Contains(line, "PROVENANCE_YTDLP_PROGRESS:") {
		return
	}
	m.runner.logs = append(m.runner.logs, line)
	maxLines := m.runner.maxLines
	if maxLines <= 0 {
		maxLines = 200
	}
	if len(m.runner.logs) > maxLines {
		m.runner.logs = m.runner.logs[len(m.runner.logs)-maxLines:]
	}
}

func (m *model) saveHistoryRun() error {
	if m.runner.title == "" || m.runner.startAt.IsZero() {
		return nil
	}
	files := make([]history.File, 0, len(m.runner.fileOrder))
	seen := map[string]struct{}{}
	for _, u := range m.runner.fileOrder {
		fp := m.runner.files[u]
		if fp == nil || fp.dest == "" {
			continue
		}
		key := fp.dest + "\x00" + fp.url
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		entry := history.File{
			URL:     fp.url,
			Path:    fp.dest,
			Size:    fp.written,
			Success: fp.err == nil,
		}
		if fp.err != nil {
			entry.Error = fp.err.Error()
		}
		files = append(files, entry)
	}
	var errText string
	if m.runner.err != nil {
		errText = m.runner.err.Error()
	}
	_, err := history.Add(history.Run{
		Title:       m.runner.title,
		URLs:        append([]string(nil), m.runner.urls...),
		Options:     m.runner.options,
		StartedAt:   m.runner.startAt,
		CompletedAt: time.Now(),
		Duration:    time.Since(m.runner.startAt),
		Succeeded:   m.runner.ok,
		Failed:      m.runner.failed,
		Skipped:     m.runner.skipped,
		TotalBytes:  historyBytes(files),
		Files:       files,
		Error:       errText,
	})
	return err
}

func historyBytes(files []history.File) int64 {
	var total int64
	for _, f := range files {
		if f.Success && f.Size > 0 {
			total += f.Size
		}
	}
	return total
}

// notifyComplete fires a desktop toast when a run finishes. Best-effort.
func notifyComplete(r runnerState) {
	title := "provenance - done"
	if r.err != nil {
		title = "provenance - failed"
	}
	body := fmt.Sprintf("%s\n%d ok, %d failed, %d skipped",
		r.title, r.ok, r.failed, r.skipped)
	_ = beeep.Notify(title, body, "")
}

// teaReporter implements dispatcher.Reporter. It forwards events to the TUI
// and, optionally, to a session for persistence.
type teaReporter struct {
	prog *tea.Program
	sess *session.Session
}

func (r *teaReporter) Queue(url, source string) {
	if r.sess != nil {
		r.sess.Queue(url, source)
	}
	r.prog.Send(runnerEventMsg{kind: "queue", url: url, note: source})
}
func (r *teaReporter) Start(url string) {
	if r.sess != nil {
		r.sess.Start(url)
	}
	r.prog.Send(runnerEventMsg{kind: "start", url: url})
}
func (r *teaReporter) Success(url string) {
	if r.sess != nil {
		r.sess.Success(url)
	}
	r.prog.Send(runnerEventMsg{kind: "ok", url: url})
}
func (r *teaReporter) Failure(url string, err error) {
	if r.sess != nil {
		r.sess.Failure(url, err)
	}
	note := ""
	if err != nil {
		note = err.Error()
	}
	r.prog.Send(runnerEventMsg{kind: "fail", url: url, note: note})
}
func (r *teaReporter) Skip(url, reason string) {
	if r.sess != nil {
		r.sess.Skip(url, reason)
	}
	r.prog.Send(runnerEventMsg{kind: "skip", url: url, note: reason})
}

// fileReporter implements downloader.ProgressReporter and forwards per-file
// byte-level updates to the TUI program.
type fileReporter struct {
	prog *tea.Program
}

func (r *fileReporter) OnStart(url, dest string, total int64) {
	r.prog.Send(fileStartMsg{url: url, dest: dest, total: total})
}
func (r *fileReporter) OnProgress(url string, written, total int64) {
	r.prog.Send(fileProgressMsg{url: url, written: written, total: total})
}
func (r *fileReporter) OnDone(url string, err error) {
	r.prog.Send(fileDoneMsg{url: url, err: err})
}

// redirectStdStreams replaces os.Stdout and os.Stderr with pipes whose lines
// are forwarded to the TUI as runnerLogMsg. This keeps yt-dlp / dispatcher
// output from corrupting the alt-screen rendering. It returns a function that
// restores the originals and closes the pipes.
func redirectStdStreams(prog *tea.Program) func() {
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, err1 := os.Pipe()
	rErr, wErr, err2 := os.Pipe()
	if err1 != nil || err2 != nil {
		if rOut != nil {
			_ = rOut.Close()
		}
		if wOut != nil {
			_ = wOut.Close()
		}
		if rErr != nil {
			_ = rErr.Close()
		}
		if wErr != nil {
			_ = wErr.Close()
		}
		return func() {}
	}
	os.Stdout = wOut
	os.Stderr = wErr

	var wg sync.WaitGroup
	pump := func(r *os.File) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := stripANSI(sc.Text())
			if line == "" {
				continue
			}
			// Defensively swallow raw progress markers - the extractor strips
			// them, but if any leak through (e.g. stdout buffering quirks) we
			// don't want to dump JSON into the log.
			if strings.Contains(line, "PROVENANCE_YTDLP_PROGRESS:") {
				continue
			}
			prog.Send(runnerLogMsg{line: line})
		}
	}
	wg.Add(2)
	go pump(rOut)
	go pump(rErr)

	return func() {
		os.Stdout = origOut
		os.Stderr = origErr
		_ = wOut.Close()
		_ = wErr.Close()
		_ = rOut.Close()
		_ = rErr.Close()
		wg.Wait()
	}
}

// stripANSI removes most ANSI escape codes so progress-bar output doesn't
// scramble the runner log. It's intentionally minimal.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) {
				if s[j] >= 0x40 && s[j] <= 0x7E {
					break
				}
				j++
			}
			i = j
			continue
		}
		if c == '\r' {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
