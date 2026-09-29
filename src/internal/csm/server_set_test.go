package csm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerSetInstances(t *testing.T) {
	m := testInstanceManager(t)
	for _, n := range []int{1, 3} {
		writeInstFile(t, m.L.StateFile(n), `{"number":1}`)
	}
	s := &ServerSet{Instances: m}
	if !s.IsInstances() || s.Noun() != "instance" || s.Count() != 2 {
		t.Fatalf("set = %v %s %d", s.IsInstances(), s.Noun(), s.Count())
	}
	if err := s.Check(3); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(2); err == nil || !strings.Contains(err.Error(), "instance 2 does not exist") {
		t.Fatalf("missing instance: %v", err)
	}
	if got := s.pick(0); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("all = %v", got)
	}
	if !strings.Contains(s.Header(), "2 instance(s)") {
		t.Fatalf("header = %q", s.Header())
	}
	if !strings.Contains(s.Notes(), "No Ready Up layer yet") {
		t.Fatalf("notes = %q", s.Notes())
	}
	log := m.L.ConsoleLog(3)
	if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := s.Logs(3, 2); err != nil || out != "b\nc\n" {
		t.Fatalf("logs = %q %v", out, err)
	}
	if _, err := s.Logs(2, 2); err == nil {
		t.Fatal("logs of a missing instance")
	}

	servers := &ServerSet{Tmux: &TmuxManager{CS2User: "cs2", NumServers: 2}}
	if servers.Noun() != "server" || servers.Count() != 2 || servers.Check(3) == nil || servers.Notes() != "" {
		t.Fatal("server-N set")
	}
}
