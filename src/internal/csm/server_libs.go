package csm

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A CS2 server that lacks these files dies at boot with
//
//	failed to dlopen .../csgo/bin/linuxsteamrt64/libserver.so error=libv8.so: cannot open shared object file
//	FATAL ERROR: CAppSystemDict: Unable to load module server
//
// This happens when the master -> server copy (or the SteamCMD download
// into master) was cut short. Bootstrap used to log the copy error and carry
// on, reporting the server as ready.

// requiredServerLib is one file a server's game/ tree must have. Any of the
// candidate paths (relative to game/) satisfies it.
type requiredServerLib struct {
	name       string
	candidates []string
}

var requiredServerLibs = []requiredServerLib{
	{"cs2", []string{filepath.Join("bin", "linuxsteamrt64", "cs2")}},
	{"libserver.so", []string{filepath.Join("csgo", "bin", "linuxsteamrt64", "libserver.so")}},
	{"libv8.so", []string{
		filepath.Join("bin", "linuxsteamrt64", "libv8.so"),
		filepath.Join("csgo", "bin", "linuxsteamrt64", "libv8.so"),
	}},
}

// missingServerLibs returns the required files that gameRoot (a server's or
// master's game/ directory) lacks. Broken symlinks count as missing.
func missingServerLibs(gameRoot string) []string {
	var missing []string
	for _, lib := range requiredServerLibs {
		found := false
		for _, rel := range lib.candidates {
			if fi, err := os.Stat(filepath.Join(gameRoot, rel)); err == nil && fi.Mode().IsRegular() {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, lib.name)
		}
	}
	return missing
}

// serverLibRepair holds what ensureServerGameLibs needs, so tests can swap
// out SteamCMD and the copy.
type serverLibRepair struct {
	cs2User   string
	masterDir string
	// validateMaster re-downloads missing master files (SteamCMD validate).
	validateMaster func(ctx context.Context, w io.Writer) error
	// syncServer copies master's game/ into serverGameDir.
	syncServer func(ctx context.Context, w io.Writer, serverGameDir string) error

	masterValidated bool
}

func newServerLibRepair(cs2User, masterDir string) *serverLibRepair {
	return &serverLibRepair{
		cs2User:   cs2User,
		masterDir: masterDir,
		validateMaster: func(ctx context.Context, w io.Writer) error {
			return validateMasterInstall(ctx, w, cs2User, masterDir)
		},
		syncServer: func(ctx context.Context, w io.Writer, serverGameDir string) error {
			return copyMasterGameToServerGame(ctx, w, cs2User, masterDir, serverGameDir, false, false)
		},
	}
}

// ensure checks that serverGameDir has the files a server needs to boot. If
// not, it validates master via SteamCMD when master lacks them too (at most
// once per repair), re-copies master into the server and checks again. It
// returns an error naming the files that are still missing.
func (r *serverLibRepair) ensure(ctx context.Context, w io.Writer, label, serverGameDir string) error {
	missing := missingServerLibs(serverGameDir)
	if len(missing) == 0 {
		return nil
	}
	fmt.Fprintf(w, "  [!] %s is missing %s; repairing from master-install...\n", label, strings.Join(missing, ", "))

	masterGame := filepath.Join(r.masterDir, "game")
	if mm := missingServerLibs(masterGame); len(mm) > 0 {
		if r.masterValidated {
			return fmt.Errorf("master-install is missing %s even after SteamCMD validate", strings.Join(mm, ", "))
		}
		fmt.Fprintf(w, "  [!] master-install is missing %s too; running SteamCMD validate...\n", strings.Join(mm, ", "))
		r.masterValidated = true
		if err := r.validateMaster(ctx, w); err != nil {
			return fmt.Errorf("SteamCMD validate of master-install failed: %w", err)
		}
		if mm := missingServerLibs(masterGame); len(mm) > 0 {
			return fmt.Errorf("master-install is missing %s even after SteamCMD validate", strings.Join(mm, ", "))
		}
	}

	if err := r.syncServer(ctx, w, serverGameDir); err != nil {
		return fmt.Errorf("re-copy from master-install failed: %w", err)
	}
	if missing := missingServerLibs(serverGameDir); len(missing) > 0 {
		return fmt.Errorf("still missing %s after re-copy from master-install", strings.Join(missing, ", "))
	}
	fmt.Fprintf(w, "  [✓] %s game files repaired\n", label)
	return nil
}

// validateMasterInstall runs SteamCMD app_update 730 validate on the master
// install, which re-downloads missing or damaged files.
func validateMasterInstall(ctx context.Context, w io.Writer, cs2User, masterDir string) error {
	return runCmdLoggedContext(ctx, w,
		"sudo", "-u", cs2User, "-H", "steamcmd",
		"+force_install_dir", masterDir,
		"+login", "anonymous",
		"+app_update", "730", "validate",
		"+quit",
	)
}
