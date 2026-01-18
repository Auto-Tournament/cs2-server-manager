package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

// configEditorState holds the state for the server config editor
type configEditorState struct {
	rconPassword string
	maxPlayers   string
	gslt         string
	cursor       int // 0=rcon, 1=maxplayers, 2=gslt, 3=apply, 4=cancel
	editing      bool
	input        textinput.Model
	errMsg       string
}

// configEditorField indices
const (
	configFieldRCON = iota
	configFieldMaxPlayers
	configFieldGSLT
	configFieldApply
	configFieldCancel
	configFieldCount
)

func (m model) viewEditServerConfigs() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Update server configs")) +
		"\n" +
		headerBorderStyle.Render("Edit configuration values, then choose Apply changes.")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	// Helper to render a field row
	renderField := func(index int, label, value string) {
		selected := index == m.configEditor.cursor
		style := menuItemStyle
		if selected {
			style = menuSelectedStyle
		}
		line := fmt.Sprintf("%-20s %s", label, value)
		fmt.Fprintln(&b, style.Render(line))
		fmt.Fprintln(&b)
	}

	// RCON password
	rconVal := m.configEditor.rconPassword
	if m.configEditor.cursor == configFieldRCON && m.configEditor.editing {
		rconVal = m.configEditor.input.View()
	}
	renderField(configFieldRCON, "RCON password:", rconVal)

	// Max players
	maxPlayersVal := m.configEditor.maxPlayers
	if m.configEditor.cursor == configFieldMaxPlayers && m.configEditor.editing {
		maxPlayersVal = m.configEditor.input.View()
	}
	renderField(configFieldMaxPlayers, "Max players:", maxPlayersVal)

	// GSLT token
	gsltVal := m.configEditor.gslt
	if m.configEditor.cursor == configFieldGSLT && m.configEditor.editing {
		gsltVal = m.configEditor.input.View()
	}
	renderField(configFieldGSLT, "GSLT token (optional):", gsltVal)

	// Action buttons
	applyLabel := "Apply changes"
	cancelLabel := "Cancel"
	renderField(configFieldApply, "", applyLabel)
	renderField(configFieldCancel, "", cancelLabel)

	// Description
	var desc string
	switch m.configEditor.cursor {
	case configFieldRCON:
		desc = "RCON password for remote console access. Applied to all servers."
	case configFieldMaxPlayers:
		desc = "Maximum number of players (0 or empty = use CS2 default, typically 10)."
	case configFieldGSLT:
		desc = "Steam Game Server Login Token (GSLT) for server authentication. Optional but recommended for public servers. Leave empty to keep current value."
	case configFieldApply:
		desc = "Apply these changes to all servers. Servers will be stopped, updated, and restarted."
	case configFieldCancel:
		desc = "Return to the main menu without applying changes."
	}

	if desc != "" {
		fmt.Fprintln(&b, subtleStyle.Render(desc))
	}
	fmt.Fprintln(&b)

	// Error message
	if m.configEditor.errMsg != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.configEditor.errMsg))
	}

	return b.String()
}

