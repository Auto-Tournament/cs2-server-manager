package csm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	rconProbeTimeout = 5 * time.Second
	// autoUpdateCooldown stops the monitor from updating the same server
	// again straight away if it keeps reporting an update.
	autoUpdateCooldown = time.Hour
	// autoUpdateBootTimeout is how long the monitor waits for an updated
	// server to answer RCON again.
	autoUpdateBootTimeout = 5 * time.Minute
	// markerScanMax bounds how much of a server log is scanned for markers.
	markerScanMax = 16 << 20
	// markerFirstScan is how much of the log tail is checked for a server
	// the monitor has no state for yet.
	markerFirstScan = 64 << 10
)

// probeServerIdle asks a running server over RCON whether anyone is on it
// and whether a match is loaded.
func probeServerIdle(addr, password string) serverIdleProbe {
	var p serverIdleProbe
	if strings.TrimSpace(password) == "" {
		return p
	}
	c, err := rconDial(addr, password, rconProbeTimeout)
	if err != nil {
		return p
	}
	defer c.Close()
	p.Reachable = true
	if out, err := c.Exec("status"); err == nil {
		p.HumanPlayers, p.PlayersKnown = parseStatusHumans(out)
	}
	if out, err := c.Exec("get5_status"); err == nil {
		p.MatchState = parseGet5GameState(out)
	}
	return p
}

// rconSay broadcasts msg on the server. Best-effort.
func rconSay(addr, password, msg string) error {
	c, err := rconDial(addr, password, rconProbeTimeout)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Exec(fmt.Sprintf("say %s", msg))
	return err
}

// waitForRCON polls until the server answers `status` over RCON or the
// timeout passes.
func waitForRCON(ctx context.Context, addr, password string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		c, err := rconDial(addr, password, rconProbeTimeout)
		if err == nil {
			_, err = c.Exec("status")
			_ = c.Close()
			if err == nil {
				return nil
			}
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("no RCON answer within %s: %v", timeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// logHasMarkerAfter reports whether any of markers appears in the log after
// byte offset (scanning at most markerScanMax bytes from the end). It also
// returns the current log size. If the log shrank (rotated or truncated) the
// whole file counts as new. Offset 0 means the server has no monitor state
// yet; then only the last markerFirstScan bytes are checked, as the monitor
// always did, so an old marker deep in the log does not trigger an update.
func logHasMarkerAfter(path string, offset int64, markers ...string) (found string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	size = fi.Size()
	start := offset
	switch {
	case offset == 0:
		start = size - markerFirstScan
		if start < 0 {
			start = 0
		}
	case offset < 0 || offset > size:
		start = 0
	}
	if size-start > markerScanMax {
		start = size - markerScanMax
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return "", size, err
	}
	for _, m := range markers {
		if bytes.Contains(buf, []byte(m)) {
			return m, size, nil
		}
	}
	return "", size, nil
}

// lockAutoUpdate takes a non-blocking lock so overlapping cron runs do not
// update servers at the same time. The returned func releases it.
func lockAutoUpdate() (func(), error) {
	path := filepath.Join(ResolveRoot(), "auto-update.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another monitor run is still in progress")
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// updateIdleServer runs the update for one idle server: announce, stop,
// update, start, then wait for it to answer RCON again.
func updateIdleServer(ctx context.Context, logf func(string, ...any), server int, addr, password string) error {
	if err := rconSay(addr, password, "[csm] Server restarting for a CS2 update. Back in a few minutes."); err != nil {
		logf("Server-%d: could not announce the restart: %v", server, err)
	}
	out, err := UpdateServerWithContext(ctx, server)
	if out != "" {
		logf("%s", out)
	}
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}
	logf("Server-%d: waiting for it to come back (up to %s)...", server, autoUpdateBootTimeout)
	if err := waitForRCON(ctx, addr, password, autoUpdateBootTimeout, 10*time.Second); err != nil {
		return fmt.Errorf("server did not come back after the update: %w (check `csm logs %d`)", err, server)
	}
	logf("Server-%d: back online after the update.", server)
	return nil
}
