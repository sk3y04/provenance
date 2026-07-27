package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sk3y04/provenance/internal/catalog"
	"github.com/sk3y04/provenance/internal/diagnose"
)

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.view == viewRunner && !m.runner.done && m.runner.cancel != nil {
				m.runner.cancel()
				m.appendLog("[provenance] cancel requested...")
				return m, nil
			}
			return m, tea.Quit
		case "Q":
			return m, tea.Quit
		}
		return m.updateView(msg)

	case sessionsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.sessions = msg.infos
			if m.sessCursor >= len(m.visibleSessions()) {
				m.sessCursor = 0
			}
			m.refreshResumeCandidate()
		}
		return m, nil

	case sessionLoadedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.sessSelected = msg.s
		m.view = viewSessionDetail
		return m, nil

	case watchesLoadedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.watches = msg.subs
			if m.watchesCur >= len(m.visibleWatches()) {
				m.watchesCur = 0
			}
		}
		return m, nil

	case historyLoadedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.history = msg.runs
			if m.historyCur >= len(m.visibleHistory()) {
				m.historyCur = 0
			}
		}
		return m, nil

	case scanPreviewTick:
		// Debounce: only run if the URL field still matches.
		if strings.TrimSpace(m.form.url.Value()) != msg.url || msg.url == "" {
			return m, nil
		}
		if m.preview.loading && m.preview.url == msg.url {
			return m, nil
		}
		m.preview = scanPreview{url: msg.url, loading: true}
		return m, m.scanPreviewCmd(msg.url)

	case scanPreviewMsg:
		if msg.canceled || strings.TrimSpace(m.form.url.Value()) != msg.url {
			return m, nil
		}
		m.preview.loading = false
		m.preview.url = msg.url
		m.preview.count = msg.count
		m.preview.size = msg.size
		m.preview.site = msg.site
		if msg.err != nil {
			m.preview.err = msg.err.Error()
		} else {
			m.preview.err = ""
		}
		return m, nil

	case scanLoadedMsg:
		m.scan.loading = false
		m.scan.sourceURL = msg.sourceURL
		if msg.err != nil {
			m.scan.err = msg.err.Error()
			return m, nil
		}
		m.scan.err = ""
		m.scan.items = msg.manifest.Items
		m.scan.site = msg.manifest.Site
		m.scan.checked = make(map[int]bool, len(m.scan.items))
		for i := range m.scan.items {
			m.scan.checked[i] = true
		}
		m.scan.cursor = 0
		return m, nil

	case runnerEventMsg:
		switch msg.kind {
		case "queue":
			m.runner.queued++
		case "start":
			m.runner.running++
			m.appendLog(fmt.Sprintf("▶ %s", trim(msg.url, 100)))
		case "ok":
			if m.runner.running > 0 {
				m.runner.running--
			}
			m.runner.ok++
			m.appendLog(fmt.Sprintf("✓ %s", trim(msg.url, 100)))
		case "fail":
			if m.runner.running > 0 {
				m.runner.running--
			}
			m.runner.failed++
			line := fmt.Sprintf("✗ %s", trim(msg.url, 100))
			if msg.note != "" {
				line += " - " + trim(msg.note, 120)
			}
			m.appendLog(line)
		case "skip":
			m.runner.skipped++
			m.appendLog(fmt.Sprintf("⤼ skip %s (%s)", trim(msg.url, 80), msg.note))
		}
		return m, nil

	case runnerLogMsg:
		m.appendLog(msg.line)
		return m, nil

	case fileStartMsg:
		if m.runner.files == nil {
			m.runner.files = map[string]*fileProgress{}
		}
		if _, exists := m.runner.files[msg.url]; !exists {
			m.runner.fileOrder = append(m.runner.fileOrder, msg.url)
		}
		m.runner.files[msg.url] = &fileProgress{
			url:       msg.url,
			dest:      msg.dest,
			total:     msg.total,
			startedAt: time.Now(),
		}
		return m, nil

	case fileProgressMsg:
		if fp, ok := m.runner.files[msg.url]; ok {
			fp.written = msg.written
			if msg.total > 0 {
				fp.total = msg.total
			}
		}
		return m, nil

	case fileDoneMsg:
		if fp, ok := m.runner.files[msg.url]; ok {
			fp.done = true
			fp.doneAt = time.Now()
			fp.err = msg.err
			if msg.err == nil && fp.total > 0 {
				fp.written = fp.total
			}
		}
		return m, nil

	case cookiesFoundMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.cookiePick.files = msg.files
		m.cookiePick.cursor = 0
		m.cookiePick.active = true
		return m, nil

	case runnerDoneMsg:
		m.runner.done = true
		m.runner.err = msg.err
		if msg.err != nil {
			m.appendLog("[provenance] FAILED: " + msg.err.Error())
			if hint := diagnose.Hint(msg.err); hint != "" {
				m.appendLog("[provenance] hint: " + hint)
			}
		} else {
			m.appendLog("[provenance] finished")
		}
		if !m.runner.notified {
			m.runner.notified = true
			go notifyComplete(m.runner)
		}
		if err := m.saveHistoryRun(); err != nil {
			m.appendLog("[provenance] WARNING: could not save history: " + err.Error())
		}
		return m, nil

	case tickMsg:
		if m.view == viewRunner && !m.runner.done {
			m.sampleThroughput()
			m.pruneFinishedFiles(20)
			return m, tickEvery(time.Second)
		}
		return m, nil

	case archiveSearchMsg:
		if !catalog.HasStore() {
			m.err = "Vault not initialized — run 'provenance vault init' first"
			return m, nil
		}
		m.archiveLoading = true
		return m, func() tea.Msg {
			result, err := catalog.Store().Search(context.Background(), msg.query, catalog.SearchOptions{Limit: 20})
			return archiveResultsMsg{query: msg.query, result: result, err: err}
		}

	case archiveResultsMsg:
		m.archiveLoading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.archiveResults = make([]archiveSearchHit, 0)
		if msg.result != nil {
			for _, h := range msg.result.Hits {
				m.archiveResults = append(m.archiveResults, archiveSearchHit{
					Title:      h.Title,
					Headline:   h.Headline,
					URL:        h.URL,
					Collection: h.CollectionName,
					Revision:   h.RevisionID,
					Date:       h.CapturedAt.Format("2006-01-02"),
				})
			}
		}
		m.archiveCur = 0
		return m, nil

	case collectionsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.collections = msg.cols
		return m, nil

	case collectionSyncedMsg:
		m.info = fmt.Sprintf("Synced %s — %d new, %d skipped", msg.name, msg.result.New, msg.result.Skipped)
		return m, m.loadCollectionsCmd()

	case archiveCollectionMsg:
		m.info = fmt.Sprintf("Archiving collection %s... (see provenance archive collection %s)", msg.name, msg.name)
		return m, nil

	case vaultLoadedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.vaultCols = msg.cols
		return m, nil
	}

	return m, nil
}

