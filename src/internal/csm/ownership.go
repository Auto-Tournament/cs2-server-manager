package csm

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

func uidForUser(username string) (int, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return 0, err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, err
	}
	return uid, nil
}

// pathOwnedByUID returns true if the path exists and is owned by uid.
// If the path doesn't exist, it returns true (nothing to fix).
func pathOwnedByUID(path string, uid int) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return os.IsNotExist(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		// If we can't read ownership metadata, don't trigger an expensive fix.
		return true
	}
	return int(st.Uid) == uid
}

// ensureTreeOwnedByUser ensures path and all descendants are owned by user:user.
// This is used as a targeted repair for config-sized trees that can be created
// as root during sudo-driven operations.
func ensureTreeOwnedByUser(username, path string) error {
	username = strings.TrimSpace(username)
	path = strings.TrimSpace(path)
	if username == "" || path == "" {
		return nil
	}
	if os.Geteuid() != 0 {
		// Only root can reliably fix ownership.
		return nil
	}
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return exec.Command("chown", "-R", fmt.Sprintf("%s:%s", username, username), path).Run()
}

// autoRepairOwnershipIfNeeded checks key trees under /home/<user> and, if it
// detects any obvious mismatches, performs a targeted recursive ownership fix.
func autoRepairOwnershipIfNeeded(username string, numServers int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return nil
	}
	uid, err := uidForUser(username)
	if err != nil {
		return nil
	}

	var roots []string
	roots = append(roots,
		filepath.Join("/home", username, "cs2-config", "game", "csgo", "cfg"),
		filepath.Join("/home", username, "cs2-config", "game", "csgo", "addons"),
		filepath.Join("/home", username, "overrides", "game", "csgo", "cfg"),
	)
	if numServers <= 0 {
		numServers = 1
	}
	for i := 1; i <= numServers; i++ {
		roots = append(roots,
			filepath.Join("/home", username, fmt.Sprintf("server-%d", i), "game", "csgo", "cfg"),
			filepath.Join("/home", username, fmt.Sprintf("server-%d", i), "game", "csgo", "addons"),
		)
	}

	needs := false
	for _, p := range roots {
		if !pathOwnedByUID(p, uid) {
			needs = true
			break
		}
	}
	if !needs {
		return nil
	}
	var firstErr error
	for _, p := range roots {
		if err := ensureTreeOwnedByUser(username, p); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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

