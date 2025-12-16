package csm

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// InstallDependencies installs the core system packages needed for CS2 Server
// Manager (tmux, steamcmd, rsync, jq, etc.). It mirrors a subset of the
// bootstrap dependency logic but as a standalone helper callable from the TUI
// or CLI.
func InstallDependencies() (string, error) {
	var buf bytes.Buffer
	if err := installDeps(&buf); err != nil {
		out := buf.String()
		AppendLog("deps.log", out)
		return out, err
	}
	out := buf.String()
	if strings.TrimSpace(out) == "" {
		out = "System dependencies installed successfully (or already up to date)."
	}
	AppendLog("deps.log", out)
	return out, nil
}

func installDeps(w io.Writer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("dependency installation must be run as root (use sudo)")
	}
	return ensureBootstrapDependencies(w)
}
