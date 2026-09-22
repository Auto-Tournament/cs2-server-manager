package csm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Smart auto-update
//
// The plugin's default auto-update mode is warn_only: when Valve ships a CS2
// update it logs [MATCHZY_UPDATE_AVAILABLE] but keeps the server running, so
// a monitor that only updates stopped servers never updates anything. The
// monitor now updates running servers itself, but only when a server is
// idle:
//
//   - no human players connected (RCON `status`),
//   - no match loaded (the plugin's `get5_status` reports gamestate "none";
//     an unreadable answer counts as "match loaded"),
//   - both have held for the idle grace period (default 10 minutes).
//
// During an event the host can put updates on hold (`csm updates hold on`).
// While on hold the monitor only reports that an update is available.
// Manual `csm update-game` / `csm update-server` ignore the hold.

// DefaultIdleGrace is how long a server must stay idle before the monitor
// updates it.
const DefaultIdleGrace = 10 * time.Minute

// AutoUpdateSettings are the host's persisted auto-update choices.
type AutoUpdateSettings struct {
	// Hold stops the monitor from restarting any server for an update.
	Hold bool `json:"hold"`
	// HoldChangedAt records when Hold was last changed (RFC3339).
	HoldChangedAt string `json:"hold_changed_at,omitempty"`
	// IdleGraceMinutes overrides DefaultIdleGrace when > 0.
	IdleGraceMinutes int `json:"idle_grace_minutes,omitempty"`
}

// IdleGrace returns the configured grace period.
func (s AutoUpdateSettings) IdleGrace() time.Duration {
	if s.IdleGraceMinutes > 0 {
		return time.Duration(s.IdleGraceMinutes) * time.Minute
	}
	return DefaultIdleGrace
}

// autoUpdateSettingsPath is where the settings live: <csm root>/auto-update.json.
func autoUpdateSettingsPath() string {
	return filepath.Join(ResolveRoot(), "auto-update.json")
}

// LoadAutoUpdateSettings reads the settings. A missing file means defaults
// (no hold, 10 minute grace).
func LoadAutoUpdateSettings() (AutoUpdateSettings, error) {
	var s AutoUpdateSettings
	data, err := os.ReadFile(autoUpdateSettingsPath())
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parse %s: %w", autoUpdateSettingsPath(), err)
	}
	return s, nil
}

// SaveAutoUpdateSettings writes the settings atomically.
func SaveAutoUpdateSettings(s AutoUpdateSettings) error {
	return writeJSONAtomic(autoUpdateSettingsPath(), s)
}

// SetUpdateHold turns the update hold on or off and persists it.
func SetUpdateHold(on bool) (AutoUpdateSettings, error) {
	s, err := LoadAutoUpdateSettings()
	if err != nil {
		return s, err
	}
	s.Hold = on
	s.HoldChangedAt = time.Now().UTC().Format(time.RFC3339)
	return s, SaveAutoUpdateSettings(s)
}

// SetIdleGraceMinutes sets the idle grace period (minutes, >= 1).
func SetIdleGraceMinutes(minutes int) (AutoUpdateSettings, error) {
	if minutes < 1 {
		return AutoUpdateSettings{}, fmt.Errorf("grace must be at least 1 minute")
	}
	s, err := LoadAutoUpdateSettings()
	if err != nil {
		return s, err
	}
	s.IdleGraceMinutes = minutes
	return s, SaveAutoUpdateSettings(s)
}

func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// serverUpdateState is what the monitor remembers per server between runs.
type serverUpdateState struct {
	// IdleSince is when the server was first seen idle (unix seconds), 0 when
	// it is not idle.
	IdleSince int64 `json:"idle_since,omitempty"`
	// LogOffset is how far into the server log update markers have been
	// handled; only markers after it count.
	LogOffset int64 `json:"log_offset,omitempty"`
	// LastUpdate is when the monitor last updated this server (unix seconds).
	LastUpdate int64 `json:"last_update,omitempty"`
}

type autoUpdateState struct {
	Servers map[string]*serverUpdateState `json:"servers"`
}

func autoUpdateStatePath() string {
	return filepath.Join(ResolveRoot(), "auto-update-state.json")
}

func loadAutoUpdateState() autoUpdateState {
	st := autoUpdateState{Servers: map[string]*serverUpdateState{}}
	if data, err := os.ReadFile(autoUpdateStatePath()); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	if st.Servers == nil {
		st.Servers = map[string]*serverUpdateState{}
	}
	return st
}

