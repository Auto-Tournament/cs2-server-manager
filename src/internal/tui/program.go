package tui

import tea "github.com/charmbracelet/bubbletea"

// program holds a reference to the running Bubble Tea program so that
// background goroutines (e.g. log tailers) can send messages back into the
// update loop for streaming output.
var program *tea.Program

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


