package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------------------
// Main menu
// ---------------------------------------------------------------------------

var mainItems = []string{
	"New grab",
	"Scan & pick",
	"Sessions",
	"Watches",
	"Collections",
	"History",
	"Archive search",
	"Vault",
	"Quit",
}

func (m *model) updateMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.mainCursor > 0 {
			m.mainCursor--
		}
	case "down", "j":
		if m.mainCursor < len(mainItems)-1 {
			m.mainCursor++
		}
	case "R":
		// One-key resume of the most recent unfinished session.
		if m.resumeCandidate != "" {
			return m.startSessionResume(m.resumeCandidate, false)
		}
	case "q":
		return m, tea.Quit
	case "enter":
		switch mainItems[m.mainCursor] {
		case "New grab":
			m.view = viewNewDownload
			m.formStep = 0
			m.form = newDownloadForm()
			m.preview = scanPreview{}
			return m, nil
		case "Scan & pick":
			m.view = viewScanPick
			m.scan = scanState{
				checked:      map[int]bool{},
				filter:       filterState{input: newFilterInput()},
				urlInput:     m.scan.urlInput,
				awaitURL:     true,
				advForm:      newAdvOptsForm(),
				showAdvanced: false,
			}
			m.scan.urlInput.SetValue("")
			m.scan.urlInput.Focus()
			return m, nil
		case "Sessions":
			m.view = viewSessions
			return m, m.loadSessionsCmd()
		case "Watches":
			m.view = viewWatches
			return m, m.loadWatchesCmd()
		case "History":
			m.view = viewHistory
			return m, m.loadHistoryCmd()
		case "Collections":
			m.view = viewCollections
			return m, m.loadCollectionsCmd()
		case "Vault":
			m.view = viewVault
			return m, m.loadVaultCmd()
		case "Archive search":
			m.view = viewArchiveSearch
			m.archiveQuery.SetValue("")
			m.archiveQuery.Focus()
			m.archiveResults = nil
			return m, nil
		case "Quit":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *model) refreshResumeCandidate() {
	m.resumeCandidate = ""
	m.resumePendingURL = 0
	for _, info := range m.sessions { // already sorted newest first
		if info.Counts.Pending+info.Counts.Failed+info.Counts.Running > 0 {
			m.resumeCandidate = info.Name
			m.resumePendingURL = info.Counts.Pending + info.Counts.Failed + info.Counts.Running
			return
		}
	}
}
