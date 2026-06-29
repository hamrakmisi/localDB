package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"localdb/internal/docker"
)

// opTimeout bounds any single Docker operation. Image pulls can be slow on the
// first run, so keep this generous.
const opTimeout = 10 * time.Minute

type (
	// listLoadedMsg carries the result of refreshing the instance list.
	listLoadedMsg struct {
		instances []docker.Instance
		err       error
	}
	// opDoneMsg reports completion of a mutating op (create/start/stop/remove/clone/restore/recreate).
	opDoneMsg struct {
		err error
	}
	// dumpDoneMsg reports completion of a dump with the output path.
	dumpDoneMsg struct {
		path string
		err  error
	}
	// pingMsg reports initial Docker reachability.
	pingMsg struct{ err error }
)

func (m model) ping() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return pingMsg{err: m.mgr.Ping(ctx)}
	}
}

func (m model) loadList() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ins, err := m.mgr.List(ctx)
		return listLoadedMsg{instances: ins, err: err}
	}
}

func (m model) createInstance(spec docker.Spec) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		return opDoneMsg{err: m.mgr.CreateAndStart(ctx, spec)}
	}
}

func (m model) startInstance(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return opDoneMsg{err: m.mgr.Start(ctx, id)}
	}
}

func (m model) stopInstance(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return opDoneMsg{err: m.mgr.Stop(ctx, id)}
	}
}

func (m model) removeInstance(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return opDoneMsg{err: m.mgr.Remove(ctx, id, false)}
	}
}

func (m model) dumpInstance(inst docker.Instance, destPath string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		err := m.mgr.Dump(ctx, inst, destPath)
		return dumpDoneMsg{path: destPath, err: err}
	}
}

func (m model) restoreInstance(inst docker.Instance, srcPath string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		return opDoneMsg{err: m.mgr.Restore(ctx, inst, srcPath)}
	}
}

func (m model) cloneInstance(src docker.Instance, newSpec docker.Spec) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		return opDoneMsg{err: m.mgr.Clone(ctx, src, newSpec)}
	}
}

func (m model) recreateInstance(inst docker.Instance, newSpec docker.Spec) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		return opDoneMsg{err: m.mgr.Recreate(ctx, inst, newSpec)}
	}
}
