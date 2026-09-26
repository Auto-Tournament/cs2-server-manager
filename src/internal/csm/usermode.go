package csm

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// User mode: after a one-time `sudo csm setup-host`, csm runs as the CS2
// service user itself (DefaultCS2User unless CS2_USER says otherwise) with no
// sudo. Everything that only touches that user's files, tmux server and
// crontab then runs directly; operations that really need root (apt, useradd,
// userdel, Docker) still require it and say so.
//
// tmux keeps its socket in /tmp/tmux-<uid>/default. `su - <user> -c tmux ...`
// (the root path) and plain `tmux ...` run as that user talk to the same
// server, so sessions started either way are found by the other.

// runAsMode says how csm runs a command that has to execute as the CS2 user.
type runAsMode int

const (
	// runAsSwitchUser switches to the user first (`su -` for shell command
	// lines, `sudo -u` for SteamCMD). This is the root path.
	runAsSwitchUser runAsMode = iota
	// runAsDirect runs the command as-is because csm already is that user.
	runAsDirect
)

// decideRunAs picks how to run a command as target, given the effective uid
// and user name csm runs as.
func decideRunAs(euid int, currentUser, target string) runAsMode {
	target = strings.TrimSpace(target)
	if euid != 0 && target != "" && strings.TrimSpace(currentUser) == target {
		return runAsDirect
	}
	return runAsSwitchUser
}

// currentUsername returns the name of the effective user, or "" if it cannot
// be resolved.
func currentUsername() string {
	if u, err := user.LookupId(strconv.Itoa(os.Geteuid())); err == nil {
		return u.Username
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return strings.TrimSpace(os.Getenv("USER"))
}

// isCurrentUser reports whether csm runs as target (and not as root).
func isCurrentUser(target string) bool {
	return decideRunAs(os.Geteuid(), currentUsername(), target) == runAsDirect
}

// configuredCS2User returns the CS2 user csm manages: CS2_USER when set,
// otherwise the user NewTmuxManager discovers (DefaultCS2User on most hosts).
func configuredCS2User() string {
	if v := strings.TrimSpace(os.Getenv("CS2_USER")); v != "" {
		return v
	}
	if mgr, err := NewTmuxManager(); err == nil && strings.TrimSpace(mgr.CS2User) != "" {
		return mgr.CS2User
	}
	return DefaultCS2User
}

// RunningAsCS2User reports whether csm runs as the CS2 service user (user
// mode) rather than as root or some other account.
func RunningAsCS2User() bool {
	if os.Geteuid() == 0 {
		return false
	}
	me := currentUsername()
	if me == "" {
		return false
	}
	if v := strings.TrimSpace(os.Getenv("CS2_USER")); v != "" {
		return me == v
	}
	if me == DefaultCS2User {
		return true
	}
	return me == configuredCS2User()
}

// CanManageServers reports whether csm may run operations that only need the
// CS2 user's privileges: it runs as root, or as the CS2 user.
func CanManageServers() bool {
	return os.Geteuid() == 0 || RunningAsCS2User()
}

// privilegeError returns nil when an operation that needs root or the CS2
// user may run, and otherwise an error that explains both ways to run it.
func privilegeError(euid int, currentUser, cs2User, what string) error {
	if euid == 0 || decideRunAs(euid, currentUser, cs2User) == runAsDirect {
		return nil
	}
	if strings.TrimSpace(cs2User) == "" {
		cs2User = DefaultCS2User
	}
	return fmt.Errorf("%s must be run as root or as the CS2 user %q (current user: %q). "+
		"Run `sudo csm setup-host` once, then run csm as %s without sudo (for example `sudo -iu %s`)",
		what, cs2User, currentUser, cs2User, cs2User)
}

// requireRootOrCS2User is privilegeError for the running process.
func requireRootOrCS2User(what, cs2User string) error {
	return privilegeError(os.Geteuid(), currentUsername(), cs2User, what)
}

// RootRequiredError is the error for operations that always need root, such
// as installing packages or managing users.
func RootRequiredError(what string) error {
	return fmt.Errorf("%s needs root: run it with sudo (the one-time host setup is `sudo csm setup-host`)", what)
}

// userShellCommand returns a command that runs the shell command line as
// target: through `su - target -c` when csm runs as root (or another user),
// or directly through a bash login shell when csm already is target.
func userShellCommand(target, cmdline string) *exec.Cmd {
	if !isCurrentUser(target) {
		return exec.Command("su", "-", target, "-c", cmdline)
	}
	// A login shell, like `su -`, so PATH and friends match the root path.
	cmd := exec.Command("bash", "-lc", cmdline)
	home := filepath.Join("/home", target)
	if fi, err := os.Stat(home); err == nil && fi.IsDir() {
		cmd.Dir = home
	}
	cmd.Env = userModeEnv(os.Environ(), home)
	return cmd
}

// userModeEnv prepares the environment for a direct (user mode) command so it
// behaves like `su -`: HOME is the CS2 user's home, and TMUX / TMUX_TMPDIR are
// dropped so tmux uses the default socket (/tmp/tmux-<uid>/default) that
// sessions started through `su -` also use, even when csm itself was started
// from inside a tmux session.
func userModeEnv(environ []string, home string) []string {
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		switch name {
		case "TMUX", "TMUX_PANE", "TMUX_TMPDIR", "HOME":
			continue
		}
		out = append(out, kv)
	}
	return append(out, "HOME="+home)
}

