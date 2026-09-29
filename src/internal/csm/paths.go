package csm

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveRoot is where csm keeps its state (overrides/, logs/, fleet/, …):
// CSM_ROOT when set, else a git checkout's directory (an overrides/ folder
// next to a csm binary outside the system bin dirs), else the home directory
// of the user running csm.
func ResolveRoot() string {
	if v := strings.TrimSpace(os.Getenv("CSM_ROOT")); v != "" {
		return v
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		dir := filepath.Dir(exe)
		if !isSystemBinDir(dir) {
			if _, err := os.Stat(filepath.Join(dir, "overrides")); err == nil {
				return dir
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return DefaultRootDir
}

// isSystemBinDir reports whether dir is a shared binary directory, which is
// never csm's state directory.
func isSystemBinDir(dir string) bool {
	switch filepath.Clean(dir) {
	case "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin", "/usr/local/sbin", "/opt/bin":
		return true
	}
	return false
}
