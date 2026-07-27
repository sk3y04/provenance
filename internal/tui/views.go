package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"github.com/sk3y04/provenance/internal/history"
	"github.com/sk3y04/provenance/internal/session"
)

// ---------------------------------------------------------------------------
// View rendering
// ---------------------------------------------------------------------------

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).Padding(0, 1)
	highlight     = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	dim           = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	infoStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	helpFooter    = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	sectionHeader = lipgloss.NewStyle().Bold(true).Underline(true)
	bannerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("11")).Padding(0, 1)
)

func (m *model) View() string {
	header := titleStyle.Render(" provenance ") + "  " + dim.Render("interactive mode")

	body := ""
	switch m.view {
	case viewMain:
		body = m.viewMain()
	case viewSessions:
		body = m.viewSessions()
	case viewSessionDetail:
		body = m.viewSessionDetail()
	case viewWatches:
		body = m.viewWatches()
	case viewHistory:
		body = m.viewHistory()
	case viewNewDownload:
		body = m.viewNewDownload()
	case viewScanPick:
		body = m.viewScanPick()
	case viewRunner:
		body = m.viewRunner()
	case viewArchiveSearch:
		body = m.viewArchiveSearch()
	case viewCollections:
		body = m.viewCollections()
	case viewCollectionDetail:
		body = m.viewCollectionDetail()
	case viewVault:
		body = m.viewVault()
	}

	footer := m.viewFooter()

	notice := ""
	if m.err != "" {
		notice = errStyle.Render("error: " + m.err)
		m.err = ""
	} else if m.info != "" {
		notice = infoStyle.Render(m.info)
		m.info = ""
	}

	parts := []string{header, "", body}
	if notice != "" {
		parts = append(parts, "", notice)
	}
	parts = append(parts, "", helpFooter.Render(footer))
	return strings.Join(parts, "\n")
}

func (m *model) viewFooter() string {
	switch m.view {
	case viewMain:
		base := "↑/↓ move • enter select • q quit"
		if m.resumeCandidate != "" {
			base = "R resume last • " + base
		}
		return base
	case viewSessions:
		if m.sessFilter.active {
			return "type to filter • enter accept • esc clear"
		}
		return "↑/↓ • enter detail • r resume • R retry-failed • e export • d delete (×2) • / filter • esc back"
	case viewSessionDetail:
		return "r resume • R retry-failed • e export • f retry failed URLs • esc back"
	case viewWatches:
		if m.watchesFilt.active {
			return "type to filter • enter accept • esc clear"
		}
		if m.watchAddForm != nil {
			return "tab/shift+tab move • enter next/submit • ctrl+s submit • esc cancel"
		}
		return "↑/↓ • enter run • n add • d remove (×2) • / filter • esc back"
	case viewHistory:
		if m.historyFilt.active {
			return "type to filter • enter accept • esc clear"
		}
		return "↑/↓ • enter/r rerun • o reveal • d delete • / filter • esc back"
	case viewNewDownload:
		if m.cookiePick.active {
			return "↑/↓ pick • enter select • esc/ctrl+f close"
		}
		return "tab/shift+tab move  •  enter next/submit  •  ctrl+s submit  •  a advanced  •  esc back"
	case viewScanPick:
		if m.scan.awaitURL {
			return "enter scan • esc back"
		}
		if m.scan.showAdvanced {
			return "tab/shift+tab move • space toggle • esc/a hide advanced"
		}
		if m.scan.filter.active {
			return "type to filter • enter accept • esc clear"
		}
		return "space toggle • A all • n none • i invert • a advanced • / filter • r reload • enter download • esc back"
	case viewRunner:
		if m.runner.done {
			return "esc back to menu"
		}
		return "ctrl+c cancel"
	case viewArchiveSearch:
		if m.archiveLoading {
			return "searching…"
		}
		return "enter search • esc back"
	case viewCollections:
		if m.collFilter.active {
			return "type to filter • enter accept • esc clear"
		}
		return "enter detail • s sync • S sync-all • a archive • / filter • esc back"
	case viewCollectionDetail:
		return "s sync • a archive • esc back"
	case viewVault:
		return "enter revisions • esc back"
	}
	return ""
}

