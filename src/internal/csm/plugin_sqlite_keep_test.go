package csm

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newServerWithDB lays out server-1 with the (renamed) plugin, its SQLite
// database and the given journal files.
func newServerWithDB(t *testing.T, journals ...string) (serverDir, pluginDir string) {
	t.Helper()
	t.Setenv("CSM_LOG_DIR", t.TempDir())
	serverDir = filepath.Join(t.TempDir(), "server-1")
	pluginDir = atcs2PluginDir(filepath.Join(serverDir, "game", "csgo"))
	writeKeepFile(t, filepath.Join(pluginDir, ATCS2DLLName), "old dll")
	writeKeepFile(t, filepath.Join(pluginDir, ATCS2SQLiteFile), "match stats")
	for _, sfx := range journals {
		writeKeepFile(t, filepath.Join(pluginDir, ATCS2SQLiteFile+sfx), "journal "+sfx)
	}
	return serverDir, pluginDir
}

func writeKeepFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readKeepFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// replaceLikeDeploy does what update-plugins does to a server's addons/:
// delete the tree and copy in a fresh plugin without a database.
func replaceLikeDeploy(serverDir string) func() error {
	return func() error {
		addons := filepath.Join(serverDir, "game", "csgo", "addons")
		if err := os.RemoveAll(addons); err != nil {
			return err
		}
		dir := atcs2PluginDir(filepath.Join(serverDir, "game", "csgo"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, ATCS2DLLName), []byte("new dll"), 0o644)
	}
}

// These exercise withATCS2SQLitePreserved (atcs2_migrate.go) purely on the
// current (renamed) plugin layout, with no legacy MatchZy folder involved;
// TestWithATCS2SQLitePreservedCarriesOverTheOldDatabase and
// TestWithATCS2SQLitePreservedKeepsTheNewDatabaseAcrossUpdates in
// atcs2_migrate_test.go cover the legacy carry-over behaviour specifically.

