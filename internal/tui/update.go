package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"localdb/internal/docker"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case pingMsg:
		m.busy = false
		if msg.err != nil {
			m.status = "ERR: " + msg.err.Error()
			return m, nil
		}
		m.status = "Loading…"
		m.busy = true
		return m, m.loadList()

	case listLoadedMsg:
		m.busy = false
		if msg.err != nil {
			m.status = "ERR: " + msg.err.Error()
			return m, nil
		}
		m.instances = msg.instances
		if m.cursor >= len(m.instances) {
			m.cursor = max(0, len(m.instances)-1)
		}
		if m.status == "Loading…" {
			m.status = ""
		}
		return m, nil

	case opDoneMsg:
		m.busy = false
		if msg.err != nil {
			m.status = "ERR: " + msg.err.Error()
			return m, nil
		}
		m.status = "Done."
		m.busy = true
		return m, m.loadList()

	case dumpDoneMsg:
		m.busy = false
		if msg.err != nil {
			m.status = "ERR: " + msg.err.Error()
			return m, nil
		}
		m.status = fmt.Sprintf("Saved → %s", filepath.Base(msg.path))
		return m, nil

	case tea.KeyMsg:
		switch m.screen {
		case screenList:
			return m.updateList(msg)
		case screenForm:
			return m.updateForm(msg)
		case screenConfirmDelete:
			return m.updateConfirm(msg)
		case screenClone:
			return m.updateClone(msg)
		case screenRestore:
			return m.updateRestore(msg)
		}
	}
	return m, nil
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.instances)-1 {
			m.cursor++
		}
	case "r":
		m.busy = true
		m.status = "Refreshing…"
		return m, m.loadList()
	case "n":
		m.resetForm()
		m.screen = screenForm
		m.status = ""
		return m, textinputBlink()
	case "e":
		if inst, ok := m.selected(); ok && !m.busy {
			m.openEditForm(inst)
			m.screen = screenForm
			m.status = ""
			return m, textinputBlink()
		}
	case "s":
		if inst, ok := m.selected(); ok && !m.busy {
			m.busy = true
			if inst.State == "running" {
				m.status = "Stopping " + inst.Name + "…"
				return m, m.stopInstance(inst.ID)
			}
			m.status = "Starting " + inst.Name + "…"
			return m, m.startInstance(inst.ID)
		}
	case "d":
		if _, ok := m.selected(); ok {
			m.screen = screenConfirmDelete
		}
	case "c":
		if inst, ok := m.selected(); ok && !m.busy {
			if inst.State != "running" {
				m.status = "ERR: start DB first to clone"
				return m, nil
			}
			m.openCloneScreen(inst)
			m.screen = screenClone
			m.status = ""
			return m, textinputBlink()
		}
	case "x":
		if inst, ok := m.selected(); ok && !m.busy {
			if inst.State != "running" {
				m.status = "ERR: start DB first to dump"
				return m, nil
			}
			home, _ := os.UserHomeDir()
			dest := filepath.Join(home, "Downloads",
				fmt.Sprintf("%s_%s.sql", inst.Name, time.Now().Format("20060102_150405")))
			m.busy = true
			m.status = "Dumping " + inst.Name + "…"
			return m, m.dumpInstance(inst, dest)
		}
	case "i":
		if inst, ok := m.selected(); ok && !m.busy {
			if inst.State != "running" {
				m.status = "ERR: start DB first to restore"
				return m, nil
			}
			m.openRestoreScreen(inst)
			m.screen = screenRestore
			m.status = ""
			return m, nil
		}
	}
	return m, nil
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		if inst, ok := m.selected(); ok {
			m.screen = screenList
			m.busy = true
			m.status = "Removing " + inst.Name + "…"
			return m, m.removeInstance(inst.ID)
		}
	case "n", "esc", "q":
		m.screen = screenList
	}
	return m, nil
}

func (m model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenList
		m.status = ""
		return m, nil
	case "tab", "down":
		m.formFocus = nextFormField(m.formFocus, m.formMode, +1)
		return m, m.refocus()
	case "shift+tab", "up":
		m.formFocus = nextFormField(m.formFocus, m.formMode, -1)
		return m, m.refocus()
	case "left", "right":
		if m.formMode == formModeCreate {
			for i, eng := range docker.Engines {
				if eng == m.formEngine {
					m.formEngine = docker.Engines[(i+1)%len(docker.Engines)]
					break
				}
			}
			m.setPortPlaceholder()
		}
		return m, nil
	case "enter":
		spec, err := m.buildSpec()
		if err != nil {
			m.status = "ERR: " + err.Error()
			return m, nil
		}
		m.screen = screenList
		m.busy = true
		if m.formMode == formModeEdit {
			m.status = "Updating " + spec.Name + "…"
			return m, m.recreateInstance(m.editTarget, spec)
		}
		m.status = "Creating " + spec.Name + " (may pull image)…"
		return m, m.createInstance(spec)
	}

	// pass key to focused input
	var cmd tea.Cmd
	m.inputs[m.formFocus], cmd = m.inputs[m.formFocus].Update(msg)
	return m, cmd
}

// nextFormField advances focus by delta (+1/-1), skipping locked fields in edit mode.
func nextFormField(current int, mode formMode, delta int) int {
	skip := func(f int) bool {
		return mode == formModeEdit && f == fName
	}
	next := current
	for {
		next = (next + delta + numFields) % numFields
		if !skip(next) {
			return next
		}
	}
}

func (m model) updateClone(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenList
		m.status = ""
		return m, nil
	case "tab", "down":
		m.cloneFocus = (m.cloneFocus + 1) % numCloneFields
		return m, m.refocusClone()
	case "shift+tab", "up":
		m.cloneFocus = (m.cloneFocus - 1 + numCloneFields) % numCloneFields
		return m, m.refocusClone()
	case "enter":
		spec, err := m.buildCloneSpec()
		if err != nil {
			m.status = "ERR: " + err.Error()
			return m, nil
		}
		m.screen = screenList
		m.busy = true
		m.status = fmt.Sprintf("Cloning %s → %s (may take a while)…", m.cloneTarget.Name, spec.Name)
		return m, m.cloneInstance(m.cloneTarget, spec)
	}

	var cmd tea.Cmd
	m.cloneInputs[m.cloneFocus], cmd = m.cloneInputs[m.cloneFocus].Update(msg)
	return m, cmd
}

func (m model) updateRestore(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.screen = screenList
		m.status = ""
		return m, nil
	case "up", "k":
		if m.restoreCursor > 0 {
			m.restoreCursor--
		}
	case "down", "j":
		if m.restoreCursor < len(m.restoreFiles)-1 {
			m.restoreCursor++
		}
	case "enter":
		if len(m.restoreFiles) == 0 {
			m.screen = screenList
			m.status = "ERR: no .sql files in ~/Downloads"
			return m, nil
		}
		src := m.restoreFiles[m.restoreCursor]
		inst := m.restoreTarget
		m.screen = screenList
		m.busy = true
		m.status = fmt.Sprintf("Restoring into %s…", inst.Name)
		return m, m.restoreInstance(inst, src)
	}
	return m, nil
}

func (m *model) refocus() tea.Cmd {
	for i := range m.inputs {
		if i == m.formFocus {
			m.inputs[i].Focus()
		} else {
			m.inputs[i].Blur()
		}
	}
	return textinputBlink()
}

func (m *model) refocusClone() tea.Cmd {
	for i := range m.cloneInputs {
		if i == m.cloneFocus {
			m.cloneInputs[i].Focus()
		} else {
			m.cloneInputs[i].Blur()
		}
	}
	return textinputBlink()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
