package csm

import (
	"fmt"
	"os"
	"os/exec"
)

// ensureOwnedByUser makes sure the given path is owned by user:user.
// It is intentionally non-recursive and is meant as a fast fix for directories
// created by root during operations that otherwise create user-owned files
// (e.g. SteamCMD running as the CS2 user, rsync preserving/chowning ownership).
func ensureOwnedByUser(user, path string) error {
	if user == "" || path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		// If it doesn't exist, there's nothing to fix.
		return nil
	}
	return exec.Command("chown", fmt.Sprintf("%s:%s", user, user), path).Run()
}

// ensureHomeWritable ensures /home/<user> is owned by the user so tools like
// SteamCMD can create ~/.steam and other state without permission errors.
// This is intentionally NOT recursive for speed.
func ensureHomeWritable(user string) error {
	if user == "" {
		return nil
	}
	return ensureOwnedByUser(user, fmt.Sprintf("/home/%s", user))
}

