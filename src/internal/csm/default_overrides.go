package csm

import (
	"os"
	"path/filepath"
)

// ensureDefaultOverridesWithTracking creates the overrides directory a fresh
// install starts from. csm ships no default override files any more (they
// were the MatchZy Enhanced stack's configs); the directory is yours.
func ensureDefaultOverridesWithTracking(overridesDir string, createdFiles *[]string) error {
	if overridesDir == "" {
		return nil
	}
	return os.MkdirAll(filepath.Join(overridesDir, "game"), 0o755)
}