func (m *model) viewMain() string {
	var b strings.Builder
	if m.resumeCandidate != "" {
		banner := bannerStyle.Render(fmt.Sprintf(" ⟳  Unfinished session %q has %d URL(s) - press R to resume ",
			m.resumeCandidate, m.resumePendingURL))
		b.WriteString(banner + "\n\n")
	}
	b.WriteString(sectionHeader.Render("Main menu") + "\n\n")
	for i, item := range mainItems {
		cursor := "  "
		line := item
		if i == m.mainCursor {
			cursor = cursorStyle.Render("➤ ")
			line = highlight.Render(item)
		}
		b.WriteString(cursor + line + "\n")
	}
	return b.String()
}

func (m *model) viewSessions() string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("Sessions") + "\n")
	if m.sessFilter.active || m.sessFilter.input.Value() != "" {
		b.WriteString(m.sessFilter.input.View() + "\n")
	}
	b.WriteString("\n")
	visible := m.visibleSessions()
	if len(visible) == 0 {
		if len(m.sessions) == 0 {
			b.WriteString(dim.Render("(no saved sessions)\n"))
		} else {
			b.WriteString(dim.Render("(no matches)\n"))
		}
		return b.String()
	}
	fmt.Fprintf(&b, "  %-22s %-17s %6s %6s %6s %6s\n",
		"Name", "Updated", "Total", "OK", "Fail", "Pend")
	for i, info := range visible {
		cursor := "  "
		if i == m.sessCursor {
			cursor = cursorStyle.Render("➤ ")
		}
		row := fmt.Sprintf("%-22s %-17s %6d %6d %6d %6d",
			trim(info.Name, 22),
			info.UpdatedAt.Format("2006-01-02 15:04"),
			info.Counts.Total,
			info.Counts.Succeeded,
			info.Counts.Failed,
			info.Counts.Pending,
		)
		if i == m.sessCursor {
			row = highlight.Render(row)
		}
		b.WriteString(cursor + row + "\n")
	}
	if m.sessPending.name != "" && time.Since(m.sessPending.at) < confirmWindow {
		b.WriteString("\n" + warnStyle.Render(fmt.Sprintf("⚠  press d again to confirm delete of %q", m.sessPending.name)) + "\n")
	}
	return b.String()
}

