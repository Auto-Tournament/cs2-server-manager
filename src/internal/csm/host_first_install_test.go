package csm

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStepWriterReportsBootstrapSteps(t *testing.T) {
	var steps []string
	var pcts []int
	w := &stepWriter{progress: func(step string, pct int) { steps = append(steps, step); pcts = append(pcts, pct) }}
	_, _ = w.Write([]byte("[1/5] Installing dependencies...\nnoise\n[3/5] Setting up Steam"))
	_, _ = w.Write([]byte(" SDK symlinks...\n"))
	if len(steps) != 2 || steps[0] != "Installing dependencies" || steps[1] != "Setting up Steam SDK symlinks" {
		t.Fatalf("steps %q", steps)
	}
	if pcts[0] != 5 || pcts[1] != 35 {
		t.Fatalf("pcts %v", pcts)
	}
}

func TestDropSteamProgress(t *testing.T) {
	in := "[1/5] Installing CS2...\n\x1b[0m Update state (0x61) downloading, progress: 74.72 (1 / 2)\n Update state (0x61) downloading, progress: 75.00 (1 / 2)\nSuccess! App '730' fully installed.\n"
	got := dropSteamProgress(in)
	want := "[1/5] Installing CS2...\nSuccess! App '730' fully installed.\n"
	if got != want {
		t.Fatalf("dropSteamProgress = %q, want %q", got, want)
	}
}

func TestStepWriterReportsSteamDownloadInsideTheStep(t *testing.T) {
	var steps []string
	var pcts []int
	w := &stepWriter{progress: func(step string, pct int) { steps = append(steps, step); pcts = append(pcts, pct) }}
	_, _ = w.Write([]byte("[2/4] Installing/updating master CS2 installation...\n"))
	_, _ = w.Write([]byte(" Update state (0x61) downloading, progress: 50.10 (21000000000 / 42000000000)\n"))
	// SteamCMD redraws with \r; the same percent is not reported twice.
	_, _ = w.Write([]byte(" Update state (0x61) downloading, progress: 50.90 (21300000000 / 42000000000)\r"))
	_, _ = w.Write([]byte(" Update state (0x61) downloading, progress: 100.00 (42000000000 / 42000000000)\r"))
	want := []string{
		"Installing/updating master CS2 installation",
		"Installing/updating master CS2 installation: downloading 50 % (21.0 of 42.0 GB)",
		"Installing/updating master CS2 installation: downloading 100 % (42.0 of 42.0 GB)",
	}
	if len(steps) != len(want) {
		t.Fatalf("steps %q", steps)
	}
	for i := range want {
		if steps[i] != want[i] {
			t.Fatalf("step %d = %q, want %q", i, steps[i], want[i])
		}
	}
	// Step 2 of 4 runs from 23 % to 41 %.
	if pcts[0] != 23 || pcts[1] != 32 || pcts[2] != 41 {
		t.Fatalf("pcts %v", pcts)
	}
}

func TestStepWriterIgnoresSteamProgressBeforeAStep(t *testing.T) {
	called := false
	w := &stepWriter{progress: func(string, int) { called = true }}
	_, _ = w.Write([]byte(" Update state (0x61) downloading, progress: 10.00 (1 / 10)\n"))
	if called {
		t.Fatal("progress reported with no step")
	}
}

func TestLockMasterPassesOnTheRunningDownload(t *testing.T) {
	masterMu.Lock()
	tap := &masterProgressTap{}
	_, _ = tap.Write([]byte(" Update state (0x61) downloading, progress: 12.00 (5 / 42)\r"))
	var mu sync.Mutex
	var out strings.Builder
	done := make(chan struct{})
	go func() {
		lockMaster(writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return out.Write(p) }))
		masterMu.Unlock()
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	masterMu.Unlock()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(out.String(), "Waiting for the CS2 download") {
		t.Fatalf("out %q", out.String())
	}
	masterLine.Lock()
	line := masterLine.line
	masterLine.line = ""
	masterLine.Unlock()
	if !strings.Contains(line, "progress: 12.00") {
		t.Fatalf("masterLine %q", line)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestWatchStallStopsASilentRun(t *testing.T) {
	old := steamStallTimeout
	steamStallTimeout = 50 * time.Millisecond
	defer func() { steamStallTimeout = old }()

	// Output keeps coming: no stop.
	ctx, cancel := context.WithCancel(context.Background())
	tap := &masterProgressTap{}
	tap.last.Store(time.Now().UnixNano())
	var stalled atomic.Bool
	done := make(chan struct{})
	go func() { watchStall(ctx, cancel, tap, 10*time.Millisecond, &stalled); close(done) }()
	for i := 0; i < 10; i++ {
		_, _ = tap.Write([]byte("Update state (0x61) downloading, progress: 1.00 (1 / 100)\n"))
		time.Sleep(20 * time.Millisecond)
	}
	if stalled.Load() || ctx.Err() != nil {
		t.Fatal("stopped while SteamCMD was printing")
	}
	// Silence: stopped.
	time.Sleep(150 * time.Millisecond)
	<-done
	if !stalled.Load() || ctx.Err() == nil {
		t.Fatal("a silent run was not stopped")
	}
}
