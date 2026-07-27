package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sk3y04/provenance/internal/config"
	"github.com/sk3y04/provenance/internal/dispatcher"
	"github.com/sk3y04/provenance/internal/manifest"
)

// ---------------------------------------------------------------------------
// Scan & pick view
// ---------------------------------------------------------------------------

func (m *model) updateScanPick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.scan.awaitURL {
		switch msg.String() {
		case "esc":
			m.view = viewMain
			return m, nil
		case "enter":
			url := strings.TrimSpace(m.scan.urlInput.Value())
			if !looksLikeURL(url) {
				m.err = "enter a http(s) URL"
				return m, nil
			}
			m.scan.awaitURL = false
			m.scan.loading = true
			m.scan.sourceURL = url
			return m, m.scanLoadCmd(url)
		case "a":
			m.scan.showAdvanced = !m.scan.showAdvanced
			return m, nil
		}
		var cmd tea.Cmd
		m.scan.urlInput, cmd = m.scan.urlInput.Update(msg)
		return m, cmd
	}

	if m.scan.filter.active {
		switch msg.String() {
		case "esc":
			m.scan.filter.active = false
			m.scan.filter.input.Blur()
			m.scan.filter.input.SetValue("")
			return m, nil
		case "enter":
			m.scan.filter.active = false
			m.scan.filter.input.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.scan.filter.input, cmd = m.scan.filter.input.Update(msg)
		m.scan.cursor = 0
		return m, cmd
	}

	if m.scan.showAdvanced {
		return m.updateScanAdvanced(msg)
	}

	visible := m.scanVisibleIdx()
	switch msg.String() {
	case "esc", "backspace":
		m.view = viewMain
		return m, nil
	case "/":
		m.scan.filter.active = true
		m.scan.filter.input.Focus()
		return m, nil
	case "a":
		m.scan.showAdvanced = !m.scan.showAdvanced
		return m, nil
	case "up", "k":
		if m.scan.cursor > 0 {
			m.scan.cursor--
		}
	case "down", "j":
		if m.scan.cursor < len(visible)-1 {
			m.scan.cursor++
		}
	case " ":
		if len(visible) == 0 {
			return m, nil
		}
		idx := visible[m.scan.cursor]
		m.scan.checked[idx] = !m.scan.checked[idx]
	case "A":
		anyOff := false
		for _, idx := range visible {
			if !m.scan.checked[idx] {
				anyOff = true
				break
			}
		}
		for _, idx := range visible {
			m.scan.checked[idx] = anyOff
		}
	case "n":
		for k := range m.scan.checked {
			m.scan.checked[k] = false
		}
	case "i":
		for _, idx := range visible {
			m.scan.checked[idx] = !m.scan.checked[idx]
		}
	case "enter":
		urls := m.scanSelectedURLs()
		if len(urls) == 0 {
			m.err = "select at least one item with space"
			return m, nil
		}
		opts := m.scanBuildOpts()
		title := fmt.Sprintf("scan-pick %s (%d items)", trim(m.scan.sourceURL, 40), len(urls))
		return m, m.startRunner(title, urls, opts, nil)
	case "r":
		m.scan.loading = true
		m.scan.items = nil
		m.scan.checked = map[int]bool{}
		return m, m.scanLoadCmd(m.scan.sourceURL)
	}
	return m, nil
}

func (m *model) updateScanAdvanced(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.scan.advForm
	steps := len(advFieldSpecDefs)
	switch msg.String() {
	case "esc", "a":
		m.scan.showAdvanced = false
		f.step = 0
		return m, nil
	case "tab", "down":
		f.step = (f.step + 1) % steps
	case "shift+tab", "up":
		f.step = (f.step - 1 + steps) % steps
	case " ":
		_, isBool := f.spec(f.step)
		if isBool {
			if bp := f.boolPtr(f.step); bp != nil {
				*bp = !*bp
			}
		} else if inp := f.inputPtr(f.step); inp != nil {
			var cmd tea.Cmd
			*inp, cmd = inp.Update(msg)
			return m, cmd
		}
		return m, nil
	default:
		if inp := f.inputPtr(f.step); inp != nil && !f.specIsBool(f.step) {
			var cmd tea.Cmd
			*inp, cmd = inp.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	return m, nil
}

func (f *advOptsForm) specIsBool(idx int) bool {
	_, isBool := f.spec(idx)
	return isBool
}

func (m *model) scanBuildOpts() dispatcher.Options {
	cfg := config.Config{
		OutputDir:   "./downloads",
		Concurrency: 4,
		Quality:     "best",
	}
	if m.scan.showAdvanced {
		f := &m.scan.advForm
		if v := strings.TrimSpace(f.quality.Value()); v != "" {
			cfg.Quality = v
		}
		cfg.CookiesFromBrowser = strings.TrimSpace(f.cookiesBrowser.Value())
		cfg.AudioOnly = f.audioOnly
		cfg.NoArchive = f.noArchive
		cfg.IncludePosts = f.includePosts
		cfg.PostLimit, _ = strconv.Atoi(strings.TrimSpace(f.postLimit.Value()))
		cfg.OutputLayout = strings.TrimSpace(f.outputLayout.Value())
		cfg.OutputTemplate = strings.TrimSpace(f.outputTemplate.Value())
		minSize, _ := manifest.ParseSize(strings.TrimSpace(f.minSize.Value()))
		maxSize, _ := manifest.ParseSize(strings.TrimSpace(f.maxSize.Value()))
		speedLimit, _ := manifest.ParseSize(strings.TrimSpace(f.speedLimit.Value()))
		cfg.Filter = manifest.FilterOptions{
			IncludeExt:  manifest.ParseCSV(strings.TrimSpace(f.includeExt.Value())),
			ExcludeExt:  manifest.ParseCSV(strings.TrimSpace(f.excludeExt.Value())),
			MinSize:     minSize,
			MaxSize:     maxSize,
			TitleMatch:  strings.TrimSpace(f.titleMatch.Value()),
			TitleReject: strings.TrimSpace(f.titleExclude.Value()),
		}
		cfg.SpeedLimit = speedLimit
	}
	return dispatcher.Options{Config: cfg, RateLimiter: m.rateLimiter}
}

func (m *model) scanVisibleIdx() []int {
	q := strings.ToLower(strings.TrimSpace(m.scan.filter.input.Value()))
	out := make([]int, 0, len(m.scan.items))
	for i, it := range m.scan.items {
		if q == "" || strings.Contains(strings.ToLower(it.Title+" "+it.Filename+" "+it.URL), q) {
			out = append(out, i)
		}
	}
	return out
}

func (m *model) scanSelectedURLs() []string {
	out := make([]string, 0)
	for i, it := range m.scan.items {
		if m.scan.checked[i] {
			out = append(out, it.URL)
		}
	}
	return out
}

func (m *model) scanLoadCmd(url string) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		opts := m.scanBuildOpts()
		man, err := dispatcher.Scan(cctx, url, opts)
		return scanLoadedMsg{sourceURL: url, manifest: man, err: err}
	}
}