func (m *model) viewSessionDetail() string {
	if m.sessSelected == nil {
		return dim.Render("(no session selected)")
	}
	s := m.sessSelected
	c := s.Counts()
	var b strings.Builder
	b.WriteString(sectionHeader.Render("Session: "+s.Name) + "\n\n")
	fmt.Fprintf(&b, "File:     %s\n", s.Path())
	fmt.Fprintf(&b, "Updated:  %s\n", s.UpdatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Output:   %s\n", s.Options.OutputDir)
	b.WriteString("\n")
	fmt.Fprintf(&b, "Total %d  •  ✓ %d  •  ⤼ %d  •  … %d  •  ▶ %d  •  ✗ %d\n",
		c.Total, c.Succeeded, c.Skipped, c.Pending, c.Running, c.Failed)

	failed := s.EntriesByStatus(session.StatusFailed)
	if len(failed) > 0 {
		b.WriteString("\n" + sectionHeader.Render("Failed URLs") + "\n")
		sort.Slice(failed, func(i, j int) bool { return failed[i].URL < failed[j].URL })
		for i, e := range failed {
			if i >= 15 {
				b.WriteString(dim.Render(fmt.Sprintf("  ... and %d more\n", len(failed)-i)))
				break
			}
			b.WriteString("  " + errStyle.Render("✗ ") + trim(e.URL, 100) + "\n")
			if e.LastError != "" {
				b.WriteString("    " + dim.Render(trim(e.LastError, 120)) + "\n")
			}
		}
	}
	return b.String()
}

func (m *model) viewWatches() string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("Watches") + "\n")
	if m.watchesFilt.active || m.watchesFilt.input.Value() != "" {
		b.WriteString(m.watchesFilt.input.View() + "\n")
	}
	b.WriteString("\n")

	if m.watchAddForm != nil {
		f := m.watchAddForm
		b.WriteString(sectionHeader.Render("Add watch") + "\n\n")
		for i, inp := range []struct {
			label string
			ti    *textinput.Model
		}{
			{"Name", &f.name},
			{"URL", &f.url},
		} {
			marker := "  "
			label := inp.label
			if i == f.step {
				marker = cursorStyle.Render("➤ ")
				label = highlight.Render(label)
			}
			fmt.Fprintf(&b, "%s%-14s %s\n", marker, label+":", inp.ti.View())
		}
		b.WriteString("\n" + dim.Render("enter to advance/submit  •  ctrl+s submit  •  esc cancel") + "\n")
		return b.String()
	}

	visible := m.visibleWatches()
	if len(visible) == 0 {
		if len(m.watches) == 0 {
			b.WriteString(dim.Render("(no watch subscriptions - press n to add one)\n"))
		} else {
			b.WriteString(dim.Render("(no matches)\n"))
		}
		return b.String()
	}
	fmt.Fprintf(&b, "  %-22s %-17s %s\n", "Name", "Last run", "URL")
	for i, sub := range visible {
		cursor := "  "
		if i == m.watchesCur {
			cursor = cursorStyle.Render("➤ ")
		}
		last := "never"
		if !sub.LastRunAt.IsZero() {
			last = sub.LastRunAt.Format("2006-01-02 15:04")
		}
		row := fmt.Sprintf("%-22s %-17s %s", trim(sub.Name, 22), last, trim(sub.URL, 80))
		if i == m.watchesCur {
			row = highlight.Render(row)
		}
		b.WriteString(cursor + row + "\n")
	}
	if m.watchPending.name != "" && time.Since(m.watchPending.at) < confirmWindow {
		b.WriteString("\n" + warnStyle.Render(fmt.Sprintf("⚠  press d again to confirm remove of %q", m.watchPending.name)) + "\n")
	}
	return b.String()
}

func (m *model) viewHistory() string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("History") + "\n")
	if m.historyFilt.active || m.historyFilt.input.Value() != "" {
		b.WriteString(m.historyFilt.input.View() + "\n")
	}
	b.WriteString("\n")
	visible := m.visibleHistory()
	if len(visible) == 0 {
		if len(m.history) == 0 {
			b.WriteString(dim.Render("(no completed runs recorded yet)\n"))
		} else {
			b.WriteString(dim.Render("(no matches)\n"))
		}
		return b.String()
	}
	fmt.Fprintf(&b, "  %-19s %-36s %6s %6s %6s %-10s %s\n", "Completed", "Title", "OK", "Fail", "Skip", "Bytes", "Destination")
	maxRows := m.height - 10
	if maxRows < 6 {
		maxRows = 6
	}
	start := 0
	if m.historyCur >= maxRows {
		start = m.historyCur - maxRows + 1
	}
	end := start + maxRows
	if end > len(visible) {
		end = len(visible)
	}
	for i := start; i < end; i++ {
		run := visible[i]
		cursor := "  "
		if i == m.historyCur {
			cursor = cursorStyle.Render("➤ ")
		}
		dest := historyDisplayDest(run)
		row := fmt.Sprintf("%-19s %-36s %6d %6d %6d %-10s %s",
			run.CompletedAt.Format("2006-01-02 15:04"),
			trim(run.Title, 36),
			run.Succeeded,
			run.Failed,
			run.Skipped,
			humanBytes(run.TotalBytes),
			trim(dest, 60),
		)
		if i == m.historyCur {
			row = highlight.Render(row)
		}
		b.WriteString(cursor + row + "\n")
	}
	if end < len(visible) {
		b.WriteString(dim.Render(fmt.Sprintf("  ... %d more\n", len(visible)-end)))
	}
	if len(visible) > 0 {
		if m.historyCur >= len(visible) {
			m.historyCur = len(visible) - 1
		}
		run := visible[m.historyCur]
		b.WriteString("\n" + sectionHeader.Render("Selected") + "\n")
		b.WriteString(fmt.Sprintf("  ID:       %s\n", run.ID))
		b.WriteString(fmt.Sprintf("  Duration: %s\n", run.Duration.Round(time.Second)))
		if run.Error != "" {
			b.WriteString("  Error:    " + errStyle.Render(trim(run.Error, 120)) + "\n")
		}
		if len(run.URLs) > 0 {
			b.WriteString("  URLs:\n")
			for i, u := range run.URLs {
				if i >= 3 {
					b.WriteString(dim.Render(fmt.Sprintf("    ... and %d more\n", len(run.URLs)-i)))
					break
				}
				b.WriteString("    - " + trim(u, 110) + "\n")
			}
		}
		if len(run.Files) > 0 {
			b.WriteString("  Files:\n")
			for i, f := range run.Files {
				if i >= 4 {
					b.WriteString(dim.Render(fmt.Sprintf("    ... and %d more\n", len(run.Files)-i)))
					break
				}
				mark := okStyle.Render("✓")
				if !f.Success {
					mark = errStyle.Render("✗")
				}
				b.WriteString(fmt.Sprintf("    %s %s %s\n", mark, trim(f.Path, 90), dim.Render(humanBytes(f.Size))))
			}
		}
	}
	return b.String()
}

