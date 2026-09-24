package csm

import (
	"fmt"
	"strings"
	"testing"
)

// The excerpts below are trimmed from real ~/logs/server-N.log files. tmux
// pipe-pane appends every run to the same file with CRLF line endings, and
// CounterStrikeSharp colours its own lines with ANSI codes.

// logStartup is the start of a run, up to the point where plugins load.
func logStartup(server int, scopeArg bool) string {
	args := fmt.Sprintf("-dedicated -ip 0.0.0.0 +map de_dust2 -port 270%d5 +tv_port 270%d0 +maxplayers 15 -usercon", server, server+1)
	if scopeArg {
		args += fmt.Sprintf(" +at_config_scope cs2-server-%d", server)
	}
	return crlf(
		`SHADER_SOURCE_ROOT   "/home/cs2servermanager/server-1/src/shaders/" `,
		`command line arguments:`,
		args,
		`Loaded /home/cs2servermanager/server-1/game/bin/linuxsteamrt64/libengine2.so, got 0x5571f7461a40`,
		`GameTypes: missing mapgroupsSP entry for game type/mode (cooperative/coopmission).`,
		"\x1b[32m[03:25:53.870] CSSharp: Initializing with command line: \"/home/cs2servermanager/server-1/game/bin/linuxsteamrt64/cs2\" "+args+"\x1b[m",
		"\x1b[32m[03:25:53.870] CSSharp: Current root directory: /home/cs2servermanager/server-1/game/csgo/addons/counterstrikesharp\x1b[m",
		"\x1b[32m[03:25:54.223] CSSharp: Loading .NET runtime...\x1b[m",
		`[Auto Tournament] [ConnectDatabase] Attempting MySQL connection - Host: 127.0.0.1, Port: 3306, Database: auto_tournament_cs2, User: auto_tournament_cs2, Password: ***, Connection Timeout: 10s, Command Timeout: 15s`,
		`[Auto Tournament] [InitializeDatabase] MySQL Database connection successful`,
		`[Auto Tournament] [LoadPersistentConfig] Loading persistent configuration from database...`,
	)
}

// logLoaded is the plugin finishing its load and running for a while.
func logLoaded(server int, version, serverID string, scoped bool) string {
	scope := ""
	saved := "Saved config: at_server_id = " + serverID
	if scoped {
		scope = fmt.Sprintf("[Auto Tournament] [ConfigScope] Using scope 'cs2-server-%d' (from start argument)", server)
		saved = fmt.Sprintf("Saved config for server 'cs2-server-%d': at_server_id = %s", server, serverID)
	}
	lines := []string{}
	if scope != "" {
		lines = append(lines, scope)
	}
	lines = append(lines,
		`[Auto Tournament] [LoadPersistentConfig] Loaded at_remote_log_url: http://192.168.50.196:3069/api/events?server_id=`+serverID,
		`[Auto Tournament] [LoadPersistentConfig] Loaded at_server_id: `+serverID,
		`[Auto Tournament] [LoadPersistentConfig] Loaded at_bootstrap_url: http://192.168.50.196:3069/api/servers/`+serverID+`/bootstrap`,
		`[Auto Tournament] [LoadPersistentConfig] Persistent configuration loaded successfully.`,
		`[Auto Tournament] [StartWarmup] Starting warmup! Executing Warmup CFG from AutoTournamentCS2/warmup.cfg`,
		`[Auto Tournament CS2 v`+version+` LOADED] Auto Tournament CS2`,
		"\x1b[32m[01:59:44.561] CSSharp: CounterStrikeSharp.API Loaded Successfully.\x1b[m",
		`[META] Loaded 1 plugin.`,
		`[Auto Tournament] [SaveConfigValue] `+saved,
		`[Auto Tournament] [SendEventAsync] SENDING DATA: {"server_id":"`+serverID+`","plugin_version":"`+version+`","timestamp":1789547462,"db_ok":true,"db_type":"mysql","db_error":null,"reason":"periodic","event":"server_health"}`,
		`661.234375 Long frame (WarmupPeriod): 54.37ms elapsed, 0.24ms sim time, 1 ticks, 42319..42319.`,
	)
	return crlf(lines...)
}

func crlf(lines ...string) string {
	return strings.Join(lines, "\r\n") + "\r\n"
}

// oldRun is a complete run on plugin 1.4.34 where every server loaded s_3.
func oldRun(server int) string {
	return logStartup(server, false) + logLoaded(server, "1.4.34", "s_3", false)
}

func factsFromLog(server int, running bool, log, deployed string) atcs2ServerFacts {
	f := sharedMySQL(server, scopingUnknown, "", running, running)
	applyATCS2LogFacts(&f, log, deployed, func() scopingSupport { return scopingUnknown })
	return f
}