func TestPluginSQLiteSurvivesAddonsReplace(t *testing.T) {
	serverDir, pluginDir := newServerWithDB(t, "-wal", "-shm")
	var out bytes.Buffer

	if err := withATCS2SQLitePreserved(&out, serverDir, replaceLikeDeploy(serverDir)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := readKeepFile(t, filepath.Join(pluginDir, ATCS2DLLName)); got != "new dll" {
		t.Fatalf("addons were not replaced: dll = %q", got)
	}
	if got := readKeepFile(t, filepath.Join(pluginDir, ATCS2SQLiteFile)); got != "match stats" {
		t.Fatalf("database = %q, want the original", got)
	}
	for _, sfx := range []string{"-wal", "-shm"} {
		if got := readKeepFile(t, filepath.Join(pluginDir, ATCS2SQLiteFile+sfx)); got != "journal "+sfx {
			t.Fatalf("%s = %q", sfx, got)
		}
	}
	if _, err := os.Stat(atcs2SQLiteStashDir(serverDir)); !os.IsNotExist(err) {
		t.Fatalf("stash should be removed when empty, stat err = %v", err)
	}
	if strings.Contains(out.String(), "WARN") {
		t.Fatalf("unexpected warning: %s", out.String())
	}
}

func TestPluginSQLiteSurvivesRepeatedUpdates(t *testing.T) {
	serverDir, pluginDir := newServerWithDB(t)
	for i := 0; i < 3; i++ {
		if err := withATCS2SQLitePreserved(&bytes.Buffer{}, serverDir, replaceLikeDeploy(serverDir)); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}
	if got := readKeepFile(t, filepath.Join(pluginDir, ATCS2SQLiteFile)); got != "match stats" {
		t.Fatalf("database = %q after three updates", got)
	}
}

func TestPluginSQLiteRestoredWhenReplaceFails(t *testing.T) {
	serverDir, pluginDir := newServerWithDB(t)
	boom := errors.New("rsync failed")

	err := withATCS2SQLitePreserved(&bytes.Buffer{}, serverDir, func() error {
		_ = os.RemoveAll(filepath.Join(serverDir, "game", "csgo", "addons"))
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the replace error", err)
	}
	if got := readKeepFile(t, filepath.Join(pluginDir, ATCS2SQLiteFile)); got != "match stats" {
		t.Fatalf("database = %q, want it put back", got)
	}
}

func TestPluginSQLiteNoDatabaseIsANoOp(t *testing.T) {
	t.Setenv("CSM_LOG_DIR", t.TempDir())
	serverDir := filepath.Join(t.TempDir(), "server-1")
	ran := false
	if err := withATCS2SQLitePreserved(&bytes.Buffer{}, serverDir, func() error { ran = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("replaceAddons was not run")
	}
	if _, err := os.Stat(atcs2SQLiteStashDir(serverDir)); !os.IsNotExist(err) {
		t.Fatalf("no stash expected, stat err = %v", err)
	}
}

// The new addons already have a database (for example the server started and
// created one while csm was interrupted): it is not overwritten, and the old
// one stays in the stash with a warning.
func TestPluginSQLiteNeverOverwritesExistingDatabase(t *testing.T) {
	serverDir, pluginDir := newServerWithDB(t)
	var out bytes.Buffer

	err := withATCS2SQLitePreserved(&out, serverDir, func() error {
		if err := replaceLikeDeploy(serverDir)(); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(pluginDir, ATCS2SQLiteFile), []byte("newer"), 0o644)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readKeepFile(t, filepath.Join(pluginDir, ATCS2SQLiteFile)); got != "newer" {
		t.Fatalf("existing database overwritten: %q", got)
	}
	stashed := filepath.Join(atcs2SQLiteStashDir(serverDir), ATCS2PluginDirName, ATCS2SQLiteFile)
	if got := readKeepFile(t, stashed); got != "match stats" {
		t.Fatalf("stashed database = %q", got)
	}
	if !strings.Contains(out.String(), "[WARN]") || !strings.Contains(out.String(), ".csm-plugin-db-stash") {
		t.Fatalf("expected a warning naming the stash, got: %s", out.String())
	}
}

// A database left in the stash by an interrupted run is put back by the next
// update, and a second copy is not lost either.
func TestPluginSQLiteInterruptedRunIsRecovered(t *testing.T) {
	t.Setenv("CSM_LOG_DIR", t.TempDir())
	serverDir := filepath.Join(t.TempDir(), "server-1")
	pluginDir := atcs2PluginDir(filepath.Join(serverDir, "game", "csgo"))
	newStash := filepath.Join(atcs2SQLiteStashDir(serverDir), ATCS2PluginDirName)
	writeKeepFile(t, filepath.Join(newStash, ATCS2SQLiteFile), "from interrupted run")

	if err := withATCS2SQLitePreserved(&bytes.Buffer{}, serverDir, replaceLikeDeploy(serverDir)); err != nil {
		t.Fatal(err)
	}
	if got := readKeepFile(t, filepath.Join(pluginDir, ATCS2SQLiteFile)); got != "from interrupted run" {
		t.Fatalf("database = %q", got)
	}

	// The stash still holds an older copy while the plugin folder has the
	// current database: the current one is put back, the older one is kept.
	writeKeepFile(t, filepath.Join(newStash, ATCS2SQLiteFile), "older copy")
	var out bytes.Buffer
	if err := withATCS2SQLitePreserved(&out, serverDir, replaceLikeDeploy(serverDir)); err != nil {
		t.Fatal(err)
	}
	if got := readKeepFile(t, filepath.Join(pluginDir, ATCS2SQLiteFile)); got != "from interrupted run" {
		t.Fatalf("database = %q, want the current one", got)
	}
	entries, _ := os.ReadDir(newStash)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), ATCS2SQLiteFile+".") {
		t.Fatalf("stash should keep only the older copy under a timestamped name, got %v", entries)
	}
	if got := readKeepFile(t, filepath.Join(newStash, entries[0].Name())); got != "older copy" {
		t.Fatalf("older copy = %q", got)
	}
	if !strings.Contains(out.String(), "[WARN]") {
		t.Fatalf("expected a warning about the kept copy, got: %s", out.String())
	}
}

// If the database cannot be moved aside, the addons are not replaced.
func TestPluginSQLiteMoveFailureSkipsReplace(t *testing.T) {
	serverDir, pluginDir := newServerWithDB(t)
	// A file where the stash directory should be makes the move fail.
	writeKeepFile(t, atcs2SQLiteStashDir(serverDir), "not a directory")

	ran := false
	err := withATCS2SQLitePreserved(&bytes.Buffer{}, serverDir, func() error { ran = true; return nil })
	if err == nil {
		t.Fatal("expected an error")
	}
	if ran {
		t.Fatal("addons were replaced although the database could not be moved aside")
	}
	if got := readKeepFile(t, filepath.Join(pluginDir, ATCS2SQLiteFile)); got != "match stats" {
		t.Fatalf("database = %q", got)
	}
	if got := readKeepFile(t, atcs2SQLiteStashDir(serverDir)); got != "not a directory" {
		t.Fatal("the unrelated file was touched")
	}
}

func TestMysqlDataVolumePrefersMountedVolume(t *testing.T) {
	cases := []struct{ configured, mounted, want string }{
		{"matchzy-mysql-data", "", "matchzy-mysql-data"},
		{"matchzy-mysql-data", "matchzy-mysql-data", "matchzy-mysql-data"},
		{"matchzy-mysql-data", "custom-volume", "custom-volume"},
		{"matchzy-mysql-data", " custom-volume\n", "custom-volume"},
	}
	for _, c := range cases {
		if got := mysqlDataVolume(c.configured, c.mounted); got != c.want {
			t.Errorf("mysqlDataVolume(%q, %q) = %q, want %q", c.configured, c.mounted, got, c.want)
		}
	}
}
