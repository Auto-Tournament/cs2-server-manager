package csm

import "testing"

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
