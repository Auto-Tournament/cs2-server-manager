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

// UpdateGame updates the master CS2 installation via SteamCMD and rsyncs
// core game files to all server instances, preserving configs and addons.
// It returns a human-readable log of what happened.
func UpdateGame() (string, error) {
	return UpdateGameWithContext(context.Background())
}

// UpdateGameWithContext is like UpdateGame but accepts a context for
// cancellation. If the context is cancelled mid-way, further work is skipped
// and the partial log plus ctx.Err() are returned.
func UpdateGameWithContext(ctx context.Context) (string, error) {
	var buf bytes.Buffer
	var logFile *os.File

	// When invoked from the TUI "Update CS2 after Valve update" action and
	// similar flows, CSM_UPDATE_GAME_LOG can be set to a temp path that the UI
	// tails in real time. Mirror all update-game log lines into that file so
	// the user can see progress across SteamCMD + rsync, not just the final
	// result.
	if logPath := strings.TrimSpace(os.Getenv("CSM_UPDATE_GAME_LOG")); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			logFile = f
			defer func() {
				if err := logFile.Close(); err != nil {
					fmt.Fprintf(os.Stderr, "CSM_UPDATE_GAME_LOG close failed: %v\n", err)
				}
			}()
		}
	}

	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !strings.HasSuffix(format, "\n") {
			buf.WriteByte('\n')
		}
		if logFile != nil {
			fmt.Fprintf(logFile, format, args...)
			if !strings.HasSuffix(format, "\n") {
				_, _ = logFile.Write([]byte{'\n'})
			}
		}
	}

	// Helper to honor cancellation between major phases.
	checkCtx := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
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

	if mgr.NumServers <= 0 {
		log("No CS2 servers found for user %s (no /home/%s/server-* directories).", cs2User, cs2User)
		log("Nothing to update. Run the install wizard from the TUI first.")
		logOut := buf.String()
		AppendLog("update-game.log", logOut)
		return logOut, nil
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	log("Stopping all servers...")
	if err := mgr.StopAll(); err != nil {
		log("Error stopping servers: %v", err)
		return buf.String(), err
	}
	log("All servers stopped.")

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	log("Updating master install at %s via SteamCMD...", masterDir)
	cmd := exec.CommandContext(ctx, "sudo", "-u", cs2User, "steamcmd",
		"+force_install_dir", masterDir,
		"+login", "anonymous",
		"+app_update", "730", "validate",
		"+quit",
	)

	// Stream SteamCMD output into both the in-memory buffer and, when
	// configured, the CSM_UPDATE_GAME_LOG file so the TUI can tail progress in
	// real time.
	var steamOut io.Writer = &buf
	if logFile != nil {
		steamOut = &teeWriter{buf: &buf, file: logFile}
	}
	cmd.Stdout = steamOut
	cmd.Stderr = steamOut
	if err := cmd.Run(); err != nil {
		log("SteamCMD update failed: %v", err)
		return buf.String(), err
	}
	log("Master CS2 install updated.")

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	log("")
	log("Syncing updated game files to server instances...")
	for i := 1; i <= mgr.NumServers; i++ {
		if err := checkCtx(); err != nil {
			return buf.String(), err
		}

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

		// rsync master game/ into server game/, excluding addons to preserve
		// plugins. Use the provided context so cancellation can terminate the
		// rsync process mid-transfer, and stream output into the same
		// log/buffer writer so the TUI can tail progress.
		srcRoot := filepath.Join(masterDir, "game") + string(os.PathSeparator)
		dstRoot := dst + string(os.PathSeparator)

		args := []string{
			"-a", "--delete",
			"--info=PROGRESS2",
			"--exclude", "csgo/addons/",
			srcRoot,
			dstRoot,
		}
		var rsyncOut io.Writer = &buf
		if logFile != nil {
			rsyncOut = &teeWriter{buf: &buf, file: logFile}
		}
		if err := runCmdLoggedContext(ctx, rsyncOut, "rsync", args...); err != nil {
			log("  [ERROR] rsync to server-%d failed: %v", i, err)
			continue
		}
		log("  [OK] Server-%d game files updated", i)
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	log("")
	log("Restarting all servers...")
	if err := mgr.StartAll(); err != nil {
		log("Error starting servers: %v", err)
		return buf.String(), err
	}
	log("[OK] All servers started after game update.")

	logOut := buf.String()
	AppendLog("update-game.log", logOut)
	return logOut, nil
}

// DeployPluginsToServers stops all servers, rsyncs plugin content from the
// shared game_files tree into each server instance, then restarts servers.
// It assumes plugins are already staged under game_files/.
func DeployPluginsToServers() (string, error) {
	return DeployPluginsToServersWithContext(context.Background())
}

// DeployPluginsToServersWithContext is like DeployPluginsToServers but accepts
// a context for cancellation. If the context is cancelled mid-way, further
// servers are skipped and the partial log plus ctx.Err() are returned.
func DeployPluginsToServersWithContext(ctx context.Context) (string, error) {
	var buf bytes.Buffer
	var w io.Writer = &buf

	// When CSM_PLUGINS_LOG is set (used by the TUI plugin update action),
	// mirror deploy logs into that file so the UI can show a live tail while
	// rsync runs across servers.
	if logPath := strings.TrimSpace(os.Getenv("CSM_PLUGINS_LOG")); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			defer func() {
				_ = f.Close()
			}()
			if bufPtr, ok := w.(*bytes.Buffer); ok {
				w = &teeWriter{buf: bufPtr, file: f}
			} else {
				w = io.MultiWriter(w, f)
			}
		}
	}

	log := func(format string, args ...any) {
		fmt.Fprintf(w, format, args...)
		if !strings.HasSuffix(format, "\n") {
			fmt.Fprintln(w)
		}
	}

	checkCtx := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
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

	if mgr.NumServers <= 0 {
		log("No CS2 servers found for user %s (no /home/%s/server-* directories).", mgr.CS2User, mgr.CS2User)
		log("Nothing to update. Run the install wizard from the TUI first.")
		out := buf.String()
		AppendLog("deploy-plugins.log", out)
		return out, nil
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	if err := mgr.StopAll(); err != nil {
		log("Error stopping servers: %v", err)
		return buf.String(), err
	}
	log("All servers stopped.")

	for i := 1; i <= mgr.NumServers; i++ {
		if err := checkCtx(); err != nil {
			return buf.String(), err
		}

		serverDir := mgr.serverDir(i)
		if fi, err := os.Stat(serverDir); err != nil || !fi.IsDir() {
			log("  Server-%d not found at %s, skipping", i, serverDir)
			continue
		}
		log("  Updating plugins on server-%d ...", i)
		dstGame := filepath.Join(serverDir, "game", "csgo")

		srcAddons := filepath.Join(gameDir, "csgo", "addons") + string(os.PathSeparator)
		if err := runCmdLoggedContext(ctx, w, "rsync", "-a", "--delete", srcAddons, filepath.Join(dstGame, "addons")+"/"); err != nil {
			log("  [ERROR] rsync addons for server-%d failed: %v", i, err)
		} else {
			log("  [OK] Updated addons on server-%d", i)
		}

		srcCfg := filepath.Join(gameDir, "csgo", "cfg") + string(os.PathSeparator)
		_ = runCmdLoggedContext(ctx, w, "rsync", "-a", srcCfg, filepath.Join(dstGame, "cfg")+"/")
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	log("")
	if err := mgr.StartAll(); err != nil {
		log("Error starting servers: %v", err)
		return buf.String(), err
	}
	log("[OK] All servers restarted after plugin update.")

	out := buf.String()
	AppendLog("deploy-plugins.log", out)
	return out, nil
}

// UpdateAndDeployPlugins downloads the latest plugin bundle into game_files/
// and then deploys those plugins to all servers.
func UpdateAndDeployPlugins() (string, error) {
	return UpdateAndDeployPluginsWithContext(context.Background())
}

// UpdateAndDeployPluginsWithContext is like UpdateAndDeployPlugins but accepts
// a context for cancellation between the download and deployment phases.
func UpdateAndDeployPluginsWithContext(ctx context.Context) (string, error) {
	var buf bytes.Buffer
	var w io.Writer = &buf

	// When CSM_PLUGINS_LOG is set (used by the TUI plugin update action),
	// mirror combined update+deploy logs into that file so the UI can show a
	// live tail across both phases.
	if logPath := strings.TrimSpace(os.Getenv("CSM_PLUGINS_LOG")); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			defer func() {
				_ = f.Close()
			}()
			if bufPtr, ok := w.(*bytes.Buffer); ok {
				w = &teeWriter{buf: bufPtr, file: f}
			} else {
				w = io.MultiWriter(w, f)
			}
		}
	}

	log := func(format string, args ...any) {
		fmt.Fprintf(w, format, args...)
		if !strings.HasSuffix(format, "\n") {
			fmt.Fprintln(w)
		}
	}

	checkCtx := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	log("=== Update and Deploy Plugins ===")
	log("")

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	out, err := UpdatePlugins()
	if out != "" {
		log("%s", out)
	}
	if err != nil {
		log("[ERROR] Plugin download failed: %v", err)
		all := buf.String()
		AppendLog("update-and-deploy-plugins.log", all)
		return all, err
	}

	if err := checkCtx(); err != nil {
		all := buf.String()
		AppendLog("update-and-deploy-plugins.log", all)
		return all, err
	}

	log("")
	log("Now syncing updated plugins to all servers...")
	out2, err := DeployPluginsToServersWithContext(ctx)
	if out2 != "" {
		log("%s", out2)
	}
	all := buf.String()
	if err != nil {
		log("[ERROR] Deploying plugins to servers failed: %v", err)
		AppendLog("update-and-deploy-plugins.log", all)
		return all, err
	}

	AppendLog("update-and-deploy-plugins.log", all)
	return all, nil
}
