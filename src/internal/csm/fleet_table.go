package csm

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// The fleet table: one row per server-N, process state from tmux next to
// match state from Ready Up. `csm status` prints it; the TUI's Servers
// dashboard renders the same text and keeps it live.

// FleetTableOptions controls rendering.
type FleetTableOptions struct {
	// Color adds ANSI colours (terminals and the TUI; not pipes).
	Color bool
	// Now is used for "offline for" ages; zero means time.Now().
	Now time.Time
}

type cellTone int

const (
	toneNone cellTone = iota
	toneGood
	toneWarn
	toneBad
	toneDim
)

type cell struct {
	text string
	tone cellTone
}

var fleetColumns = []string{"#", "PORT", "PROC", "MAP", "PHASE", "SCORE", "PLAYERS", "MATCH", "PLATFORM", "READY UP", "CS2", "SAFE"}

const matchColumnMax = 30

// RenderFleetTable renders rows as an aligned table followed by notes for
// rows that need explaining (no answer, failing selftest, …).
func RenderFleetTable(rows []FleetRow, opts FleetTableOptions) string {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	if len(rows) == 0 {
		return "No CS2 servers found. Run the install wizard (sudo csm) to create servers.\n"
	}

	grid := make([][]cell, 0, len(rows))
	var notes []string
	for _, r := range rows {
		c, note := fleetRowCells(r, now)
		grid = append(grid, c)
		notes = append(notes, note...)
	}

	widths := make([]int, len(fleetColumns))
	for i, h := range fleetColumns {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range grid {
		for i, c := range row {
			if n := utf8.RuneCountInString(c.text); n > widths[i] {
				widths[i] = n
			}
		}
	}

	var b strings.Builder
	head := make([]cell, len(fleetColumns))
	for i, h := range fleetColumns {
		head[i] = cell{text: h, tone: toneDim}
	}
	writeFleetLine(&b, head, widths, opts.Color, true)
	for _, row := range grid {
		writeFleetLine(&b, row, widths, opts.Color, false)
	}
	if len(notes) > 0 {
		b.WriteString("\n")
		for _, n := range notes {
			b.WriteString("  ")
			b.WriteString(n)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func writeFleetLine(b *strings.Builder, row []cell, widths []int, color, header bool) {
	for i, c := range row {
		if i > 0 {
			b.WriteString("  ")
		}
		text := c.text
		pad := widths[i] - utf8.RuneCountInString(text)
		if i == len(row)-1 {
			pad = 0 // no trailing spaces
		}
		if color {
			text = colorize(text, c.tone, header)
		}
		b.WriteString(text)
		if pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
	}
	b.WriteString("\n")
}

func colorize(s string, tone cellTone, bold bool) string {
	code := ""
	switch tone {
	case toneGood:
		code = "32"
	case toneWarn:
		code = "33"
	case toneBad:
		code = "31"
	case toneDim:
		code = "90"
	}
	if bold {
		code = "1"
	}
	if code == "" || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// fleetRowCells builds one table row and any notes it needs.
func fleetRowCells(r FleetRow, now time.Time) ([]cell, []string) {
	t := r.Target
	name := fmt.Sprintf("server-%d", t.Server)
	proc := cell{"stopped", toneBad}
	switch {
	case t.Updating:
		proc = cell{"updating", toneWarn}
	case t.Running:
		proc = cell{"running", toneGood}
	}
	cells := []cell{
		{text: fmt.Sprintf("%d", t.Server)},
		{text: fmt.Sprintf("%d", t.GamePort)},
		proc,
	}
	dash := cell{"-", toneDim}
	blank := func(phase cell) []cell {
		return append(cells, dash, phase, dash, dash, dash, dash, dash, dash, dash)
	}

	switch r.State {
	case ReadyUpStopped:
		return blank(dash), nil
	case ReadyUpNone:
		return blank(cell{"no Ready Up", toneDim}), nil
	case ReadyUpNoResponse:
		note := fmt.Sprintf("%s: Ready Up is not answering on port %d", name, r.StatusPort)
		if r.Err != "" {
			note += " (" + r.Err + ")"
		}
		return blank(cell{"no answer", toneWarn}), []string{note}
	}

	st := r.Status
	if st == nil {
		return blank(dash), nil
	}
	s := st.Summary
	var notes []string

	mapName := s.Map
	if mapName == "" {
		mapName = "-"
	}
	if s.NumMaps > 1 && s.MapNumber > 0 {
		mapName = fmt.Sprintf("%s %d/%d", mapName, s.MapNumber, s.NumMaps)
	}

	phase := PhaseLabel(s)
	phaseTone := toneNone
	switch {
	case strings.HasPrefix(phase, "live"):
		phaseTone = toneGood
	case phase == "paused" || phase == "error":
		phaseTone = toneWarn
	case phase == "idle":
		phaseTone = toneDim
	}

	inMatch := phase != "idle"
	score := "-"
	if inMatch {
		score = fmt.Sprintf("%d-%d", s.Score.Team1, s.Score.Team2)
		if s.NumMaps > 1 {
			score += fmt.Sprintf(" (%d-%d)", s.SeriesScore.Team1, s.SeriesScore.Team2)
		}
	}

	players := fmt.Sprintf("%d", s.Players.Connected)
	playersTone := toneNone
	if s.Players.Expected > 0 {
		players = fmt.Sprintf("%d/%d", s.Players.Connected, s.Players.Expected)
		if s.Players.Connected < s.Players.Expected {
			playersTone = toneWarn
		}
	}

	match := strings.TrimSpace(string(s.MatchID))
	if s.Teams != nil && (s.Teams.Team1 != "" || s.Teams.Team2 != "") {
		vs := s.Teams.Team1 + " vs " + s.Teams.Team2
		if match != "" {
			match += " "
		}
		match += vs
	}
	if match == "" {
		match = "-"
	}
	match = truncateRunes(match, matchColumnMax)

	platform := platformCell(st.Platform, now)

	ru := strings.TrimSpace(string(st.Versions.Core))
	if ru == "" {
		ru = "?"
	}
	ruTone := toneNone
	if st.Selftest != nil && !st.Selftest.Pass && st.Selftest.Total > 0 {
		ruTone = toneWarn
		note := fmt.Sprintf("%s: Ready Up selftest failed (%d/%d passed)", name, st.Selftest.Passed, st.Selftest.Total)
		if len(st.Selftest.Failures) > 0 {
			note += ": " + strings.Join(st.Selftest.Failures, ", ")
		}
		notes = append(notes, note)
	}

	cs2 := strings.TrimSpace(string(st.Versions.CS2Build))
	if cs2 == "" {
		cs2 = "-"
	}

	safe := cell{"?", toneDim}
	if st.UpdateSafe != nil {
		if *st.UpdateSafe {
			safe = cell{"yes", toneGood}
		} else {
			safe = cell{"NO", toneBad}
		}
	}

	cells = append(cells,
		cell{text: mapName},
		cell{phase, phaseTone},
		cell{text: score},
		cell{players, playersTone},
		cell{text: match},
		platform,
		cell{ru, ruTone},
		cell{text: cs2},
		safe,
	)
	return cells, notes
}

// PhaseLabel is the short phase text shown in the table: idle, warmup, knife,
// live R14, paused, postgame, …
func PhaseLabel(s ReadyUpSummary) string {
	phase := strings.ToLower(strings.TrimSpace(s.Phase))
	mode := strings.ToLower(strings.TrimSpace(s.Mode))
	if s.Paused && phase != "map_end" && phase != "series_end" {
		return "paused"
	}
	switch phase {
	case "", "none", "idle":
		if mode != "" && mode != "idle" && mode != "match" {
			return mode // scrim, practice
		}
		return "idle"
	case "live":
		if s.Round > 0 {
			return fmt.Sprintf("live R%d", s.Round)
		}
		return "live"
	case "overtime":
		if s.Round > 0 {
			return fmt.Sprintf("live R%d OT", s.Round)
		}
		return "live OT"
	case "map_end", "series_end":
		return "postgame"
	case "side_pick":
		return "side pick"
	}
	return phase
}

func platformCell(p ReadyUpPlatform, now time.Time) cell {
	mode := strings.ToLower(p.Mode)
	state := strings.ToLower(p.State)
	if mode == "standalone" || (mode == "" && state == "") {
		return cell{"standalone", toneDim}
	}
	switch state {
	case "online":
		return cell{"online", toneGood}
	case "offline":
		text := "offline"
		if p.Since > 0 {
			since := p.Since
			if since > 1e12 { // milliseconds
				since /= 1000
			}
			if d := now.Sub(time.Unix(since, 0)); d > 0 {
				text += " " + shortDuration(d)
			}
		}
		if p.AutoPauseInS != nil && *p.AutoPauseInS > 0 {
			text += fmt.Sprintf(", pause in %ds", *p.AutoPauseInS)
		}
		return cell{text, toneBad}
	case "":
		return cell{"?", toneDim}
	}
	return cell{state, toneWarn} // enrolling, rejected
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}
