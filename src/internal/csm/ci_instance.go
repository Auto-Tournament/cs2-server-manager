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

// The CI server as a csm instance (`csm ci setup --instance N`)
//
// Instead of a ~70 GB CS2 install of its own, the CI server is instance N:
// the shared game version plus, per CI run, a private Ready Up layer built
// from that run's bundle (`csm instance layer build --for N`). The runner's
// .env tells the Ready Up workflow:
//
//	CS2_CI_INSTANCE=N        the instance
//	CS2_CI_DIR=<merged>      its view, visible inside `csm instance exec N`
//	CS2_CI_PORT=<game port>  base+10N
//	CS2_CI_CSM=<csm>         the csm binary that set this up (it has exec)
//
// The instance is pinned (instance_private.go), so it is not one of the
// host's servers and no other instance ever mounts a CI layer.

// ciEnvKeys are the runner .env keys csm ci owns.
var ciEnvKeys = []string{"CS2_CI_DIR", "CS2_CI_PORT", "CS2_CI_INSTANCE", "CS2_CI_CSM"}

func ciEnvKey(line string) bool {
	for _, k := range ciEnvKeys {
		if strings.HasPrefix(line, k+"=") {
			return true
		}
	}
	return false
}

// RenderCIRunnerEnvInstance is the runner's .env for CI instance n.
func RenderCIRunnerEnvInstance(existing, merged string, port, n int, csmBin string) string {
	out := RenderCIRunnerEnv(existing, merged, port)
	out += fmt.Sprintf("CS2_CI_INSTANCE=%d\n", n)
	if csmBin != "" {
		out += "CS2_CI_CSM=" + csmBin + "\n"
	}
	return out
}

