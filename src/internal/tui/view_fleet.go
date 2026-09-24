package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

// The Servers dashboard: a live fleet table fed by each server's Ready Up
// /stream (csm.FleetWatcher). Process state (tmux) is re-read every
// fleetProcInterval, because tmux knows nothing about Ready Up and the other
// way round.

const fleetProcInterval = 10 * time.Second

// fleetState is the dashboard while it is open. Messages carry the pointer so
// a message from a dashboard that was already closed is ignored.
type fleetState struct {
	mgr     *csm.TmuxManager
	watcher *csm.FleetWatcher
	ctx     context.Context
	cancel  context.CancelFunc
	opened  time.Time
}

type fleetStartedMsg struct{ fleet *fleetState }

type fleetChangedMsg struct{ fleet *fleetState }

type fleetProcTickMsg struct{ fleet *fleetState }

type fleetProcMsg struct {
	fleet   *fleetState
	targets []csm.FleetTarget
}

// startFleetCmd discovers the servers (tmux checks go through su, so this
// runs off the UI goroutine) and starts the watcher.
func startFleetCmd() tea.Cmd {
	return func() tea.Msg {
		mgr, err := csm.NewTmuxManager()
		if err != nil {
			return viewportFinishedMsg{
				title:   "Servers dashboard",
				content: fmt.Sprintf("Failed to load servers: %v\n\nIf you haven't installed servers yet, run the install wizard first from the Setup tab.", err),
				err:     err,
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		f := &fleetState{
			mgr:     mgr,
			watcher: csm.NewFleetWatcher(mgr.FleetTargets(), csm.WatchOptions{}),
			ctx:     ctx,
			cancel:  cancel,
			opened:  time.Now(),
		}
		f.watcher.Run(ctx)
		return fleetStartedMsg{fleet: f}
	}
}

// waitFleetChange blocks until the watcher reports a change, then waits a
// moment so a burst of patches becomes one redraw.
func waitFleetChange(f *fleetState) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-f.ctx.Done():
			return nil
		case <-f.watcher.Changed():
		}
		select {
		case <-f.ctx.Done():
			return nil
		case <-time.After(200 * time.Millisecond):
		}
		return fleetChangedMsg{fleet: f}
	}
}

func fleetProcTick(f *fleetState) tea.Cmd {
	return tea.Tick(fleetProcInterval, func(time.Time) tea.Msg { return fleetProcTickMsg{fleet: f} })
}

func refreshFleetProcs(f *fleetState) tea.Cmd {
	return func() tea.Msg {
		if f.ctx.Err() != nil {
			return nil
		}
		return fleetProcMsg{fleet: f, targets: f.mgr.FleetTargets()}
	}
}

func (m *model) closeFleet() {
	if m.fleet != nil {
		m.fleet.cancel()
		m.fleet = nil
	}
}

// updateFleetMsg handles the dashboard's own messages. handled is false for
// anything else.
func (m model) updateFleetMsg(msg tea.Msg) (model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case fleetStartedMsg:
		m.running = false
		m.closeFleet()
		m.fleet = msg.fleet
		m.view = viewFleet
		m.status = ""
		return m, tea.Batch(waitFleetChange(msg.fleet), fleetProcTick(msg.fleet)), true
	case fleetChangedMsg:
		if m.fleet == nil || msg.fleet != m.fleet {
			return m, nil, true
		}
		// Re-rendering happens on return; just keep listening.
		return m, waitFleetChange(m.fleet), true
	case fleetProcTickMsg:
		if m.fleet == nil || msg.fleet != m.fleet {
			return m, nil, true
		}
		return m, tea.Batch(refreshFleetProcs(m.fleet), fleetProcTick(m.fleet)), true
	case fleetProcMsg:
		if m.fleet == nil || msg.fleet != m.fleet {
			return m, nil, true
		}
		for _, t := range msg.targets {
			m.fleet.watcher.SetProcess(t)
		}
		return m, nil, true
	}
	return m, nil, false
}

func (m model) updateFleetKey(key tea.KeyMsg) (model, tea.Cmd) {
	switch key.String() {
	case "enter", "q", "esc":
		m.closeFleet()
		m.view = viewMain
		return m, nil
	case "r":
		if m.fleet != nil {
			return m, refreshFleetProcs(m.fleet)
		}
	case "ctrl+c":
		m.closeFleet()
		return m, tea.Quit
	}
	return m, nil
}

func (m model) viewFleet() string {
	var b strings.Builder
	header := headerBorderStyle.Render(titleStyle.Render("Servers dashboard")) +
		"\n" +
		headerBorderStyle.Render("Live from Ready Up • r re-checks processes • Enter/q/Esc to return")
	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	if m.fleet == nil {
		fmt.Fprintln(&b, "Loading…")
		return b.String()
	}
	rows := m.fleet.watcher.Rows()
	fmt.Fprint(&b, csm.RenderFleetTable(rows, csm.FleetTableOptions{Color: true}))
	fmt.Fprintln(&b)

	streaming, polling := 0, 0
	for _, r := range rows {
		switch r.Source {
		case "stream":
			streaming++
		case "poll":
			polling++
		}
	}
	footer := fmt.Sprintf("Ready Up: %d live via /stream, %d polled", streaming, polling)
	if busy := csm.BusyServers(rows); len(busy) > 0 {
		footer += fmt.Sprintf(" • %d server(s) in a match: restart/stop/updates are held", len(busy))
	}
	fmt.Fprintln(&b, statusBarStyle.Render(footer))
	return b.String()
}
