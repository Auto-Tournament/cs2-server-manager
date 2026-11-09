package csm

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// steamProgressLine matches SteamCMD's download progress, e.g.
// " Update state (0x61) downloading, progress: 74.72 (31527428343 / 42193960412)".
var steamProgressLine = regexp.MustCompile(`Update state \(0x[0-9a-fA-F]+\) ([a-z ]+), progress: ([0-9.]+) \(([0-9]+) / ([0-9]+)\)`)

// steamProgress is one parsed SteamCMD progress line.
type steamProgress struct {
	state       string // "downloading", "verifying install", ...
	pct         int
	done, total int64 // bytes
}

func parseSteamProgress(line string) (steamProgress, bool) {
	m := steamProgressLine.FindStringSubmatch(line)
	if m == nil {
		return steamProgress{}, false
	}
	f, _ := strconv.ParseFloat(m[2], 64)
	done, _ := strconv.ParseInt(m[3], 10, 64)
	total, _ := strconv.ParseInt(m[4], 10, 64)
	pct := int(f)
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	return steamProgress{state: m[1], pct: pct, done: done, total: total}, true
}

// String is the progress as the platform shows it: "downloading 74 % (31.5 of 42.2 GB)".
func (p steamProgress) String() string {
	if p.total <= 0 {
		return fmt.Sprintf("%s %d %%", p.state, p.pct)
	}
	return fmt.Sprintf("%s %d %% (%.1f of %.1f GB)", p.state, p.pct, float64(p.done)/1e9, float64(p.total)/1e9)
}

// masterLine is the newest SteamCMD progress line of the master-install run holding
// masterMu, so a bootstrap waiting for that run can pass it on.
var masterLine struct {
	sync.Mutex
	line string
}

// masterProgressTap records SteamCMD's progress lines into masterLine.
type masterProgressTap struct{ pending []byte }

func (t *masterProgressTap) Write(p []byte) (int, error) {
	t.pending = append(t.pending, p...)
	for {
		i := indexLineEnd(t.pending)
		if i < 0 {
			break
		}
		if line := string(t.pending[:i]); steamProgressLine.MatchString(line) {
			masterLine.Lock()
			masterLine.line = line
			masterLine.Unlock()
		}
		t.pending = t.pending[i+1:]
	}
	return len(p), nil
}

// indexLineEnd is the index of the first '\n' or '\r' (SteamCMD redraws its progress
// line with '\r' when it thinks it writes to a terminal), or -1.
func indexLineEnd(b []byte) int {
	for i, c := range b {
		if c == '\n' || c == '\r' {
			return i
		}
	}
	return -1
}

// lockMaster takes masterMu. While another run holds it (the agent's background first
// download), it writes that run's newest progress line to w every 30 s, so whoever reads
// w (the platform, through stepWriter) sees the download move instead of a silent wait.
func lockMaster(w io.Writer) {
	if masterMu.TryLock() {
		return
	}
	fmt.Fprintln(w, "  [*] Waiting for the CS2 download that is already running...")
	last := time.Now()
	for !masterMu.TryLock() {
		time.Sleep(time.Second)
		if time.Since(last) < 30*time.Second {
			continue
		}
		last = time.Now()
		masterLine.Lock()
		line := masterLine.line
		masterLine.Unlock()
		if line != "" {
			fmt.Fprintln(w, line)
		}
	}
}
