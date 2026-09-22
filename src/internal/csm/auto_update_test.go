package csm

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDecideAutoUpdate(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	grace := 10 * time.Minute
	idle := serverIdleProbe{Reachable: true, PlayersKnown: true, HumanPlayers: 0, MatchState: "none"}
	with := func(f func(*serverIdleProbe)) serverIdleProbe { p := idle; f(&p); return p }

	tests := []struct {
		name      string
		probe     serverIdleProbe
		hold      bool
		idleSince time.Time
		wantIdle  bool
		wantUpd   bool
		wantSince time.Time
		reason    string
	}{
		{name: "idle past grace updates", probe: idle, idleSince: now.Add(-11 * time.Minute), wantIdle: true, wantUpd: true, wantSince: now.Add(-11 * time.Minute)},
		{name: "idle exactly grace updates", probe: idle, idleSince: now.Add(-grace), wantIdle: true, wantUpd: true, wantSince: now.Add(-grace)},
		{name: "first time idle starts the clock", probe: idle, wantIdle: true, wantSince: now, reason: "grace period"},
		{name: "idle within grace waits", probe: idle, idleSince: now.Add(-5 * time.Minute), wantIdle: true, wantSince: now.Add(-5 * time.Minute), reason: "grace period"},
		{name: "idle since in the future resets", probe: idle, idleSince: now.Add(time.Hour), wantIdle: true, wantSince: now},
		{name: "players connected", probe: with(func(p *serverIdleProbe) { p.HumanPlayers = 2 }), idleSince: now.Add(-time.Hour), reason: "2 player(s) connected"},
		{name: "one spectator counts as a player", probe: with(func(p *serverIdleProbe) { p.HumanPlayers = 1 }), idleSince: now.Add(-time.Hour), reason: "1 player(s)"},
		{name: "match loaded in warmup", probe: with(func(p *serverIdleProbe) { p.MatchState = "warmup" }), idleSince: now.Add(-time.Hour), reason: "match loaded (gamestate warmup)"},
		{name: "match live", probe: with(func(p *serverIdleProbe) { p.MatchState = "live" }), reason: "match loaded"},
		{name: "post game still loaded", probe: with(func(p *serverIdleProbe) { p.MatchState = "post_game" }), reason: "match loaded"},
		{name: "match state unknown is not idle", probe: with(func(p *serverIdleProbe) { p.MatchState = "" }), idleSince: now.Add(-time.Hour), reason: "match state unknown"},
		{name: "player count unknown is not idle", probe: with(func(p *serverIdleProbe) { p.PlayersKnown = false }), reason: "player count"},
		{name: "rcon unreachable is not idle", probe: serverIdleProbe{}, idleSince: now.Add(-time.Hour), reason: "RCON unreachable"},
		{name: "hold blocks idle server", probe: idle, hold: true, idleSince: now.Add(-time.Hour), wantIdle: true, wantSince: now.Add(-time.Hour), reason: "on hold"},
		{name: "hold with players", probe: with(func(p *serverIdleProbe) { p.HumanPlayers = 3 }), hold: true, reason: "on hold"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := decideAutoUpdate(tt.probe, tt.hold, tt.idleSince, now, grace)
			if d.Idle != tt.wantIdle || d.Update != tt.wantUpd {
				t.Fatalf("idle=%v update=%v, want idle=%v update=%v (%s)", d.Idle, d.Update, tt.wantIdle, tt.wantUpd, d.Reason)
			}
			if !d.IdleSince.Equal(tt.wantSince) {
				t.Fatalf("IdleSince = %v, want %v", d.IdleSince, tt.wantSince)
			}
			if tt.reason != "" && !strings.Contains(d.Reason, tt.reason) {
				t.Fatalf("reason %q does not mention %q", d.Reason, tt.reason)
			}
			if tt.hold && d.Update {
				t.Fatal("never update while on hold")
			}
		})
	}
}

