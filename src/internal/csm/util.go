package csm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// runRsyncLoggedContext runs rsync with logging and (when running as root)
// forces destination ownership to cs2User:cs2User. This avoids leaving
// root-owned files under /home/<cs2User> when CSM is executed via sudo.
func runRsyncLoggedContext(ctx context.Context, w io.Writer, cs2User string, args ...string) error {
	cs2User = strings.TrimSpace(cs2User)

	// Avoid duplicate flags if callers already specified them.
	hasChown := false
	hasNoOwner := false
	hasNoGroup := false
	for _, a := range args {
		if strings.HasPrefix(a, "--chown=") {
			hasChown = true
		}
		if a == "--no-owner" {
			hasNoOwner = true
		}
		if a == "--no-group" {
			hasNoGroup = true
		}
	}

	finalArgs := make([]string, 0, len(args)+3)
	if os.Geteuid() == 0 && cs2User != "" && !hasChown {
		finalArgs = append(finalArgs, "--chown="+cs2User+":"+cs2User)
	} else if os.Geteuid() != 0 {
		// rsync -a includes -o/-g; for non-root runs avoid ownership errors.
		if !hasNoOwner {
			finalArgs = append(finalArgs, "--no-owner")
		}
		if !hasNoGroup {
			finalArgs = append(finalArgs, "--no-group")
		}
	}
	finalArgs = append(finalArgs, args...)

	return runCmdLoggedContext(ctx, w, "rsync", finalArgs...)
}

// runRsyncLogged is like runRsyncLoggedContext but uses a background context.
func runRsyncLogged(w io.Writer, cs2User string, args ...string) error {
	return runRsyncLoggedContext(context.Background(), w, cs2User, args...)
}

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

// --- Mutex locks for concurrent operation protection ---

var (
	pluginUpdateMutex sync.Mutex
	gameUpdateMutex   sync.Mutex
	pluginDeployMutex sync.Mutex
)

// withPluginUpdateLock runs the provided function while holding a mutex to prevent
// concurrent plugin updates.
func withPluginUpdateLock(fn func() error) error {
	pluginUpdateMutex.Lock()
	defer pluginUpdateMutex.Unlock()
	return fn()
}

// withGameUpdateLock runs the provided function while holding a mutex to prevent
// concurrent game updates.
func withGameUpdateLock(fn func() (string, error)) (string, error) {
	gameUpdateMutex.Lock()
	defer gameUpdateMutex.Unlock()
	return fn()
}

// withPluginDeployLock runs the provided function while holding a mutex to prevent
// concurrent plugin deployments.
func withPluginDeployLock(fn func() (string, error)) (string, error) {
	pluginDeployMutex.Lock()
	defer pluginDeployMutex.Unlock()
	return fn()
}

// --- HTTP retry configuration and helpers ---

// RetryConfig holds configuration for HTTP retry operations.
type RetryConfig struct {
	MaxRetries        int
	InitialDelay      time.Duration
	MaxDelay          time.Duration
	BackoffMultiplier float64
}

// DefaultRetryConfig returns a default retry configuration for HTTP operations.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:        3,
		InitialDelay:      1 * time.Second,
		MaxDelay:          10 * time.Second,
		BackoffMultiplier: 2.0,
	}
}

// RetryHTTPRead performs an HTTP GET request with retry logic.
func RetryHTTPRead(client *http.Client, url string, cfg RetryConfig) ([]byte, error) {
	var lastErr error
	delay := cfg.InitialDelay

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(delay)
			if delay < cfg.MaxDelay {
				delay = time.Duration(float64(delay) * cfg.BackoffMultiplier)
				if delay > cfg.MaxDelay {
					delay = cfg.MaxDelay
				}
			}
		}

		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
			continue
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		return data, nil
	}

	return nil, fmt.Errorf("failed after %d retries: %w", cfg.MaxRetries+1, lastErr)
}

// RetryHTTPGet performs an HTTP GET request with retry logic and returns the response.
func RetryHTTPGet(client *http.Client, url string, cfg RetryConfig) (*http.Response, error) {
	var lastErr error
	delay := cfg.InitialDelay

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(delay)
			if delay < cfg.MaxDelay {
				delay = time.Duration(float64(delay) * cfg.BackoffMultiplier)
				if delay > cfg.MaxDelay {
					delay = cfg.MaxDelay
				}
			}
		}

		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("failed after %d retries: %w", cfg.MaxRetries+1, lastErr)
}

// EnsureDirectoryExists creates a directory and all parent directories if they don't exist.
func EnsureDirectoryExists(path string) error {
	return os.MkdirAll(path, 0o755)
}
