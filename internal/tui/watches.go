package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sk3y04/provenance/internal/config"
	"github.com/sk3y04/provenance/internal/dispatcher"
	"github.com/sk3y04/provenance/internal/session"
	"github.com/sk3y04/provenance/internal/watch"
)

// ---------------------------------------------------------------------------
// Watches
// ---------------------------------------------------------------------------

func (m *model) loadWatchesCmd() tea.Cmd {
	return func() tea.Msg {
		subs, err := watch.List()
		return watchesLoadedMsg{subs: subs, err: err}
	}
}

func (m *model) visibleWatches() []watch.Subscription {
	q := strings.ToLower(strings.TrimSpace(m.watchesFilt.input.Value()))
	if q == "" {
		return m.watches
	}
	out := make([]watch.Subscription, 0, len(m.watches))
	for _, s := range m.watches {
		if strings.Contains(strings.ToLower(s.Name), q) || strings.Contains(strings.ToLower(s.URL), q) {
			out = append(out, s)
		}
	}
	return out
}

type watchAddForm struct {
	name textinput.Model
	url  textinput.Model
	step int
}

func newWatchAddForm() watchAddForm {
	mk := func(placeholder, value string) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.SetValue(value)
		ti.Width = 60
		return ti
	}
	f := watchAddForm{
		name: mk("watch name", ""),
		url:  mk("https://...", ""),
	}
	f.name.Focus()
	return f
}

func (m *model) updateWatches(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.watchesFilt.active {
		switch msg.String() {
		case "esc":
			m.watchesFilt.active = false
			m.watchesFilt.input.Blur()
			m.watchesFilt.input.SetValue("")
			return m, nil
		case "enter":
			m.watchesFilt.active = false
			m.watchesFilt.input.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.watchesFilt.input, cmd = m.watchesFilt.input.Update(msg)
		m.watchesCur = 0
		return m, cmd
	}

	if m.view == viewWatches && m.watchAddForm != nil {
		return m.updateWatchAdd(msg)
	}

	visible := m.visibleWatches()
	if m.watchesCur >= len(visible) && len(visible) > 0 {
		m.watchesCur = 0
	}
	switch msg.String() {
	case "esc", "backspace", "h":
		m.view = viewMain
		m.watchPending = pendingDelete{}
	case "/":
		m.watchesFilt.active = true
		m.watchesFilt.input.Focus()
		return m, nil
	case "up", "k":
		if m.watchesCur > 0 {
			m.watchesCur--
		}
	case "down", "j":
		if m.watchesCur < len(visible)-1 {
			m.watchesCur++
		}
	case "enter":
		if len(visible) == 0 {
			return m, nil
		}
		sub := visible[m.watchesCur]
		sess, err := session.OpenOrCreate("watch-"+sub.Name, sub.Options)
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		opts := dispatcher.Options{Config: sub.Options, RateLimiter: m.rateLimiter}
		title := fmt.Sprintf("watch %q -> %s", sub.Name, sub.URL)
		_ = watch.MarkRun(sub.Name)
		return m, m.startRunner(title, []string{sub.URL}, opts, sess)
	case "n":
		f := newWatchAddForm()
		m.watchAddForm = &f
		m.info = "enter new watch: fill name + URL, then ctrl+s to save"
		return m, nil
	case "d":
		if len(visible) == 0 {
			return m, nil
		}
		name := visible[m.watchesCur].Name
		if m.watchPending.name == name && time.Since(m.watchPending.at) < confirmWindow {
			if err := watch.Remove(name); err != nil {
				m.err = err.Error()
			} else {
				m.info = "removed watch " + name
			}
			m.watchPending = pendingDelete{}
			return m, m.loadWatchesCmd()
		}
		m.watchPending = pendingDelete{name: name, at: time.Now()}
		m.info = fmt.Sprintf("press d again within %ds to remove %q", int(confirmWindow.Seconds()), name)
		return m, nil
	}
	return m, nil
}

func (m *model) updateWatchAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.watchAddForm
	switch msg.String() {
	case "esc":
		m.watchAddForm = nil
		m.info = ""
		return m, m.loadWatchesCmd()
	case "ctrl+s":
		return m.submitWatchAdd()
	case "tab", "down":
		f.step = (f.step + 1) % 2
		if f.step == 0 {
			f.name.Focus()
			f.url.Blur()
		} else {
			f.name.Blur()
			f.url.Focus()
		}
	case "shift+tab", "up":
		f.step = (f.step - 1 + 2) % 2
		if f.step == 0 {
			f.name.Focus()
			f.url.Blur()
		} else {
			f.name.Blur()
			f.url.Focus()
		}
	case "enter":
		if f.step == 0 {
			f.name.Blur()
			f.url.Focus()
			f.step = 1
		} else {
			return m.submitWatchAdd()
		}
	default:
		if f.step == 0 {
			var cmd tea.Cmd
			f.name, cmd = f.name.Update(msg)
			return m, cmd
		} else {
			var cmd tea.Cmd
			f.url, cmd = f.url.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *model) submitWatchAdd() (tea.Model, tea.Cmd) {
	f := m.watchAddForm
	if f == nil {
		return m, nil
	}
	name := strings.TrimSpace(f.name.Value())
	if name == "" {
		m.err = "watch name is required"
		return m, nil
	}
	url := strings.TrimSpace(f.url.Value())
	if !looksLikeURL(url) {
		m.err = "valid URL is required"
		return m, nil
	}
	cfg := config.Config{
		OutputDir:   "./downloads",
		Concurrency: 4,
		Quality:     "best",
	}
	if err := watch.Add(name, url, cfg); err != nil {
		m.err = err.Error()
		return m, nil
	}
	m.info = "added watch " + name
	m.watchAddForm = nil
	return m, m.loadWatchesCmd()
}