func (m model) updateEditServerConfigs(msg tea.Msg) (model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "up", "k":
		if m.configEditor.cursor > 0 {
			m.configEditor.cursor--
			m.configEditor.editing = false
			m.configEditor.errMsg = ""
		}
		return m, nil
	case "down", "j":
		if m.configEditor.cursor < configFieldCount-1 {
			m.configEditor.cursor++
			m.configEditor.editing = false
			m.configEditor.errMsg = ""
		}
		return m, nil
	case "esc":
		if m.configEditor.editing {
			m.configEditor.editing = false
			m.configEditor.errMsg = ""
			return m, nil
		}
		m.view = viewMain
		return m, nil
	case "ctrl+c", "q":
		if m.configEditor.editing {
			// When editing, cancel editing instead of quitting
			m.configEditor.editing = false
			m.configEditor.errMsg = ""
			return m, nil
		}
		// When not editing, quit like other menus
		return m, tea.Quit
	}

	// When editing a field
	if m.configEditor.editing {
		switch key.String() {
		case "enter":
			val := strings.TrimSpace(m.configEditor.input.Value())
			switch m.configEditor.cursor {
			case configFieldRCON:
				m.configEditor.rconPassword = val
			case configFieldMaxPlayers:
				m.configEditor.maxPlayers = val
			case configFieldGSLT:
				m.configEditor.gslt = val
			}
			m.configEditor.editing = false
			m.configEditor.errMsg = ""
			return m, nil
		case "ctrl+c", "esc":
			// Cancel editing
			m.configEditor.editing = false
			m.configEditor.errMsg = ""
			return m, nil
		default:
			var cmd tea.Cmd
			m.configEditor.input, cmd = m.configEditor.input.Update(key)
			return m, cmd
		}
	}

	// Not editing: handle field selection and actions
	switch key.String() {
	case "enter", " ":
		switch m.configEditor.cursor {
		case configFieldRCON, configFieldMaxPlayers, configFieldGSLT:
			m.configEditor.editing = true
			m.configEditor.errMsg = ""
			var initial string
			switch m.configEditor.cursor {
			case configFieldRCON:
				initial = m.configEditor.rconPassword
			case configFieldMaxPlayers:
				initial = m.configEditor.maxPlayers
			case configFieldGSLT:
				initial = m.configEditor.gslt
			}
			m.configEditor.input.SetValue(initial)
			m.configEditor.input.CursorEnd()
			return m, nil
		case configFieldApply:
			// Validate and apply
			if strings.TrimSpace(m.configEditor.rconPassword) == "" {
				m.configEditor.errMsg = "RCON password is required"
				return m, nil
			}

			maxPlayers := 0
			if strings.TrimSpace(m.configEditor.maxPlayers) != "" {
				if n, err := strconv.Atoi(strings.TrimSpace(m.configEditor.maxPlayers)); err == nil && n >= 0 {
					maxPlayers = n
				} else {
					m.configEditor.errMsg = "Max players must be a non-negative integer"
					return m, nil
				}
			}

			// Apply changes
			m.view = viewMain
			m.running = true
			m.status = "Updating server configurations..."
			m.lastOutput = ""

			cfg := csm.UpdateServerConfigsConfig{
				RCONPassword: m.configEditor.rconPassword,
				MaxPlayers:   maxPlayers,
				GSLT:         m.configEditor.gslt,
			}

			return m, tea.Batch(runUpdateServerConfigsGo(cfg), m.spin.Tick)
		case configFieldCancel:
			m.view = viewMain
			return m, nil
		}
	}

	return m, nil
}

// runUpdateServerConfigsGo runs the update with specific config values
func runUpdateServerConfigsGo(cfg csm.UpdateServerConfigsConfig) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		SetInstallCancel(cancel)
		defer CancelInstall()

		out, err := csm.UpdateServerConfigsWithContext(ctx, cfg)
		return commandFinishedMsg{
			item: menuItem{
				title: "Update server configs",
				kind:  itemUpdateServerConfigs,
			},
			output: out,
			err:    err,
		}
	}
}

// initConfigEditor loads current values from existing servers
func (m *model) initConfigEditor() {
	mgr, err := csm.NewTmuxManager()
	if err != nil || mgr.NumServers <= 0 {
		// Defaults if we can't detect
		m.configEditor.rconPassword = ""
		m.configEditor.maxPlayers = ""
		m.configEditor.gslt = ""
		m.configEditor.cursor = 0
		m.configEditor.editing = false
		m.configEditor.errMsg = ""
		return
	}

	user := mgr.CS2User
	rcon := csm.DetectRCONPassword(user)
	maxPlayers := csm.DetectMaxPlayers(user)
	gslt := csm.DetectGSLT(user)

	m.configEditor.rconPassword = rcon
	if maxPlayers > 0 {
		m.configEditor.maxPlayers = fmt.Sprintf("%d", maxPlayers)
	} else {
		m.configEditor.maxPlayers = ""
	}
	m.configEditor.gslt = gslt
	m.configEditor.cursor = 0
	m.configEditor.editing = false
	m.configEditor.errMsg = ""
	
	// Reset input
	m.configEditor.input = textinput.New()
	m.configEditor.input.Placeholder = ""
	m.configEditor.input.Focus()
}