func historyDisplayDest(run history.Run) string {
	if p := revealPathForRun(run); p != "" {
		return p
	}
	return run.Options.OutputDir
}

func (m *model) viewNewDownload() string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("New grab") + "\n\n")
	inputs := m.formInputs()
	// Basic fields (always visible).
	basicCount := len(formLabels)
	renderCount := basicCount
	if !m.showAdvanced {
		renderCount = len(inputs)
	}
	for i := 0; i < renderCount; i++ {
		ti := inputs[i]
		label := formLabels[i]
		marker := "  "
		if i == m.formStep {
			marker = cursorStyle.Render("➤ ")
			label = highlight.Render(label)
		}
		b.WriteString(fmt.Sprintf("%s%-14s %s\n", marker, label+":", ti.View()))
		if i == 0 {
			b.WriteString(m.viewScanPreview())
		}
		if i == 3 && m.cookiePick.active {
			b.WriteString(m.viewCookiePicker())
		}
	}

	if !m.showAdvanced {
		b.WriteString("\n" + dim.Render("a toggle advanced options") + "\n")
	} else {
		b.WriteString("\n" + dim.Render("a hide advanced options") + "\n\n")
		specs := m.advFieldSpecs()
		baseStep := basicStepCount
		for i, s := range specs {
			stepIdx := baseStep + i
			marker := "  "
			label := s.label
			if stepIdx == m.formStep {
				marker = cursorStyle.Render("➤ ")
				label = highlight.Render(label)
			}
			if s.isBool {
				toggle := "[ ] no"
				if *s.boolP {
					toggle = okStyle.Render("[✓]") + " yes"
				}
				b.WriteString(fmt.Sprintf("%s%-22s %s\n", marker, label+":", toggle))
			} else {
				b.WriteString(fmt.Sprintf("%s%-22s %s\n", marker, label+":", s.input.View()))
			}
		}
	}

	b.WriteString("\n" + dim.Render("tab/shift+tab move  •  enter advance  •  ctrl+s submit  •  a advanced options") + "\n")
	return b.String()
}

func (m *model) viewCookiePicker() string {
	cp := m.cookiePick
	if len(cp.files) == 0 {
		return "                " + dim.Render("(no cookies files found)") + "\n"
	}
	var b strings.Builder
	b.WriteString("                " + dim.Render(fmt.Sprintf("── cookies found (%d) ──", len(cp.files))) + "\n")
	maxRows := 8
	if len(cp.files) < maxRows {
		maxRows = len(cp.files)
	}
	start := 0
	if cp.cursor >= maxRows {
		start = cp.cursor - maxRows + 1
	}
	end := start + maxRows
	if end > len(cp.files) {
		end = len(cp.files)
	}
	for i := start; i < end; i++ {
		marker := "  "
		if i == cp.cursor {
			marker = cursorStyle.Render("➤ ")
		}
		line := cp.files[i]
		if i == cp.cursor {
			line = highlight.Render(line)
		}
		b.WriteString(fmt.Sprintf("               %s%s\n", marker, trim(line, 60)))
	}
	if end < len(cp.files) {
		b.WriteString("                " + dim.Render(fmt.Sprintf("  ... %d more\n", len(cp.files)-end)))
	}
	return b.String()
}

