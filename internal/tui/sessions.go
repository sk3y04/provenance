package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sk3y04/provenance/internal/dispatcher"
	"github.com/sk3y04/provenance/internal/session"
)

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

func (m *model) loadSessionsCmd() tea.Cmd {
	return func() tea.Msg {
		infos, err := session.List()
		return sessionsLoadedMsg{infos: infos, err: err}
	}
}

func (m *model) visibleSessions() []session.Info {
	q := strings.ToLower(strings.TrimSpace(m.sessFilter.input.Value()))
	if q == "" {
		return m.sessions
	}
	out := make([]session.Info, 0, len(m.sessions))
	for _, s := range m.sessions {
		if strings.Contains(strings.ToLower(s.Name), q) {
			out = append(out, s)
		}
	}
	return out
}

func (m *model) updateSessions(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sessFilter.active {
		switch msg.String() {
		case "esc":
			m.sessFilter.active = false
			m.sessFilter.input.Blur()
			m.sessFilter.input.SetValue("")
			return m, nil
		case "enter":
			m.sessFilter.active = false
			m.sessFilter.input.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.sessFilter.input, cmd = m.sessFilter.input.Update(msg)
		m.sessCursor = 0
		return m, cmd
	}

	visible := m.visibleSessions()
	if m.sessCursor >= len(visible) && len(visible) > 0 {
		m.sessCursor = 0
	}
	switch msg.String() {
	case "esc", "backspace", "h":
		m.view = viewMain
		m.sessPending = pendingDelete{}
	case "/":
		m.sessFilter.active = true
		m.sessFilter.input.Focus()
		return m, nil
	case "up", "k":
		if m.sessCursor > 0 {
			m.sessCursor--
		}
	case "down", "j":
		if m.sessCursor < len(visible)-1 {
			m.sessCursor++
		}
	case "enter":
		if len(visible) == 0 {
			return m, nil
		}
		name := visible[m.sessCursor].Name
		return m, func() tea.Msg {
			s, err := session.Load(name)
			return sessionLoadedMsg{s: s, err: err}
		}
	case "r":
		if len(visible) == 0 {
			return m, nil
		}
		return m.startSessionResume(visible[m.sessCursor].Name, false)
	case "R":
		if len(visible) == 0 {
			return m, nil
		}
		return m.startSessionResume(visible[m.sessCursor].Name, true)
	case "e":
		if len(visible) == 0 {
			return m, nil
		}
		return m.exportSession(visible[m.sessCursor].Name)
	case "f":
		if len(visible) == 0 {
			return m, nil
		}
		name := visible[m.sessCursor].Name
		if m.sessSelected != nil && m.sessSelected.Name == name {
			m.sessSelected = nil
			return m, nil
		}
		return m, func() tea.Msg {
			s, err := session.Load(name)
			return sessionLoadedMsg{s: s, err: err}
		}
	case "d":
		if len(visible) == 0 {
			return m, nil
		}
		name := visible[m.sessCursor].Name
		if m.sessPending.name == name && time.Since(m.sessPending.at) < confirmWindow {
			if err := session.Delete(name); err != nil {
				m.err = err.Error()
			} else {
				m.info = "deleted session " + name
			}
			m.sessPending = pendingDelete{}
			return m, m.loadSessionsCmd()
		}
		m.sessPending = pendingDelete{name: name, at: time.Now()}
		m.info = fmt.Sprintf("press d again within %ds to delete %q", int(confirmWindow.Seconds()), name)
		return m, nil
	}
	return m, nil
}

func (m *model) updateSessionDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace", "h":
		m.view = viewSessions
		m.sessSelected = nil
		return m, m.loadSessionsCmd()
	case "r":
		return m.startSessionResume(m.sessSelected.Name, false)
	case "R":
		return m.startSessionResume(m.sessSelected.Name, true)
	case "e":
		return m.exportSession(m.sessSelected.Name)
	case "f":
		urls := m.sessSelected.URLsByStatus(session.StatusFailed)
		if len(urls) == 0 {
			m.info = "no failed URLs in this session"
			return m, nil
		}
		return m.startFailedExport(m.sessSelected)
	}
	return m, nil
}

func (m *model) exportSession(name string) (tea.Model, tea.Cmd) {
	s, err := session.Load(name)
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	exportPath := filepath.Join(".", name+".export.json")
	if err := os.WriteFile(exportPath, data, 0o644); err != nil {
		m.err = err.Error()
		return m, nil
	}
	m.info = fmt.Sprintf("exported %s to %s", name, exportPath)
	return m, nil
}

func (m *model) startFailedExport(s *session.Session) (tea.Model, tea.Cmd) {
	urls := s.URLsByStatus(session.StatusFailed)
	if len(urls) == 0 {
		m.info = "no failed URLs"
		return m, nil
	}
	opts := dispatcher.Options{Config: s.Options, RateLimiter: m.rateLimiter}
	title := fmt.Sprintf("retry %s failed (%d urls)", s.Name, len(urls))
	return m, m.startRunner(title, urls, opts, s)
}

func (m *model) startSessionResume(name string, retryFailed bool) (tea.Model, tea.Cmd) {
	s, err := session.Load(name)
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	if !retryFailed {
		if _, err := s.ResetRunning(); err != nil {
			m.err = err.Error()
			return m, nil
		}
	}
	var urls []string
	if retryFailed {
		urls = s.URLsByStatus(session.StatusFailed)
	} else {
		urls = s.URLsByStatus(session.StatusPending, session.StatusFailed)
	}
	if len(urls) == 0 {
		m.info = "nothing to do for session " + name
		return m, nil
	}
	opts := dispatcher.Options{Config: s.Options, RateLimiter: m.rateLimiter}
	title := fmt.Sprintf("session %q (%d url(s))", name, len(urls))
	return m, m.startRunner(title, urls, opts, s)
}
