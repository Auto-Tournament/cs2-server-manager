package csm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
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
		// Provide more context in error messages
		operation := fmt.Sprintf("%s %v", name, args)
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s timed out (operation took too long): %w", operation, err)
		}
		if ctx.Err() == context.Canceled {
			return fmt.Errorf("%s was cancelled: %w", operation, err)
		}
		return fmt.Errorf("%s failed: %w", operation, err)
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

// contextWithTimeout creates a context with a timeout, but respects the parent
// context's deadline if it's shorter. This ensures we don't extend timeouts
// unnecessarily when called from TUI operations that already have timeouts.
func contextWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	
	// If parent already has a deadline, use the shorter of the two
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}
	
	return context.WithTimeout(parent, timeout)
}

// improveErrorMessage adds context to error messages to make them more actionable
func improveErrorMessage(operation string, err error, additionalContext ...string) error {
	if err == nil {
		return nil
	}
	
	msg := fmt.Sprintf("%s failed", operation)
	if len(additionalContext) > 0 {
		msg += ": " + strings.Join(additionalContext, "; ")
	}
	msg += ": " + err.Error()
	
	return fmt.Errorf(msg)
}
