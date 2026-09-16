package csm

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// liveWorkaroundDBJSON is the hand-edited file an operator put on a production
// box to work around the shared-config bug. It carries a __CSM_NOTE, but not
// CSM's own wording, so CSM must leave it alone.
const liveWorkaroundDBJSON = `{
  "DatabaseType": "SQLite",
  "MySqlHost": "127.0.0.1",
  "MySqlPort": 3306,
  "MySqlDatabase": "matchzy",
  "MySqlUsername": "matchzy",
  "MySqlPassword": "matchzy",
  "__CSM_NOTE": "SQLite per server: a shared MySQL made every server load the same matchzy_server_id/bootstrap_url (all reported s_3). MAT keeps its own database.",
  "__CSM_DB_MODE": "docker"
}`

func readDBFile(t *testing.T, path string) matchzyDBFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f matchzyDBFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("invalid JSON in %s: %v", path, err)
	}
	return f
}

func TestWriteManagedMatchzyDBConfigLeavesOperatorFilesAlone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{"custom MySQL file without note", `{"DatabaseType":"MySQL","MySqlHost":"db.internal","MySqlPort":3307,"MySqlDatabase":"stats","MySqlUsername":"u","MySqlPassword":"p"}`},
		{"operator note replacing CSM's", liveWorkaroundDBJSON},
		{"unparseable file", "{ this is not json"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "database.json")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			desired, mode := wizardMatchzyDBConfig(BootstrapConfig{DBMode: "docker"})
			wrote, err := writeManagedMatchzyDBConfig(path, desired, mode)
			if err != nil {
				t.Fatal(err)
			}
			if wrote {
				t.Fatal("wrote over an operator-owned database.json")
			}
			got, _ := os.ReadFile(path)
			if !bytes.Equal(got, []byte(tt.content)) {
				t.Fatalf("file changed:\n%s", got)
			}
		})
	}
}

func TestWriteManagedMatchzyDBConfigRewritesManagedAndCreatesMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	managed := filepath.Join(dir, "managed.json")
	embedded, err := defaultOverridesFS.ReadFile("defaults/overrides/game/csgo/cfg/MatchZy/database.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, embedded, 0o644); err != nil {
		t.Fatal(err)
	}

	desired, mode := wizardMatchzyDBConfig(BootstrapConfig{DBEngine: MatchzyDBEngineSQLite})
	for _, path := range []string{missing, managed} {
		wrote, err := writeManagedMatchzyDBConfig(path, desired, mode)
		if err != nil {
			t.Fatal(err)
		}
		if !wrote {
			t.Fatalf("%s: expected write", filepath.Base(path))
		}
		f := readDBFile(t, path)
		if f.DatabaseType != "SQLite" || f.DBMode != MatchzyDBEngineSQLite || !isCSMManagedDBNote(f.CSMNote) {
			t.Fatalf("%s: got %+v", filepath.Base(path), f)
		}
	}
}

func TestWizardMatchzyDBConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cfg      BootstrapConfig
		wantType string
		wantMode string
		wantHost string
	}{
		{"default docker mysql", BootstrapConfig{DBMode: "docker"}, "MySQL", "docker", "127.0.0.1"},
		{"sqlite per server", BootstrapConfig{DBMode: "docker", DBEngine: "sqlite"}, "SQLite", MatchzyDBEngineSQLite, "127.0.0.1"},
		{"sqlite wins over external", BootstrapConfig{DBMode: "external", DBEngine: "SQLite", ExternalDBHost: "db"}, "SQLite", MatchzyDBEngineSQLite, "127.0.0.1"},
		{"external mysql", BootstrapConfig{DBMode: "external", DBEngine: "mysql", ExternalDBHost: "db.internal", ExternalDBPort: 3307}, "MySQL", "external", "db.internal"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, mode := wizardMatchzyDBConfig(tt.cfg)
			if c.DatabaseType != tt.wantType || mode != tt.wantMode || c.MySQLHost != tt.wantHost {
				t.Fatalf("got type=%q mode=%q host=%q, want %q %q %q", c.DatabaseType, mode, c.MySQLHost, tt.wantType, tt.wantMode, tt.wantHost)
			}
		})
	}
}