// steamcmdArgv returns the argv that runs steamcmd with args as target:
// `sudo -u target [-H] steamcmd ...` from root, or plain `steamcmd ...` when
// csm already is target. setHome adds -H so HOME is the target's home.
func steamcmdArgv(euid int, currentUser, target string, setHome bool, args ...string) []string {
	if decideRunAs(euid, currentUser, target) == runAsDirect {
		return append([]string{"steamcmd"}, args...)
	}
	argv := []string{"sudo", "-u", target}
	if setHome {
		argv = append(argv, "-H")
	}
	argv = append(argv, "steamcmd")
	return append(argv, args...)
}

// steamcmdPreflightNeed says what running SteamCMD as target requires.
type steamcmdPreflightNeed int

const (
	// steamcmdNoSudo: csm is target (user mode) or root; nothing to check.
	steamcmdNoSudo steamcmdPreflightNeed = iota
	// steamcmdNeedsSudo: csm is some other user and needs `sudo -u target`.
	steamcmdNeedsSudo
)

func steamcmdPreflightFor(euid int, currentUser, target string) steamcmdPreflightNeed {
	if euid == 0 || decideRunAs(euid, currentUser, target) == runAsDirect {
		return steamcmdNoSudo
	}
	return steamcmdNeedsSudo
}

// steamcmdRunAsPreflight fails early when SteamCMD could not be run as
// target: csm is neither root nor target, and sudo does not work without a
// password prompt. Callers run it before stopping anything.
func steamcmdRunAsPreflight(target string) error {
	if steamcmdPreflightFor(os.Geteuid(), currentUsername(), target) == steamcmdNoSudo {
		return nil
	}
	if err := exec.Command("sudo", "-n", "-u", target, "true").Run(); err != nil {
		return fmt.Errorf("SteamCMD must run as %s, and csm runs as %s without working sudo. "+
			"Run csm as %s (sudo -iu %s), or with sudo", target, currentUsername(), target, target)
	}
	return nil
}

// steamcmdAsUser is steamcmdArgv for the running process.
func steamcmdAsUser(target string, setHome bool, args ...string) []string {
	return steamcmdArgv(os.Geteuid(), currentUsername(), target, setHome, args...)
}

// canChown reports whether csm can change file ownership, i.e. runs as root.
// In user mode everything csm creates already belongs to the CS2 user, so the
// chown calls that fix up root-created files are skipped.
func canChown() bool {
	return os.Geteuid() == 0
}

// pathOwnerUID returns the uid that owns path.
func pathOwnerUID(path string) (int, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

// userModeCS2User returns the CS2 user when this host has been switched to
// user mode (the state directory belongs to that user, which setup-host
// does), or "" otherwise.
func userModeCS2User() string {
	cs2User := configuredCS2User()
	uid, err := uidForUser(cs2User)
	if err != nil {
		return ""
	}
	if owner, ok := pathOwnerUID(ResolveRoot()); ok && owner == uid && uid != 0 {
		return cs2User
	}
	return ""
}
