package csm

import (
	"os"
	"path/filepath"
)

// ensureDefaultOverrides makes sure the overrides directory exists. In the
// original project this also copied built-in override configs out of the
// binary; here we keep it simple so that user-provided overrides continue to
// work and the rest of the bootstrap logic can rely on the directory existing.
func ensureDefaultOverrides(overridesDir string) error {
	if overridesDir == "" {
		return nil
	}
	return os.MkdirAll(filepath.Join(overridesDir, "game"), 0o755)
}
