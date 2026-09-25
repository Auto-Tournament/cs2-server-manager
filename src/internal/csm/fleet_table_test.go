package csm

import (
	"strings"
	"testing"
	"time"
)

func statusOf(t *testing.T, doc map[string]any) *ReadyUpStatus {
	t.Helper()
	st, err := statusFromDoc(doc)
	must(t, err)
	return st
}

// fleetFixture is one of every kind of row.
func fleetFixture(t *testing.T) []FleetRow {
	live := statusOf(t, liveStatus())

	idle := statusOf(t, idleStatus())

	offlineDoc := liveStatus()
	offlineDoc["platform"] = map[string]any{"mode": "fleet", "state": "offline", "since": 1790339820000, "auto_pause_in_s": 142}
	s := offlineDoc["summary"].(map[string]any)
	s["phase"], s["map"], s["num_maps"], s["map_number"], s["round"] = "warmup", "de_nuke", 1, 1, 0
	s["score"] = map[string]any{"team1": 0, "team2": 0}
	s["players"] = map[string]any{"connected": 7, "expected": 10}
	s["match_id"], s["teams"] = "413", map[string]any{"team1": "Heroic Esports International", "team2": "Team Vitality"}
	offlineDoc["selftest"] = map[string]any{"pass": false, "passed": 10, "total": 12, "failures": []any{"sig:CCSGameRules", "sig:RoundEnd"}}
	offline := statusOf(t, offlineDoc)

	return []FleetRow{
		{Target: FleetTarget{Server: 1, GamePort: 27015, Running: true}, StatusPort: 27022, State: ReadyUpOK, Status: live},
		{Target: FleetTarget{Server: 2, GamePort: 27025, Running: true}, StatusPort: 27032, State: ReadyUpOK, Status: idle},
		{Target: FleetTarget{Server: 3, GamePort: 27035, Running: true}, StatusPort: 27042, State: ReadyUpNone},
		{Target: FleetTarget{Server: 4, GamePort: 27045, Running: true}, StatusPort: 27052, State: ReadyUpOK, Status: offline},
		{Target: FleetTarget{Server: 5, GamePort: 27055, Running: true}, StatusPort: 27062, State: ReadyUpNoResponse, Err: "context deadline exceeded"},
		{Target: FleetTarget{Server: 6, GamePort: 27065, Running: false}, StatusPort: 27072, State: ReadyUpStopped},
		{Target: FleetTarget{Server: 7, GamePort: 27075, Running: false, Updating: true}, StatusPort: 27082, State: ReadyUpStopped},
	}
}

func TestRenderFleetTable(t *testing.T) {
	now := time.Unix(1790340000, 0)
	got := RenderFleetTable(fleetFixture(t), FleetTableOptions{Now: now})
	t.Logf("fleet table:\n%s", got)

	want := `#  PORT   PROC      MAP            PHASE        SCORE      PLAYERS  MATCH                           PLATFORM                   READY UP  CS2    SAFE
1  27015  running   de_mirage 2/3  live R14     8-5 (1-0)  10/10    412 NAVI vs G2                  online                     0.9.0     14090  NO
2  27025  running   de_dust2       idle         -          2        -                               standalone                 0.9.0     14090  yes
3  27035  running   -              no Ready Up  -          -        -                               -                          -         -      -
4  27045  running   de_nuke        warmup       0-0        7/10     413 Heroic Esports Internatio…  offline 3m, pause in 142s  0.9.0     14090  NO
5  27055  running   -              no answer    -          -        -                               -                          -         -      -
6  27065  stopped   -              -            -          -        -                               -                          -         -      -
7  27075  updating  -              -            -          -        -                               -                          -         -      -

  server-4: Ready Up selftest failed (10/12 passed): sig:CCSGameRules, sig:RoundEnd
  server-5: Ready Up is not answering on port 27062 (context deadline exceeded)
`
	if got != want {
		t.Fatalf("table mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	colored := RenderFleetTable(fleetFixture(t), FleetTableOptions{Now: now, Color: true})
	if !strings.Contains(colored, "\x1b[31mNO\x1b[0m") || !strings.Contains(colored, "\x1b[32mlive R14\x1b[0m") {
		t.Fatalf("colours missing:\n%q", colored)
	}
	// Colour must not change alignment: strip it and compare.
	if stripANSI(colored) != got {
		t.Fatalf("coloured table is not the plain table plus colour codes:\n%s", stripANSI(colored))
	}

	if out := RenderFleetTable(nil, FleetTableOptions{}); !strings.Contains(out, "No CS2 servers") {
		t.Fatalf("empty fleet: %q", out)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestPhaseLabel(t *testing.T) {
	for _, tt := range []struct {
		s    ReadyUpSummary
		want string
	}{
		{ReadyUpSummary{Mode: "idle"}, "idle"},
		{ReadyUpSummary{}, "idle"},
		{ReadyUpSummary{Mode: "practice"}, "practice"},
		{ReadyUpSummary{Mode: "match", Phase: "loading"}, "loading"},
		{ReadyUpSummary{Mode: "match", Phase: "warmup"}, "warmup"},
		{ReadyUpSummary{Mode: "match", Phase: "knife"}, "knife"},
		{ReadyUpSummary{Mode: "match", Phase: "side_pick"}, "side pick"},
		{ReadyUpSummary{Mode: "match", Phase: "live", Round: 14}, "live R14"},
		{ReadyUpSummary{Mode: "match", Phase: "live"}, "live"},
		{ReadyUpSummary{Mode: "match", Phase: "overtime", Round: 25}, "live R25 OT"},
		{ReadyUpSummary{Mode: "match", Phase: "live", Round: 9, Paused: true}, "paused"},
		{ReadyUpSummary{Mode: "match", Phase: "paused"}, "paused"},
		{ReadyUpSummary{Mode: "match", Phase: "halftime"}, "halftime"},
		{ReadyUpSummary{Mode: "match", Phase: "map_end"}, "postgame"},
		{ReadyUpSummary{Mode: "match", Phase: "series_end", Paused: true}, "postgame"},
	} {
		if got := PhaseLabel(tt.s); got != tt.want {
			t.Errorf("%+v: %q, want %q", tt.s, got, tt.want)
		}
	}
}
