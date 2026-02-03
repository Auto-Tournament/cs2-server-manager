package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

func (m model) viewFastCopyConfirm() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Fast copy mode")) +
		"\n" +
		headerBorderStyle.Render("Speed up master → servers replication")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "Enable the fastest copy method for this install run?")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Recommended: Yes")
	fmt.Fprintln(&b, "  - Auto mode will try a copy-on-write clone (reflink) when supported,")
	fmt.Fprintln(&b, "    otherwise it uses a tuned local rsync.")
	fmt.Fprintln(&b, "  - Ownership will still be fixed at the end.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Press Enter/Y to enable fast copy, N to keep legacy behaviour, or Esc to cancel.")

	return b.String()
}

func (m model) updateFastCopyConfirmKey(key tea.KeyMsg) (model, tea.Cmd) {
	switch key.String() {
	case "esc":
		// Go back to wizard without starting.
		m.view = viewInstallWizard
		m.wizard.active = true
		return m, nil
	case "y", "Y", "enter":
		return m.startInstallWithCopyMode("auto")
	case "n", "N":
		return m.startInstallWithCopyMode("legacy")
	case "ctrl+c", "q":
		return m, tea.Quit
	default:
		return m, nil
	}
}

func (m model) startInstallWithCopyMode(mode string) (model, tea.Cmd) {
	// Store previous env so we can restore after wizard completes.
	if prev, ok := os.LookupEnv("CSM_COPY_MODE"); ok {
		m.installCopyModeHadPrev = true
		m.installCopyModePrev = prev
	} else {
		m.installCopyModeHadPrev = false
		m.installCopyModePrev = ""
	}
	_ = os.Setenv("CSM_COPY_MODE", mode)
	// Reset copy stats so the final wizard summary reflects this install run.
	csm.ResetCopyStats()

	// Initialize live timing for step 1.
	m.currentInstallStep = installStepPlugins
	m.installStepStart = time.Now()
	m.installStatusBase = "Step 1/4: Preparing plugin update..."
	m.installExpected = "~1–5 minutes"

	m.wizard.active = false
	m.view = viewMain
	m.running = true
	m.status = m.installStatusBase
	m.lastOutput = ""

	cfg := m.wizard.cfg
	return m, tea.Batch(runInstallStep(cfg, installStepPlugins), m.spin.Tick, tea.Tick(time.Second, func(time.Time) tea.Msg {
		return installElapsedMsg{}
	}))
}

func (m *model) restoreCopyModeEnvIfNeeded() {
	// Only restore if we previously captured a value (i.e., doctor/wizard set it).
	if m.installCopyModeHadPrev {
		_ = os.Setenv("CSM_COPY_MODE", m.installCopyModePrev)
	} else {
		_ = os.Unsetenv("CSM_COPY_MODE")
	}
	// Reset marker so we don't restore repeatedly.
	m.installCopyModeHadPrev = false
	m.installCopyModePrev = ""
}