func (m *model) viewScanPreview() string {
	p := m.preview
	if p.url == "" {
		return ""
	}
	prefix := "                "
	switch {
	case p.loading:
		return prefix + dim.Render("scanning...") + "\n"
	case p.err != "":
		return prefix + warnStyle.Render("preview: "+trim(p.err, 80)) + "\n"
	case p.count > 0:
		size := ""
		if p.size > 0 {
			size = fmt.Sprintf(", ~%s", humanBytes(p.size))
		}
		return prefix + okStyle.Render(fmt.Sprintf("≈ %d items%s (%s)", p.count, size, p.site)) + "\n"
	}
	return ""
}

func (m *model) viewScanPick() string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("Scan & pick") + "\n\n")
	if m.scan.awaitURL {
		b.WriteString("URL to scan:\n")
		b.WriteString(m.scan.urlInput.View() + "\n\n")
		b.WriteString(dim.Render("Press enter to scan. Twitter/X, Reddit, and generic URLs are all supported.") + "\n")
		return b.String()
	}
	if m.scan.loading {
		b.WriteString(dim.Render("scanning "+trim(m.scan.sourceURL, 80)+"...") + "\n")
		return b.String()
	}
	if m.scan.err != "" {
		b.WriteString(errStyle.Render("scan failed: "+m.scan.err) + "\n")
		return b.String()
	}
	if len(m.scan.items) == 0 {
		b.WriteString(dim.Render("(no items found)") + "\n")
		return b.String()
	}

	visible := m.scanVisibleIdx()
	selected := 0
	var totalSize int64
	for i, it := range m.scan.items {
		if m.scan.checked[i] {
			selected++
			totalSize += it.Size
		}
	}
	b.WriteString(fmt.Sprintf("Source: %s   site=%s\n", trim(m.scan.sourceURL, 70), m.scan.site))
	b.WriteString(fmt.Sprintf("Items:  %d total, %d shown, %d selected (~%s)\n",
		len(m.scan.items), len(visible), selected, humanBytes(totalSize)))
	if m.scan.filter.active || m.scan.filter.input.Value() != "" {
		b.WriteString(m.scan.filter.input.View() + "\n")
	}
	b.WriteString("\n")

	if m.scan.showAdvanced {
		b.WriteString(dim.Render("a hide advanced") + "\n")
		f := &m.scan.advForm
		for i := 0; i < len(advFieldSpecDefs); i++ {
			label, isBool := f.spec(i)
			marker := "  "
			if i == f.step {
				marker = cursorStyle.Render("➤ ")
				label = highlight.Render(label)
			}
			if isBool {
				toggle := "[ ] no"
				if bp := f.boolPtr(i); bp != nil && *bp {
					toggle = okStyle.Render("[✓]") + " yes"
				}
				b.WriteString(fmt.Sprintf("%s%-22s %s\n", marker, label+":", toggle))
			} else if inp := f.inputPtr(i); inp != nil {
				b.WriteString(fmt.Sprintf("%s%-22s %s\n", marker, label+":", inp.View()))
			}
		}
		b.WriteString("\n")
	} else {
		b.WriteString(dim.Render("a advanced options") + "\n\n")
	}

	maxFileRows := m.height - 14
	if m.scan.showAdvanced {
		maxFileRows -= len(advFieldSpecDefs) + 2
	}
	if maxFileRows < 5 {
		maxFileRows = 5
	}
	start := 0
	if m.scan.cursor >= maxFileRows {
		start = m.scan.cursor - maxFileRows + 1
	}
	end := start + maxFileRows
	if end > len(visible) {
		end = len(visible)
	}

	for vi := start; vi < end; vi++ {
		idx := visible[vi]
		it := m.scan.items[idx]
		mark := "[ ]"
		if m.scan.checked[idx] {
			mark = okStyle.Render("[✓]")
		}
		cursor := "  "
		if vi == m.scan.cursor {
			cursor = cursorStyle.Render("➤ ")
		}
		title := it.Title
		if title == "" {
			title = it.Filename
		}
		size := ""
		if it.Size > 0 {
			size = " " + dim.Render("("+humanBytes(it.Size)+")")
		}
		row := fmt.Sprintf("%s %-50s %s", mark, trim(title, 50), trim(it.URL, 70))
		if vi == m.scan.cursor {
			row = highlight.Render(row)
		}
		b.WriteString(cursor + row + size + "\n")
	}
	if end < len(visible) {
		b.WriteString(dim.Render(fmt.Sprintf("  ... %d more (scroll with ↓)\n", len(visible)-end)))
	}
	return b.String()
}

