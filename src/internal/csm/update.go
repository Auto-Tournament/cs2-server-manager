package csm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// UpdateGame updates the master CS2 installation via SteamCMD and rsyncs
// core game files to all server instances, preserving configs and addons.
// It returns a human-readable log of what happened.
func UpdateGame() (string, error) {
	var buf bytes.Buffer
	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !strings.HasSuffix(format, "\n") {
			buf.WriteByte('\n')
		}
	}

	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}
	cs2User := mgr.CS2User
	masterDir := filepath.Join("/home", cs2User, "master-install")

	log("=== Update CS2 Game Files (After Valve Update) ===")
	log("This will:")
	log("  • Update master CS2 installation via SteamCMD")
	log("  • Stop all servers")
	log("  • Update game files on all servers")
	log("  • Restart all servers")
	log("")

	if fi, err := os.Stat(masterDir); err != nil || !fi.IsDir() {
		return buf.String(), fmt.Errorf("master install not found at %s", masterDir)
	}

	log("Stopping all servers...")
	if err := mgr.StopAll(); err != nil {
		log("Error stopping servers: %v", err)
		return buf.String(), err
	}
	log("All servers stopped.")

	log("Updating master install at %s via SteamCMD...", masterDir)
	cmd := exec.Command("sudo", "-u", cs2User, "steamcmd",
		"+force_install_dir", masterDir,
		"+login", "anonymous",
		"+app_update", "730", "validate",
		"+quit",
	)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log("%s", string(out))
	}
	if err != nil {
		log("SteamCMD update failed: %v", err)
		return buf.String(), err
	}
	log("Master CS2 install updated.")

	log("")
	log("Syncing updated game files to server instances...")
	for i := 1; i <= mgr.NumServers; i++ {
		serverDir := mgr.serverDir(i)
		if fi, err := os.Stat(serverDir); err != nil || !fi.IsDir() {
			log("  Server-%d not found at %s, skipping", i, serverDir)
			continue
		}
		log("  Updating server-%d ...", i)
		dst := filepath.Join(serverDir, "game")

		// Back up cfg and addons directories, similar to original script.
		cfgDir := filepath.Join(dst, "csgo", "cfg")
		addonsDir := filepath.Join(dst, "csgo", "addons")
		if fi, err := os.Stat(cfgDir); err == nil && fi.IsDir() {
			_ = exec.Command("cp", "-a", cfgDir, cfgDir+".backup").Run()
		}
		if fi, err := os.Stat(addonsDir); err == nil && fi.IsDir() {
			_ = exec.Command("cp", "-a", addonsDir, addonsDir+".backup").Run()
		}

		// rsync master game/ into server game/, excluding addons to preserve plugins.
		args := []string{
			"-a", "--delete",
			"--exclude", "csgo/addons/",
			masterDir + string(os.PathSeparator),
			dst + string(os.PathSeparator),
		}
		if err := runCmdLogged(&buf, "rsync", args...); err != nil {
			log("  [ERROR] rsync to server-%d failed: %v", i, err)
			continue
		}
		log("  [OK] Server-%d game files updated", i)
	}

	log("")
	log("Restarting all servers...")
	if err := mgr.StartAll(); err != nil {
		log("Error starting servers: %v", err)
		return buf.String(), err
	}
	log("[OK] All servers started after game update.")

	return buf.String(), nil
}

// DeployPluginsToServers stops all servers, rsyncs plugin content from the
// shared game_files tree into each server instance, then restarts servers.
func DeployPluginsToServers() (string, error) {
	var buf bytes.Buffer
	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !strings.HasSuffix(format, "\n") {
			buf.WriteByte('\n')
		}
	}

	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}

	up := NewPluginUpdater()
	gameDir := up.GameDir

	log("=== Update Plugins on All Servers ===")
	log("This will:")
	log("  • Stop all servers")
	log("  • Sync plugins from %s to each server", gameDir)
	log("  • Restart all servers")
	log("")

	if err := mgr.StopAll(); err != nil {
		log("Error stopping servers: %v", err)
		return buf.String(), err
	}
	log("All servers stopped.")

	for i := 1; i <= mgr.NumServers; i++ {
		serverDir := mgr.serverDir(i)
		if fi, err := os.Stat(serverDir); err != nil || !fi.IsDir() {
			log("  Server-%d not found at %s, skipping", i, serverDir)
			continue
		}
		log("  Updating plugins on server-%d ...", i)
		dstGame := filepath.Join(serverDir, "game", "csgo")

		srcAddons := filepath.Join(gameDir, "csgo", "addons") + string(os.PathSeparator)
		if err := runCmdLogged(&buf, "rsync", "-a", "--delete", srcAddons, filepath.Join(dstGame, "addons")+"/"); err != nil {
			log("  [ERROR] rsync addons for server-%d failed: %v", i, err)
		} else {
			log("  [OK] Updated addons on server-%d", i)
		}

		srcCfg := filepath.Join(gameDir, "csgo", "cfg") + string(os.PathSeparator)
		_ = runCmdLogged(&buf, "rsync", "-a", srcCfg, filepath.Join(dstGame, "cfg")+"/")
	}

	log("")
	if err := mgr.StartAll(); err != nil {
		log("Error starting servers: %v", err)
		return buf.String(), err
	}
	log("[OK] All servers restarted after plugin update.")

	return buf.String(), nil
}


