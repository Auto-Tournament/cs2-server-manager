package csm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// CleanupConfig controls how CleanupAll behaves.
type CleanupConfig struct {
	// CS2User is the account whose servers are removed. The CLI fills it
	// from --user, else SUDO_USER (the account that ran sudo).
	CS2User string
	// InvokingUser is the account that ran sudo (SUDO_USER). That account is
	// never deleted: in user mode it is a person's login.
	InvokingUser string
	// DeleteUser also deletes CS2User and its home directory (userdel -r),
	// for a dedicated service account such as the cs2servermanager user of
	// older installs. Without it only csm's files in the home are removed.
	DeleteUser       bool
	MatchzyContainer string
	MatchzyVolume    string
}

// cleanupHomePaths are what csm creates in the CS2 user's home; cleanup-all
// removes these and nothing else there unless the user itself is deleted.
var cleanupHomePaths = []string{"server-*", "master-install", "cs2-config", "game_files", "overrides", "instances", "instances.json"}

// checkCleanupTarget refuses a target cleanup-all must never act on: none
// at all, root, a system account, or (with deleteUser) the account that ran
// sudo.
func checkCleanupTarget(target, invoking string, uid int, deleteUser bool) error {
	switch {
	case target == "":
		return fmt.Errorf("no user to clean up: run it with sudo from the account that runs csm, or pass --user <name>")
	case target == "root" || uid == 0:
		return fmt.Errorf("refusing to clean up root")
	case uid < 1000:
		return fmt.Errorf("refusing to clean up system account %s (uid %d)", target, uid)
	case deleteUser && target == invoking:
		return fmt.Errorf("refusing to delete %s: that is the account you ran sudo from. Leave out --delete-user to remove only the servers", target)
	}
	return nil
}

// CleanupAll stops and removes all CS2 servers of one user and their data,
// and the MatchZy MySQL Docker container and volume. It deletes the user
// itself only with DeleteUser. It needs root (userdel, Docker, another
// user's files) and returns a human-readable log.
func CleanupAll(cfg CleanupConfig) (string, error) {
	if os.Geteuid() != 0 {
		return "", fmt.Errorf("wiping the servers needs root: run `sudo csm cleanup-all`")
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

	if cfg.CS2User == "" {
		return "", checkCleanupTarget("", cfg.InvokingUser, -1, cfg.DeleteUser)
	}
	u, err := user.Lookup(cfg.CS2User)
	if err != nil {
		log("User %q not found. Nothing to clean up.", cfg.CS2User)
		logOut := buf.String()
		AppendLog("cleanup.log", logOut)
		return logOut, nil
	}
	uid, _ := strconv.Atoi(u.Uid)
	if err := checkCleanupTarget(cfg.CS2User, cfg.InvokingUser, uid, cfg.DeleteUser); err != nil {
		return "", err
	}
	home := u.HomeDir

	log("=== CS2 Server Cleanup ===")
	log("This will DELETE all CS2 servers and their data!")
	log("")

	log("CS2 User: %s", cfg.CS2User)
	log("Home Dir: %s", home)
	log("")

	// Stop the servers as their user: `csm stop --force` knows both the tmux
	// servers and the instances. Then kill any cs2-* tmux session left.
	log("[*] Stopping the servers...")
	_ = userShellCommand(cfg.CS2User, "csm stop --force >/dev/null 2>&1").Run()

	// Best-effort direct kill of any remaining cs2-* sessions.
	cmdList := userShellCommand(cfg.CS2User, "tmux list-sessions 2>/dev/null | grep cs2- | cut -d: -f1")
	out, _ := cmdList.CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		session := strings.TrimSpace(line)
		if session == "" {
			continue
		}
		log("  [*] Stopping tmux session: %s", session)
		_ = userShellCommand(cfg.CS2User, "tmux send-keys -t "+session+" 'quit' C-m 2>/dev/null").Run()
		_ = userShellCommand(cfg.CS2User, "tmux kill-session -t "+session+" 2>/dev/null").Run()
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

	// The monitor cron entry would otherwise keep running against servers
	// that are gone.
	_ = userShellCommand(cfg.CS2User, "csm remove-monitor-cron >/dev/null 2>&1").Run()

	if cfg.DeleteUser {
		log("[*] Deleting user %s and %s...", cfg.CS2User, home)
		if out, err := exec.Command("userdel", "-r", cfg.CS2User).CombinedOutput(); err != nil {
			log("  [!] userdel -r %s: %v %s", cfg.CS2User, err, strings.TrimSpace(string(out)))
		}
	} else {
		log("[*] Removing csm's files from %s (the user stays)...", home)
		for _, pattern := range cleanupHomePaths {
			matches, _ := filepath.Glob(filepath.Join(home, pattern))
			for _, path := range matches {
				log("  [*] %s", path)
				if err := os.RemoveAll(path); err != nil {
					log("  [!] %v", err)
				}
			}
		}
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
