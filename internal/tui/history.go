package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sk3y04/provenance/internal/dispatcher"
	"github.com/sk3y04/provenance/internal/history"
)

// ---------------------------------------------------------------------------
// History
// ---------------------------------------------------------------------------

func (m *model) loadHistoryCmd() tea.Cmd {
	return func() tea.Msg {
		runs, err := history.List()
		return historyLoadedMsg{runs: runs, err: err}
	}
}

func (m *model) visibleHistory() []history.Run {
	q := strings.ToLower(strings.TrimSpace(m.historyFilt.input.Value()))
	if q == "" {
		return m.history
	}
	out := make([]history.Run, 0, len(m.history))
	for _, run := range m.history {
		haystack := strings.ToLower(run.Title + " " + strings.Join(run.URLs, " ") + " " + run.Options.OutputDir)
		for _, f := range run.Files {
			haystack += " " + strings.ToLower(f.Path)
		}
		if strings.Contains(haystack, q) {
			out = append(out, run)
		}
	}
	return out
}

func (m *model) updateHistory(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.historyFilt.active {
		switch msg.String() {
		case "esc":
			m.historyFilt.active = false
			m.historyFilt.input.Blur()
			m.historyFilt.input.SetValue("")
			return m, nil
		case "enter":
			m.historyFilt.active = false
			m.historyFilt.input.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.historyFilt.input, cmd = m.historyFilt.input.Update(msg)
		m.historyCur = 0
		return m, cmd
	}

	visible := m.visibleHistory()
	if m.historyCur >= len(visible) && len(visible) > 0 {
		m.historyCur = 0
	}
	switch msg.String() {
	case "esc", "backspace", "h":
		m.view = viewMain
	case "/":
		m.historyFilt.active = true
		m.historyFilt.input.Focus()
		return m, nil
	case "up", "k":
		if m.historyCur > 0 {
			m.historyCur--
		}
	case "down", "j":
		if m.historyCur < len(visible)-1 {
			m.historyCur++
		}
	case "r", "enter":
		if len(visible) == 0 {
			return m, nil
		}
		run := visible[m.historyCur]
		if len(run.URLs) == 0 {
			m.err = "history record has no URLs to rerun"
			return m, nil
		}
		opts := dispatcher.Options{Config: run.Options, RateLimiter: m.rateLimiter}
		title := "rerun " + run.Title
		return m, m.startRunner(title, append([]string(nil), run.URLs...), opts, nil)
	case "o":
		if len(visible) == 0 {
			return m, nil
		}
		path := revealPathForRun(visible[m.historyCur])
		if path == "" {
			m.err = "no destination path recorded for this history item"
			return m, nil
		}
		if err := openPath(path); err != nil {
			m.err = err.Error()
		} else {
			m.info = "opened " + path
		}
	case "d":
		if len(visible) == 0 {
			return m, nil
		}
		if err := history.Delete(visible[m.historyCur].ID); err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.info = "deleted history item"
		return m, m.loadHistoryCmd()
	}
	return m, nil
}
