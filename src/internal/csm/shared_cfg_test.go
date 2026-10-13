package csm

import (
	"strings"
	"testing"
)

// The shared server.cfg is copied over each server's: it must turn the game log on, or Ready Up
// never sees players' chat (.r).
func TestSharedServerConfigTurnsTheGameLogOn(t *testing.T) {
	got := strings.Join(sharedServerConfigLines("", "pw", 12), "\n")
	for _, want := range []string{`rcon_password "pw"`, "maxplayers 12", "\nlog on\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("shared cfg missing %q:\n%s", want, got)
		}
	}
}

func TestSharedServerConfigKeepsTheRestAndAddsLogOnOnce(t *testing.T) {
	existing := "rcon_password \"old\"\nmaxplayers 10\nsv_cheats 0\nlog on\n"
	got := strings.Join(sharedServerConfigLines(existing, "new", 15), "\n")
	if strings.Count(got, "log on") != 1 {
		t.Fatalf("log on should appear once:\n%s", got)
	}
	if strings.Contains(got, `"old"`) || strings.Contains(got, "maxplayers 10") {
		t.Fatalf("old rcon/maxplayers kept:\n%s", got)
	}
	if !strings.Contains(got, "sv_cheats 0") || !strings.Contains(got, `rcon_password "new"`) {
		t.Fatalf("other settings or new rcon lost:\n%s", got)
	}
}
