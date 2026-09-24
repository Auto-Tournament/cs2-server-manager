package csm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Fatalf("%s still exists", path)
	}
}

func TestCarryOverLegacyCfgMovesEverythingAndRemovesTheOldFolder(t *testing.T) {
	t.Parallel()

	cfg := t.TempDir()
	writeTestFile(t, filepath.Join(cfg, "MatchZy", "config.cfg"), "at_knife_enabled_default false\n")
	writeTestFile(t, filepath.Join(cfg, "MatchZy", "database.json"), `{"DatabaseType":"SQLite"}`)
	writeTestFile(t, filepath.Join(cfg, "MatchZy", "logs", "match.log"), "old log")

	moved, left, err := carryOverLegacyCfg(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 3 || len(left) != 0 {
		t.Fatalf("moved %v, left %v", moved, left)
	}
	if got := readTestFile(t, filepath.Join(cfg, ATCS2CfgDirName, "config.cfg")); got != "at_knife_enabled_default false\n" {
		t.Fatalf("config.cfg = %q", got)
	}
	if got := readTestFile(t, filepath.Join(cfg, ATCS2CfgDirName, "logs", "match.log")); got != "old log" {
		t.Fatalf("logs/match.log = %q", got)
	}
	mustNotExist(t, filepath.Join(cfg, "MatchZy"))
}

func TestCarryOverLegacyCfgNeverOverwrites(t *testing.T) {
	t.Parallel()

	cfg := t.TempDir()
	writeTestFile(t, filepath.Join(cfg, "MatchZy", "config.cfg"), "operator's old file")
	writeTestFile(t, filepath.Join(cfg, "MatchZy", "live.cfg"), "mp_maxrounds 24")
	writeTestFile(t, filepath.Join(cfg, ATCS2CfgDirName, "config.cfg"), "already new")

	moved, left, err := carryOverLegacyCfg(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(moved, ",") != "live.cfg" || strings.Join(left, ",") != "config.cfg" {
		t.Fatalf("moved %v, left %v", moved, left)
	}
	if got := readTestFile(t, filepath.Join(cfg, ATCS2CfgDirName, "config.cfg")); got != "already new" {
		t.Fatalf("new config.cfg was overwritten: %q", got)
	}
	// The old file that could not move is kept, not deleted.
	if got := readTestFile(t, filepath.Join(cfg, "MatchZy", "config.cfg")); got != "operator's old file" {
		t.Fatalf("old config.cfg = %q", got)
	}
}

func TestCarryOverLegacyCfgWithoutOldFolderIsANoop(t *testing.T) {
	t.Parallel()

	moved, left, err := carryOverLegacyCfg(t.TempDir())
	if err != nil || len(moved) != 0 || len(left) != 0 {
		t.Fatalf("moved %v, left %v, err %v", moved, left, err)
	}
}

func TestCarryOverLegacyCfgOnceRunsOnceAndLogs(t *testing.T) {
	t.Setenv("CSM_LOG_DIR", t.TempDir())

	root := t.TempDir()
	cfg := filepath.Join(root, "game", "csgo", "cfg")
	writeTestFile(t, filepath.Join(cfg, "MatchZy", "config.cfg"), "// comment\nmatchzy_knife_enabled_default false\n  matchzy_autostart_mode 1\n")

	var buf bytes.Buffer
	ran, err := carryOverLegacyCfgOnce(&buf, "overrides", root, cfg)
	if err != nil || !ran {
		t.Fatalf("ran %v, err %v", ran, err)
	}
	out := buf.String()
	for _, want := range []string{"moved config.cfg", "has 2 matchzy_* setting(s)", "rename each to at_*"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(root, atcs2CfgCarryOverMarker)); err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	// The operator's file is moved as it is; csm does not rewrite settings.
	if got := readTestFile(t, filepath.Join(cfg, ATCS2CfgDirName, "config.cfg")); !strings.Contains(got, "matchzy_knife_enabled_default false") {
		t.Fatalf("config.cfg content changed: %q", got)
	}

	// A folder that shows up again later is not moved a second time.
	writeTestFile(t, filepath.Join(cfg, "MatchZy", "late.cfg"), "x")
	buf.Reset()
	if ran, err := carryOverLegacyCfgOnce(&buf, "overrides", root, cfg); ran || err != nil || buf.Len() != 0 {
		t.Fatalf("second run: ran %v, err %v, log %q", ran, err, buf.String())
	}
}

func TestCarryOverLegacyCfgOnceSkipsMissingTree(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "not-created-yet")
	if ran, err := carryOverLegacyCfgOnce(&bytes.Buffer{}, "cs2-config", root, filepath.Join(root, "game", "csgo", "cfg")); ran || err != nil {
		t.Fatalf("ran %v, err %v", ran, err)
	}
	mustNotExist(t, root)
}

func TestSetupATCS2DatabaseCarriesOverLegacyDatabaseJSON(t *testing.T) {
	t.Setenv("CSM_LOG_DIR", t.TempDir())

	overrides := t.TempDir()
	oldPath := filepath.Join(overrides, "game", "csgo", "cfg", "MatchZy", "database.json")
	custom := `{"DatabaseType":"MySQL","MySqlHost":"db.internal","MySqlPort":3306,"MySqlDatabase":"stats","MySqlUsername":"u","MySqlPassword":"p"}`
	writeTestFile(t, oldPath, custom)

	var buf bytes.Buffer
	err := setupATCS2DatabaseGo(&buf, BootstrapConfig{
		CS2User:         "nobody-csm-test",
		OverridesDir:    overrides,
		ATCS2SkipDocker: true,
	})
	if err != nil {
		t.Fatalf("setupATCS2DatabaseGo: %v\n%s", err, buf.String())
	}
	newPath := filepath.Join(overrides, "game", "csgo", "cfg", ATCS2CfgDirName, "database.json")
	if got := readTestFile(t, newPath); got != custom {
		t.Fatalf("database.json was not carried over unchanged: %q", got)
	}
	mustNotExist(t, oldPath)
	if strings.Contains(buf.String(), "Created") {
		t.Fatalf("a default database.json was created next to the operator's:\n%s", buf.String())
	}
}

func TestRemoveLegacyATCS2Plugin(t *testing.T) {
	t.Setenv("CSM_LOG_DIR", t.TempDir())

	t.Run("removed when the new plugin is installed", func(t *testing.T) {
		csgo := t.TempDir()
		writeTestFile(t, filepath.Join(legacyATCS2PluginDir(csgo), "MatchZy.dll"), "old")
		writeTestFile(t, filepath.Join(atcs2PluginDir(csgo), ATCS2DLLName), "new")
		var buf bytes.Buffer
		if err := removeLegacyATCS2Plugin(&buf, "server-1", csgo); err != nil {
			t.Fatal(err)
		}
		mustNotExist(t, legacyATCS2PluginDir(csgo))
		if !strings.Contains(buf.String(), "removed the old plugin folder") {
			t.Fatalf("log: %s", buf.String())
		}
	})

	t.Run("kept with a warning when the new plugin is missing", func(t *testing.T) {
		csgo := t.TempDir()
		writeTestFile(t, filepath.Join(legacyATCS2PluginDir(csgo), "MatchZy.dll"), "old")
		var buf bytes.Buffer
		if err := removeLegacyATCS2Plugin(&buf, "server-1", csgo); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(legacyATCS2PluginDir(csgo)); err != nil {
			t.Fatal("the only plugin on the server was removed")
		}
		if !strings.Contains(buf.String(), "sudo csm update-plugins") {
			t.Fatalf("log: %s", buf.String())
		}
	})
}

// fakeReplaceAddons does what a plugin deploy does to a server: wipe addons/
// and put the new plugin bundle in place.
func fakeReplaceAddons(t *testing.T, csgo string) func() error {
	return func() error {
		if err := os.RemoveAll(filepath.Join(csgo, "addons")); err != nil {
			return err
		}
		writeTestFile(t, filepath.Join(atcs2PluginDir(csgo), ATCS2DLLName), "new")
		return nil
	}
}

func TestWithATCS2SQLitePreservedCarriesOverTheOldDatabase(t *testing.T) {
	t.Setenv("CSM_LOG_DIR", t.TempDir())

	server := t.TempDir()
	csgo := filepath.Join(server, "game", "csgo")
	writeTestFile(t, filepath.Join(legacyATCS2PluginDir(csgo), "MatchZy.dll"), "old")
	writeTestFile(t, filepath.Join(legacyATCS2PluginDir(csgo), "matchzy.db"), "stats")
	writeTestFile(t, filepath.Join(legacyATCS2PluginDir(csgo), "matchzy.db-wal"), "wal")

	var buf bytes.Buffer
	if err := withATCS2SQLitePreserved(&buf, server, fakeReplaceAddons(t, csgo)); err != nil {
		t.Fatal(err)
	}
	newDB := filepath.Join(atcs2PluginDir(csgo), ATCS2SQLiteFile)
	if got := readTestFile(t, newDB); got != "stats" {
		t.Fatalf("%s = %q", ATCS2SQLiteFile, got)
	}
	if got := readTestFile(t, newDB+"-wal"); got != "wal" {
		t.Fatalf("journal = %q", got)
	}
	mustNotExist(t, legacyATCS2PluginDir(csgo))
	mustNotExist(t, atcs2SQLiteStashDir(server))
	if !strings.Contains(buf.String(), "carried the plugin database over") {
		t.Fatalf("log: %s", buf.String())
	}
}

func TestWithATCS2SQLitePreservedKeepsTheNewDatabaseAcrossUpdates(t *testing.T) {
	t.Setenv("CSM_LOG_DIR", t.TempDir())

	server := t.TempDir()
	csgo := filepath.Join(server, "game", "csgo")
	writeTestFile(t, filepath.Join(atcs2PluginDir(csgo), ATCS2SQLiteFile), "current")
	// An old database left behind as well: the current one wins, the old one
	// is kept aside and reported, never deleted.
	writeTestFile(t, filepath.Join(legacyATCS2PluginDir(csgo), "matchzy.db"), "older")

	var buf bytes.Buffer
	if err := withATCS2SQLitePreserved(&buf, server, fakeReplaceAddons(t, csgo)); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, filepath.Join(atcs2PluginDir(csgo), ATCS2SQLiteFile)); got != "current" {
		t.Fatalf("database = %q, want current", got)
	}
	if got := readTestFile(t, filepath.Join(atcs2SQLiteStashDir(server), "MatchZy", "matchzy.db")); got != "older" {
		t.Fatalf("old database not kept: %q", got)
	}
	if !strings.Contains(buf.String(), "were kept in") {
		t.Fatalf("log: %s", buf.String())
	}
}

