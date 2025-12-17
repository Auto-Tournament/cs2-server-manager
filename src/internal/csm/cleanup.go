package csm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CleanupConfig controls how CleanupAll behaves.
type CleanupConfig struct {
	CS2User          string
	MatchzyContainer string
	MatchzyVolume    string
}

// CleanupAll removes all CS2 servers, their data, and (optionally) the
// MatchZy MySQL Docker container and volume. It mirrors the behaviour of
// scripts/cleanup_cs2.sh and returns a human-readable log.
func CleanupAll(cfg CleanupConfig) (string, error) {
	if os.Geteuid() != 0 {
		return "", fmt.Errorf("cleanup must be run as root (use sudo)")
	}

	if cfg.CS2User == "" {
		// Default to the dedicated service user created by the installer.
		cfg.CS2User = DefaultCS2User
	}
	if cfg.MatchzyContainer == "" {
		cfg.MatchzyContainer = DefaultMatchzyContainerName
	}
	if cfg.MatchzyVolume == "" {
		cfg.MatchzyVolume = DefaultMatchzyVolumeName
	}

	var buf bytes.Buffer
	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !strings.HasSuffix(format, "\n") {
			buf.WriteByte('\n')
		}
	}

	log("=== CS2 Server Cleanup ===")
	log("This will DELETE all CS2 servers and their data!")
	log("")

	// Check that the user exists.
	if err := exec.Command("id", "-u", cfg.CS2User).Run(); err != nil {
		log("User %q not found. Nothing to clean up.", cfg.CS2User)
		logOut := buf.String()
		AppendLog("cleanup.log", logOut)
		return logOut, nil
	}

	log("CS2 User: %s", cfg.CS2User)
	log("Home Dir: /home/%s", cfg.CS2User)
	log("")

	// Stop all tmux sessions belonging to the CS2 user. We reuse the
	// TmuxManager where possible, but also fall back to a direct scan
	// to mirror the shell script behaviour.
	log("[*] Stopping all tmux sessions...")
	if mgr, err := NewTmuxManager(); err == nil {
		_ = mgr.StopAll()
	}

	// Best-effort direct kill of any remaining cs2-* sessions.
	cmdList := exec.Command("su", "-", cfg.CS2User, "-c", "tmux list-sessions 2>/dev/null | grep cs2- | cut -d: -f1")
	out, _ := cmdList.CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		session := strings.TrimSpace(line)
		if session == "" {
			continue
		}
		log("  [*] Stopping tmux session: %s", session)
		_ = exec.Command("su", "-", cfg.CS2User, "-c", "tmux send-keys -t "+session+" 'quit' C-m 2>/dev/null").Run()
		_ = exec.Command("su", "-", cfg.CS2User, "-c", "tmux kill-session -t "+session+" 2>/dev/null").Run()
	}

	// Docker cleanup for MatchZy.
	log("[*] Cleaning up MatchZy MySQL Docker container...")
	if _, err := exec.LookPath("docker"); err == nil {
		// Check if container exists.
		if err := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}").Run(); err == nil {
			if hasDockerName(cfg.MatchzyContainer) {
				log("  [*] Stopping and removing Docker container: %s", cfg.MatchzyContainer)
				_ = exec.Command("docker", "stop", cfg.MatchzyContainer).Run()
				_ = exec.Command("docker", "rm", cfg.MatchzyContainer).Run()
				log("  [*] Removing Docker volume: %s", cfg.MatchzyVolume)
				_ = exec.Command("docker", "volume", "rm", cfg.MatchzyVolume).Run()
			} else {
				log("  [i] MatchZy MySQL container not found")
			}
		}
	} else {
		log("  [i] Docker not installed, skipping container cleanup")
	}

	// Remove the user and home directory.
	log("[*] Removing user and home directory...")
	log("  [*] Deleting /home/%s", cfg.CS2User)

	del := exec.Command("userdel", "-r", cfg.CS2User)
	if err := del.Run(); err != nil {
		// Fallback: try manual directory removal and userdel without -r.
		_ = os.RemoveAll("/home/" + cfg.CS2User)
		_ = exec.Command("userdel", cfg.CS2User).Run()
	}

	log("")
	log("[✓] Cleanup complete!")
	log("You can now run csm to install or repair servers via the TUI.")

	logOut := buf.String()
	AppendLog("cleanup.log", logOut)
	return logOut, nil
}

func hasDockerName(name string) bool {
	cmd := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}