func TestParseStatusHumans(t *testing.T) {
	summary := `hostname  : CS2 Server #1
version   : 1.40.9.1/14091 10471 secure  public
os/type   : Linux dedicated
players   : 1 humans, 1 bots (10 max) (not hibernating) (unreserved)`
	table := `---------players--------
  id     time ping loss      state   rate adr name
65535 [NoChan]    0    0 challenging      0unknown ''
    1      BOT    0    0     active      0 'CSTV'
    2    00:13   32    0     active 786432 10.0.0.5:27005 'alice'
    3    01:02   40    0     active 786432 10.0.0.6:27005 'bob'
#end`
	emptyTable := `---------players--------
  id     time ping loss      state   rate adr name
65535 [NoChan]    0    0 challenging      0unknown ''
    1      BOT    0    0     active      0 'CSTV'
#end`
	tests := []struct {
		name   string
		out    string
		want   int
		wantOK bool
	}{
		{"summary line", summary, 1, true},
		{"summary with zero", "players  : 0 humans, 0 bots (10 max)", 0, true},
		{"table fallback", table, 2, true},
		{"table with only GOTV and placeholder", emptyTable, 0, true},
		{"unparsable", "Unknown command 'status'", 0, false},
		{"empty", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseStatusHumans(tt.out)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("parseStatusHumans = %d, %v; want %d, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestParseGet5GameState(t *testing.T) {
	tests := []struct{ out, want string }{
		{`{"plugin_version":"0.15.0","gamestate":"none","paused":false}`, "none"},
		{"L 09/22/2026 - 12:00:00: {\"gamestate\":\"live\",\"matchid\":42}\n", "live"},
		{`{"gamestate":"WARMUP"}`, "warmup"},
		{`Unknown command "get5_status"`, ""},
		{`{"plugin_version":"0.15.0"}`, ""},
		{`{broken`, ""},
	}
	for _, tt := range tests {
		if got := parseGet5GameState(tt.out); got != tt.want {
			t.Errorf("parseGet5GameState(%q) = %q, want %q", tt.out, got, tt.want)
		}
	}
}

func TestUpdateHoldPersistence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CSM_ROOT", root)

	s, err := LoadAutoUpdateSettings()
	if err != nil || s.Hold || s.IdleGrace() != DefaultIdleGrace {
		t.Fatalf("defaults = %+v, %v", s, err)
	}

	if _, err := SetUpdateHold(true); err != nil {
		t.Fatal(err)
	}
	if s, _ = LoadAutoUpdateSettings(); !s.Hold || s.HoldChangedAt == "" {
		t.Fatalf("hold on not persisted: %+v", s)
	}

	if _, err := SetIdleGraceMinutes(15); err != nil {
		t.Fatal(err)
	}
	if s, _ = LoadAutoUpdateSettings(); !s.Hold || s.IdleGrace() != 15*time.Minute {
		t.Fatalf("grace change lost the hold or grace: %+v", s)
	}

	if _, err := SetUpdateHold(false); err != nil {
		t.Fatal(err)
	}
	if s, _ = LoadAutoUpdateSettings(); s.Hold || s.IdleGrace() != 15*time.Minute {
		t.Fatalf("hold off not persisted or grace lost: %+v", s)
	}

	if _, err := SetIdleGraceMinutes(0); err == nil {
		t.Fatal("grace 0 must be rejected")
	}
	if _, err := os.Stat(filepath.Join(root, "auto-update.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}

	// A corrupt file is an error; the monitor then treats updates as held.
	if err := os.WriteFile(filepath.Join(root, "auto-update.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAutoUpdateSettings(); err == nil {
		t.Fatal("corrupt settings must return an error")
	}
}

func TestAutoUpdateStateRoundTrip(t *testing.T) {
	t.Setenv("CSM_ROOT", t.TempDir())
	st := loadAutoUpdateState()
	st.server(2).IdleSince = 123
	st.server(2).LogOffset = 456
	if err := st.save(); err != nil {
		t.Fatal(err)
	}
	got := loadAutoUpdateState().server(2)
	if got.IdleSince != 123 || got.LogOffset != 456 {
		t.Fatalf("state round trip = %+v", got)
	}
}

func TestLogHasMarkerAfter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	old := strings.Repeat("noise\n", 20000) // > 64 KB
	if err := os.WriteFile(path, []byte(matchzyUpdateAvailableMarker+"1\n"+old), 0o644); err != nil {
		t.Fatal(err)
	}

	// No state yet: only the tail is checked, so the ancient marker is ignored.
	m, size, err := logHasMarkerAfter(path, 0, matchzyUpdateAvailableMarker)
	if err != nil || m != "" {
		t.Fatalf("first scan found %q (err %v); old markers beyond the tail must not count", m, err)
	}

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("L 12:00: " + matchzyUpdateAvailableMarker + "2\n")
	_ = f.Close()

	if m, _, _ := logHasMarkerAfter(path, size, matchzyUpdateAvailableMarker); m == "" {
		t.Fatal("marker after the offset not found")
	}
	_, size2, _ := logHasMarkerAfter(path, size, matchzyUpdateAvailableMarker)
	if m, _, _ := logHasMarkerAfter(path, size2, matchzyUpdateAvailableMarker); m != "" {
		t.Fatal("handled marker found again")
	}

	// Log rotated (smaller than the offset): scan it all.
	if err := os.WriteFile(path, []byte(autoUpdaterShutdownMarker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, _, _ := logHasMarkerAfter(path, size2, matchzyUpdateAvailableMarker, autoUpdaterShutdownMarker); m != autoUpdaterShutdownMarker {
		t.Fatalf("rotated log: got %q", m)
	}
}

// fakeRCONServer answers one connection with the given password and
// command outputs. Long outputs are split over several packets.
func fakeRCONServer(t *testing.T, password string, replies map[string]string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					id, typ, body, err := rconReadPacket(c)
					if err != nil {
						return
					}
					switch typ {
					case rconTypeAuth:
						_ = rconWritePacket(c, id, rconTypeResponse, "")
						if body == password {
							_ = rconWritePacket(c, id, rconTypeAuthResponse, "")
						} else {
							_ = rconWritePacket(c, -1, rconTypeAuthResponse, "")
						}
					case rconTypeExec:
						out := replies[body]
						for len(out) > 1000 {
							_ = rconWritePacket(c, id, rconTypeResponse, out[:1000])
							out = out[1000:]
						}
						_ = rconWritePacket(c, id, rconTypeResponse, out)
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func TestRCONClientAndProbe(t *testing.T) {
	long := "players   : 0 humans, 1 bots (10 max)\n" + strings.Repeat("x", 2500)
	addr := fakeRCONServer(t, "s3cret", map[string]string{
		"status":      long,
		"get5_status": `{"plugin_version":"0.15.0","gamestate":"none","paused":false}`,
	})

	if _, err := rconDial(addr, "wrong", time.Second); !errors.Is(err, ErrRCONAuth) {
		t.Fatalf("wrong password: err = %v, want ErrRCONAuth", err)
	}

	c, err := rconDial(addr, "s3cret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Exec("status")
	_ = c.Close()
	if err != nil || out != long {
		t.Fatalf("multi-packet exec: len %d (want %d), err %v", len(out), len(long), err)
	}

	p := probeServerIdle(addr, "s3cret")
	want := serverIdleProbe{Reachable: true, PlayersKnown: true, HumanPlayers: 0, MatchState: "none"}
	if p != want {
		t.Fatalf("probe = %+v, want %+v", p, want)
	}
	if p := probeServerIdle(addr, "wrong"); p.Reachable {
		t.Fatal("wrong password must not count as reachable")
	}
	if p := probeServerIdle(addr, ""); p.Reachable {
		t.Fatal("no password must not probe")
	}
}
