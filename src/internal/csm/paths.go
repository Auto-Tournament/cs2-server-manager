package csm

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveRoot determines the base directory where CSM keeps its persistent
// state (overrides/, game_files/, logs/, etc.).
//
// Priority:
//   1) CSM_ROOT environment variable, if set.
//   2) A local overrides/ directory next to the csm binary (for dev/git checkouts).
//   3) DefaultRootDir (typically /opt/cs2-server-manager).
func ResolveRoot() string {
	if v := strings.TrimSpace(os.Getenv("CSM_ROOT")); v != "" {
		return v
	}

	// When running from a git checkout or local build, prefer the directory
	// containing the binary if it already has an overrides/ folder. This keeps
	// local/dev behaviour intuitive without requiring CSM_ROOT.
	if exe, err := os.Executable(); err == nil && exe != "" {
		if dir := filepath.Dir(exe); dir != "" {
			if _, err := os.Stat(filepath.Join(dir, "overrides")); err == nil {
				return dir
			}
		}
	}

	// Fallback to the global default root. This will be created on demand by
	// bootstrap/update flows as needed.
	return DefaultRootDir
}


