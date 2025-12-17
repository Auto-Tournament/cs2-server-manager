package csm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// runCmdLogged runs a command, streaming its combined output into the provided
// writer. It returns any error from exec.Command. Callers that need
// cancellation should use runCmdLoggedContext with an explicit context.
func runCmdLogged(w io.Writer, name string, args ...string) error {
	return runCmdLoggedContext(context.Background(), w, name, args...)
}

// runCmdLoggedContext is like runCmdLogged but accepts a context so callers
// can terminate long-running operations (rsync, docker, apt-get, etc.) when a
// TUI/CLI operation is cancelled.
func runCmdLoggedContext(ctx context.Context, w io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)

	// When CSM_SCALE_LOG is set and the writer is a *bytes.Buffer, mirror the
	// command's output into both the buffer and the designated on-disk log
	// file so long-running operations (like scaling servers) can be tailed by
	// the TUI for live progress.
	target := w
	if logPath := strings.TrimSpace(os.Getenv("CSM_SCALE_LOG")); logPath != "" {
		if buf, ok := w.(*bytes.Buffer); ok {
			if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
				defer func() {
					if cerr := f.Close(); cerr != nil {
						fmt.Fprintf(os.Stderr, "CSM_SCALE_LOG close failed: %v\n", cerr)
					}
				}()
				target = &teeWriter{buf: buf, file: f}
			}
		}
	}

	cmd.Stdout = target
	cmd.Stderr = target
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v failed: %w", name, args, err)
	}
	return nil
}

// getenvDefault returns the value of the environment variable key, or def if
// the variable is unset or empty.
func getenvDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
