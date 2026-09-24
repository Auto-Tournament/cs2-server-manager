package csm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGateDisruptive(t *testing.T) {
	logs := t.TempDir()
	t.Setenv("CSM_LOG_DIR", logs)

	live := newFakeReadyUp(t, liveStatus())
	idle := newFakeReadyUp(t, idleStatus())
	liveDir, idleDir := t.TempDir(), t.TempDir()
	writeDiscovery(t, liveDir, map[string]any{"port": live.port()})
	busy := FleetTarget{Server: 1, Dir: liveDir, GamePort: 27015, Running: true}
	free := FleetTarget{Server: 2, Dir: idleDir, GamePort: idle.port() - ReadyUpPortOffset, Running: true}
	noRU := FleetTarget{Server: 3, Dir: t.TempDir(), GamePort: freePort(t) - ReadyUpPortOffset, Running: true}
	stopped := FleetTarget{Server: 4, Dir: liveDir, GamePort: 27045, Running: false}
	ctx := context.Background()

	// Idle Ready Up, no Ready Up at all, and a stopped server never block.
	for _, targets := range [][]FleetTarget{{free}, {noRU}, {stopped}, {free, noRU, stopped}} {
		if err := GateDisruptive(ctx, "restart", targets, false, nil); err != nil {
			t.Fatalf("targets %+v: %v", targets, err)
		}
	}

	// A live match blocks, and says where and what.
	err := GateDisruptive(ctx, "update-game", []FleetTarget{free, busy, noRU}, false, nil)
	var mip *MatchInProgressError
	if !errors.As(err, &mip) {
		t.Fatalf("live match: err = %v, want *MatchInProgressError", err)
	}
	if len(mip.Blocked) != 1 || mip.Blocked[0].Target.Server != 1 {
		t.Fatalf("blocked %+v, want only server-1", mip.Blocked)
	}
	msg := err.Error()
	for _, want := range []string{"update-game refused", "server-1", "live R14", "de_mirage", "8-5", "412 NAVI vs G2", "--force"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	t.Logf("refusal:\n%s", msg)

	// Two busy servers are listed one per line.
	live2Dir := t.TempDir()
	writeDiscovery(t, live2Dir, map[string]any{"port": live.port()})
	err = GateDisruptive(ctx, "stop", []FleetTarget{busy, {Server: 5, Dir: live2Dir, GamePort: 27055, Running: true}}, false, nil)
	if err == nil || !strings.Contains(err.Error(), "matches are in progress on 2 servers") || !strings.Contains(err.Error(), "server-5") {
		t.Fatalf("two busy servers: %v", err)
	}

	// --force goes ahead, warns, and leaves a trace in csm.log.
	var out bytes.Buffer
	if err := GateDisruptive(ctx, "restart server-1", []FleetTarget{busy}, true, &out); err != nil {
		t.Fatalf("forced: %v", err)
	}
	if !strings.Contains(out.String(), "--force: running restart server-1 although a match is in progress on server-1") {
		t.Fatalf("forced warning: %q", out.String())
	}
	logged, _ := os.ReadFile(filepath.Join(logs, "csm.log"))
	if !strings.Contains(string(logged), "restart server-1 (forced during match)") || !strings.Contains(string(logged), "update-game (refused: match in progress)") {
		t.Fatalf("csm.log does not record the refusal and the override:\n%s", logged)
	}
}

func TestUpdateSafeVerdict(t *testing.T) {
	yes, no := true, false
	for _, tt := range []struct {
		row         FleetRow
		safe, known bool
	}{
		{FleetRow{State: ReadyUpOK, Status: &ReadyUpStatus{UpdateSafe: &no}}, false, true},
		{FleetRow{State: ReadyUpOK, Status: &ReadyUpStatus{UpdateSafe: &yes}}, true, true},
		{FleetRow{State: ReadyUpOK, Status: &ReadyUpStatus{}}, true, false}, // older Ready Up without the field
		{FleetRow{State: ReadyUpNone}, true, false},
		{FleetRow{State: ReadyUpNoResponse}, true, false},
		{FleetRow{State: ReadyUpStopped}, true, false},
	} {
		safe, known := tt.row.UpdateSafe()
		if safe != tt.safe || known != tt.known {
			t.Errorf("%s: %v,%v want %v,%v", tt.row.State, safe, known, tt.safe, tt.known)
		}
	}
}
