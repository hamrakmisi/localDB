package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"localdb/internal/docker"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	selStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

func textinputBlink() tea.Cmd { return textinput.Blink }

func (m model) View() string {
	switch m.screen {
	case screenForm:
		return m.viewForm()
	case screenConfirmDelete:
		return m.viewConfirm()
	case screenClone:
		return m.viewClone()
	case screenRestore:
		return m.viewRestore()
	default:
		return m.viewList()
	}
}

func (m model) statusLine() string {
	if m.status == "" {
		return ""
	}
	if strings.HasPrefix(m.status, "ERR:") {
		return errStyle.Render(m.status)
	}
	return okStyle.Render(m.status)
}

func (m model) viewList() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("localdb — local databases for dev") + "\n\n")

	if len(m.instances) == 0 {
		b.WriteString(dimStyle.Render("  No databases yet. Press 'n' to create one.") + "\n")
	} else {
		b.WriteString(m.renderTable() + "\n")
	}

	if inst, ok := m.selected(); ok {
		b.WriteString("\n" + dimStyle.Render("  conn: ") + connString(inst) + "\n")
	}

	if s := m.statusLine(); s != "" {
		b.WriteString("\n  " + s + "\n")
	}

	b.WriteString("\n" + helpStyle.Render(
		"  ↑/↓ move · n new · e edit · s start/stop · c clone · x dump · i import · d delete · r refresh · q quit"))
	return b.String()
}

// renderTable builds the bordered instance table.
func (m model) renderTable() string {
	rows := make([][]string, 0, len(m.instances))
	for i, inst := range m.instances {
		cur := " "
		if i == m.cursor {
			cur = "›"
		}
		rows = append(rows, []string{
			cur, inst.Name, string(inst.Engine), inst.Port,
			inst.State, trunc(inst.Status, 18),
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("63"))).
		Headers("", "NAME", "ENGINE", "PORT", "STATE", "STATUS").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			st := lipgloss.NewStyle().Padding(0, 1)
			switch {
			case row == table.HeaderRow:
				return st.Bold(true).Foreground(lipgloss.Color("63"))
			case row == m.cursor:
				return st.Bold(true).Foreground(lipgloss.Color("212"))
			case col == 4 && row < len(m.instances):
				if m.instances[row].State == "running" {
					return st.Foreground(lipgloss.Color("42"))
				}
				return st.Foreground(lipgloss.Color("203"))
			}
			return st
		})

	return lipgloss.NewStyle().MarginLeft(2).Render(t.String())
}

func (m model) viewForm() string {
	var b strings.Builder
	if m.formMode == formModeEdit {
		b.WriteString(titleStyle.Render("Edit database — "+m.editTarget.Name) + "\n\n")
	} else {
		b.WriteString(titleStyle.Render("New local database") + "\n\n")
	}

	for i := range m.inputs {
		if m.formMode == formModeEdit && i == fName {
			b.WriteString("  " + dimStyle.Render(m.inputs[i].Prompt+m.inputs[i].Value()+" (locked)") + "\n")
			continue
		}
		b.WriteString("  " + m.inputs[i].View() + "\n")
	}

	if m.formMode == formModeCreate {
		engines := make([]string, 0, len(docker.Engines))
		for _, eng := range docker.Engines {
			name := dimStyle.Render("  " + string(eng) + "  ")
			if m.formEngine == eng {
				name = selStyle.Render("[ " + string(eng) + " ]")
			}
			engines = append(engines, name)
		}
		b.WriteString("\n  Engine    > " + strings.Join(engines, " ") +
			dimStyle.Render("  (←/→ to toggle)") + "\n")
	} else {
		b.WriteString("\n  " + dimStyle.Render("Engine    > "+string(m.editTarget.Engine)+" (locked)") + "\n")
	}

	if s := m.statusLine(); s != "" {
		b.WriteString("\n  " + s + "\n")
	}

	if m.formMode == formModeEdit {
		b.WriteString("\n" + helpStyle.Render(
			"  tab/↑↓ field · enter save · esc cancel"))
	} else {
		b.WriteString("\n" + helpStyle.Render(
			"  tab/↑↓ field · ←/→ engine · enter create · esc cancel"))
	}
	return b.String()
}

func (m model) viewConfirm() string {
	inst, _ := m.selected()
	var b strings.Builder
	b.WriteString(titleStyle.Render("Delete database") + "\n\n")
	b.WriteString(fmt.Sprintf("  Remove container %q and its data volume?\n", inst.Name))
	b.WriteString(errStyle.Render("  This deletes the stored data permanently.") + "\n\n")
	b.WriteString(helpStyle.Render("  y confirm · n/esc cancel"))
	return b.String()
}

func (m model) viewClone() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Clone — "+m.cloneTarget.Name) + "\n\n")
	b.WriteString(dimStyle.Render("  Data and structure copied to a new instance.\n\n"))

	for i := range m.cloneInputs {
		b.WriteString("  " + m.cloneInputs[i].View() + "\n")
	}

	if s := m.statusLine(); s != "" {
		b.WriteString("\n  " + s + "\n")
	}

	b.WriteString("\n" + helpStyle.Render(
		"  tab/↑↓ field · enter clone · esc cancel"))
	return b.String()
}

func (m model) viewRestore() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Restore into — "+m.restoreTarget.Name) + "\n\n")

	if len(m.restoreFiles) == 0 {
		b.WriteString(dimStyle.Render("  No .sql files found in ~/Downloads.\n"))
		b.WriteString("\n" + helpStyle.Render("  esc cancel"))
		return b.String()
	}

	b.WriteString(dimStyle.Render("  Pick a dump file from ~/Downloads:\n\n"))
	for i, path := range m.restoreFiles {
		name := filepath.Base(path)
		if i == m.restoreCursor {
			b.WriteString("  " + selStyle.Render("› "+name) + "\n")
		} else {
			b.WriteString("  " + dimStyle.Render("  "+name) + "\n")
		}
	}

	b.WriteString("\n" + helpStyle.Render(
		"  ↑/↓ move · enter restore · esc cancel"))
	return b.String()
}

func connString(inst docker.Instance) string {
	if inst.Engine == docker.Postgres {
		return fmt.Sprintf("postgresql://%s:****@127.0.0.1:%s/%s", inst.User, inst.Port, inst.Database)
	}
	return fmt.Sprintf("mysql://%s:****@127.0.0.1:%s/%s",
		inst.User, inst.Port, inst.Database)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
