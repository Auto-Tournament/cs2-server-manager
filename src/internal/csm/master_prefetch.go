package csm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// masterMu serializes SteamCMD runs on master-install: the agent's background first
// download (PrefetchMaster) and a bootstrap's master install. A server.create that comes in
// while CS2 is still downloading waits for it, then finds the game there.
var masterMu sync.Mutex

// masterHasGame reports whether master-install already holds a CS2 dedicated server.
func masterHasGame(master string) bool {
	st, err := os.Stat(filepath.Join(master, "game", "bin", "linuxsteamrt64", "cs2"))
	return err == nil && !st.IsDir()
}

// steamcmdRunningFor reports whether a SteamCMD process is already installing into dir. The
// agent's service keeps its children on a restart (KillMode=process, so servers survive), so a
// download started by an earlier run of the agent may still be going.
func steamcmdRunningFor(dir string) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() || strings.Trim(e.Name(), "0123456789") != "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if len(args) == 0 || !strings.Contains(filepath.Base(args[0]), "steamcmd") {
			continue
		}
		for i, a := range args {
			if a == "+force_install_dir" && i+1 < len(args) && filepath.Clean(args[i+1]) == filepath.Clean(dir) {
				return true
			}
		}
	}
	return false
}

// PrefetchMaster downloads CS2 into master-install when the host has none yet, so a machine
// linked to a platform is ready for its first server without anyone waiting on it (about
// 60 GB). It does nothing when CS2 is there already. The agent runs it in the background
// right after the link.
func (b *HostBackend) PrefetchMaster(ctx context.Context, logf func(format string, args ...any)) error {
	user := configuredOrDetectedUser()
	master := filepath.Join("/home", user, "master-install")
	if masterHasGame(master) {
		return nil
	}
	masterMu.Lock()
	defer masterMu.Unlock()
	if masterHasGame(master) {
		return nil
	}
	if steamcmdRunningFor(master) {
		logf("host agent: CS2 is already downloading into %s (an earlier run of the agent started it)", master)
		return nil
	}
	logf("host agent: no CS2 on this machine yet; downloading it into %s in the background", master)
	steamCtx, cancel := contextWithTimeout(ctx, TimeoutSteamCMD)
	defer cancel()
	var out bytes.Buffer
	err := installMasterViaSteamCMD(steamCtx, &out, BootstrapConfig{CS2User: user})
	LogAction("agent", "first CS2 download (after link)", dropSteamProgress(out.String()), err)
	if err != nil {
		logf("host agent: downloading CS2 failed: %v (the first server create tries again)", err)
		return err
	}
	logf("host agent: CS2 downloaded; this machine is ready for servers")
	return nil
}
