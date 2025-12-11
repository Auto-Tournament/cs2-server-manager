package csm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type TmuxManager struct {
	CS2User    string
	NumServers int
	BasePort   int
}

func NewTmuxManager() (*TmuxManager, error) {
	user := getenvDefault("CS2_USER", "cs2")
	basePort := intFromEnv("BASE_GAME_PORT", 27015)

	numServers := intFromEnv("NUM_SERVERS", 0)
	if numServers <= 0 {
		// Auto-detect number of servers by scanning /home/<user>/server-*
		root := filepath.Join("/home", user)
		entries, err := os.ReadDir(root)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() && strings.HasPrefix(e.Name(), "server-") {
					numServers++
				}
			}
		}
		if numServers == 0 {
			numServers = 3
		}
	}

	if err := ensureTmuxInstalled(); err != nil {
		return nil, err
	}

	return &TmuxManager{
		CS2User:    user,
		NumServers: numServers,
		BasePort:   basePort,
	}, nil
}

// Status returns a human-readable overview of all servers and their tmux state.
func (m *TmuxManager) Status() (string, error) {
	var b bytes.Buffer
	fmt.Fprintln(&b, "==========================================")
	fmt.Fprintln(&b, "  CS2 Server Status (Tmux)")
	fmt.Fprintln(&b, "==========================================")
	fmt.Fprintln(&b)

	found := 0
	for i := 1; i <= m.NumServers; i++ {
		serverDir := m.serverDir(i)
		if _, err := os.Stat(serverDir); err != nil {
			continue
		}
		found++

		port := m.serverPort(i)
		session := m.sessionName(i)

		fmt.Fprintf(&b, "Server %d (Port %d): ", i, port)
		if m.sessionExists(session) {
			fmt.Fprintln(&b, "RUNNING")
			fmt.Fprintf(&b, "  Attach: csm (attach not yet implemented, use: tmux attach -t %s)\n", session)
		} else {
			fmt.Fprintln(&b, "STOPPED")
			fmt.Fprintf(&b, "  Start:  use CSM start all or start single when implemented\n")
		}
		fmt.Fprintln(&b)
	}

	if found == 0 {
		return "No CS2 servers detected.\n\nInstall servers via the Setup tab (Install / redeploy servers) and then return to this dashboard.", nil
	}

	fmt.Fprintln(&b, "==========================================")
	return b.String(), nil
}

func (m *TmuxManager) StartAll() error {
	for i := 1; i <= m.NumServers; i++ {
		if err := m.Start(i); err != nil {
			return err
		}
	}
	return nil
}

// StopAll stops all configured CS2 servers by killing their tmux sessions.
func (m *TmuxManager) StopAll() error {
	for i := 1; i <= m.NumServers; i++ {
		if err := m.Stop(i); err != nil {
			return err
		}
	}
	return nil
}

// Stop stops a single CS2 server (if running) by killing its tmux session.
func (m *TmuxManager) Stop(server int) error {
	if err := ensureTmuxInstalled(); err != nil {
		return err
	}
	session := m.sessionName(server)
	if !m.sessionExists(session) {
		return nil
	}
	// Try a graceful shutdown first by sending "quit" to the console, then
	// force-kill the session if it is still present.
	send := m.tmuxCmd("send-keys", "-t", session, "quit", "C-m")
	_ = send.Run()
	time.Sleep(2 * time.Second)

	kill := m.tmuxCmd("kill-session", "-t", session)
	if out, err := kill.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to stop server %d: %w (%s)", server, err, string(out))
	}
	return nil
}

func (m *TmuxManager) Start(server int) error {
	if err := ensureTmuxInstalled(); err != nil {
		return err
	}

	serverDir := m.serverDir(server)
	if _, err := os.Stat(serverDir); err != nil {
		return fmt.Errorf("server %d not found at %s", server, serverDir)
	}

	session := m.sessionName(server)
	if m.sessionExists(session) {
		return nil
	}

	port := m.serverPort(server)
	gameDir := filepath.Join(serverDir, "game")

	args := []string{
		"new-session", "-d",
		"-s", session,
		"-c", gameDir,
		"./cs2.sh", "-dedicated", "-ip", "0.0.0.0",
		"+map", "de_dust2",
		"-port", strconv.Itoa(port),
		"+tv_port", strconv.Itoa(port + 5),
		"+maxplayers", "10",
		"-usercon",
	}

	cmd := m.tmuxCmd(args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start server %d: %w (%s)", server, err, string(out))
	}

	// Give tmux a moment to start the session.
	time.Sleep(500 * time.Millisecond)
	return nil
}

// Restart restarts a single server by stopping and then starting its tmux
// session.
func (m *TmuxManager) Restart(server int) error {
	if err := m.Stop(server); err != nil {
		return err
	}
	time.Sleep(1 * time.Second)
	return m.Start(server)
}

// RestartAll restarts all configured servers.
func (m *TmuxManager) RestartAll() error {
	for i := 1; i <= m.NumServers; i++ {
		if err := m.Restart(i); err != nil {
			return err
		}
	}
	return nil
}

