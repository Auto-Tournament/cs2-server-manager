package csm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Extra CS2 launch arguments per server, set from the platform (server.set_launch_args).
// They sit in launch_args.json next to the server (instance-N/ or server-N/), so every way
// of starting it (agent, CLI, monitor) reads the same file, and csm appends them to the
// line it builds.

const launchArgsFile = "launch_args.json"

// MaxLaunchArgs and MaxLaunchArgLen match the protocol's limits.
const (
	MaxLaunchArgs   = 64
	MaxLaunchArgLen = 256
)

// launchArgToken is what an argument may contain. The server-N launch line is a shell
// string, so no spaces, quotes or shell characters.
var launchArgToken = regexp.MustCompile(`^[A-Za-z0-9_.:/=+,@%-]+$`)

// managedLaunchArgs are set by csm on every start (or by its own settings); a server's
// extra arguments can't repeat or override them.
var managedLaunchArgs = map[string]bool{
	"-dedicated": true, "-ip": true, "-port": true, "-usercon": true, "-gslt": true,
	"+tv_port": true, "+clientport": true, "+maxplayers": true, "+map": true,
	"+game_type": true, "+game_mode": true,
}

// ValidateLaunchArgs checks extra arguments: at most 64, each up to 256 characters of
// [A-Za-z0-9_.:/=+,@%-], the first one a flag (- or +), and none of the ones csm manages.
func ValidateLaunchArgs(args []string) error {
	if len(args) > MaxLaunchArgs {
		return fmt.Errorf("at most %d launch arguments", MaxLaunchArgs)
	}
	for i, a := range args {
		if a == "" || len(a) > MaxLaunchArgLen {
			return fmt.Errorf("argument %d is empty or longer than %d characters", i+1, MaxLaunchArgLen)
		}
		if !launchArgToken.MatchString(a) {
			return fmt.Errorf("%q: only letters, digits and _ . : / = + , @ %% - are allowed", a)
		}
		flag := a[0] == '-' || a[0] == '+'
		if i == 0 && !flag {
			return fmt.Errorf("%q: the first argument must start with - or +", a)
		}
		if flag && managedLaunchArgs[strings.ToLower(a)] {
			return fmt.Errorf("%s is set by csm and can't be changed here", a)
		}
	}
	return nil
}

// readLaunchArgs is the extra arguments saved in dir; nil when there are none or the file
// is unreadable or invalid (a bad file must not keep the server from starting).
func readLaunchArgs(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, launchArgsFile))
	if err != nil {
		return nil
	}
	var v struct {
		Args []string `json:"args"`
	}
	if json.Unmarshal(data, &v) != nil || ValidateLaunchArgs(v.Args) != nil {
		return nil
	}
	return v.Args
}

// writeLaunchArgs saves args in dir (an empty list removes the file).
func writeLaunchArgs(dir string, args []string) error {
	if err := ValidateLaunchArgs(args); err != nil {
		return err
	}
	path := filepath.Join(dir, launchArgsFile)
	if len(args) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(struct {
		Args []string `json:"args"`
	}{args}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SetLaunchArgs is the agent's LaunchArgsSetter: it saves args next to the server.
func (b *HostBackend) SetLaunchArgs(server int, args []string) error {
	var dir string
	if InstanceBackendOn() {
		m, err := NewInstanceManager()
		if err != nil {
			return err
		}
		dir = m.L.Dir(server)
	} else {
		mgr, err := NewTmuxManager()
		if err != nil {
			return err
		}
		dir = mgr.serverDir(server)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("server-%d does not exist on this host", server)
	}
	return writeLaunchArgs(dir, args)
}
