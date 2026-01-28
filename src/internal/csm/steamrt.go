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

const steamRuntimeAppID = "1628350"

// steamRuntimeDir returns the default on-disk location for the Steam Runtime
// install for a given CS2 user.
func steamRuntimeDir(cs2User string) string {
	return filepath.Join("/home", cs2User, "steamrt")
}

func steamRuntimeRunPath(cs2User string) string {
	return filepath.Join(steamRuntimeDir(cs2User), "run")
}

func steamRuntimeInstalled(cs2User string) bool {
	fi, err := os.Stat(steamRuntimeRunPath(cs2User))
	return err == nil && !fi.IsDir()
}

// ensureSteamRuntimeInstalled installs Steam Runtime (SteamRT3) via steamcmd
// (app 1628350) into /home/<cs2User>/steamrt when missing.
//
// This is used as a compatibility workaround on newer distros where
// CounterStrikeSharp may fail under the system runtime.
func ensureSteamRuntimeInstalled(ctx context.Context, w io.Writer, cs2User string) error {
	if strings.TrimSpace(cs2User) == "" {
		cs2User = DefaultCS2User
	}

	if steamRuntimeInstalled(cs2User) {
		return nil
	}

	if _, err := exec.LookPath("steamcmd"); err != nil {
		return fmt.Errorf("steamcmd not found in PATH (required to install Steam Runtime)")
	}

	dest := steamRuntimeDir(cs2User)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("failed to create steam runtime dir %s: %w", dest, err)
	}
	// Ensure ownership so steamcmd running as the CS2 user can write there.
	_ = exec.Command("chown", "-R", fmt.Sprintf("%s:%s", cs2User, cs2User), dest).Run()

	fmt.Fprintf(w, "  [*] Installing Steam Runtime (SteamRT3) into %s...\n", dest)
	cmd := exec.CommandContext(ctx, "sudo", "-u", cs2User, "-H", "steamcmd",
		"+force_install_dir", dest,
		"+login", "anonymous",
		"+app_update", steamRuntimeAppID, "validate",
		"+quit",
	)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("steam runtime install failed: %w", err)
	}

	if !steamRuntimeInstalled(cs2User) {
		return fmt.Errorf("steam runtime install completed but %s was not found", steamRuntimeRunPath(cs2User))
	}

	fmt.Fprintln(w, "  [✓] Steam Runtime installed")
	return nil
}
