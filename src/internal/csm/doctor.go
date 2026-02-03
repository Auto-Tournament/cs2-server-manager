package csm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FixLibV8 runs a targeted repair for the common startup failure:
//
//	dlopen(libserver.so) -> error=libv8.so: cannot open shared object file
//
// It validates the master install via SteamCMD, then re-rsyncs master game
// files into the requested server(s) (excluding csgo/addons/).
//
// server:
//   - 0 => all servers
//   - N => server-N
func FixLibV8(server int) (string, error) {
	return FixLibV8WithContext(context.Background(), server)
}

func FixLibV8WithContext(ctx context.Context, server int) (string, error) {
	var buf bytes.Buffer

	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}
	user := strings.TrimSpace(mgr.CS2User)
	if user == "" {
		user = DefaultCS2User
	}

	// Build the server list, but don't rely solely on mgr.NumServers: some
	// broken installs can prevent proper discovery. We still validate paths.
	var servers []int
	if server == 0 {
		for i := 1; i <= mgr.NumServers; i++ {
			servers = append(servers, i)
		}
		if len(servers) == 0 {
			// Fall back to server-1 only if it exists on disk.
			if _, statErr := os.Stat(filepath.Join("/home", user, "server-1")); statErr == nil {
				servers = []int{1}
			}
		}
	} else {
		servers = []int{server}
	}

	if len(servers) == 0 {
		return "", fmt.Errorf("no servers found to repair (expected /home/%s/server-*)", user)
	}

	masterDir := filepath.Join("/home", user, "master-install")
	fmt.Fprintf(&buf, "[*] Fix libv8.so / libserver.so dependencies\n")
	fmt.Fprintf(&buf, "    CS2 user: %s\n", user)
	fmt.Fprintf(&buf, "    Master:   %s\n", masterDir)
	fmt.Fprintf(&buf, "    Targets:  %v\n\n", servers)

	// 1) Validate master install so missing libraries are re-downloaded.
	// This repair intentionally forces validate regardless of CSM_STEAMCMD_VALIDATE.
	fmt.Fprintf(&buf, "[*] Validating master install via SteamCMD (app 730, validate forced)...\n")
	if err := runCmdLoggedContext(ctx, &buf,
		"sudo", "-u", user, "-H", "steamcmd",
		"+force_install_dir", masterDir,
		"+login", "anonymous",
		"+app_update", "730", "validate",
		"+quit",
	); err != nil {
		fmt.Fprintf(&buf, "[!] SteamCMD validate failed: %v\n", err)
		return buf.String(), err
	}
	fmt.Fprintf(&buf, "[✓] Master validated\n\n")

	// 2) Re-rsync master -> server(s) (excluding addons).
	masterGameDir := filepath.Join(masterDir, "game") + string(os.PathSeparator)
	for _, n := range servers {
		if n <= 0 {
			continue
		}
		serverDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", n))
		serverGameDir := filepath.Join(serverDir, "game") + string(os.PathSeparator)
		if _, statErr := os.Stat(serverDir); statErr != nil {
			fmt.Fprintf(&buf, "[!] server-%d not found at %s (skipping)\n", n, serverDir)
			continue
		}

		fmt.Fprintf(&buf, "[*] Syncing master -> server-%d (rsync)...\n", n)
		if err := runCmdLoggedContext(ctx, &buf,
			"rsync", "-a", "--delete",
			"--exclude", "csgo/addons/",
			masterGameDir,
			serverGameDir,
		); err != nil {
			fmt.Fprintf(&buf, "[!] rsync to server-%d failed: %v\n", n, err)
			return buf.String(), err
		}

		// 3) Sanity check: ensure libv8 exists somewhere reasonable after sync.
		root := filepath.Join(serverDir, "game")
		ok, hint := serverHasLibV8(root)
		if !ok {
			fmt.Fprintf(&buf, "[!] server-%d still appears to be missing libv8.so\n", n)
			fmt.Fprintf(&buf, "    Checked: %s\n", hint)
			fmt.Fprintf(&buf, "    Tip: run `ldd %s` and look for \"not found\" lines.\n",
				filepath.Join(root, "csgo", "bin", "linuxsteamrt64", "libserver.so"),
			)
			return buf.String(), fmt.Errorf("libv8.so not found after validate+sync (server-%d)", n)
		}

		fmt.Fprintf(&buf, "[✓] server-%d synced; libv8.so present (%s)\n\n", n, hint)
	}

	fmt.Fprintf(&buf, "[OK] Done.\n")
	fmt.Fprintf(&buf, "If the server still fails, it may be a launch-environment issue (LD_LIBRARY_PATH) or an incompatible Metamod/CSS plugin build.\n")
	return buf.String(), nil
}

func serverHasLibV8(serverGameRoot string) (ok bool, hint string) {
	// On some builds, libv8.so lives under game/bin/... and is referenced from
	// csgo/bin/... via rpath or symlink. We check both common locations.
	candidates := []string{
		filepath.Join(serverGameRoot, "bin", "linuxsteamrt64", "libv8.so"),
		filepath.Join(serverGameRoot, "csgo", "bin", "linuxsteamrt64", "libv8.so"),
		filepath.Join(serverGameRoot, "bin", "linuxsteamrt64", "libv8_libplatform.so"),
		filepath.Join(serverGameRoot, "csgo", "bin", "linuxsteamrt64", "libv8_libplatform.so"),
	}
	var checked []string
	for _, p := range candidates {
		checked = append(checked, p)
		// os.Stat follows symlinks; broken symlinks show up as missing here,
		// which is exactly what we want.
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return true, p
		}
	}
	return false, strings.Join(checked, ", ")
}
