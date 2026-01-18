package csm

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

// defaultOverridesFS embeds the built-in overrides tree so that CSM can seed
// a fresh installation without requiring a git checkout of the overrides/
// directory on the target host.
//
// The layout under defaults/overrides/ mirrors the runtime overrides/
// directory structure (game/csgo/...).
//
//go:embed defaults/overrides/**
var defaultOverridesFS embed.FS

// ensureDefaultOverrides makes sure the overrides directory exists and, when
// empty, seeds it with the built-in defaults embedded in the binary. Existing
// user-provided overrides are never overwritten; the embedded files are only
// written when the target path does not already exist.
func ensureDefaultOverrides(overridesDir string) error {
	return ensureDefaultOverridesWithTracking(overridesDir, nil)
}

// ensureDefaultOverridesWithTracking is like ensureDefaultOverrides but tracks
// which files were created for cleanup on cancellation.
func ensureDefaultOverridesWithTracking(overridesDir string, createdFiles *[]string) error {
	if overridesDir == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Join(overridesDir, "game"), 0o755); err != nil {
		return err
	}

	const root = "defaults/overrides"

	return fs.WalkDir(defaultOverridesFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}

		outPath := filepath.Join(overridesDir, rel)

		if d.IsDir() {
			return os.MkdirAll(outPath, 0o755)
		}

		// Don't clobber any existing on-disk overrides; those are considered
		// user-managed and should win over the embedded defaults.
		if _, err := os.Stat(outPath); err == nil {
			return nil
		}

		data, err := defaultOverridesFS.ReadFile(path)
		if err != nil {
			return err
		}

		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			return err
		}

		// Track this file as created if tracking is enabled
		if createdFiles != nil {
			*createdFiles = append(*createdFiles, outPath)
		}

		return nil
	})
}
