package csm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SetupHostOptions configures `csm setup-host`.
type SetupHostOptions struct {
	// CS2User is the service user csm runs as afterwards (default
	// DefaultCS2User).
	CS2User string
	// SkipDeps skips `apt-get install` of the system dependencies. Use it on a
	// host that already runs servers: apt may upgrade tmux, and a new tmux
	// client cannot talk to the tmux server the running servers live in.
	SkipDeps bool
	// SkipLinger skips `loginctl enable-linger`.
	SkipLinger bool
}

// SetupHost is the one-time root setup for user mode: it installs the system
// dependencies, creates the CS2 user, enables lingering for it, gives it the
// csm state directory and moves the monitor cron entry from root's crontab to
// the user's. Afterwards csm runs as that user without sudo.
//
// It is idempotent and never starts, stops, restarts or updates a server.
func SetupHost(ctx context.Context, w io.Writer, opts SetupHostOptions) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("setup-host must be run as root: sudo csm setup-host")
	}
	cs2User := strings.TrimSpace(opts.CS2User)
	if cs2User == "" {
		cs2User = DefaultCS2User
	}
	if cs2User == "root" {
		return fmt.Errorf("the CS2 user cannot be root")
	}

	fmt.Fprintf(w, "=== csm setup-host (CS2 user: %s) ===\n", cs2User)
	fmt.Fprintln(w, "Running servers are not started, stopped, restarted or updated.")
	fmt.Fprintln(w)

	fmt.Fprintln(w, "[1/5] System dependencies")
	if opts.SkipDeps {
		fmt.Fprintln(w, "  [i] Skipped (--skip-deps).")
	} else if err := installDeps(ctx, w); err != nil {
		return fmt.Errorf("installing dependencies failed: %w", err)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "[2/5] CS2 user")
	var ubuf bytes.Buffer
	err := createCS2User(&ubuf, cs2User)
	_, _ = w.Write(ubuf.Bytes())
	if err != nil {
		return err
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "[3/5] Lingering (keeps the user's systemd instance running without a login)")
	if opts.SkipLinger {
		fmt.Fprintln(w, "  [i] Skipped (--skip-linger).")
	} else if err := enableLinger(ctx, w, cs2User); err != nil {
		// Not fatal: csm works without it, cron and tmux do not depend on it.
		fmt.Fprintf(w, "  [!] %v\n", err)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "[4/5] File ownership")
	if err := chownStateForUserMode(w, cs2User); err != nil {
		return err
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "[5/5] Auto-update monitor cron entry")
	out, err := MigrateMonitorCronToUser(ctx, cs2User)
	if out != "" {
		fmt.Fprintf(w, "  [✓] %s", out)
	}
	if err != nil {
		return fmt.Errorf("moving the monitor cron entry failed: %w", err)
	}
	fmt.Fprintln(w)

	fmt.Fprint(w, setupHostNextSteps(cs2User))
	return nil
}

// enableLinger runs `loginctl enable-linger <user>` unless lingering is
// already on.
func enableLinger(ctx context.Context, w io.Writer, cs2User string) error {
	if _, err := os.Stat(filepath.Join("/var/lib/systemd/linger", cs2User)); err == nil {
		fmt.Fprintf(w, "  [✓] Lingering already enabled for %s\n", cs2User)
		return nil
	}
	if _, err := exec.LookPath("loginctl"); err != nil {
		return fmt.Errorf("loginctl not found (no systemd?); skipping enable-linger")
	}
	if out, err := exec.CommandContext(ctx, "loginctl", "enable-linger", cs2User).CombinedOutput(); err != nil {
		return fmt.Errorf("loginctl enable-linger %s failed: %v: %s", cs2User, err, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(w, "  [✓] Lingering enabled for %s\n", cs2User)
	return nil
}

// chownStateForUserMode gives cs2User the csm state directory (logs,
// settings, locks, game_files), the leftovers of earlier root runs in /tmp,
// and fixes obviously root-owned trees in the user's home.
func chownStateForUserMode(w io.Writer, cs2User string) error {
	owner := cs2User + ":" + cs2User

	root := filepath.Clean(ResolveRoot())
	if !filepath.IsAbs(root) || root == "/" {
		return fmt.Errorf("refusing to chown state directory %q", root)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", root, err)
	}
	if out, err := exec.Command("chown", "-R", owner, root).CombinedOutput(); err != nil {
		return fmt.Errorf("chown -R %s %s failed: %v: %s", owner, root, err, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(w, "  [✓] %s is owned by %s\n", root, cs2User)

	// Logs and the plugin download cache that root runs left in /tmp; the
	// CS2 user could not overwrite them otherwise.
	// Only root-owned regular files and directories: /tmp is world-writable,
	// so symlinks and other users' files are left alone.
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "csm-*"))
	fixed := 0
	for _, p := range matches {
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeSymlink != 0 || (!fi.IsDir() && !fi.Mode().IsRegular()) {
			continue
		}
		if uid, ok := pathOwnerUID(p); !ok || uid != 0 {
			continue
		}
		if err := exec.Command("chown", "-R", owner, p).Run(); err != nil {
			fmt.Fprintf(w, "  [!] chown %s failed: %v\n", p, err)
			continue
		}
		fixed++
	}
	if fixed > 0 {
		fmt.Fprintf(w, "  [✓] %d csm temp file(s) in %s now belong to %s\n", fixed, os.TempDir(), cs2User)
	}

	if err := ensureHomeWritable(cs2User); err != nil {
		fmt.Fprintf(w, "  [!] chown /home/%s failed: %v\n", cs2User, err)
	}
	numServers := 0
	if mgr, err := NewTmuxManager(); err == nil && mgr.CS2User == cs2User {
		numServers = mgr.NumServers
	}
	if err := autoRepairOwnershipIfNeeded(cs2User, numServers); err != nil {
		fmt.Fprintf(w, "  [!] Ownership repair under /home/%s failed: %v\n", cs2User, err)
	} else {
		fmt.Fprintf(w, "  [✓] Config and plugin trees under /home/%s are owned by %s\n", cs2User, cs2User)
	}
	return nil
}

// setupHostNextSteps is the text setup-host ends with.
func setupHostNextSteps(cs2User string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "Host setup done.")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "From now on run csm as %s, no sudo:\n", cs2User)
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "  sudo -iu %s     # or log in as %s\n", cs2User, cs2User)
	fmt.Fprintln(&b, "  csm status")
	fmt.Fprintln(&b, "  csm            # TUI")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Running servers keep running and are found as before (same tmux server).")
	fmt.Fprintln(&b, "Root is still needed for: csm install-deps, csm cleanup-all, and the Docker")
	fmt.Fprintln(&b, "MySQL container (sudo csm bootstrap). Avoid mixing in other `sudo csm` runs;")
	fmt.Fprintln(&b, "if you do, run `sudo csm setup-host --skip-deps` again to fix ownership.")
	return b.String()
}
