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

// CheckDiskSpaceForGameUpdate checks if there's sufficient disk space
// for game updates. It estimates space needed based on the number of servers.
// The CS2 user's home does not need to exist yet; the check inspects the
// closest existing parent (ultimately /).
func CheckDiskSpaceForGameUpdate(cs2User string, numServers int) error {
	path := "/"
	if strings.TrimSpace(cs2User) != "" {
		path = filepath.Join("/home", cs2User)
	}

	// Estimate space needed: master + per-server overhead, plus a 10% buffer.
	estimatedGB := EstimateInstallDisk(0, numServers, VPKHardlinksEnabled()).TotalGB
	return requireFreeDiskGB(path, estimatedGB*1.1)
}

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

// liveOutput, when set, receives update-game's log as it happens (the CLI
// passes stdout), so SteamCMD and rsync progress show live instead of all at
// the end.
var liveOutput *os.File

// SetLiveOutput streams update-game's log to f while it runs. nil turns it off.
func SetLiveOutput(f *os.File) { liveOutput = f }

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

	if logFile == nil && liveOutput != nil {
		logFile = liveOutput // not closed: it belongs to the caller
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

	log("=== Update CS2 Game Files (After Valve Update) ===")
	if mgr.NumServers <= 0 && InstancesExist() {
		log("Instances: SteamCMD updates a new CS2 version next to the running one; idle instances")
		log("restart onto it, busy ones (a match on, players connected) once they are idle.")
		log("")
	} else {
		log("This will:")
		log("  • Update master CS2 installation via SteamCMD")
		log("  • Stop all servers")
		if VPKHardlinksEnabled() {
			log("  • Update game files on all servers (VPKs re-hardlinked from master)")
		} else {
			log("  • Update game files on all servers")
		}
		log("  • Restart all servers")
		log("")
	}

	if fi, err := os.Stat(masterDir); err != nil || !fi.IsDir() {
		return buf.String(), fmt.Errorf("master install not found at %s", masterDir)
	}

	if mgr.NumServers <= 0 && InstancesExist() {
		// Instance mode: one new game version, a layer rebuild, and restarts of
		// idle instances only (instance_update.go).
		im, ierr := NewInstanceManager()
		if ierr == nil {
			// Live, like the server-N path: SteamCMD can run for minutes, and output held
			// back until the end looked like a hang (2026-10-13).
			var iw io.Writer = &buf
			if logFile != nil {
				iw = io.MultiWriter(&buf, logFile)
			}
			ierr = im.updateGameLocked(ctx, iw)
			if ierr == nil {
				im.RestartIdleInstances(ctx, iw, instanceHoldNow(ctx))
				im.GC(iw)
			}
		}
		logOut := buf.String()
		AppendLog("update-game.log", logOut)
		return logOut, ierr
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

	// SteamCMD runs as the CS2 user. When csm is that user it runs directly;
	// otherwise it goes through `sudo -u`, which must work before any server
	// is stopped.
	if err := steamcmdRunAsPreflight(cs2User); err != nil {
		log("[!] %v", err)
		log("No servers were stopped.")
		logOut := buf.String()
		AppendLog("update-game.log", logOut)
		return logOut, err
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
		// The servers' game files are untouched: bring them back on the
		// current build rather than leaving them down.
		log("Master update failed; restarting the servers on their current game files...")
		if startErr := mgr.StartAll(); startErr != nil {
			log("  [!] Restarting servers failed: %v (run: csm start)", startErr)
		} else {
			log("[OK] Servers restarted (not updated).")
		}
		logOut := buf.String()
		AppendLog("update-game.log", logOut)
		return logOut, err
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	log("")
	log("Syncing updated game files to server instances...")
	libRepair := newServerLibRepair(cs2User, masterDir)
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
		if err := libRepair.ensure(ctx, &buf, fmt.Sprintf("server-%d", i), filepath.Join(mgr.serverDir(i), "game")); err != nil {
			log("  [!] server-%d game files are incomplete and it will not boot: %v (try: csm fix-libv8 %d)", i, err, i)
		}

	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	// Avoid an expensive recursive chown over /home/<user>. We chown specific
	// directories at creation time and rsync writes correct ownership directly.
	_ = ensureHomeWritable(cs2User)
	_ = ensureOwnedByUser(cs2User, masterDir)

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

	// Instance hosts update the shared Ready Up layer; server-N hosts update
	// Ready Up in each server folder. Ready Up is the only plugin stack.
	var out string
	var err error
	if InstanceBackendOn() {
		log("Servers: instances (a new Ready Up layer)")
		m, merr := NewInstanceManager()
		if merr != nil {
			err = merr
		} else {
			var lb bytes.Buffer
			err = m.UpdateReadyUp(ctx, &lb, InstanceHoldNow(ctx), nil)
			out = lb.String()
		}
	} else {
		out, err = UpdateReadyUpOnServers(ctx, nil)
	}
	if out != "" {
		log("%s", out)
	}
	if err != nil {
		log("[ERROR] Ready Up update failed: %v", err)
	}
	all := buf.String()
	AppendLog("update-and-deploy-plugins.log", all)
	return all, err
}

// UpdateServerWithContext updates the game files for a single server via
// SteamCMD (against the shared master install) and rsync, without touching
// other servers. It is intended for targeted update flows such as reacting to
// an update shutdown of a specific server.
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
	if err := newServerLibRepair(cs2User, masterDir).ensure(ctx, &buf, fmt.Sprintf("server-%d", server), filepath.Join(mgr.serverDir(server), "game")); err != nil {
		log("  [!] server-%d game files are incomplete and it will not boot: %v (try: csm fix-libv8 %d)", server, err, server)
	}

	if err := checkCtx(); err != nil {
		return buf.String(), err
	}

	log("")

	// Avoid an expensive recursive chown over /home/<user>. Ensure the key dirs
	// are still owned/writable.
	_ = ensureHomeWritable(cs2User)
	_ = ensureOwnedByUser(cs2User, masterDir)

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

	validateStr := "on"
	if !SteamcmdShouldValidate() {
		validateStr = "off"
	}
	log("Updating master install at %s via SteamCMD (validate: %s)...", masterDir, validateStr)
	steamArgs := []string{
		"+force_install_dir", masterDir,
		"+login", "anonymous",
		"+app_update", "730",
	}
	if SteamcmdShouldValidate() {
		steamArgs = append(steamArgs, "validate")
	}
	steamArgs = append(steamArgs, "+quit")
	args := steamcmdAsUser(cs2User, false, steamArgs...)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)

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
	var rsyncOut io.Writer = w
	if logFile != nil {
		rsyncOut = &teeWriter{buf: w, file: logFile}
	}
	if err := copyMasterGameToServerGame(ctx, rsyncOut, mgr.CS2User, masterDir, dst, false, true); err != nil {
		log("  [ERROR] sync to server-%d failed: %v", server, err)
		return err
	}

	// Ensure alternate launcher exists after rsync --delete (opt-in workaround).
	if err := ensureCSMLauncherSh(ctx, rsyncOut, mgr.CS2User, dst); err != nil {
		log("  [WARN] ensure csm.sh failed for server-%d: %v", server, err)
	}
	log("  [OK] Server-%d game files updated", server)
	return nil
}

// doneOrNil is a tiny helper used to satisfy the checkCtx pattern while
// keeping the code concise.
func doneOrNil(err error) error {
	return err
}
