package csm

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Holding disruptive commands while a match is live
//
// Ready Up's /status carries `update_safe`: false from `loading` to
// `series_end` and while a demo upload is pending (FLEET.md §17.2). csm's
// update-game, update-plugins, restart and stop refuse to touch a server that
// says false. --force goes ahead anyway and is logged.
//
// Only an explicit false blocks. A server without Ready Up, a Ready Up that
// does not answer, or one without the field keeps csm's behaviour from before
// Ready Up: the command runs.

// MatchInProgressError is returned when servers are busy and force is off.
type MatchInProgressError struct {
	Action  string
	Blocked []FleetRow
}

func (e *MatchInProgressError) Error() string {
	var b strings.Builder
	if len(e.Blocked) == 1 {
		fmt.Fprintf(&b, "%s refused: a match is in progress on %s", e.Action, describeBusyServer(e.Blocked[0]))
	} else {
		fmt.Fprintf(&b, "%s refused: matches are in progress on %d servers:", e.Action, len(e.Blocked))
		for _, r := range e.Blocked {
			b.WriteString("\n  - ")
			b.WriteString(describeBusyServer(r))
		}
	}
	b.WriteString("\nReady Up reports update_safe=false. Wait for the match to end, or run the command again with --force to go ahead anyway (this is logged).")
	return b.String()
}

// describeBusyServer: "server-2 (live R14, de_mirage, 8-5, 412 NAVI vs G2)".
func describeBusyServer(r FleetRow) string {
	name := fmt.Sprintf("server-%d", r.Target.Server)
	if r.Status == nil {
		return name
	}
	s := r.Status.Summary
	parts := []string{PhaseLabel(s)}
	if s.Map != "" {
		parts = append(parts, s.Map)
	}
	if PhaseLabel(s) != "idle" {
		parts = append(parts, fmt.Sprintf("%d-%d", s.Score.Team1, s.Score.Team2))
	}
	match := strings.TrimSpace(string(s.MatchID))
	if s.Teams != nil && (s.Teams.Team1 != "" || s.Teams.Team2 != "") {
		match = strings.TrimSpace(match + " " + s.Teams.Team1 + " vs " + s.Teams.Team2)
	}
	if match != "" {
		parts = append(parts, match)
	}
	return name + " (" + strings.Join(parts, ", ") + ")"
}

// BusyServers returns the rows whose Ready Up says update_safe=false.
func BusyServers(rows []FleetRow) []FleetRow {
	var busy []FleetRow
	for _, r := range rows {
		if safe, known := r.UpdateSafe(); known && !safe {
			busy = append(busy, r)
		}
	}
	return busy
}

// GateDisruptive checks targets before a disruptive action. It returns a
// *MatchInProgressError when any of them is busy and force is false. With
// force it writes a warning to w, logs the override and returns nil.
func GateDisruptive(ctx context.Context, action string, targets []FleetTarget, force bool, w io.Writer) error {
	busy := BusyServers(ProbeFleet(ctx, targets))
	return gateRows(action, busy, force, w)
}

func gateRows(action string, busy []FleetRow, force bool, w io.Writer) error {
	if len(busy) == 0 {
		return nil
	}
	gateErr := &MatchInProgressError{Action: action, Blocked: busy}
	if !force {
		LogAction("gate", action+" (refused: match in progress)", gateErr.Error(), nil)
		return gateErr
	}
	names := make([]string, 0, len(busy))
	for _, r := range busy {
		names = append(names, describeBusyServer(r))
	}
	msg := fmt.Sprintf("--force: running %s although a match is in progress on %s", action, strings.Join(names, "; "))
	if w != nil {
		fmt.Fprintf(w, "[!] %s\n", msg)
	}
	LogWarn("forced disruptive action during a live match", "action", action, "servers", strings.Join(names, "; "))
	LogAction("gate", action+" (forced during match)", msg, nil)
	return nil
}

// GateServers is GateDisruptive for server numbers of this install: all of
// them when servers is empty.
func (m *TmuxManager) GateServers(ctx context.Context, action string, servers []int, force bool, w io.Writer) error {
	var targets []FleetTarget
	if len(servers) == 0 {
		targets = m.FleetTargets()
	} else {
		for _, n := range servers {
			targets = append(targets, m.fleetTarget(n))
		}
	}
	return GateDisruptive(ctx, action, targets, force, w)
}
