package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) viewDoctorConfirm() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Doctor")) +
		"\n" +
		headerBorderStyle.Render("Diagnose common install/runtime issues")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "This will scan for common issues and can optionally apply safe, automated fixes:")
	fmt.Fprintln(&b, "  - SteamCMD missing / wrapper missing")
	fmt.Fprintln(&b, "  - Wrong ownership under /home/<cs2user>")
	fmt.Fprintln(&b, "  - Steam Runtime missing when expected")
	fmt.Fprintln(&b, "  - Missing libv8.so (validate master + resync to servers)")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Choose an action:")
	fmt.Fprintln(&b, "  Enter/Y : Diagnose and apply fixes (recommended)")
	fmt.Fprintln(&b, "  R       : Diagnose only (no changes)")
	fmt.Fprintln(&b, "  Esc/N   : Cancel")

	return b.String()
}

func (m model) updateDoctorConfirmKey(key tea.KeyMsg) (model, tea.Cmd) {
	switch key.String() {
	case "esc", "n", "N":
		m.view = viewMain
		m.status = ""
		m.lastOutput = ""
		return m, nil
	case "r", "R":
		m.view = viewMain
		m.running = true
		m.status = "Running doctor (report only)..."
		m.lastOutput = ""
		return m, tea.Batch(runDoctorViewport(false), m.spin.Tick)
	case "enter", "y", "Y":
		m.view = viewMain
		m.running = true
		m.status = "Running doctor (applying fixes where possible)..."
		m.lastOutput = ""
		return m, tea.Batch(runDoctorViewport(true), m.spin.Tick)
	case "ctrl+c", "q":
		return m, tea.Quit
	default:
		return m, nil
	}
}
