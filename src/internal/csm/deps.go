package csm

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// InstallDependencies installs the core system packages needed for CS2 Server
// Manager (tmux, steamcmd, rsync, jq, etc.). It mirrors a subset of the
// bootstrap dependency logic but as a standalone helper callable from the TUI
// or CLI.
func InstallDependencies() (string, error) {
	var buf bytes.Buffer
	if err := installDeps(&buf); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

func installDeps(w io.Writer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("dependency installation must be run as root (use sudo)")
	}
	return ensureBootstrapDependencies(w)
}


