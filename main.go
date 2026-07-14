// Command localdb is a terminal UI to run local database containers for
// development.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"localdb/internal/docker"
	"localdb/internal/tui"
)

func main() {
	mgr, err := docker.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "localdb:", err)
		os.Exit(1)
	}
	defer mgr.Close()

	p := tea.NewProgram(tui.New(mgr), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "localdb:", err)
		os.Exit(1)
	}
}
