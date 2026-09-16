package csm

import (
	"strings"
	"testing"
)

const sharedTarget = "localhost:3306/matchzy"

func sharedMySQL(server int, scoping scopingSupport, serverID string, running, scopeArg bool) matchzyServerFacts {
	return matchzyServerFacts{
		Server:      server,
		DBEngine:    MatchzyDBEngineMySQL,
		MySQLTarget: sharedTarget,
		Scoping:     scoping,
		ServerID:    serverID,
		Running:     running,
		HasScopeArg: scopeArg,
	}
}

func TestEvaluateMatchzyScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		facts      []matchzyServerFacts
		want       DoctorCheckStatus
		detailHas  []string
		fixHas     []string
		fixMissing []string
	}{
		{
			name: "production bug: shared MySQL, old plugin, all report s_3",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingNo, "s_3", true, false),
				sharedMySQL(2, scopingNo, "s_3", true, false),
				sharedMySQL(3, scopingNo, "s_3", true, false),
			},
			want:      DoctorFail,
			detailHas: []string{`server-1, server-2, server-3 all report matchzy_server_id "s_3"`, "without per-server config scoping"},
			fixHas:    []string{"sudo csm update-plugins", `"DatabaseType": "SQLite"`, "reconfigure every server", "sudo csm doctor"},
		},
		{
			name: "fixed: shared MySQL, scoping plugin, scope args, distinct ids",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, true),
				sharedMySQL(2, scopingYes, "s_2", true, true),
				sharedMySQL(3, scopingYes, "s_3", true, true),
			},
			want:      DoctorOK,
			detailHas: []string{"per-server config scoping"},
		},
		{
			name: "scoping plugin but servers still running with old start args",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, false),
				sharedMySQL(2, scopingYes, "s_2", true, false),
			},
			want:       DoctorFail,
			detailHas:  []string{"running without +matchzy_config_scope"},
			fixHas:     []string{"sudo csm restart"},
			fixMissing: []string{"update-plugins", "reconfigure"},
		},
		{
			name: "stopped servers are not blamed for missing start args",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingYes, "", false, false),
				sharedMySQL(2, scopingYes, "", false, false),
			},
			want: DoctorOK,
		},
		{
			name: "every server on its own SQLite",
			facts: []matchzyServerFacts{
				{Server: 1, DBEngine: MatchzyDBEngineSQLite, Scoping: scopingNo, ServerID: "s_1"},
				{Server: 2, DBEngine: MatchzyDBEngineSQLite, Scoping: scopingNo, ServerID: "s_2"},
			},
			want:      DoctorOK,
			detailHas: []string{"own SQLite"},
		},
		{
			name: "SQLite but ids still duplicated until the manager reconfigures",
			facts: []matchzyServerFacts{
				{Server: 1, DBEngine: MatchzyDBEngineSQLite, ServerID: "s_3"},
				{Server: 2, DBEngine: MatchzyDBEngineSQLite, ServerID: "s_3"},
			},
			want:       DoctorFail,
			fixHas:     []string{"reconfigure every server"},
			fixMissing: []string{"update-plugins"},
		},
		{
			name: "plugin missing on a shared-MySQL server",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, true),
				sharedMySQL(2, scopingUnknown, "", false, false),
			},
			want:      DoctorWarn,
			detailHas: []string{"MatchZy.dll was not found for server-2"},
			fixHas:    []string{"sudo csm update-plugins"},
		},
		{
			name: "separate MySQL databases do not share config",
			facts: []matchzyServerFacts{
				{Server: 1, DBEngine: MatchzyDBEngineMySQL, MySQLTarget: "localhost:3306/matchzy1", Scoping: scopingNo, ServerID: "s_1", Running: true},
				{Server: 2, DBEngine: MatchzyDBEngineMySQL, MySQLTarget: "localhost:3306/matchzy2", Scoping: scopingNo, ServerID: "s_2", Running: true},
			},
			want: DoctorOK,
		},
		{
			name:  "single server",
			facts: []matchzyServerFacts{sharedMySQL(1, scopingNo, "s_1", true, false)},
			want:  DoctorOK,
		},
		{
			name: "only the old-plugin servers in a shared group are named",
			facts: []matchzyServerFacts{
				sharedMySQL(1, scopingYes, "s_1", true, true),
				sharedMySQL(2, scopingNo, "s_2", true, true),
				{Server: 3, DBEngine: MatchzyDBEngineMySQL, MySQLTarget: "db:3306/other", Scoping: scopingNo, ServerID: "s_9", Running: true},
			},
			want:      DoctorFail,
			detailHas: []string{"and server-2 run a MatchZy build without"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := evaluateMatchzyScope("cs2servermanager", tt.facts)
			if got.Status != tt.want {
				t.Fatalf("status = %s, want %s\ndetail: %s\nfix: %s", got.Status, tt.want, got.Detail, got.FixHint)
			}
			if got.Fix != nil {
				t.Fatal("this check must never restart or rewrite anything automatically")
			}
			for _, s := range tt.detailHas {
				if !strings.Contains(got.Detail, s) {
					t.Errorf("detail missing %q:\n%s", s, got.Detail)
				}
			}
			for _, s := range tt.fixHas {
				if !strings.Contains(got.FixHint, s) {
					t.Errorf("fix hint missing %q:\n%s", s, got.FixHint)
				}
			}
			for _, s := range tt.fixMissing {
				if strings.Contains(got.FixHint, s) {
					t.Errorf("fix hint should not mention %q:\n%s", s, got.FixHint)
				}
			}
		})
	}
}

