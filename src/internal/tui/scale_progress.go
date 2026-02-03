package tui

import (
	"math"
	"strconv"
	"strings"
)

// parsePercentFromLine scans a log line for a percentage and returns it as an
// integer 0-100. It first looks for a "progress: <float>" pattern (used by
// SteamCMD and similar tools), then falls back to tokens like "NN%". This is
// used to drive progress bars from rsync-style output, SteamCMD logs, and
// other percent-bearing lines.
func parsePercentFromLine(line string) (int, bool) {
	// Fast path: look for "progress: <float>".
	if idx := strings.Index(line, "progress:"); idx != -1 {
		rest := strings.TrimSpace(line[idx+len("progress:"):])
		if rest != "" {
			tok := strings.Fields(rest)[0]
			// Strip common trailing punctuation/brackets.
			tok = strings.Trim(tok, "[](),")
			if tok != "" {
				if f, err := strconv.ParseFloat(tok, 64); err == nil && f >= 0 && f <= 100 {
					// Floor instead of rounding so we only show 65% once progress is >= 65.0,
					// not at 64.6%.
					return int(math.Floor(f)), true
				}
			}
		}
	}

	// Fallback: scan for "NN%" tokens.
	for _, tok := range strings.Fields(line) {
		if !strings.HasSuffix(tok, "%") {
			continue
		}
		num := strings.TrimSuffix(tok, "%")
		if num == "" {
			continue
		}
		if n, err := strconv.Atoi(num); err == nil && n >= 0 && n <= 100 {
			return n, true
		}
	}
	return 0, false
}
