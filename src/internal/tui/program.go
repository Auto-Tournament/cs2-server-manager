package tui

import tea "github.com/charmbracelet/bubbletea"

// program holds a reference to the running Bubble Tea program so that
// background goroutines (e.g. log tailers) can send messages back into the
// update loop for streaming output.
var program *tea.Program

// installCancel, when set, cancels the currently running install operation
// (bootstrap/plugins) so long-running processes like steamcmd are terminated
// when the user quits the TUI.
var installCancel func()

// scaleCancel, when set, cancels the currently running scale operation
// (add/remove servers) so long-running rsync operations can be terminated
// without quitting the entire TUI.
var scaleCancel func()

// SetProgram is called from main after creating the Bubble Tea program.
func SetProgram(p *tea.Program) {
	program = p
}

// send pushes a message into the running program, if available.
func send(msg tea.Msg) {
	if program != nil && msg != nil {
		program.Send(msg)
	}
}

// SetInstallCancel registers a cancel function for the active install
// operation. Passing nil clears any existing cancel function.
func SetInstallCancel(cancel func()) {
	installCancel = cancel
}

// CancelInstall cancels any active install operation. It's safe to call even
// if no install is currently running.
func CancelInstall() {
	if installCancel != nil {
		installCancel()
		installCancel = nil
	}
}

// SetScaleCancel registers a cancel function for the active scaling
// operation. Passing nil clears any existing cancel function.
func SetScaleCancel(cancel func()) {
	scaleCancel = cancel
}

// CancelScale cancels any active scaling operation. It's safe to call even
// if no scale operation is currently running.
func CancelScale() {
	if scaleCancel != nil {
		scaleCancel()
		scaleCancel = nil
	}
}
