package csm

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The host agent as a systemd service
//
// `csm agent` runs in the foreground; `csm agent install` wraps it in a
// systemd unit so it starts on boot and restarts when it exits, the way
// `csm ci setup` runs the CI runner:
//
//   - as root: a system unit, /etc/systemd/system/csm-agent.service
//     (running as root, like the legacy root cron monitor);
//   - as the CS2 user (user mode): a systemd --user unit in
//     ~/.config/systemd/user, which needs lingering (sudo csm setup-host
//     turns it on) so it runs without a login.

// HostAgentUnit is the unit name.
const HostAgentUnit = "csm-agent.service"

// RenderHostAgentUnit returns the unit file for the agent binary.
func RenderHostAgentUnit(exe, csmRoot string, system bool) string {
	wanted := "default.target"
	if system {
		wanted = "multi-user.target"
	}
	return fmt.Sprintf(`# Written by csm agent install. Auto Tournament host agent (docs: csm README, "Host agent").
[Unit]
Description=CS2 Server Manager host agent (Auto Tournament platform link)
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
Environment=CSM_ROOT=%s
ExecStart=%s agent
Restart=always
RestartSec=10
KillMode=process
TimeoutStopSec=30

[Install]
WantedBy=%s
`, csmRoot, exe, wanted)
}

type agentService struct {
	system   bool
	unitPath string
}

