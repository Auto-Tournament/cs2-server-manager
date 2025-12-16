package csm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// TmuxManager provides a Go-native interface for managing CS2 tmux sessions.
// It replaces the old scripts/cs2_tmux.sh helper.
type TmuxManager struct {
	CS2User    string
	NumServers int
}

// NewTmuxManager discovers the CS2 service user and number of servers by
// inspecting /home/<user>/server-* directories.
func NewTmuxManager() (*TmuxManager, error) {
	user := os.Getenv("CS2_USER")
	if user == "" {
		user = "cs2servermanager"
	}

	home := filepath.Join("/home", user)
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", home, err)
	}

	maxServer := 0
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, "server-") {
			continue
		}
		nStr := strings.TrimPrefix(name, "server-")
		if n, err := strconv.Atoi(nStr); err == nil && n > maxServer {
			maxServer = n
		}
	}

	return &TmuxManager{
		CS2User:    user,
		NumServers: maxServer,
	}, nil
}

// serverDir returns /home/<user>/server-N.
func (m *TmuxManager) serverDir(server int) string {
	return filepath.Join("/home", m.CS2User, fmt.Sprintf("server-%d", server))
}

// sessionName returns the tmux session name for a given server.
func (m *TmuxManager) sessionName(server int) string {
	return fmt.Sprintf("cs2-%d", server)
}

func (m *TmuxManager) runAsCS2User(cmdline string) *exec.Cmd {
	return exec.Command("su", "-", m.CS2User, "-c", cmdline)
}

// Status returns a human-readable status for all known servers/sessions.
func (m *TmuxManager) Status() (string, error) {
	var buf bytes.Buffer
	fmt.Fprintln(&buf, "==========================================")
	fmt.Fprintln(&buf, "  CS2 Server Status (Tmux)")
	fmt.Fprintln(&buf, "==========================================")
	fmt.Fprintln(&buf)

	for i := 1; i <= m.NumServers; i++ {
		session := m.sessionName(i)
		cmd := m.runAsCS2User("tmux has-session -t " + session)
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(&buf, "Server %d: STOPPED\n\n", i)
			continue
		}
		gamePort := 27015 + (i-1)*10
		fmt.Fprintf(&buf, "Server %d (Port %d): RUNNING\n", i, gamePort)
		fmt.Fprintf(&buf, "  Attach: csm (attach not yet implemented, use: tmux attach -t %s)\n\n", session)
	}

	fmt.Fprintln(&buf, "==========================================")
	return buf.String(), nil
}

// StartAll starts all servers (creating tmux sessions if needed).
func (m *TmuxManager) StartAll() error {
	for i := 1; i <= m.NumServers; i++ {
		if err := m.Start(i); err != nil {
			return err
		}
	}
	return nil
}

// Start starts a single server in tmux.
func (m *TmuxManager) Start(server int) error {
	session := m.sessionName(server)
	serverDir := m.serverDir(server)
	gameDir := filepath.Join(serverDir, "game")

	// Kill any existing session first to ensure a clean log/console.
	_ = m.runAsCS2User("tmux kill-session -t " + session).Run()

	// Use the Valve cs2.sh script from the game directory.
	cmdline := fmt.Sprintf("cd %s && tmux new-session -d -s %s './cs2.sh -dedicated -ip 0.0.0.0 -usercon'", gameDir, session)
	if err := m.runAsCS2User(cmdline).Run(); err != nil {
		return fmt.Errorf("failed to start server %d in tmux: %w", server, err)
	}
	return nil
}

// StopAll stops all servers by killing their tmux sessions.
func (m *TmuxManager) StopAll() error {
	for i := 1; i <= m.NumServers; i++ {
		if err := m.Stop(i); err != nil {
			return err
		}
	}
	return nil
}

// Stop stops a single server by killing its tmux session.
func (m *TmuxManager) Stop(server int) error {
	session := m.sessionName(server)
	cmd := m.runAsCS2User("tmux kill-session -t " + session)
	if err := cmd.Run(); err != nil {
		// Treat "no such session" as non-fatal.
		return nil
	}
	return nil
}

// RestartAll restarts all servers.
func (m *TmuxManager) RestartAll() error {
	if err := m.StopAll(); err != nil {
		return err
	}
	return m.StartAll()
}

// Restart restarts a single server.
func (m *TmuxManager) Restart(server int) error {
	if err := m.Stop(server); err != nil {
		return err
	}
	return m.Start(server)
}

// Logs returns the last N lines from the tmux pane for a given server.
// If lines <= 0, the full pane history is returned.
func (m *TmuxManager) Logs(server, lines int) (string, error) {
	session := m.sessionName(server)
	args := []string{"capture-pane", "-p", "-t", session}
	if lines > 0 {
		// Start N lines from the bottom.
		args = append(args, "-S", fmt.Sprintf("-%d", lines))
	}
	cmd := m.runAsCS2User("tmux " + strings.Join(args, " "))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux logs failed: %w", err)
	}
	return string(out), nil
}

// Attach attaches the current terminal to a server's tmux session.
func (m *TmuxManager) Attach(server int) error {
	session := m.sessionName(server)
	cmd := m.runAsCS2User("tmux attach -t " + session)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ListSessions lists all cs2-* tmux sessions.
func (m *TmuxManager) ListSessions() (string, error) {
	cmd := m.runAsCS2User("tmux list-sessions 2>/dev/null || true")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Debug starts a single server in the foreground (no tmux) so all output goes
// to the current terminal.
func (m *TmuxManager) Debug(server int) error {
	serverDir := m.serverDir(server)
	gameDir := filepath.Join(serverDir, "game")
	cmd := m.runAsCS2User(fmt.Sprintf("cd %s && ./cs2.sh -dedicated -ip 0.0.0.0 -usercon", gameDir))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