// ciInstance is the CI instance the runner's .env names (0 = none).
func (e ciEnv) ciInstance() int {
	data, err := os.ReadFile(filepath.Join(e.runnerDir, ".env"))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(parseEnvFile(string(data))["CS2_CI_INSTANCE"])
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// csmExecutable is this csm's own path, for the workflow to call.
func csmExecutable() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

func ciSetupInstance(ctx context.Context, w io.Writer, env ciEnv, cmd CICommand) error {
	m, err := NewInstanceManager()
	if err != nil {
		return err
	}
	n := cmd.Instance
	ports, err := m.Ports(n)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "=== csm ci setup (user %s, repo %s, instance %d) ===\n", env.cs2User, cmd.Repo, n)
	fmt.Fprintln(w, "No numbered server or other instance is started, stopped, restarted or updated.")
	fmt.Fprintln(w)

	fmt.Fprintln(w, "[1/4] Checks")
	if !lingerEnabled(env.cs2User) {
		return fmt.Errorf("lingering is off for %s, so its systemd --user services stop when it logs out. "+
			"Enable it once as root: `sudo loginctl enable-linger %s` (or `sudo csm setup-host`)", env.cs2User, env.cs2User)
	}
	for _, bin := range []string{"systemctl", "tar", "unshare"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found in PATH (install the dependencies once: `sudo csm install-deps`)", bin)
		}
	}
	if m.Exists(n) && !m.Pinned(n) {
		return fmt.Errorf("instance %d already exists and is one of this host's servers; pick a free number for CI", n)
	}
	if m.Exists(n) && m.IsRunning(n) {
		return fmt.Errorf("instance %d is running; stop it first (csm instance stop %d)", n, n)
	}
	csmBin := csmExecutable()
	fmt.Fprintf(w, "  [✓] user %s, linger on, instance %d (game %d, GOTV %d, Ready Up status %d), csm %s\n\n",
		env.cs2User, n, ports.Game, ports.TV, ports.Status, csmBin)

	fmt.Fprintf(w, "[2/4] GitHub Actions runner in %s\n", env.runnerDir)
	if err := ensureRunnerFiles(ctx, w, env.runnerDir); err != nil {
		return err
	}
	if err := configureRunner(ctx, w, env.runnerDir, cmd); err != nil {
		return err
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "[3/4] CI instance %d\n", n)
	if !m.Exists(n) {
		if _, err := m.Create(w, n); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(w, "  [i] instance %d exists (%s)\n", n, m.L.Dir(n))
	}
	if err := m.Reserve(n); err != nil {
		return err
	}
	fmt.Fprintf(w, "  [✓] instance %d is private: not a server of this host; each CI run builds its own layer for it\n", n)
	envPath := filepath.Join(env.runnerDir, ".env")
	existing, _ := os.ReadFile(envPath)
	if err := os.WriteFile(envPath, []byte(RenderCIRunnerEnvInstance(string(existing), m.L.Merged(n), ports.Game, n, csmBin)), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", envPath, err)
	}
	fmt.Fprintf(w, "  [✓] .env: CS2_CI_INSTANCE=%d CS2_CI_DIR=%s CS2_CI_PORT=%d CS2_CI_CSM=%s\n\n", n, m.L.Merged(n), ports.Game, csmBin)

	fmt.Fprintf(w, "[4/4] systemd --user service %s\n", CIRunnerUnit)
	if err := os.MkdirAll(filepath.Dir(env.unitPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(env.unitPath, []byte(RenderCIRunnerUnit(env.runnerDir)), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", env.unitPath, err)
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", CIRunnerUnit}, {"restart", CIRunnerUnit}} {
		c := env.systemctl(ctx, args...)
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	fmt.Fprintf(w, "  [✓] %s enabled and running\n\n", CIRunnerUnit)
	if old, err := resolveCIDir("", env.home, ResolveRoot()); err == nil && fileExists(filepath.Join(old, ciMarkerFile)) {
		fmt.Fprintf(w, "The old CI install %s is no longer used; delete it with `rm -rf %s` once a CI run passed on the instance.\n", old, old)
	}
	fmt.Fprintln(w, "Check it any time with `csm ci status`.")
	return nil
}

func ciStatusInstance(ctx context.Context, w io.Writer, env ciEnv, n int) error {
	state := func(verb string) string {
		out, _ := env.systemctl(ctx, verb, CIRunnerUnit).Output()
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
		return "unknown"
	}
	fmt.Fprintf(w, "Runner service:  %s: %s (%s)\n", CIRunnerUnit, state("is-active"), state("is-enabled"))
	if !lingerEnabled(env.cs2User) {
		fmt.Fprintf(w, "                 warning: lingering is off; `sudo loginctl enable-linger %s`\n", env.cs2User)
	}
	if name, url, ok := runnerInfo(env.runnerDir); ok {
		fmt.Fprintf(w, "Runner:          %s -> %s\n", name, url)
		fmt.Fprintf(w, "Labels:          %s\n", CIRunnerLabel)
	} else {
		fmt.Fprintln(w, "Runner:          not registered (csm ci setup --instance N --token <token>)")
	}
	fmt.Fprintf(w, "Runner dir:      %s\n", env.runnerDir)
	vars := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(env.runnerDir, ".env")); err == nil {
		vars = parseEnvFile(string(data))
	}

	m, err := NewInstanceManager()
	if err != nil {
		return err
	}
	ports, _ := m.Ports(n)
	fmt.Fprintf(w, "CI server:       csm instance %d (game port %d, GOTV %d, Ready Up status %d; .env port %s)\n",
		n, ports.Game, ports.TV, ports.Status, vars["CS2_CI_PORT"])
	if !m.Exists(n) {
		fmt.Fprintf(w, "                 warning: instance %d does not exist (csm ci setup --instance %d)\n", n, n)
		return nil
	}
	state2 := "stopped (the workflow runs it with csm instance exec)"
	switch {
	case m.IsRunning(n):
		state2 = "running (csm instance start; the workflow needs it stopped)"
	case m.execBusy(n):
		state2 = "in use by a CI run (csm instance exec)"
	}
	fmt.Fprintf(w, "State:           %s\n", state2)
	fmt.Fprintf(w, "View:            %s (inside csm instance exec %d)\n", m.L.Merged(n), n)
	if l := m.PinnedLayer(n); l != "" {
		info := m.ReadLayerInfo(l)
		fmt.Fprintf(w, "Ready Up layer:  %s (private; core %s, %s, built %s)\n", info.ID, info.Core, info.Bundle, info.BuiltAt)
	} else {
		fmt.Fprintln(w, "Ready Up layer:  none of its own yet (the next CI run builds one)")
	}
	game := m.CurrentGame()
	if l, err := m.EffectiveLayer(n); err == nil {
		game = m.layerGame(l)
	}
	fmt.Fprintf(w, "CS2 build:       %s (game version %s)\n", ciBuildString(game), m.ReadGameInfo(game).ID)
	fmt.Fprintf(w, "csm:             %s\n", vars["CS2_CI_CSM"])
	var used []string
	for _, d := range []string{m.L.Dir(n), m.PinnedLayer(n)} {
		if d == "" {
			continue
		}
		// du exits 1 on overlayfs' unreadable work/work but still prints the total.
		out, _ := exec.CommandContext(ctx, "du", "-sh", d).Output()
		if f := strings.Fields(string(out)); len(f) > 0 {
			used = append(used, f[0]+" "+d)
		}
	}
	if len(used) > 0 {
		fmt.Fprintf(w, "Disk used:       %s (CS2 itself is the shared master install)\n", strings.Join(used, ", "))
	}
	if free, checked, err := freeDiskGB(m.L.Root); err == nil {
		fmt.Fprintf(w, "Disk free:       %.1f GB on %s\n", free, checked)
	}
	if old, err := resolveCIDir("", env.home, ResolveRoot()); err == nil && fileExists(filepath.Join(old, ciMarkerFile)) {
		fmt.Fprintf(w, "Old CI install:  %s is still there and no longer used (rm -rf %s)\n", old, old)
	}
	return nil
}

func ciRemoveInstance(ctx context.Context, w io.Writer, env ciEnv, cmd CICommand, n int) error {
	if err := ciRemoveRunner(ctx, w, env, cmd); err != nil {
		return err
	}
	if !cmd.Purge {
		fmt.Fprintf(w, "Runner files and CI instance %d are kept (--purge deletes them).\n", n)
		return nil
	}
	m, err := NewInstanceManager()
	if err != nil {
		return err
	}
	if !m.Exists(n) {
		return nil
	}
	if !m.Pinned(n) {
		fmt.Fprintf(w, "  [i] instance %d is not a private (CI) instance; not deleting it\n", n)
		return nil
	}
	if err := m.Stop(n, 30*time.Second); err != nil {
		return err
	}
	if err := m.Remove(w, n, true); err != nil {
		return err
	}
	m.GC(w)
	return nil
}
