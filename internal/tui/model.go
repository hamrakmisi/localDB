// Package tui implements the Bubble Tea terminal UI for localdb.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"localdb/internal/docker"
)

type screen int

const (
	screenList screen = iota
	screenForm
	screenConfirmDelete
	screenClone
	screenRestore
	screenLogs
)

type formMode int

const (
	formModeCreate formMode = iota
	formModeEdit
)

// form field indices
const (
	fName = iota
	fPort
	fUser
	fPassword
	fDatabase
	numFields
)

// clone form field indices
const (
	cName = iota
	cPort
	cPassword
	numCloneFields
)

type model struct {
	mgr *docker.Manager

	screen    screen
	instances []docker.Instance
	cursor    int

	// create / edit form
	inputs     []textinput.Model
	formEngine docker.Engine
	formFocus  int
	formMode   formMode
	editTarget docker.Instance

	// clone screen
	cloneInputs []textinput.Model
	cloneFocus  int
	cloneTarget docker.Instance

	// restore screen
	restoreFiles  []string // absolute paths to .sql files
	restoreCursor int
	restoreTarget docker.Instance

	// log screen
	logsTarget  docker.Instance
	logsContent string

	busy    bool   // an async op is in flight
	status  string // transient status / error line
	quitErr error
}

// New builds the initial model.
func New(mgr *docker.Manager) model {
	inputs := make([]textinput.Model, numFields)
	for i := range inputs {
		ti := textinput.New()
		ti.CharLimit = 64
		switch i {
		case fName:
			ti.Placeholder = "myapp"
			ti.Prompt = "Name      > "
		case fPort:
			ti.Placeholder = "3306"
			ti.Prompt = "Host port > "
		case fUser:
			ti.Placeholder = "dev"
			ti.Prompt = "User      > "
		case fPassword:
			ti.Placeholder = "secret"
			ti.Prompt = "Password  > "
		case fDatabase:
			ti.Placeholder = "myapp"
			ti.Prompt = "Database  > "
		}
		inputs[i] = ti
	}

	cloneInputs := make([]textinput.Model, numCloneFields)
	for i := range cloneInputs {
		ti := textinput.New()
		ti.CharLimit = 64
		switch i {
		case cName:
			ti.Placeholder = "myapp-copy"
			ti.Prompt = "New name  > "
		case cPort:
			ti.Placeholder = "3307"
			ti.Prompt = "Host port > "
		case cPassword:
			ti.Placeholder = "(same as source)"
			ti.Prompt = "Password  > "
		}
		cloneInputs[i] = ti
	}

	return model{
		mgr:         mgr,
		screen:      screenList,
		inputs:      inputs,
		cloneInputs: cloneInputs,
		formEngine:  docker.MySQL,
		status:      "Connecting to Docker…",
		busy:        true,
	}
}

func (m model) Init() tea.Cmd {
	return m.ping()
}

// selected returns the currently highlighted instance, if any.
func (m model) selected() (docker.Instance, bool) {
	if m.cursor < 0 || m.cursor >= len(m.instances) {
		return docker.Instance{}, false
	}
	return m.instances[m.cursor], true
}

func (m *model) resetForm() {
	for i := range m.inputs {
		m.inputs[i].SetValue("")
		m.inputs[i].Blur()
	}
	m.formEngine = docker.MySQL
	m.setPortPlaceholder()
	m.formMode = formModeCreate
	m.formFocus = fName
	m.inputs[fName].Focus()
}

func (m *model) setPortPlaceholder() {
	m.inputs[fPort].Placeholder = strconv.Itoa(m.formEngine.DefaultPort())
}

func (m *model) openEditForm(inst docker.Instance) {
	m.formMode = formModeEdit
	m.editTarget = inst
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	m.inputs[fName].SetValue(inst.Name)
	m.inputs[fPort].SetValue(inst.Port)
	m.inputs[fUser].SetValue(inst.User)
	m.inputs[fPassword].SetValue("")
	m.inputs[fDatabase].SetValue(inst.Database)
	m.formEngine = inst.Engine
	// start focus on port (name/engine are locked in edit mode)
	m.formFocus = fPort
	m.inputs[fPort].Focus()
}

func (m *model) openCloneScreen(inst docker.Instance) {
	m.cloneTarget = inst
	for i := range m.cloneInputs {
		m.cloneInputs[i].SetValue("")
		m.cloneInputs[i].Blur()
	}
	m.cloneInputs[cName].SetValue(inst.Name + "-copy")
	m.cloneInputs[cPort].SetValue("")
	m.cloneFocus = cName
	m.cloneInputs[cName].Focus()
}

func (m *model) openRestoreScreen(inst docker.Instance) {
	m.restoreTarget = inst
	m.restoreCursor = 0
	m.restoreFiles = listDumpFiles()
}

// listDumpFiles returns .sql files from ~/Downloads sorted newest first.
func listDumpFiles() []string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Downloads")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type entry struct {
		path    string
		modTime time.Time
	}
	var files []entry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, entry{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	// sort newest first
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].modTime.After(files[j-1].modTime); j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	return paths
}

// buildSpec validates form input and returns a docker.Spec.
func (m model) buildSpec() (docker.Spec, error) {
	name := strings.TrimSpace(m.inputs[fName].Value())
	if m.formMode == formModeEdit {
		name = m.editTarget.Name // locked in edit mode
	}
	if name == "" {
		return docker.Spec{}, fmt.Errorf("name required")
	}
	if strings.ContainsAny(name, " /\\:") {
		return docker.Spec{}, fmt.Errorf("name: no spaces or / \\ :")
	}
	eng := m.formEngine
	if m.formMode == formModeEdit {
		eng = m.editTarget.Engine // locked in edit mode
	}
	portStr := strings.TrimSpace(m.inputs[fPort].Value())
	if portStr == "" {
		portStr = strconv.Itoa(eng.DefaultPort())
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return docker.Spec{}, fmt.Errorf("port must be 1-65535")
	}
	user := strings.TrimSpace(m.inputs[fUser].Value())
	if user == "" {
		user = "dev"
	}
	pass := m.inputs[fPassword].Value()
	db := strings.TrimSpace(m.inputs[fDatabase].Value())
	if db == "" {
		db = name
	}
	return docker.Spec{
		Name:     name,
		Engine:   eng,
		Port:     port,
		User:     user,
		Password: pass,
		Database: db,
	}, nil
}

// buildCloneSpec validates clone form and returns a docker.Spec derived from cloneTarget.
func (m model) buildCloneSpec() (docker.Spec, error) {
	name := strings.TrimSpace(m.cloneInputs[cName].Value())
	if name == "" {
		return docker.Spec{}, fmt.Errorf("name required")
	}
	if strings.ContainsAny(name, " /\\:") {
		return docker.Spec{}, fmt.Errorf("name: no spaces or / \\ :")
	}
	portStr := strings.TrimSpace(m.cloneInputs[cPort].Value())
	if portStr == "" {
		portStr = "3307"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return docker.Spec{}, fmt.Errorf("port must be 1-65535")
	}
	pass := strings.TrimSpace(m.cloneInputs[cPassword].Value())
	// empty = Clone() will reuse source container's root password
	return docker.Spec{
		Name:     name,
		Engine:   m.cloneTarget.Engine,
		Port:     port,
		User:     m.cloneTarget.User,
		Password: pass,
		Database: m.cloneTarget.Database,
	}, nil
}