func (m *model) viewRunner() string {
	var b strings.Builder
	status := "running"
	style := infoStyle
	if m.runner.done {
		if m.runner.err != nil {
			status = "failed"
			style = errStyle
		} else {
			status = "done"
			style = okStyle
		}
	}
	b.WriteString(sectionHeader.Render("Run: "+m.runner.title) + "  " + style.Render("["+status+"]") + "\n\n")
	elapsed := time.Since(m.runner.startAt).Round(time.Second)

	// Aggregate current bytes across all files for an ETA.
	var done, total int64
	for _, fp := range m.runner.files {
		done += fp.written
		if fp.total > 0 {
			total += fp.total
		}
	}
	speed := m.runner.speedBps
	speedStr := "-"
	etaStr := "-"
	if speed > 0 {
		speedStr = humanBytes(int64(speed)) + "/s"
		if total > done {
			etaSecs := int64(float64(total-done) / speed)
			etaStr = (time.Duration(etaSecs) * time.Second).String()
		}
	}
	b.WriteString(fmt.Sprintf("Elapsed: %s   queued %d  ▶ %d  ✓ %d  ⤼ %d  ✗ %d\n",
		elapsed, m.runner.queued, m.runner.running, m.runner.ok, m.runner.skipped, m.runner.failed))
	b.WriteString(fmt.Sprintf("Speed:   %s   ETA: %s   bytes: %s / %s\n\n",
		speedStr, etaStr, humanBytes(done), humanBytes(total)))

	// Active file rows (yt-dlp JSON progress).
	if len(m.runner.fileOrder) > 0 {
		b.WriteString(sectionHeader.Render("Files") + "\n")
		maxFileRows := 8
		shown := 0
		// Render newest in-flight first, then most recent finished.
		order := append([]string(nil), m.runner.fileOrder...)
		// Active before done.
		sort.SliceStable(order, func(i, j int) bool {
			fi, fj := m.runner.files[order[i]], m.runner.files[order[j]]
			if fi == nil || fj == nil {
				return false
			}
			if fi.done != fj.done {
				return !fi.done
			}
			return fi.startedAt.After(fj.startedAt)
		})
		for _, u := range order {
			fp := m.runner.files[u]
			if fp == nil {
				continue
			}
			if shown >= maxFileRows {
				b.WriteString(dim.Render(fmt.Sprintf("  ... and %d more\n", len(order)-shown)))
				break
			}
			shown++
			b.WriteString(m.renderFileRow(fp) + "\n")
		}
		b.WriteString("\n")
	}

	maxRows := m.height - 18 - 2*len(m.runner.fileOrder)
	if maxRows < 4 {
		maxRows = 4
	}
	logs := m.runner.logs
	if len(logs) > maxRows {
		logs = logs[len(logs)-maxRows:]
	}
	for _, line := range logs {
		switch {
		case strings.HasPrefix(line, "✓"):
			b.WriteString(okStyle.Render(line) + "\n")
		case strings.HasPrefix(line, "✗"), strings.Contains(line, "FAILED"):
			b.WriteString(errStyle.Render(line) + "\n")
		case strings.HasPrefix(line, "▶"):
			b.WriteString(infoStyle.Render(line) + "\n")
		default:
			b.WriteString(dim.Render(line) + "\n")
		}
	}
	return b.String()
}