func TestSelectATCS2Asset(t *testing.T) {
	t.Parallel()

	assets := []metamodReleaseAsset{
		{Name: "MatchZy-1.4.34.zip", URL: "old"},
		{Name: "AutoTournamentCS2-2.0.0.tar.gz", URL: "wrong-suffix"},
		{Name: "AutoTournamentCS2-2.0.0.zip", URL: "new"},
	}
	if name, url := selectATCS2Asset(assets); name != "AutoTournamentCS2-2.0.0.zip" || url != "new" {
		t.Fatalf("got %q %q", name, url)
	}
	// A release from before the rename has no usable asset; csm must not
	// fall back to the old zip.
	if _, url := selectATCS2Asset(assets[:2]); url != "" {
		t.Fatalf("picked %q from a pre-2.0.0 release", url)
	}
}

func TestKeepExistingDockerDBCredentials(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existing := filepath.Join(dir, "database.json")
	writeTestFile(t, existing, `{"DatabaseType":"MySQL","MySqlHost":"127.0.0.1","MySqlPort":3306,"MySqlDatabase":"legacydb","MySqlUsername":"legacyuser","MySqlPassword":"legacypass"}`)

	desired, mode := wizardATCS2DBConfig(BootstrapConfig{DBMode: "docker"})
	keepExistingDockerDBCredentials(existing, &desired, mode)
	if desired.MySQLDatabase != "legacydb" || desired.MySQLUsername != "legacyuser" || desired.MySQLPassword != "legacypass" {
		t.Fatalf("existing credentials not kept: %+v", desired)
	}

	fresh, mode := wizardATCS2DBConfig(BootstrapConfig{DBMode: "docker"})
	keepExistingDockerDBCredentials(filepath.Join(dir, "missing.json"), &fresh, mode)
	if fresh.MySQLDatabase != DefaultATCS2DBName {
		t.Fatalf("fresh install: got %q, want %q", fresh.MySQLDatabase, DefaultATCS2DBName)
	}

	remote := filepath.Join(dir, "remote.json")
	writeTestFile(t, remote, `{"DatabaseType":"MySQL","MySqlHost":"db.internal","MySqlDatabase":"x","MySqlUsername":"y","MySqlPassword":"z"}`)
	fromRemote, mode := wizardATCS2DBConfig(BootstrapConfig{DBMode: "docker"})
	keepExistingDockerDBCredentials(remote, &fromRemote, mode)
	if fromRemote.MySQLDatabase != DefaultATCS2DBName {
		t.Fatalf("an external database's settings were copied into Docker: %+v", fromRemote)
	}
}

func TestLegacyATCS2EnvProblems(t *testing.T) {
	t.Setenv("MATCHZY_DB_VOLUME", "my-volume")
	t.Setenv("CSM_MATCHZY_SCOPE_PREFIX", "eu-1")
	got := strings.Join(legacyATCS2EnvProblems(), "\n")
	for _, want := range []string{"MATCHZY_DB_VOLUME is set but no longer read; rename it to AT_DB_VOLUME", "rename it to CSM_AT_SCOPE_PREFIX"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestSetupATCS2DatabaseRefusesLegacyEnv(t *testing.T) {
	t.Setenv("MATCHZY_DB_ROOT_PASSWORD", "secret")
	var buf bytes.Buffer
	err := setupATCS2DatabaseGo(&buf, BootstrapConfig{CS2User: "nobody-csm-test", OverridesDir: t.TempDir()})
	if err == nil || !strings.Contains(buf.String(), "AT_DB_ROOT_PASSWORD") {
		t.Fatalf("err %v, log:\n%s", err, buf.String())
	}
}
