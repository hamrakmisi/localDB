package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
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
	logsLoadedMsg struct {
		content string
		err     error
	}
	copyDoneMsg struct{ err error }
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
		if err == nil {
			readyCtx, cancelReady := context.WithTimeout(ctx, 5*time.Second)
			var wg sync.WaitGroup
			for i := range ins {
				if ins[i].State != "running" {
					continue
				}
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					ins[i].Ready = m.mgr.Ready(readyCtx, ins[i]) == nil
				}(i)
			}
			wg.Wait()
			cancelReady()
		}
		return listLoadedMsg{instances: ins, err: err}
	}
}

func (m model) loadLogs(inst docker.Instance) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		content, err := m.mgr.Logs(ctx, inst.ID)
		return logsLoadedMsg{content: content, err: err}
	}
}

func (m model) copyConnection(inst docker.Instance) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		uri, err := m.mgr.ConnectionURI(ctx, inst)
		if err != nil {
			return copyDoneMsg{err: err}
		}
		return copyDoneMsg{err: copyToClipboard(uri)}
	}
}

func copyToClipboard(value string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command = "pbcopy"
	case "windows":
		command = "clip"
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			command = "wl-copy"
		} else if _, err := exec.LookPath("xclip"); err == nil {
			command, args = "xclip", []string{"-selection", "clipboard"}
		} else {
			return fmt.Errorf("clipboard tool not found (install wl-copy or xclip)")
		}
	}
	cmd := exec.Command(command, args...)
	cmd.Stdin = strings.NewReader(value)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copy connection URI: %w", err)
	}
	return nil
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