func hostAgentService() (agentService, error) {
	if os.Geteuid() == 0 {
		return agentService{system: true, unitPath: filepath.Join("/etc/systemd/system", HostAgentUnit)}, nil
	}
	if !RunningAsCS2User() {
		return agentService{}, fmt.Errorf("run csm agent as the CS2 user (%s) or as root", configuredCS2User())
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = filepath.Join("/home", currentUsername())
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	return agentService{unitPath: filepath.Join(cfg, "systemd", "user", HostAgentUnit)}, nil
}

func (s agentService) systemctl(ctx context.Context, args ...string) *exec.Cmd {
	if s.system {
		return exec.CommandContext(ctx, "systemctl", args...)
	}
	cmd := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	cmd.Env = userBusEnv(os.Environ(), os.Getuid())
	return cmd
}

// InstallHostAgentService writes and starts the unit.
func InstallHostAgentService(ctx context.Context, w io.Writer) error {
	s, err := hostAgentService()
	if err != nil {
		return err
	}
	if !s.system && !lingerEnabled(currentUsername()) {
		return fmt.Errorf("lingering is off for %s, so its systemd --user services stop when it logs out; run `sudo loginctl enable-linger %s` (or `sudo csm setup-host`) first", currentUsername(), currentUsername())
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if !s.system {
		// Lingering was just switched on (sudo csm setup-host): the user's
		// manager may still be starting. Give it a moment before systemctl.
		waitForUserBus(ctx, strconv.Itoa(os.Getuid()), 20*time.Second)
	}
	if err := os.MkdirAll(filepath.Dir(s.unitPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(s.unitPath, []byte(RenderHostAgentUnit(exe, ResolveRoot(), s.system)), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", s.unitPath, err)
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", HostAgentUnit}, {"restart", HostAgentUnit}} {
		if out, err := s.systemctl(ctx, args...).CombinedOutput(); err != nil {
			text := strings.TrimSpace(string(out))
			if !s.system && strings.Contains(text, "Failed to connect to bus") {
				user := currentUsername()
				return fmt.Errorf("systemctl --user %s: %s. %s has no systemd user manager running: run `sudo loginctl enable-linger %s` (or `sudo csm setup-host`), then run csm agent install again as %s, logged in as that user (ssh %s@<host>, or `sudo -iu %s`)",
					strings.Join(args, " "), text, user, user, user, user, user)
			}
			return fmt.Errorf("systemctl %s failed: %v: %s", strings.Join(args, " "), err, text)
		}
	}
	fmt.Fprintf(w, "[✓] %s installed (%s) and running.\n", HostAgentUnit, s.unitPath)
	if s.system {
		fmt.Fprintf(w, "Logs: journalctl -u %s -f\n", HostAgentUnit)
	} else {
		fmt.Fprintf(w, "Logs: journalctl --user -u %s -f\n", HostAgentUnit)
	}
	return nil
}

// RemoveHostAgentService stops, disables and deletes the unit.
func RemoveHostAgentService(ctx context.Context, w io.Writer) error {
	s, err := hostAgentService()
	if err != nil {
		return err
	}
	_ = s.systemctl(ctx, "disable", "--now", HostAgentUnit).Run()
	if err := os.Remove(s.unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = s.systemctl(ctx, "daemon-reload").Run()
	fmt.Fprintf(w, "[✓] %s removed.\n", HostAgentUnit)
	return nil
}

// HostAgentServiceState is "active", "inactive", "not installed", ...
func HostAgentServiceState(ctx context.Context) string {
	s, err := hostAgentService()
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	if _, err := os.Stat(s.unitPath); err != nil {
		return "not installed (csm agent install)"
	}
	out, _ := s.systemctl(ctx, "is-active", HostAgentUnit).Output()
	state := strings.TrimSpace(string(out))
	if state == "" {
		state = "unknown"
	}
	return state
}

// RestartHostAgentServiceIfInstalled restarts a running agent so it picks up
// new credentials at once (it would otherwise notice within 30 s).
func RestartHostAgentServiceIfInstalled(ctx context.Context) bool {
	s, err := hostAgentService()
	if err != nil {
		return false
	}
	if _, err := os.Stat(s.unitPath); err != nil {
		return false
	}
	return s.systemctl(ctx, "restart", HostAgentUnit).Run() == nil
}

// RestartHostAgentsAfterUpdate restarts every installed host agent so it runs
// the csm that was just installed: without it the agent keeps the old binary
// in memory until the next reboot. As root it restarts the system unit and
// each user's `systemd --user` unit (`systemctl --user -M <user>@`); as a
// user, that user's own. Running servers are not touched (KillMode=process
// stops only the agent). It returns what it did, one line each.
func RestartHostAgentsAfterUpdate(ctx context.Context) []string {
	var done []string
	if os.Geteuid() != 0 {
		if RestartHostAgentServiceIfInstalled(ctx) {
			done = append(done, "restarted the host agent ("+currentUsername()+")")
		}
		return done
	}
	if _, err := os.Stat(filepath.Join("/etc/systemd/system", HostAgentUnit)); err == nil {
		if exec.CommandContext(ctx, "systemctl", "restart", HostAgentUnit).Run() == nil {
			done = append(done, "restarted the host agent (system service)")
		}
	}
	for _, user := range usersWithAgentUnit() {
		cmd := exec.CommandContext(ctx, "systemctl", "--user", "-M", user+"@", "restart", HostAgentUnit)
		if cmd.Run() == nil {
			done = append(done, "restarted the host agent ("+user+")")
		}
	}
	return done
}

// usersWithAgentUnit lists the users whose home has a user-mode agent unit.
func usersWithAgentUnit() []string {
	homes, _ := filepath.Glob("/home/*")
	var users []string
	for _, home := range homes {
		if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", HostAgentUnit)); err == nil {
			users = append(users, filepath.Base(home))
		}
	}
	return users
}

// StaleHostAgentRunning reports whether a host agent still runs a csm binary
// that has since been replaced on disk (Linux marks its exe "(deleted)"):
// an update that ran from an older csm, which did not restart the agent.
func StaleHostAgentRunning() bool {
	procs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, cmdline := range procs {
		data, err := os.ReadFile(cmdline)
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		if len(args) < 2 || filepath.Base(args[0]) != "csm" || args[1] != "agent" {
			continue
		}
		exe, err := os.Readlink(filepath.Join(filepath.Dir(cmdline), "exe"))
		if err == nil && strings.HasSuffix(exe, " (deleted)") {
			return true
		}
	}
	return false
}
