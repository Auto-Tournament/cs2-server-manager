package csm

import (
	"fmt"
	"io"
	"os"
)

// needsSteamcmdWrapper reports whether we likely need to create /usr/bin/steamcmd
// that execs /usr/games/steamcmd (common on Debian/Ubuntu packages).
func needsSteamcmdWrapper() bool {
	if _, err := os.Stat("/usr/games/steamcmd"); err != nil {
		return false
	}
	fi, err := os.Stat("/usr/bin/steamcmd")
	if err != nil {
		return true
	}
	return fi.Mode()&0o111 == 0
}

// EnsureSteamcmdWrapper ensures /usr/bin/steamcmd exists and is executable,
// delegating to /usr/games/steamcmd. It is safe to call multiple times.
func EnsureSteamcmdWrapper(w io.Writer) error {
	if _, err := os.Stat("/usr/games/steamcmd"); err != nil {
		return fmt.Errorf("/usr/games/steamcmd not found; install steamcmd first")
	}

	const wrapper = "/usr/bin/steamcmd"
	fi, err := os.Stat(wrapper)
	if err == nil && fi.Mode()&0o111 != 0 {
		if w != nil {
			fmt.Fprintln(w, "[i] /usr/bin/steamcmd wrapper already present")
		}
		return nil
	}

	_ = os.Remove(wrapper)
	content := "#!/usr/bin/env bash\nexec /usr/games/steamcmd \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(content), 0o755); err != nil {
		return err
	}
	if w != nil {
		fmt.Fprintln(w, "[✓] Installed /usr/bin/steamcmd wrapper")
	}
	return nil
}