func TestNormalizeMatchzyDBEngine(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"":                  "",
		"mysql":             MatchzyDBEngineMySQL,
		" MySQL ":           MatchzyDBEngineMySQL,
		"shared":            MatchzyDBEngineMySQL,
		"sqlite":            MatchzyDBEngineSQLite,
		"SQLite-per-server": MatchzyDBEngineSQLite,
	} {
		got, err := NormalizeMatchzyDBEngine(in)
		if err != nil || got != want {
			t.Fatalf("NormalizeMatchzyDBEngine(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeMatchzyDBEngine("postgres"); err == nil {
		t.Fatal("expected an error for an unknown engine")
	}
}

func newOverridesDir(t *testing.T) (overrides, dbPath string) {
	t.Helper()
	overrides = t.TempDir()
	dbPath = filepath.Join(overrides, "game", "csgo", "cfg", "MatchZy", "database.json")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	return overrides, dbPath
}

func TestSetupMatchZyDatabaseSQLiteMode(t *testing.T) {
	t.Parallel()

	overrides, dbPath := newOverridesDir(t)
	var buf bytes.Buffer
	// Fresh install: the embedded default (CSM-managed) is already seeded.
	embedded, _ := defaultOverridesFS.ReadFile("defaults/overrides/game/csgo/cfg/MatchZy/database.json")
	if err := os.WriteFile(dbPath, embedded, 0o644); err != nil {
		t.Fatal(err)
	}

	err := setupMatchZyDatabaseGo(&buf, BootstrapConfig{
		CS2User:      "nobody-csm-test",
		NumServers:   3,
		OverridesDir: overrides,
		DBMode:       "docker",
		DBEngine:     MatchzyDBEngineSQLite,
	})
	if err != nil {
		t.Fatalf("setupMatchZyDatabaseGo: %v\n%s", err, buf.String())
	}
	if f := readDBFile(t, dbPath); f.DatabaseType != "SQLite" {
		t.Fatalf("DatabaseType = %q, want SQLite", f.DatabaseType)
	}
	if strings.Contains(buf.String(), "docker") && strings.Contains(buf.String(), "Started") {
		t.Fatalf("SQLite mode must not provision MySQL:\n%s", buf.String())
	}
}

func TestSetupMatchZyDatabaseLeavesCustomFile(t *testing.T) {
	t.Parallel()

	overrides, dbPath := newOverridesDir(t)
	custom := `{"DatabaseType":"MySQL","MySqlHost":"db.internal","MySqlPort":3306,"MySqlDatabase":"stats","MySqlUsername":"u","MySqlPassword":"p"}`
	if err := os.WriteFile(dbPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := setupMatchZyDatabaseGo(&buf, BootstrapConfig{
		CS2User:      "nobody-csm-test",
		NumServers:   3,
		OverridesDir: overrides,
		DBMode:       "docker",
		DBEngine:     MatchzyDBEngineSQLite,
		// Keep the test off Docker: the untouched file is MySQL.
		MatchzySkipDocker: true,
	})
	if err != nil {
		t.Fatalf("setupMatchZyDatabaseGo: %v\n%s", err, buf.String())
	}
	if got, _ := os.ReadFile(dbPath); string(got) != custom {
		t.Fatalf("custom database.json was modified:\n%s", got)
	}
	if !strings.Contains(buf.String(), "not managed by CSM") {
		t.Fatalf("expected a log line explaining the file was left alone:\n%s", buf.String())
	}
}

func TestSharedMySQLScopingNoticeNamesRequirement(t *testing.T) {
	t.Parallel()

	msg := sharedMySQLScopingNotice(3)
	for _, want := range []string{MatchzyScopingRequirement(), MatchzyConfigScopeArg, "SQLite"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("notice %q does not mention %q", msg, want)
		}
	}
}
