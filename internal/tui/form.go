package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sk3y04/provenance/internal/config"
	"github.com/sk3y04/provenance/internal/dispatcher"
	"github.com/sk3y04/provenance/internal/manifest"
	"github.com/sk3y04/provenance/internal/session"
)

// ---------------------------------------------------------------------------
// New download form
// ---------------------------------------------------------------------------

func (m *model) formInputs() []*textinput.Model {
	basic := []*textinput.Model{
		&m.form.url,
		&m.form.output,
		&m.form.concurrency,
		&m.form.cookies,
		&m.form.sessionName,
	}
	if !m.showAdvanced {
		return basic
	}
	specs := m.advFieldSpecs()
	for _, s := range specs {
		if !s.isBool {
			basic = append(basic, s.input)
		}
	}
	return basic
}

var formLabels = []string{
	"URL",
	"Output dir",
	"Concurrency",
	"Cookies file",
	"Session name",
}

func (m *model) updateNewDownload(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	inputs := m.formInputs()
	totalSteps := m.formStepCount()

	// Cookie picker active - handle its own navigation.
	if m.cookiePick.active {
		return m.updateCookiePick(msg, inputs)
	}

	switch msg.String() {
	case "esc":
		m.view = viewMain
		return m, nil
	case "ctrl+f":
		if m.formStep == 3 {
			m.cookiePick.files = nil
			m.cookiePick.cursor = 0
			return m, func() tea.Msg {
				files := findCookieFiles()
				return cookiesFoundMsg{files: files}
			}
		}
		return m, nil
	case "a":
		m.showAdvanced = !m.showAdvanced
		if !m.showAdvanced && m.formStep >= basicStepCount {
			m.formStep = basicStepCount - 1
		}
		return m, nil
	case "tab", "down":
		if m.formStep < len(inputs) {
			inputs[m.formStep].Blur()
		}
		m.formStep = (m.formStep + 1) % totalSteps
		idx := m.formStep
		if idx >= basicStepCount && m.showAdvanced {
			specs := m.advFieldSpecs()
			ai := idx - basicStepCount
			if !specs[ai].isBool {
				specs[ai].input.Focus()
			}
		} else {
			inputs[m.formStep].Focus()
		}
		return m, nil
	case "shift+tab", "up":
		if m.formStep < len(inputs) {
			inputs[m.formStep].Blur()
		}
		m.formStep = (m.formStep - 1 + totalSteps) % totalSteps
		idx := m.formStep
		if idx >= basicStepCount && m.showAdvanced {
			specs := m.advFieldSpecs()
			ai := idx - basicStepCount
			if !specs[ai].isBool {
				specs[ai].input.Focus()
			}
		} else {
			inputs[m.formStep].Focus()
		}
		return m, nil
	case " ":
		if m.showAdvanced && m.formStep >= basicStepCount {
			specs := m.advFieldSpecs()
			ai := m.formStep - basicStepCount
			if specs[ai].isBool && specs[ai].boolP != nil {
				*specs[ai].boolP = !*specs[ai].boolP
				return m, nil
			}
		}
		// If space is pressed on a text input, let it update normally.
		if m.formStep < len(inputs) {
			var cmd tea.Cmd
			*inputs[m.formStep], cmd = inputs[m.formStep].Update(msg)
			return m, cmd
		}
		return m, nil
	case "ctrl+s":
		return m.submitForm()
	case "enter":
		if m.formStep < totalSteps-1 {
			if m.formStep < len(inputs) {
				inputs[m.formStep].Blur()
			}
			m.formStep++
			idx := m.formStep
			if idx >= basicStepCount && m.showAdvanced {
				specs := m.advFieldSpecs()
				ai := idx - basicStepCount
				if !specs[ai].isBool {
					specs[ai].input.Focus()
				}
			} else if m.formStep < len(inputs) {
				inputs[m.formStep].Focus()
			}
			return m, nil
		}
		return m.submitForm()
	}

	if m.formStep < len(inputs) {
		prevURL := strings.TrimSpace(m.form.url.Value())
		var cmd tea.Cmd
		*inputs[m.formStep], cmd = inputs[m.formStep].Update(msg)
		cmds := []tea.Cmd{cmd}

		newURL := strings.TrimSpace(m.form.url.Value())
		if m.formStep == 0 && newURL != prevURL && looksLikeURL(newURL) {
			m.preview = scanPreview{url: newURL, loading: false}
			cmds = append(cmds, tea.Tick(700*time.Millisecond, func(time.Time) tea.Msg {
				return scanPreviewTick{url: newURL}
			}))
		} else if newURL == "" {
			m.preview = scanPreview{}
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func (m *model) scanPreviewCmd(url string) tea.Cmd {
	ctx := m.ctx
	cookiesFile := strings.TrimSpace(m.form.cookies.Value())
	return func() tea.Msg {
		// Use a derived context with a generous timeout so a slow site can't
		// stall the form forever.
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		man, err := dispatcher.Scan(cctx, url, dispatcher.Options{
			Config: config.Config{
				OutputDir:   "./downloads",
				CookiesFile: cookiesFile,
			},
			RateLimiter: m.rateLimiter,
		})
		if err != nil {
			return scanPreviewMsg{url: url, err: err}
		}
		var size int64
		for _, it := range man.Items {
			size += it.Size
		}
		return scanPreviewMsg{url: url, count: len(man.Items), size: size, site: man.Site}
	}
}

func (m *model) submitForm() (tea.Model, tea.Cmd) {
	url := strings.TrimSpace(m.form.url.Value())
	if url == "" {
		m.err = "URL is required"
		return m, nil
	}
	out := strings.TrimSpace(m.form.output.Value())
	if out == "" {
		out = "./downloads"
	}
	conc := 4
	if v := strings.TrimSpace(m.form.concurrency.Value()); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			conc = n
		}
	}
	cfg := config.Config{
		OutputDir:   out,
		CookiesFile: strings.TrimSpace(m.form.cookies.Value()),
		Concurrency: conc,
		Quality:     "best",
	}
	if m.showAdvanced {
		if v := strings.TrimSpace(m.form.quality.Value()); v != "" {
			cfg.Quality = v
		}
		cfg.CookiesFromBrowser = strings.TrimSpace(m.form.cookiesBrowser.Value())
		cfg.AudioOnly = m.form.audioOnly
		cfg.NoArchive = m.form.noArchive
		cfg.IncludePosts = m.form.includePosts
		cfg.PostLimit, _ = strconv.Atoi(strings.TrimSpace(m.form.postLimit.Value()))
		cfg.OutputLayout = strings.TrimSpace(m.form.outputLayout.Value())
		cfg.OutputTemplate = strings.TrimSpace(m.form.outputTemplate.Value())
		minSize, _ := manifest.ParseSize(strings.TrimSpace(m.form.minSize.Value()))
		maxSize, _ := manifest.ParseSize(strings.TrimSpace(m.form.maxSize.Value()))
		speedLimit, _ := manifest.ParseSize(strings.TrimSpace(m.form.speedLimit.Value()))
		cfg.Filter = manifest.FilterOptions{
			IncludeExt:  manifest.ParseCSV(strings.TrimSpace(m.form.includeExt.Value())),
			ExcludeExt:  manifest.ParseCSV(strings.TrimSpace(m.form.excludeExt.Value())),
			MinSize:     minSize,
			MaxSize:     maxSize,
			TitleMatch:  strings.TrimSpace(m.form.titleMatch.Value()),
			TitleReject: strings.TrimSpace(m.form.titleExclude.Value()),
		}
		cfg.SpeedLimit = speedLimit
	}
	opts := dispatcher.Options{
		Config:      cfg,
		RateLimiter: m.rateLimiter,
	}
	var sess *session.Session
	if name := strings.TrimSpace(m.form.sessionName.Value()); name != "" {
		s, err := session.OpenOrCreate(name, opts.Config)
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		if _, err := s.AddURLs([]string{url}, "tui"); err != nil {
			m.err = err.Error()
			return m, nil
		}
		sess = s
	}
	title := "grab " + url
	return m, m.startRunner(title, []string{url}, opts, sess)
}

func (m *model) updateCookiePick(msg tea.KeyMsg, inputs []*textinput.Model) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+f":
		m.cookiePick.active = false
		return m, nil
	case "up", "k":
		if m.cookiePick.cursor > 0 {
			m.cookiePick.cursor--
		}
	case "down", "j":
		if m.cookiePick.cursor < len(m.cookiePick.files)-1 {
			m.cookiePick.cursor++
		}
	case "enter":
		if len(m.cookiePick.files) > 0 {
			file := m.cookiePick.files[m.cookiePick.cursor]
			m.form.cookies.SetValue(file)
		}
		m.cookiePick.active = false
		return m, nil
	}
	// Block normal form input while picker is open.
	return m, nil
}
