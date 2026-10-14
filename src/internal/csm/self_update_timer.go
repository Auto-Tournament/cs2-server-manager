package csm

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// csm keeps itself up to date
//
// csm lives in /usr/local/bin, owned by root, so only root can replace it.
// `sudo csm setup-host` and every `sudo csm self-update` install a root
// systemd timer that runs `csm self-update --auto` every 6 hours (plus a
// random delay). The CS2 user gets no extra rights. --auto skips while
// updates are on hold (`csm updates hold on`, or the platform says a
// tournament is running); an update restarts only the host agent, never a
// server (self-update restarts the agents itself).

const (
	selfUpdateServiceUnit = "csm-self-update.service"
	selfUpdateTimerUnit   = "csm-self-update.timer"
)

// RenderSelfUpdateUnits returns the service and timer for the csm at exe.
func RenderSelfUpdateUnits(exe, csmRoot string) (service, timer string) {
	service = fmt.Sprintf(`# Written by csm (setup-host / self-update): keeps csm up to date.
[Unit]
Description=Update CS2 Server Manager (csm) to its latest release
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
Environment=CSM_ROOT=%s
ExecStart=%s self-update --auto
`, csmRoot, exe)
	timer = `# Written by csm (setup-host / self-update): keeps csm up to date.
[Unit]
Description=Update csm every 6 hours

[Timer]
OnBootSec=20min
OnUnitActiveSec=6h
RandomizedDelaySec=30min
Persistent=true

[Install]
WantedBy=timers.target
`
	return service, timer
}

// InstallSelfUpdateTimer installs (or refreshes) the root timer. Root only;
// a no-op elsewhere or without systemd. Never fails the caller: it says what
// went wrong instead.
func InstallSelfUpdateTimer(ctx context.Context, w io.Writer) {
	if os.Geteuid() != 0 {
		return
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return
	}
	exe := "/usr/local/bin/csm"
	if cur, err := os.Executable(); err == nil {
		if resolved, rerr := filepath.EvalSymlinks(cur); rerr == nil {
			exe = resolved
		}
	}
	csmRoot := agentCSMRoot()
	service, timer := RenderSelfUpdateUnits(exe, csmRoot)
	changed := false
	for name, content := range map[string]string{selfUpdateServiceUnit: service, selfUpdateTimerUnit: timer} {
		path := filepath.Join("/etc/systemd/system", name)
		if old, err := os.ReadFile(path); err == nil && string(old) == content {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			fmt.Fprintf(w, "warning: could not install %s: %v\n", path, err)
			return
		}
		changed = true
	}
	if changed {
		_ = exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "enable", "--now", selfUpdateTimerUnit).CombinedOutput(); err != nil {
		fmt.Fprintf(w, "warning: could not turn on automatic csm updates: %v %s\n", err, strings.TrimSpace(string(out)))
		return
	}
	if changed {
		fmt.Fprintln(w, "csm now keeps itself up to date (csm-self-update.timer, every 6 hours; held while updates are on hold).")
	}
}

// agentCSMRoot is the CSM_ROOT the host agent runs with (its unit says), so
// the timer reads the same hold settings; else the sudo user's home.
func agentCSMRoot() string {
	// The sudo user's agent first, then the configured CS2 user's, then any.
	var units []string
	for _, u := range []string{os.Getenv("SUDO_USER"), configuredCS2User()} {
		if home := homeOf(u); home != "" && u != "root" {
			units = append(units, filepath.Join(home, ".config", "systemd", "user", HostAgentUnit))
		}
	}
	units = append(units, filepath.Join("/etc/systemd/system", HostAgentUnit))
	others, _ := filepath.Glob("/home/*/.config/systemd/user/" + HostAgentUnit)
	units = append(units, others...)
	for _, unit := range units {
		data, err := os.ReadFile(unit)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Environment=CSM_ROOT="); ok && v != "" {
				return v
			}
		}
	}
	for _, u := range []string{os.Getenv("SUDO_USER"), configuredCS2User()} {
		if u != "" && u != "root" {
			if home := homeOf(u); home != "" {
				return home
			}
		}
	}
	return ResolveRoot()
}

// homeOf is a user's home directory, or "" when unknown.
func homeOf(user string) string {
	if user == "" {
		return ""
	}
	out, err := exec.Command("getent", "passwd", user).Output()
	if err != nil {
		return ""
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(fields) >= 6 {
		return fields[5]
	}
	return ""
}

// SelfUpdateHeld reports why an automatic csm update should wait, or "" when
// it may go ahead: the host's own hold, or the platform's (a tournament is
// running), as the auto-update monitor reads them.
func SelfUpdateHeld(ctx context.Context) string {
	settings, err := LoadAutoUpdateSettings()
	if err != nil {
		return "the auto-update settings could not be read"
	}
	if hold := ResolveUpdateHold(ctx, settings); hold.On {
		return hold.Describe()
	}
	return ""
}