func TestLastMatchzyServerID(t *testing.T) {
	t.Parallel()

	log := strings.Join([]string{
		`[LoadPersistentConfig] Loaded matchzy_bootstrap_url: http://192.168.50.196:3069/api/servers/s_3/bootstrap`,
		`[LoadPersistentConfig] Loaded matchzy_server_id: s_3`,
		`[SaveConfigValue] Saved config: matchzy_server_id = s_1`,
		`[SendEventAsync] {"event":"going_live","server_id":"s_2","matchid":"42"}`,
	}, "\n")
	if got := lastMatchzyServerID(log); got != "s_2" {
		t.Fatalf("lastMatchzyServerID = %q, want s_2", got)
	}
	if got := lastMatchzyServerID(`[SaveConfigValue] Saved config: matchzy_server_id = s_3`); got != "s_3" {
		t.Fatalf("SaveConfigValue form: got %q", got)
	}
	if got := lastMatchzyServerID("no ids here"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestScopingSupportInBytes(t *testing.T) {
	t.Parallel()

	junk := []byte("\x00MZ\x90\x00 some assembly bytes ")
	if got := scopingSupportInBytes(append(junk, utf16le("matchzy_config_scope")...)); got != scopingYes {
		t.Fatalf("UTF-16LE literal: got %v", got)
	}
	if got := scopingSupportInBytes(append(junk, []byte("+matchzy_config_scope")...)); got != scopingYes {
		t.Fatalf("UTF-8 literal: got %v", got)
	}
	if got := scopingSupportInBytes(append(junk, utf16le("matchzy_server_id")...)); got != scopingNo {
		t.Fatalf("old build: got %v", got)
	}
	if got := dllScopingSupport("/nonexistent/MatchZy.dll"); got != scopingUnknown {
		t.Fatalf("missing dll: got %v", got)
	}
}

func TestScopeArgOnRunningServer(t *testing.T) {
	t.Parallel()

	ps := strings.Join([]string{
		"tmux new-session -d -s cs2-1 ./cs2.sh -dedicated -ip 0.0.0.0 +map de_dust2 -port 27015 +tv_port 27020 +maxplayers 10 -usercon +matchzy_config_scope cs2-server-1",
		"/home/cs2servermanager/server-1/game/bin/linuxsteamrt64/cs2 -dedicated -ip 0.0.0.0 +map de_dust2 -port 27015 +tv_port 27020 +maxplayers 10 -usercon +matchzy_config_scope cs2-server-1",
		"/home/cs2servermanager/server-2/game/bin/linuxsteamrt64/cs2 -dedicated -ip 0.0.0.0 +map de_dust2 -port 27025 +tv_port 27030 +maxplayers 10 -usercon",
		"/home/other/cs2 -port 270150",
		"nc -port 27035",
	}, "\n")

	tests := []struct {
		port         int
		running, arg bool
	}{
		{27015, true, true},
		{27025, true, false},
		{27035, false, false}, // only a non-cs2 process
		{27045, false, false},
	}
	for _, tt := range tests {
		running, arg := scopeArgOnRunningServer(ps, tt.port)
		if running != tt.running || arg != tt.arg {
			t.Fatalf("port %d: running=%v arg=%v, want %v %v", tt.port, running, arg, tt.running, tt.arg)
		}
	}
	// 270150 must not count as 27015 on its own.
	if running, _ := scopeArgOnRunningServer("/x/cs2 -port 270150", 27015); running {
		t.Fatal("-port 270150 matched port 27015")
	}
}

func TestParseMatchzyDBEngine(t *testing.T) {
	t.Parallel()

	engine, target := parseMatchzyDBEngine([]byte(`{"DatabaseType":"MySQL","MySqlHost":"localhost","MySqlPort":3306,"MySqlDatabase":"matchzy"}`))
	_, target2 := parseMatchzyDBEngine([]byte(`{"DatabaseType":"mysql","MySqlHost":"127.0.0.1","MySqlPort":0,"MySqlDatabase":"matchzy"}`))
	if engine != MatchzyDBEngineMySQL || target != sharedTarget || target2 != sharedTarget {
		t.Fatalf("got %q %q %q", engine, target, target2)
	}
	if engine, target := parseMatchzyDBEngine([]byte(liveWorkaroundDBJSON)); engine != MatchzyDBEngineSQLite || target != "" {
		t.Fatalf("sqlite: got %q %q", engine, target)
	}
	if engine, _ := parseMatchzyDBEngine([]byte("nope")); engine != "" {
		t.Fatalf("invalid json: got %q", engine)
	}
}
