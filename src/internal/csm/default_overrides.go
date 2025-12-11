package csm

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// defaultOverridesFS contains the built-in overrides that ship with the CSM
// binary. These are used to seed an overrides directory on first install if
// no overrides are present yet.
//
//go:embed defaults/overrides/*
var defaultOverridesFS embed.FS

// ensureDefaultOverrides writes the embedded default overrides into the given
// overridesDir if it does not already exist. Existing files are never
// overwritten.
func ensureDefaultOverrides(overridesDir string) error {
	if overridesDir == "" {
		return fmt.Errorf("overridesDir is empty")
	}

	if fi, err := os.Stat(overridesDir); err == nil && fi.IsDir() {
		// Directory already exists; assume user has their own overrides.
		return nil
	}

	if err := os.MkdirAll(overridesDir, 0o755); err != nil {
		return err
	}

	prefix := "defaults/overrides/"
	return fs.WalkDir(defaultOverridesFS, prefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel := strings.TrimPrefix(path, prefix)
			if rel == "" {
				return nil
			}
			return os.MkdirAll(filepath.Join(overridesDir, rel), 0o755)
		}

		rel := strings.TrimPrefix(path, prefix)
		dest := filepath.Join(overridesDir, rel)

		// Don't overwrite if the user already created this file.
		if _, err := os.Stat(dest); err == nil {
			return nil
		}

		data, err := defaultOverridesFS.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
}