type archiveResultsMsg struct {
	query  string
	result *catalog.SearchResult
	err    error
}

type archiveSearchMsg struct {
	query string
}

func (m *model) updateView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.view {
	case viewMain:
		return m.updateMain(msg)
	case viewSessions:
		return m.updateSessions(msg)
	case viewSessionDetail:
		return m.updateSessionDetail(msg)
	case viewWatches:
		return m.updateWatches(msg)
	case viewHistory:
		return m.updateHistory(msg)
	case viewNewDownload:
		return m.updateNewDownload(msg)
	case viewScanPick:
		return m.updateScanPick(msg)
	case viewRunner:
		return m.updateRunner(msg)
	case viewArchiveSearch:
		return m.updateArchiveSearch(msg)
	case viewCollections:
		return m.updateCollections(msg)
	case viewCollectionDetail:
		return m.updateCollectionDetail(msg)
	case viewVault:
		return m.updateVault(msg)
	}
	return m, nil
}

func (m *model) updateArchiveSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = viewMain
		m.archiveQuery.Blur()
		m.archiveResults = nil
		return m, nil
	case "up", "k":
		if m.archiveCur > 0 {
			m.archiveCur--
		}
	case "down", "j":
		if m.archiveResults != nil && m.archiveCur < len(m.archiveResults)-1 {
			m.archiveCur++
		}
	case "enter":
		if !m.archiveLoading {
			return m, m.performArchiveSearch()
		}
	default:
		var cmd tea.Cmd
		m.archiveQuery, cmd = m.archiveQuery.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) performArchiveSearch() tea.Cmd {
	return func() tea.Msg {
		return archiveSearchMsg{query: m.archiveQuery.Value()}
	}
}