func (m *model) renderFileRow(fp *fileProgress) string {
	name := prettyFileName(fp.dest)
	if name == "" {
		name = trim(fp.url, 40)
	}
	pct := 0.0
	if fp.total > 0 {
		pct = float64(fp.written) / float64(fp.total)
		if pct > 1 {
			pct = 1
		}
	}
	bar := m.runner.bar.ViewAs(pct)
	pctStr := "  ?%"
	if fp.total > 0 {
		pctStr = fmt.Sprintf("%3d%%", int(pct*100))
	}
	sizeStr := fmt.Sprintf("%s / %s", humanBytes(fp.written), humanBytes(fp.total))
	if fp.total <= 0 {
		sizeStr = humanBytes(fp.written)
	}
	mark := infoStyle.Render("▶")
	switch {
	case fp.err != nil:
		mark = errStyle.Render("✗")
	case fp.done:
		mark = okStyle.Render("✓")
	}
	return fmt.Sprintf("  %s %-30s %s %s  %s",
		mark, trim(name, 30), bar, pctStr, dim.Render(sizeStr))
}

// filepathBase is filepath.Base but works on either "/" or "\" separators
// so cross-platform destinations look right in the file row.
func filepathBase(p string) string {
	if p == "" {
		return ""
	}
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// prettyFileName trims download work suffixes and the noisy "NA" / "Unknown"
// directory yt-dlp uses when uploader metadata is missing.
func prettyFileName(p string) string {
	name := filepathBase(p)
	name = strings.TrimSuffix(name, ".part")
	if name == "" || strings.EqualFold(name, "NA") || strings.EqualFold(name, "Unknown") {
		// Walk one level up if the basename is a placeholder folder name.
		trimmed := strings.TrimRight(p, `/\`)
		if i := strings.LastIndexAny(trimmed, `/\`); i >= 0 {
			parent := trimmed[:i]
			if base := filepathBase(parent); base != "" {
				return base
			}
		}
	}
	return name
}

func revealPathForRun(run history.Run) string {
	for _, f := range run.Files {
		if strings.TrimSpace(f.Path) == "" {
			continue
		}
		if _, err := os.Stat(f.Path); err == nil {
			return revealTarget(f.Path)
		}
		if dir := filepath.Dir(f.Path); dir != "." {
			if _, err := os.Stat(dir); err == nil {
				return dir
			}
		}
	}
	if out := strings.TrimSpace(run.Options.OutputDir); out != "" {
		return out
	}
	return ""
}

func revealTarget(path string) string {
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return filepath.Dir(path)
	}
	return path
}

func openPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func (m *model) viewArchiveSearch() string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("Archive search") + "\n\n")
	b.WriteString(m.archiveQuery.View())
	b.WriteString("\n\n")

	if m.archiveLoading {
		b.WriteString(dim.Render("Searching..."))
		return b.String()
	}

	if m.archiveResults == nil {
		b.WriteString(dim.Render("Enter a search query and press enter."))
		return b.String()
	}

	if len(m.archiveResults) == 0 {
		b.WriteString(dim.Render("No results."))
		return b.String()
	}

	for i, h := range m.archiveResults {
		cursor := "  "
		if i == m.archiveCur {
			cursor = highlight.Render("▶ ")
		}
		fmt.Fprint(&b, cursor)
		if h.Title != "" {
			fmt.Fprintf(&b, "%s\n", h.Title)
		}
		if h.Headline != "" {
			fmt.Fprintf(&b, "    %s %s\n", dim.Render("…"), dim.Render(h.Headline))
		}
		fmt.Fprintf(&b, "    %s %s @ %s\n\n", dim.Render(h.URL), dim.Render(h.Collection), dim.Render(h.Date))
	}
	return b.String()
}
