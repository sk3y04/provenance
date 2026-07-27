package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sk3y04/provenance/internal/archive"
	"github.com/sk3y04/provenance/internal/catalog"
)

func (m *model) loadVaultCmd() tea.Cmd {
	return func() tea.Msg {
		hasVault := catalog.HasStore()
		var cols []archive.ArchiveCollection
		if hasVault {
			c, _ := catalog.Store().ListCollections(context.Background())
			cols = c
		}
		return vaultLoadedMsg{initialized: hasVault, cols: cols}
	}
}

type vaultLoadedMsg struct {
	initialized bool
	cols        []archive.ArchiveCollection
	err         error
}

func (m *model) updateVault(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace", "h", "q":
		m.view = viewMain
		return m, nil
	case "up", "k":
		if m.vaultCur > 0 {
			m.vaultCur--
		}
	case "down", "j":
		if len(m.vaultCols) > 0 && m.vaultCur < len(m.vaultCols)-1 {
			m.vaultCur++
		}
	}
	return m, nil
}

func (m *model) viewVault() string {
	var b strings.Builder
	b.WriteString(sectionHeader.Render("Vault") + "\n\n")

	if !catalog.HasStore() {
		b.WriteString(dim.Render("Vault not initialized."))
		b.WriteString("\nRun: provenance vault init")
		b.WriteString("\nSet: PROVENANCE_DATABASE_URL=postgres://user:pass@localhost/dbname")
		return b.String()
	}

	b.WriteString(okStyle.Render("vault ready") + "\n\n")

	if len(m.vaultCols) == 0 {
		b.WriteString(dim.Render("No archive collections."))
		b.WriteString("\n\nUse 'archive' commands or collect sync --record to populate the vault.")
		return b.String()
	}

	fmt.Fprintf(&b, "  %-24s %s\n", "Collection", "Revisions")
	for i, c := range m.vaultCols {
		cursor := "  "
		if i == m.vaultCur {
			cursor = highlight.Render("▶ ")
		}
		revs, _ := catalog.ListRevisions(c.VaultRoot)
		fmt.Fprintf(&b, "%s%-24s %d\n", cursor, c.Name, len(revs))
	}
	b.WriteString("\n" + dim.Render("[enter] show revisions  [esc] back"))
	return b.String()
}
