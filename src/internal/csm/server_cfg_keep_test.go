package csm

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRCONPassword(t *testing.T) {
	tests := []struct {
		name string
		cfg  string
		want string
	}{
		{"none", "hostname \"x\"\n", ""},
		{"quoted", `rcon_password "secret"`, "secret"},
		{"unquoted", "rcon_password secret", "secret"},
		{"quoted with spaces", `rcon_password "my secret pass"`, "my secret pass"},
		{"trailing comment", "rcon_password secret // set by host", "secret"},
		{"commented out", "// rcon_password old\nrcon_password new", "new"},
		{"last one wins", "rcon_password \"first\"\nsv_lan 0\nrcon_password \"second\"", "second"},
		{"empty value ignored", "rcon_password \"kept\"\nrcon_password \"\"", "kept"},
		{"other cvar", "rcon_password_extra foo", ""},
		{"tab separated", "rcon_password\tsecret", "secret"},
		{"crlf", "rcon_password \"win\"\r\n", "win"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseRCONPassword(tt.cfg); got != tt.want {
				t.Fatalf("parseRCONPassword(%q) = %q, want %q", tt.cfg, got, tt.want)
			}
		})
	}
}

func TestServerCfgKeepExcludes(t *testing.T) {
	root := t.TempDir()
	master := filepath.Join(root, "master", "game")
	server := filepath.Join(root, "server-1", "game")

	// Fresh server: nothing to keep, master's cfg is copied as before.
	if got := serverCfgKeepExcludes(master, server); len(got) != 0 {
		t.Fatalf("fresh server: got %v, want none", got)
	}

	for _, rel := range []string{"server.cfg", "gamemode_competitive.cfg"} {
		writeFile(t, filepath.Join(master, "csgo", "cfg", rel), "master", testMtime)
	}
	for _, rel := range []string{"server.cfg", "autoexec.cfg", "banned_ip.cfg", "gamemode_competitive.cfg", "custom.cfg", filepath.Join("AutoTournamentCS2", "config.cfg")} {
		writeFile(t, filepath.Join(server, "csgo", "cfg", rel), "server", testMtime)
	}

	got := strings.Join(serverCfgKeepExcludes(master, server), " ")
	for _, want := range []string{
		"--exclude /csgo/cfg/server.cfg",
		"--exclude /csgo/cfg/autoexec.cfg",
		"--exclude /csgo/cfg/banned_ip.cfg",
		"--exclude /csgo/cfg/custom.cfg",
		"--exclude /csgo/cfg/AutoTournamentCS2/",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	// A Valve cfg both sides have still gets master's update.
	if strings.Contains(got, "gamemode_competitive.cfg") {
		t.Errorf("gamemode_competitive.cfg must still sync from master: %q", got)
	}
}

// TestResyncKeepsServerConfigs runs the real update-game sync against an
// existing server and checks its RCON configs survive.
func TestResyncKeepsServerConfigs(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}
	for _, mode := range []string{"legacy", "rsync"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CSM_COPY_MODE", mode)
			t.Setenv("CSM_VPK_HARDLINK", "")

			root := t.TempDir()
			master := newMaster(t, root)
			masterDir := filepath.Dir(master)
			writeFile(t, filepath.Join(master, "csgo", "cfg", "gamemode_competitive.cfg"), "old valve", testMtime)
			server := filepath.Join(root, "server-1", "game")
			if err := os.MkdirAll(server, 0o755); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := copyMasterGameToServerGame(context.Background(), &out, "", masterDir, server, false, false); err != nil {
				t.Fatalf("initial sync: %v\n%s", err, out.String())
			}

			cfg := filepath.Join(server, "csgo", "cfg")
			serverCfg := "rcon_password \"from-server-cfg\"\nhostname \"Server #1\""
			files := map[string]string{
				"server.cfg":    serverCfg,
				"autoexec.cfg":  "rcon_password \"from-server-cfg\"",
				"banned_ip.cfg": "",
				filepath.Join("AutoTournamentCS2", "live.cfg"): "mp_maxrounds 24",
			}
			for rel, body := range files {
				writeFile(t, filepath.Join(cfg, rel), body, testMtime)
			}

			// Valve update touches a shared cfg.
			writeFile(t, filepath.Join(master, "csgo", "cfg", "gamemode_competitive.cfg"), "new valve", testMtime.Add(1e9))

			out.Reset()
			if err := copyMasterGameToServerGame(context.Background(), &out, "", masterDir, server, false, false); err != nil {
				t.Fatalf("resync: %v\n%s", err, out.String())
			}
			for rel, body := range files {
				if got := readFile(t, filepath.Join(cfg, rel)); got != body {
					t.Errorf("%s = %q after update, want %q", rel, got, body)
				}
			}
			if got := readFile(t, filepath.Join(cfg, "gamemode_competitive.cfg")); got != "new valve" {
				t.Errorf("gamemode_competitive.cfg = %q, want master's update", got)
			}
		})
	}
}
