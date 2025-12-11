package csm

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// InstallDependencies installs the core system dependencies required by CSM
// (tmux, steamcmd, rsync, jq, etc.) using the same logic as the bootstrap
// flow. It is intended to be run once on a fresh host.
func InstallDependencies() (string, error) {
	var buf bytes.Buffer

	if os.Geteuid() != 0 {
		return "", fmt.Errorf("dependency installation must be run as root (use sudo)")
	}

	// If CSM_DEPS_LOG is set, mirror dependency installation logs into that
	// file so the TUI can display live progress while the step runs.
	w := io.Writer(&buf)
	if logPath, ok := os.LookupEnv("CSM_DEPS_LOG"); ok && logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			defer f.Close()
			w = &teeWriter{buf: &buf, file: f}
		}
	}

	if err := ensureBootstrapDependencies(w); err != nil {
		return buf.String(), err
	}

	return buf.String(), nil
}