func (st autoUpdateState) server(n int) *serverUpdateState {
	key := strconv.Itoa(n)
	s, ok := st.Servers[key]
	if !ok || s == nil {
		s = &serverUpdateState{}
		st.Servers[key] = s
	}
	return s
}

func (st autoUpdateState) save() error {
	return writeJSONAtomic(autoUpdateStatePath(), st)
}

// serverIdleProbe is what the monitor learned from a running server.
type serverIdleProbe struct {
	// Reachable is false when RCON could not connect or authenticate.
	Reachable bool
	// PlayersKnown is false when the `status` output could not be parsed.
	PlayersKnown bool
	HumanPlayers int
	// MatchState is the plugin's get5 gamestate ("none" when no match is
	// loaded). Empty means unknown.
	MatchState string
}

// autoUpdateDecision is the monitor's verdict for one running server.
type autoUpdateDecision struct {
	Idle   bool
	Update bool
	Reason string
	// IdleSince is the new idle-since value to remember (zero when not idle).
	IdleSince time.Time
}

// decideAutoUpdate decides whether a running server with a pending update
// may be restarted now. It is pure so it can be table-tested.
func decideAutoUpdate(p serverIdleProbe, hold bool, idleSince, now time.Time, grace time.Duration) autoUpdateDecision {
	notIdle := func(reason string) autoUpdateDecision {
		if hold {
			reason += "; updates are on hold"
		}
		return autoUpdateDecision{Reason: reason}
	}
	switch {
	case !p.Reachable:
		return notIdle("RCON unreachable, cannot confirm the server is idle")
	case !p.PlayersKnown:
		return notIdle("could not read the player count from `status`")
	case p.HumanPlayers > 0:
		return notIdle(fmt.Sprintf("%d player(s) connected", p.HumanPlayers))
	case p.MatchState == "":
		return notIdle("match state unknown (no answer from get5_status), treating as match loaded")
	case p.MatchState != "none":
		return notIdle(fmt.Sprintf("match loaded (gamestate %s)", p.MatchState))
	}

	if idleSince.IsZero() || idleSince.After(now) {
		idleSince = now
	}
	d := autoUpdateDecision{Idle: true, IdleSince: idleSince}
	idleFor := now.Sub(idleSince)
	switch {
	case hold:
		d.Reason = "idle, but updates are on hold (csm updates hold off to allow)"
	case idleFor < grace:
		d.Reason = fmt.Sprintf("idle for %s, waiting for the %s grace period", idleFor.Round(time.Second), grace)
	default:
		d.Update = true
		d.Reason = fmt.Sprintf("idle for %s (no players, no match loaded)", idleFor.Round(time.Second))
	}
	return d
}

var (
	// CS2: "players  : 0 humans, 1 bots (10 max) ..."
	statusHumansRe = regexp.MustCompile(`(?m)^\s*players\s*:\s*(\d+)\s+humans?`)
	// Player table rows: "  2    00:13   32    0     active 786432 1.2.3.4:27005 'name'"
	statusRowRe = regexp.MustCompile(`^\s*(\d+)\s+(\S+)\s+`)
)

// parseStatusHumans returns the number of human players in CS2 `status`
// output. It prefers the "players : N humans" summary and otherwise counts
// rows in the player table, skipping bots (GOTV included) and the
// placeholder 65535 slot.
func parseStatusHumans(out string) (int, bool) {
	if m := statusHumansRe.FindStringSubmatch(out); m != nil {
		n, err := strconv.Atoi(m[1])
		return n, err == nil
	}
	lines := strings.Split(out, "\n")
	inTable := false
	humans := 0
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if strings.Contains(l, "---------players--------") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if l == "#end" || strings.HasPrefix(l, "#end") {
			return humans, true
		}
		if strings.HasPrefix(l, "id ") || l == "" {
			continue
		}
		m := statusRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if m[1] == "65535" || strings.EqualFold(m[2], "BOT") || strings.Contains(l, " BOT ") {
			continue
		}
		humans++
	}
	if inTable {
		return humans, true
	}
	return 0, false
}

// parseGet5GameState pulls "gamestate" out of the plugin's get5_status JSON
// reply. It returns "" when the reply has no parsable JSON object.
func parseGet5GameState(out string) string {
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end <= start {
		return ""
	}
	var v struct {
		GameState *string `json:"gamestate"`
	}
	if err := json.Unmarshal([]byte(out[start:end+1]), &v); err != nil || v.GameState == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(*v.GameState))
}
