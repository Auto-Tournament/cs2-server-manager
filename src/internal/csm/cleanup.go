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
	CS2User        string
	ATCS2Container string
	ATCS2Volume    string
}

// CleanupAll removes all CS2 servers, their data, and (optionally) the
// Auto Tournament CS2 MySQL Docker container and volume. It mirrors the behaviour of
// scripts/cleanup_cs2.sh and returns a human-readable log.
func CleanupAll(cfg CleanupConfig) (string, error) {
	if os.Geteuid() != 0 {
		return "", fmt.Errorf("cleanup must be run as root (use sudo)")
	}

	if cfg.CS2User == "" {
		// Default to the dedicated service user created by the installer.
		cfg.CS2User = DefaultCS2User
	}
	if cfg.ATCS2Container == "" {
		cfg.ATCS2Container = DefaultATCS2ContainerName
	}
	if cfg.ATCS2Volume == "" {
		cfg.ATCS2Volume = DefaultATCS2VolumeName
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

	// Docker cleanup for the plugin database. A host that never ran an
	// update since the plugin rename still has the container under its old
	// name, so both names are removed.
	log("[*] Cleaning up the Auto Tournament CS2 MySQL Docker container...")
	if _, err := exec.LookPath("docker"); err == nil {
		// Check if container exists.
		if err := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}").Run(); err == nil {
			found := false
			for _, name := range []string{cfg.ATCS2Container, LegacyATCS2ContainerName} {
				if !hasDockerName(name) {
					continue
				}
				found = true
				log("  [*] Stopping and removing Docker container: %s", name)
				_ = exec.Command("docker", "stop", name).Run()
				_ = exec.Command("docker", "rm", name).Run()
			}
			if found {
				log("  [*] Removing Docker volume: %s", cfg.ATCS2Volume)
				_ = exec.Command("docker", "volume", "rm", cfg.ATCS2Volume).Run()
			} else {
				log("  [i] Auto Tournament CS2 MySQL container not found")
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
