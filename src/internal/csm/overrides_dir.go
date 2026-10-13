package csm

import (
	"path/filepath"
	"strings"
)

// The overrides folder holds the user's own config files. `csm update-plugins`
// and bootstrap copy it over the plugin defaults, so edits there survive
// updates.
//
// The location is /home/<cs2 user>/overrides (game files live under its game/
// subfolder).

// homeBaseDir is the parent of the CS2 user's home directory. It is a variable
// so tests can point it at a temp dir.
var homeBaseDir = "/home"

// OverridesDir returns the canonical overrides folder for cs2User:
// /home/<cs2User>/overrides. When the CS2 user is unknown it falls back to
// <csm root>/overrides so dev checkouts without a CS2 user still work.
func OverridesDir(cs2User string) string {
	return overridesDirFor(homeBaseDir, ResolveRoot(), cs2User)
}

// OverridesGameDir returns the game/ subfolder of OverridesDir, which mirrors
// a server's game/ tree.
func OverridesGameDir(cs2User string) string {
	return filepath.Join(OverridesDir(cs2User), "game")
}

func overridesDirFor(homeBase, root, cs2User string) string {
	cs2User = strings.TrimSpace(cs2User)
	if cs2User == "" {
		return filepath.Join(root, "overrides")
	}
	return filepath.Join(homeBase, cs2User, "overrides")
}
