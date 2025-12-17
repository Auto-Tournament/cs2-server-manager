package csm

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// TmuxManager provides a Go-native interface for managing CS2 tmux sessions.
type TmuxManager struct {
	CS2User    string
	NumServers int
}

// NewTmuxManager discovers the CS2 service user and number of servers.
// It prefers the CS2_USER environment variable when set, then falls back to
// scanning /home for any user that has server-* directories. This makes it
// resilient to older installs that might have used a different CS2 user.
func NewTmuxManager() (*TmuxManager, error) {
	// Helper to count server-* directories for a given user.
	countServers := func(user string) (int, error) {
		home := filepath.Join("/home", user)
		entries, err := os.ReadDir(home)
		if err != nil {
			return 0, err
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
		return maxServer, nil
	}

	// 1) If CS2_USER is explicitly set, trust it.
	if envUser := os.Getenv("CS2_USER"); envUser != "" {
		n, err := countServers(envUser)
		if err != nil {
			return nil, fmt.Errorf("CS2_USER=%q is set but /home/%s could not be inspected for servers: %w", envUser, envUser, err)
		}
		log.Printf("[tmux] NewTmuxManager: using CS2_USER=%q with %d server(s)", envUser, n)
		return &TmuxManager{
			CS2User:    envUser,
			NumServers: n,
		}, nil
	}

	// 2) Prefer the modern default user if it exists.
	if n, err := countServers(DefaultCS2User); err == nil && n > 0 {
		log.Printf("[tmux] NewTmuxManager: discovered %s with %d server(s)", DefaultCS2User, n)
		return &TmuxManager{
			CS2User:    DefaultCS2User,
			NumServers: n,
		}, nil
	}

	// 3) Fall back to scanning all users under /home to support older setups
	// that may have used a different CS2 user name.
	homeRoot := "/home"
	homeEntries, err := os.ReadDir(homeRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", homeRoot, err)
	}

	bestUser := ""
	bestCount := 0
	for _, e := range homeEntries {
		if !e.IsDir() {
			continue
		}
		user := e.Name()
		n, err := countServers(user)
		if err != nil || n == 0 {
			continue
		}
		if n > bestCount {
			bestCount = n
			bestUser = user
		}
	}

	if bestUser == "" {
		// No server-* directories found anywhere under /home; treat as a
		// "no servers installed yet" situation.
		log.Printf("[tmux] NewTmuxManager: no server-* directories found under /home; returning NumServers=0")
		return &TmuxManager{
			CS2User:    DefaultCS2User,
			NumServers: 0,
		}, nil
	}

	log.Printf("[tmux] NewTmuxManager: selected user=%q with %d server(s)", bestUser, bestCount)
	return &TmuxManager{
		CS2User:    bestUser,
		NumServers: bestCount,
	}, nil
}

// serverDir returns /home/<user>/server-N.
func (m *TmuxManager) serverDir(server int) string {
	return filepath.Join("/home", m.CS2User, fmt.Sprintf("server-%d", server))
}

// serverLogFile returns a persistent log file path for a given server.
// We keep these under the CS2 user's home so they are writable by that user
// and survive tmux session restarts.
func (m *TmuxManager) serverLogFile(server int) string {
	return filepath.Join("/home", m.CS2User, "logs", fmt.Sprintf("server-%d.log", server))
}

// ServerLogPath exposes the underlying log file path for a given server.
// This is used by CLI/TUI helpers so users can discover or tail logs directly.
func (m *TmuxManager) ServerLogPath(server int) string {
	return m.serverLogFile(server)
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

	if m.NumServers <= 0 {
		fmt.Fprintln(&buf, "No CS2 servers found.")
		fmt.Fprintln(&buf, "Run the install wizard from the Setup tab to create servers.")
		fmt.Fprintln(&buf)
		fmt.Fprintln(&buf, "==========================================")
		return buf.String(), nil
	}

	for i := 1; i <= m.NumServers; i++ {
		gamePort, tvPort := detectServerPorts(m.CS2User, i)
		session := m.sessionName(i)
		cmd := m.runAsCS2User("tmux has-session -t " + session)
		if err := cmd.Run(); err != nil {
			// STOPPED in red.
			fmt.Fprintf(&buf, "Server %d (Game %d, GOTV %d): \x1b[31mSTOPPED\x1b[0m\n\n", i, gamePort, tvPort)
			continue
		}
		// RUNNING in green.
		fmt.Fprintf(&buf, "Server %d (Game %d, GOTV %d): \x1b[32mRUNNING\x1b[0m\n", i, gamePort, tvPort)
		fmt.Fprintf(&buf, "  Attach: csm attach %d\n", i)
		fmt.Fprintln(&buf)
	}

	fmt.Fprintln(&buf, "==========================================")
	return buf.String(), nil
}

// StartAll starts all servers (creating tmux sessions if needed).
func (m *TmuxManager) StartAll() error {
	if m.NumServers <= 0 {
		log.Printf("[tmux] StartAll: no servers to start (NumServers=0, user=%q)", m.CS2User)
		return fmt.Errorf("no CS2 servers found; run the install wizard first")
	}
	log.Printf("[tmux] StartAll: starting %d server(s) for user=%q", m.NumServers, m.CS2User)
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
	logFile := m.serverLogFile(server)

	// Kill any existing session first to ensure a clean log/console.
	_ = m.runAsCS2User("tmux kill-session -t " + session).Run()

	// Use the Valve cs2.sh script from the game directory and tee output into
	// a persistent per-server log file so logs survive tmux restarts.
	cmdline := fmt.Sprintf(
		"mkdir -p %s && cd %s && tmux new-session -d -s %s './cs2.sh -dedicated -ip 0.0.0.0 -usercon 2>&1 | tee -a %s'",
		filepath.Dir(logFile),
		gameDir,
		session,
		logFile,
	)
	log.Printf("[tmux] Start: server=%d user=%q session=%q serverDir=%q gameDir=%q cmdline=%q", server, m.CS2User, session, serverDir, gameDir, cmdline)
	if err := m.runAsCS2User(cmdline).Run(); err != nil {
		log.Printf("[tmux] Start: failed to start server %d: %v", server, err)
		return fmt.Errorf("failed to start server %d in tmux: %w", server, err)
	}
	return nil
}

// StopAll stops all servers by killing their tmux sessions.
func (m *TmuxManager) StopAll() error {
	if m.NumServers <= 0 {
		return fmt.Errorf("no CS2 servers found; run the install wizard first")
	}
	for i := 1; i <= m.NumServers; i++ {
		if err := m.Stop(i); err != nil {
			return err
		}
	}
	return nil
}

// Stop stops a single server by killing its tmux session.
func (m *TmuxManager) Stop(server int) error {
	// Before stopping the session, capture and persist logs so they survive
	// server shutdown and can be inspected later.
	if out, err := m.Logs(server, 0); err != nil {
		LogAction("tmux", fmt.Sprintf("logs server-%d (before stop)", server), "", err)
	} else if strings.TrimSpace(out) != "" {
		LogAction("tmux", fmt.Sprintf("logs server-%d (before stop)", server), out, nil)
	}

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
	if m.NumServers <= 0 {
		return fmt.Errorf("no CS2 servers found; run the install wizard first")
	}
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
// If lines <= 0, the full history is returned. Prefer the persistent per-
// server log file when available so logs survive tmux restarts; fall back
// to tmux capture-pane otherwise.
func (m *TmuxManager) Logs(server, lines int) (string, error) {
	logPath := m.serverLogFile(server)
	if fi, err := os.Stat(logPath); err == nil && !fi.IsDir() {
		data, err := os.ReadFile(logPath)
		if err != nil {
			return "", fmt.Errorf("failed to read server log file %s: %w", logPath, err)
		}
		text := string(data)
		if lines <= 0 {
			return text, nil
		}
		allLines := strings.Split(text, "\n")
		if len(allLines) > lines {
			allLines = allLines[len(allLines)-lines:]
		}
		return strings.Join(allLines, "\n"), nil
	}

	// Fallback: capture from the live tmux pane.
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
