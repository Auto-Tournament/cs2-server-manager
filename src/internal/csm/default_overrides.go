package csm

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// legacyOnlyOverrides are the defaults only the legacy MatchZy Enhanced stack
// reads: MatchZy's configs and CounterStrikeSharp's admins. A Ready Up install
// does not get them, so its servers have no MatchZy folder that nothing
// uses (issue #108).
var legacyOnlyOverrides = []string{
	filepath.Join("game", "csgo", "cfg", "MatchZy"),
	filepath.Join("game", "csgo", "addons", "counterstrikesharp"),
}

func isLegacyOnlyOverride(rel string) bool {
	for _, p := range legacyOnlyOverrides {
		if rel == p || strings.HasPrefix(rel, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ensureDefaultOverridesWithTracking seeds the built-in overrides, tracking
// which files were created for cleanup on cancellation. Without `legacy`
// (the Ready Up stack) the MatchZy / CounterStrikeSharp ones are left out.
func ensureDefaultOverridesWithTracking(overridesDir string, createdFiles *[]string, legacy bool) error {
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

		if !legacy && isLegacyOnlyOverride(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
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