func TestCurrentServerRun(t *testing.T) {
	t.Parallel()

	two := oldRun(1) + logStartup(1, true) + logLoaded(1, "2.0.0", "s_1", true)
	run, saw := currentServerRun(two)
	if !saw {
		t.Fatal("startup marker not found")
	}
	if strings.Contains(run, "1.4.34") || strings.Contains(run, "s_3") {
		t.Fatalf("current run still contains the previous run:\n%s", run)
	}
	if got := lastATCS2PluginVersion(run); got != "2.0.0" {
		t.Fatalf("version = %q, want 2.0.0", got)
	}
	if got := lastATCS2ServerID(run); got != "s_1" {
		t.Fatalf("server id = %q, want s_1", got)
	}

	// A tail that starts mid-run has no marker: all of it is the current run.
	mid := logLoaded(1, "2.0.0", "s_1", true)
	if run, saw := currentServerRun(mid); saw || run != mid {
		t.Fatalf("no marker: saw=%v, run changed", saw)
	}

	// The marker text inside another line (chat, a URL) is not a startup.
	chat := oldRun(1) + crlf(`[Auto Tournament] [Chat] player: command line arguments: none`, `[Auto Tournament] note CSSharp: Initializing with command line: x`)
	if run, _ := currentServerRun(chat); !strings.Contains(run, "1.4.34") {
		t.Fatal("a marker in the middle of a line was treated as a restart")
	}

	// A start that crashed before CounterStrikeSharp still only has the
	// engine line, and still counts as a new run.
	crashed := oldRun(1) + crlf(`command line arguments:`, `-dedicated -port 27015`, `Segmentation fault`)
	if run, saw := currentServerRun(crashed); !saw || strings.Contains(run, "1.4.34") {
		t.Fatalf("engine-only start not treated as a new run: saw=%v", saw)
	}
}

func TestATCS2ScopeUsesCurrentRunOnly(t *testing.T) {
	t.Parallel()

	servers := []int{1, 2, 3}
	ids := map[int]string{1: "s_1", 2: "s_2", 3: "s_3"}

	t.Run("restarted onto a new build: old run's 1.4.34 and s_3 are ignored", func(t *testing.T) {
		t.Parallel()
		var facts []atcs2ServerFacts
		for _, n := range servers {
			log := oldRun(n) + logStartup(n, true) + logLoaded(n, "2.0.0", ids[n], true)
			f := factsFromLog(n, true, log, "v2.0.0")
			if f.PluginVersion != "2.0.0" || f.ServerID != ids[n] || f.Starting {
				t.Fatalf("server-%d: version %q id %q starting %v", n, f.PluginVersion, f.ServerID, f.Starting)
			}
			facts = append(facts, f)
		}
		got := evaluateATCS2Scope("cs2servermanager", facts)
		if got.Status != DoctorOK {
			t.Fatalf("status = %s, want OK\n%s", got.Status, got.Detail)
		}
	})

	t.Run("just restarted, new run has not logged its version yet", func(t *testing.T) {
		t.Parallel()
		var facts []atcs2ServerFacts
		for _, n := range servers {
			log := oldRun(n) + logStartup(n, true)
			f := factsFromLog(n, true, log, "v2.0.0")
			if !f.Starting || f.PluginVersion != "2.0.0" || f.ServerID != "" {
				t.Fatalf("server-%d: starting %v version %q id %q", n, f.Starting, f.PluginVersion, f.ServerID)
			}
			facts = append(facts, f)
		}
		got := evaluateATCS2Scope("cs2servermanager", facts)
		if got.Status != DoctorWarn {
			t.Fatalf("status = %s, want WARN\n%s", got.Status, got.Detail)
		}
		for _, s := range []string{"server-1, server-2, server-3: server is still starting", "run sudo csm doctor again in a minute"} {
			if !strings.Contains(strings.ToLower(got.Detail), strings.ToLower(s)) {
				t.Fatalf("detail missing %q:\n%s", s, got.Detail)
			}
		}
		if strings.Contains(got.Detail, "1.4.34") || strings.Contains(got.Detail, "s_3") {
			t.Fatalf("stale run leaked into the result:\n%s", got.Detail)
		}
	})

	t.Run("still starting but the deployed release is itself too old", func(t *testing.T) {
		t.Parallel()
		var facts []atcs2ServerFacts
		for _, n := range []int{1, 2} {
			facts = append(facts, factsFromLog(n, true, oldRun(n)+logStartup(n, true), "v1.4.34"))
		}
		got := evaluateATCS2Scope("cs2servermanager", facts)
		if got.Status != DoctorFail || !strings.Contains(got.Detail, "still starting") {
			t.Fatalf("status = %s, want FAIL with the starting note\n%s", got.Status, got.Detail)
		}
	})

	t.Run("stopped server whose last start crashed is not 'starting'", func(t *testing.T) {
		t.Parallel()
		f := factsFromLog(1, false, oldRun(1)+logStartup(1, true), "v2.0.0")
		if f.Starting {
			t.Fatal("a server that is not running cannot be starting")
		}
	})

	t.Run("single run in the log is read normally", func(t *testing.T) {
		t.Parallel()
		var facts []atcs2ServerFacts
		for _, n := range servers {
			f := factsFromLog(n, true, oldRun(n), "v2.0.0")
			if f.PluginVersion != "1.4.34" || f.ServerID != "s_3" || f.Starting {
				t.Fatalf("server-%d: version %q id %q starting %v", n, f.PluginVersion, f.ServerID, f.Starting)
			}
			facts = append(facts, f)
		}
		got := evaluateATCS2Scope("cs2servermanager", facts)
		if got.Status != DoctorFail {
			t.Fatalf("status = %s, want FAIL", got.Status)
		}
		for _, s := range []string{`all report at_server_id "s_3"`, "server-1 (plugin 1.4.34)"} {
			if !strings.Contains(got.Detail, s) {
				t.Fatalf("detail missing %q:\n%s", s, got.Detail)
			}
		}
	})
}
