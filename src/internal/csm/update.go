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
// This function is protected by a mutex to prevent concurrent updates.
func UpdateGame() (string, error) {
	return UpdateGameWithContext(context.Background())
}

// UpdateGameWithContext is like UpdateGame but accepts a context for
// cancellation. If the context is cancelled mid-way, further work is skipped
// and the partial log plus ctx.Err() are returned.
// This function is protected by a mutex to prevent concurrent updates.
func UpdateGameWithContext(ctx context.Context) (string, error) {
	return withGameUpdateLock(func() (string, error) {
		return updateGameWithContextLocked(ctx)
	})
}

func updateGameWithContextLocked(ctx context.Context) (string, error) {
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

	// Check disk space before starting update
	if err := CheckDiskSpaceForGameUpdate(cs2User, mgr.NumServers); err != nil {
		log("  [!] Disk space check failed: %v", err)
		log("  [*] Update may fail if disk fills up during operation")
		// Continue anyway - the check is conservative and users may have enough space
	}

	// Preserve the current Metamod enabled/disabled state so we can re-apply
	// it after refreshing game files from the master install. Valve updates
	// may overwrite gameinfo.gi, which would otherwise drop the Metamod line.
	enableMetamod := detectMetamodEnabled(cs2User)

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

	if err := updateMasterInstallWithContext(ctx, &buf, logFile, cs2User, masterDir); err != nil {
		return buf.String(), err
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	log("")
	log("Syncing updated game files to server instances...")
	for i := 1; i <= mgr.NumServers; i++ {
		if err := checkCtx(); err != nil {
			return buf.String(), err
		}
		// Add timeout for rsync operations to prevent indefinite hanging
		rsyncCtx, rsyncCancel := contextWithTimeout(ctx, TimeoutRsync)
		err := syncMasterToServerWithContext(rsyncCtx, &buf, logFile, masterDir, mgr, i)
		rsyncCancel()
		if err != nil {
			if rsyncCtx.Err() == context.DeadlineExceeded {
				log("  [!] Syncing server-%d timed out after %v", i, TimeoutRsync)
				log("  [*] This may indicate disk I/O issues or very large file transfers")
			}
			// Errors are logged inside syncMasterToServerWithContext; continue
			// to try remaining servers so a partial update doesn't block
			// others.
			continue
		}

		// Ensure Metamod remains in sync with the pre-update setting.
		if err := configureMetamodGo(&buf, cs2User, i, enableMetamod); err != nil {
			log("  [!] Configure Metamod for server-%d failed after game update: %v", i, err)
		}
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	// As a safety net, ensure the CS2 user owns everything under its home
	// directory. This avoids subtle permission issues after rsync/SteamCMD
	// operations run as root.
	homeDir := filepath.Join("/home", cs2User)
	log("Ensuring ownership of %s for user %s", homeDir, cs2User)
	if err := exec.Command("chown", "-R", fmt.Sprintf("%s:%s", cs2User, cs2User), homeDir).Run(); err != nil {
		log("  [!] Warning: Failed to set ownership of %s: %v", homeDir, err)
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
// This function is protected by a mutex to prevent concurrent deployments.
func DeployPluginsToServers() (string, error) {
	return DeployPluginsToServersWithContext(context.Background())
}

// DeployPluginsToServersWithContext is like DeployPluginsToServers but accepts
// a context for cancellation. If the context is cancelled mid-way, further
// servers are skipped and the partial log plus ctx.Err() are returned.
// This function is protected by a mutex to prevent concurrent deployments.
func DeployPluginsToServersWithContext(ctx context.Context) (string, error) {
	return withPluginDeployLock(func() (string, error) {
		return deployPluginsToServersWithContextLocked(ctx)
	})
}

func deployPluginsToServersWithContextLocked(ctx context.Context) (string, error) {
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
	sharedCfgDir := filepath.Join("/home", mgr.CS2User, "cs2-config", "game", "csgo", "cfg")

	// Preserve the current Metamod enabled/disabled state so we can re-apply
	// it on each server after syncing updated plugins/configs.
	enableMetamod := detectMetamodEnabled(mgr.CS2User)

	log("=== Update Plugins on All Servers ===")
	log("This will:")
	log("  • Stop all servers")
	log("  • Replace all files under each server's game/csgo/addons directory with the latest plugin bundle")
	log("  • Sync plugin configs from %s to each server", sharedCfgDir)
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
		dstAddons := filepath.Join(dstGame, "addons")

		// Fully replace the server's addons tree so no stale plugin files linger
		// between updates.
		if err := os.RemoveAll(dstAddons); err != nil && !os.IsNotExist(err) {
			log("  [WARN] Failed to clean addons for server-%d at %s: %v", i, dstAddons, err)
		}
		if err := os.MkdirAll(dstAddons, 0o755); err != nil {
			log("  [ERROR] Failed to recreate addons directory for server-%d at %s: %v", i, dstAddons, err)
			continue
		}

		// Add timeout for rsync operations
		rsyncCtx, rsyncCancel := contextWithTimeout(ctx, TimeoutRsync)
		srcAddons := filepath.Join(gameDir, "csgo", "addons") + string(os.PathSeparator)
		if err := runCmdLoggedContext(rsyncCtx, w, "rsync", "-a", "--delete", srcAddons, dstAddons+string(os.PathSeparator)); err != nil {
			if rsyncCtx.Err() == context.DeadlineExceeded {
				log("  [ERROR] rsync addons for server-%d timed out after %v", i, TimeoutRsync)
			} else {
				log("  [ERROR] rsync addons for server-%d failed: %v", i, err)
			}
		} else {
			log("  [OK] Updated addons on server-%d", i)
		}
		rsyncCancel()

		// For configs, treat the shared cs2-config tree as the canonical source
		// so that any MatchZy or other plugin configs maintained there are
		// propagated to all servers.
		if fi, err := os.Stat(sharedCfgDir); err == nil && fi.IsDir() {
			cfgRsyncCtx, cfgRsyncCancel := contextWithTimeout(ctx, TimeoutRsync)
			if err := runCmdLoggedContext(cfgRsyncCtx, w, "rsync",
				"-a",
				sharedCfgDir+string(os.PathSeparator),
				filepath.Join(dstGame, "cfg")+"/",
			); err != nil {
				if cfgRsyncCtx.Err() == context.DeadlineExceeded {
					log("  [ERROR] rsync cfg for server-%d timed out after %v", i, TimeoutRsync)
				} else {
					log("  [ERROR] rsync cfg for server-%d failed: %v", i, err)
				}
			} else {
				log("  [OK] Synced cfg from %s to server-%d", sharedCfgDir, i)
			}
			cfgRsyncCancel()
		} else {
			log("  [i] Shared cfg directory %s not found; skipping cfg sync for server-%d", sharedCfgDir, i)
		}

		// Re-apply Metamod configuration for each server so that any changes
		// to plugins/configs do not drop the Metamod line from gameinfo.gi.
		if err := configureMetamodGo(&buf, mgr.CS2User, i, enableMetamod); err != nil {
			log("  [!] Configure Metamod for server-%d failed after plugin deploy: %v", i, err)
		}
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	// Ensure the CS2 user owns everything under its home directory after
	// rsyncing plugins/configs as root.
	homeDir := filepath.Join("/home", mgr.CS2User)
	log("Ensuring ownership of %s for user %s", homeDir, mgr.CS2User)
	if err := exec.Command("chown", "-R", fmt.Sprintf("%s:%s", mgr.CS2User, mgr.CS2User), homeDir).Run(); err != nil {
		log("  [!] Warning: Failed to set ownership of %s: %v", homeDir, err)
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

	// Update cs2-config with new default configs from plugin bundles, then apply overrides
	mgr, err := NewTmuxManager()
	if err == nil {
		log("")
		log("Updating shared configs with new plugin defaults...")
		if err := updateSharedConfigsFromPluginBundle(w, mgr); err != nil {
			log("[WARN] Failed to update shared configs: %v", err)
		} else {
			log("[OK] Shared configs updated")
		}

		log("")
		log("Applying user overrides to shared configs...")
		if err := applyOverridesToSharedConfigs(w, mgr); err != nil {
			log("[WARN] Failed to apply overrides: %v", err)
		} else {
			log("[OK] User overrides applied")
		}
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

// updateSharedConfigsFromPluginBundle updates cs2-config with new default configs
// from the plugin bundle (game_files/cfg). Only copies files that don't exist in
// cs2-config, preserving user customizations.
func updateSharedConfigsFromPluginBundle(w io.Writer, mgr *TmuxManager) error {
	up := NewPluginUpdater()
	srcCfgDir := filepath.Join(up.GameDir, "csgo", "cfg")
	dstCfgDir := filepath.Join("/home", mgr.CS2User, "cs2-config", "game", "csgo", "cfg")

	// Ensure destination exists
	if err := os.MkdirAll(dstCfgDir, 0o755); err != nil {
		return fmt.Errorf("failed to create cs2-config cfg directory: %w", err)
	}

	// Check if source has any config files
	if fi, err := os.Stat(srcCfgDir); err != nil || !fi.IsDir() {
		fmt.Fprintf(w, "  [i] No default configs found in plugin bundle\n")
		return nil
	}

	// Use rsync with --update to only copy files that are newer or don't exist
	// This preserves existing user customizations while adding new default configs
	fmt.Fprintf(w, "  [*] Syncing new default configs from plugin bundle to cs2-config...\n")
	if err := runCmdLogged(w, "rsync", "-a", "--update", srcCfgDir+string(os.PathSeparator), dstCfgDir+string(os.PathSeparator)); err != nil {
		return fmt.Errorf("failed to sync configs: %w", err)
	}

	return nil
}

// applyOverridesToSharedConfigs applies user overrides from overrides/game/csgo/cfg
// to cs2-config/game/csgo/cfg. This ensures user customizations always win.
func applyOverridesToSharedConfigs(w io.Writer, mgr *TmuxManager) error {
	up := NewPluginUpdater()
	srcOverridesDir := filepath.Join(up.OverridesDir, "csgo", "cfg")
	dstCfgDir := filepath.Join("/home", mgr.CS2User, "cs2-config", "game", "csgo", "cfg")

	// Check if overrides directory exists
	if fi, err := os.Stat(srcOverridesDir); err != nil || !fi.IsDir() {
		fmt.Fprintf(w, "  [i] No overrides directory found, skipping\n")
		return nil
	}

	// Ensure destination exists
	if err := os.MkdirAll(dstCfgDir, 0o755); err != nil {
		return fmt.Errorf("failed to create cs2-config cfg directory: %w", err)
	}

	// Apply overrides - this will overwrite any matching files in cs2-config
	// with user customizations from overrides/
	fmt.Fprintf(w, "  [*] Applying user overrides to cs2-config...\n")
	if err := runCmdLogged(w, "rsync", "-a", srcOverridesDir+string(os.PathSeparator), dstCfgDir+string(os.PathSeparator)); err != nil {
		return fmt.Errorf("failed to apply user overrides from %s to %s: %w (check permissions and ensure override files are readable)", srcOverridesDir, dstCfgDir, err)
	}

	return nil
}

// UpdateServerWithContext updates the game files for a single server via
// SteamCMD (against the shared master install) and rsync, without touching
// other servers. It is intended for targeted update flows such as reacting to
// a MatchZy-driven update shutdown of a specific server.
func UpdateServerWithContext(ctx context.Context, server int) (string, error) {
	var buf bytes.Buffer

	if server <= 0 {
		return "", fmt.Errorf("invalid server number %d", server)
	}

	// Discover the CS2 service user and total number of servers.
	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}
	if server > mgr.NumServers {
		return "", fmt.Errorf("server-%d not found (only %d server(s) detected)", server, mgr.NumServers)
	}
	cs2User := mgr.CS2User
	masterDir := filepath.Join("/home", cs2User, "master-install")

	// Preserve the current Metamod enabled/disabled state so we can re-apply
	// it on this server after refreshing game files from the master install.
	enableMetamod := detectMetamodEnabled(cs2User)

	// Track a transient "UPDATING" status for this server so the TUI/CLI status
	// view can show that work is in progress rather than simply "STOPPED".
	statusPath := mgr.serverStatusFile(server)
	setStatus := func(state string) {
		if strings.TrimSpace(statusPath) == "" {
			return
		}
		if state == "" {
			_ = os.Remove(statusPath)
			return
		}
		if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
			return
		}
		if err := os.WriteFile(statusPath, []byte(state+"\n"), 0o644); err != nil {
			// Status file write failure is non-critical, just log it
			fmt.Fprintf(os.Stderr, "[update] Warning: Failed to write status file %s: %v\n", statusPath, err)
		}
	}
	setStatus("UPDATING")
	defer setStatus("")

	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !strings.HasSuffix(format, "\n") {
			buf.WriteByte('\n')
		}
	}

	// Optional log file for streaming progress to the TUI when invoked from a
	// long-running monitor or CLI wrapper that sets CSM_UPDATE_GAME_LOG.
	var logFile *os.File
	if logPath := strings.TrimSpace(os.Getenv("CSM_UPDATE_GAME_LOG")); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			logFile = f
			defer func() {
				_ = f.Close()
			}()
		}
	}

	// Helper to honor cancellation between major phases.
	checkCtx := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return doneOrNil(nil)
		}
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	if fi, err := os.Stat(masterDir); err != nil || !fi.IsDir() {
		return buf.String(), fmt.Errorf("master install not found at %s", masterDir)
	}

	log("=== Update CS2 Game Files for server-%d (After Valve Update) ===", server)
	log("This will:")
	log("  • Update master CS2 installation via SteamCMD (if needed)")
	log("  • Stop server-%d", server)
	log("  • Update game files for server-%d from the master install", server)
	log("  • Restart server-%d", server)
	log("")

	// Stop the target server first so the subsequent SteamCMD/rsync cannot
	// affect a running process.
	if err := mgr.Stop(server); err != nil {
		log("Error stopping server-%d: %v", server, err)
		return buf.String(), err
	}
	log("server-%d stopped.", server)

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	// Update the shared master install via SteamCMD.
	if err := updateMasterInstallWithContext(ctx, &buf, logFile, cs2User, masterDir); err != nil {
		return buf.String(), err
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	// Sync the updated master game files into the specific server instance.
	if err := syncMasterToServerWithContext(ctx, &buf, logFile, masterDir, mgr, server); err != nil {
		// Errors are logged inside the helper.
		return buf.String(), err
	}

	// Ensure Metamod remains in sync with the pre-update setting.
	if err := configureMetamodGo(&buf, cs2User, server, enableMetamod); err != nil {
		log("Error configuring Metamod for server-%d after update: %v", server, err)
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	log("")

	// As a safety net, ensure the CS2 user owns everything under its home
	// directory after rsync/SteamCMD work.
	homeDir := filepath.Join("/home", cs2User)
	log("Ensuring ownership of %s for user %s", homeDir, cs2User)
	if err := exec.Command("chown", "-R", fmt.Sprintf("%s:%s", cs2User, cs2User), homeDir).Run(); err != nil {
		log("  [!] Warning: Failed to set ownership of %s: %v", homeDir, err)
	}

	log("")
	log("Restarting server-%d...", server)
	if err := mgr.Start(server); err != nil {
		log("Error starting server-%d: %v", server, err)
		return buf.String(), err
	}
	log("[OK] Server-%d started after game update.", server)

	out := buf.String()
	AppendLog("update-server.log", out)
	return out, nil
}

// updateMasterInstallWithContext runs SteamCMD against the shared master
// install for the given CS2 user, streaming output into the provided buffer
// and optional log file.
func updateMasterInstallWithContext(ctx context.Context, w *bytes.Buffer, logFile *os.File, cs2User, masterDir string) error {
	log := func(format string, args ...any) {
		fmt.Fprintf(w, format, args...)
		if !strings.HasSuffix(format, "\n") {
			w.WriteString("\n")
		}
		if logFile != nil {
			fmt.Fprintf(logFile, format, args...)
			if !strings.HasSuffix(format, "\n") {
				_, _ = logFile.Write([]byte{'\n'})
			}
		}
	}

	log("Updating master install at %s via SteamCMD...", masterDir)
	cmd := exec.CommandContext(ctx, "sudo", "-u", cs2User, "steamcmd",
		"+force_install_dir", masterDir,
		"+login", "anonymous",
		"+app_update", "730", "validate",
		"+quit",
	)

	var steamOut io.Writer = w
	if logFile != nil {
		steamOut = &teeWriter{buf: w, file: logFile}
	}
	cmd.Stdout = steamOut
	cmd.Stderr = steamOut
	if err := cmd.Run(); err != nil {
		log("SteamCMD update failed: %v", err)
		return err
	}
	log("Master CS2 install updated.")
	return nil
}

// syncMasterToServerWithContext rsyncs the master CS2 game install into a
// single server's game directory, preserving plugins and streaming progress
// into the provided buffer and optional log file.
func syncMasterToServerWithContext(ctx context.Context, w *bytes.Buffer, logFile *os.File, masterDir string, mgr *TmuxManager, server int) error {
	log := func(format string, args ...any) {
		fmt.Fprintf(w, format, args...)
		if !strings.HasSuffix(format, "\n") {
			w.WriteString("\n")
		}
		if logFile != nil {
			fmt.Fprintf(logFile, format, args...)
			if !strings.HasSuffix(format, "\n") {
				_, _ = logFile.Write([]byte{'\n'})
			}
		}
	}

	serverDir := mgr.serverDir(server)
	if fi, err := os.Stat(serverDir); err != nil || !fi.IsDir() {
		log("  Server-%d not found at %s, skipping", server, serverDir)
		return fmt.Errorf("server-%d not found at %s", server, serverDir)
	}
	log("  Updating server-%d ...", server)
	dst := filepath.Join(serverDir, "game")

	// Back up cfg and addons directories, similar to the full update flow.
	cfgDir := filepath.Join(dst, "csgo", "cfg")
	addonsDir := filepath.Join(dst, "csgo", "addons")
	if fi, err := os.Stat(cfgDir); err == nil && fi.IsDir() {
		_ = exec.Command("cp", "-a", cfgDir, cfgDir+".backup").Run()
	}
	if fi, err := os.Stat(addonsDir); err == nil && fi.IsDir() {
		_ = exec.Command("cp", "-a", addonsDir, addonsDir+".backup").Run()
	}

	// Use the provided masterDir as the authoritative source for updated game
	// files so callers can control which master install to sync from.
	srcRoot := filepath.Join(masterDir, "game") + string(os.PathSeparator)
	dstRoot := dst + string(os.PathSeparator)

	args := []string{
		"-a", "--delete",
		"--info=PROGRESS2",
		"--exclude", "csgo/addons/",
		srcRoot,
		dstRoot,
	}
	var rsyncOut io.Writer = w
	if logFile != nil {
		rsyncOut = &teeWriter{buf: w, file: logFile}
	}
	if err := runCmdLoggedContext(ctx, rsyncOut, "rsync", args...); err != nil {
		log("  [ERROR] rsync to server-%d failed: %v", server, err)
		return err
	}
	log("  [OK] Server-%d game files updated", server)
	return nil
}

// doneOrNil is a tiny helper used to satisfy the checkCtx pattern while
// keeping the code concise.
func doneOrNil(err error) error {
	return err
}