func (m *TmuxManager) Logs(server, lines int) (string, error) {
	serverDir := m.serverDir(server)
	if _, err := os.Stat(serverDir); err != nil {
		return "", fmt.Errorf("server %d not found at %s", server, serverDir)
	}
	session := m.sessionName(server)
	if m.sessionExists(session) {
		args := []string{"capture-pane", "-t", session, "-p"}
		cmd := m.tmuxCmd(args...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			text := string(out)
			if lines > 0 {
				return tailLines(text, lines), nil
			}
			return text, nil
		}
	}

	// Fallback to CounterStrikeSharp logs if tmux session is not available,
	// similar to the legacy shell script behaviour.
	logDir := filepath.Join(serverDir, "game", "csgo", "addons", "counterstrikesharp", "logs")
	entries, err := os.ReadDir(logDir)
	if err != nil || len(entries) == 0 {
		return "", fmt.Errorf("no tmux session or CounterStrikeSharp logs found for server %d", server)
	}

	var latestPath string
	var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "log-all") || !strings.HasSuffix(name, ".txt") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestPath = filepath.Join(logDir, name)
		}
	}

	if latestPath == "" {
		return "", fmt.Errorf("no suitable log-all*.txt files found for server %d", server)
	}

	data, err := os.ReadFile(latestPath)
	if err != nil {
		return "", fmt.Errorf("failed to read log file %s: %w", latestPath, err)
	}
	text := string(data)
	if lines > 0 {
		text = tailLines(text, lines)
	}
	header := fmt.Sprintf("Latest CounterStrikeSharp log for server %d:\nFile: %s\n\n", server, latestPath)
	return header + text, nil
}

// Attach attaches the current terminal to the tmux session for a given
// server, mirroring `cs2_tmux.sh attach`.
func (m *TmuxManager) Attach(server int) error {
	if err := ensureTmuxInstalled(); err != nil {
		return err
	}
	session := m.sessionName(server)
	if !m.sessionExists(session) {
		return fmt.Errorf("server %d is not running (session %s not found)", server, session)
	}
	cmd := m.tmuxCmd("attach-session", "-t", session)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ListSessions returns a summary of all tmux sessions related to CS2,
// similar to `cs2_tmux.sh list`.
func (m *TmuxManager) ListSessions() (string, error) {
	if err := ensureTmuxInstalled(); err != nil {
		return "", err
	}
	cmd := m.tmuxCmd("list-sessions")
	out, err := cmd.CombinedOutput()
	// If tmux exits non-zero because there are no sessions, that's fine.
	lines := strings.Split(string(out), "\n")
	var filtered []string
	for _, line := range lines {
		if strings.Contains(line, "cs2-") {
			filtered = append(filtered, line)
		}
	}
	if len(filtered) == 0 {
		return "No CS2 tmux sessions running.", nil
	}
	return strings.Join(filtered, "\n"), err
}

// Debug runs a server in the foreground (no tmux), closely matching the
// behaviour of the `debug` mode in the original script.
func (m *TmuxManager) Debug(server int) error {
	serverDir := m.serverDir(server)
	if _, err := os.Stat(serverDir); err != nil {
		return fmt.Errorf("server %d not found at %s", server, serverDir)
	}
	port := m.serverPort(server)
	gameDir := filepath.Join(serverDir, "game")

	args := []string{
		"./cs2.sh",
		"-dedicated",
		"-ip", "0.0.0.0",
		"+map", "de_dust2",
		"-port", strconv.Itoa(port),
		"+tv_port", strconv.Itoa(port + 5),
		"+maxplayers", "10",
		"-usercon",
	}

	var cmd *exec.Cmd
	if os.Geteuid() == 0 && runtime.GOOS != "windows" {
		base := []string{"-u", m.CS2User}
		cmd = exec.Command("sudo", append(base, args...)...)
	} else {
		cmd = exec.Command(args[0], args[1:]...)
	}
	cmd.Dir = gameDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("Starting server %d in DEBUG mode (port %d). Press Ctrl+C to stop.\n\n", server, port)
	return cmd.Run()
}

// Helpers

func (m *TmuxManager) serverDir(n int) string {
	return filepath.Join("/home", m.CS2User, fmt.Sprintf("server-%d", n))
}

func (m *TmuxManager) serverPort(n int) int {
	return m.BasePort + (n-1)*10
}

func (m *TmuxManager) sessionName(n int) string {
	return fmt.Sprintf("cs2-%d", n)
}

func (m *TmuxManager) sessionExists(name string) bool {
	cmd := m.tmuxCmd("has-session", "-t", name)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func (m *TmuxManager) tmuxCmd(args ...string) *exec.Cmd {
	// If running as root, use sudo -u cs2user to mimic the shell script behavior.
	if os.Geteuid() == 0 && runtime.GOOS != "windows" {
		base := []string{"-u", m.CS2User, "tmux"}
		return exec.Command("sudo", append(base, args...)...)
	}
	return exec.Command("tmux", args...)
}

func ensureTmuxInstalled() error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is not installed; install it with your package manager (e.g. apt-get install tmux)")
	}
	return nil
}

func getenvDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func intFromEnv(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if n <= 0 || n >= len(lines) {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}


