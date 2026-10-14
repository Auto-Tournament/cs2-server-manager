package csm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func putFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func getFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMoveStateEntriesMergesAndKeepsTheHomesCopies(t *testing.T) {
	bin, home := t.TempDir(), t.TempDir()
	putFile(t, filepath.Join(bin, "fleet", "credentials.json"), "agent")
	putFile(t, filepath.Join(bin, "plugins.json"), "bin")
	putFile(t, filepath.Join(bin, "logs", "csm.log"), "bin log")
	putFile(t, filepath.Join(bin, "logs", "a.log"), "a")
	putFile(t, filepath.Join(bin, "csm"), "binary")
	putFile(t, filepath.Join(home, "plugins.json"), "home")
	putFile(t, filepath.Join(home, "logs", "csm.log"), "home log")
	putFile(t, filepath.Join(home, "logs", "b.log"), "b")

	n, err := moveStateEntries(bin, home)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("moved %d items, want 3", n)
	}
	// The bin dir's copies are the ones the agent used: they win.
	if got := getFile(t, filepath.Join(home, "plugins.json")); got != "bin" {
		t.Fatalf("plugins.json = %q", got)
	}
	if got := getFile(t, filepath.Join(home, "logs", "csm.log")); got != "bin log" {
		t.Fatalf("logs/csm.log = %q", got)
	}
	// The home's are kept beside them.
	if got := getFile(t, filepath.Join(home, "plugins.json"+movedAsideSuffix)); got != "home" {
		t.Fatalf("plugins.json aside = %q", got)
	}
	if got := getFile(t, filepath.Join(home, "logs", "csm.log"+movedAsideSuffix)); got != "home log" {
		t.Fatalf("logs/csm.log aside = %q", got)
	}
	for _, p := range []string{"fleet/credentials.json", "logs/a.log", "logs/b.log"} {
		if _, err := os.Stat(filepath.Join(home, p)); err != nil {
			t.Fatalf("%s missing in home: %v", p, err)
		}
	}
	// Binaries stay; state is gone from the bin dir.
	if _, err := os.Stat(filepath.Join(bin, "csm")); err != nil {
		t.Fatal("the binary moved")
	}
	for _, p := range []string{"fleet", "plugins.json", "logs"} {
		if _, err := os.Stat(filepath.Join(bin, p)); err == nil {
			t.Fatalf("%s still in the bin dir", p)
		}
	}
}

func TestMoveIntoNeverOverwritesAnEarlierAside(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	putFile(t, filepath.Join(src, "x"), "new")
	putFile(t, filepath.Join(dst, "x"), "old")
	putFile(t, filepath.Join(dst, "x"+movedAsideSuffix), "older")
	if err := moveInto(filepath.Join(src, "x"), filepath.Join(dst, "x")); err != nil {
		t.Fatal(err)
	}
	if got := getFile(t, filepath.Join(dst, "x"+movedAsideSuffix)); got != "older" {
		t.Fatalf("aside = %q", got)
	}
	if got := getFile(t, filepath.Join(dst, "x"+movedAsideSuffix+".2")); got != "old" {
		t.Fatalf("aside.2 = %q", got)
	}
}

func TestRotateConsolidatedLogKeepsTheNewestPart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "csm.log")
	big := strings.Repeat("o", consolidatedLogMax) + strings.Repeat("n", consolidatedLogKeep)
	putFile(t, path, big)
	rotateConsolidatedLog(path)
	if info, _ := os.Stat(path); info.Size() != 0 {
		t.Fatalf("csm.log is %d bytes after rotating", info.Size())
	}
	kept := getFile(t, path+".1")
	if len(kept) != consolidatedLogKeep || strings.Contains(kept, "o") {
		t.Fatalf("csm.log.1 kept %d bytes, not the newest", len(kept))
	}
	// Under the limit nothing happens.
	putFile(t, path, "small")
	rotateConsolidatedLog(path)
	if got := getFile(t, path); got != "small" {
		t.Fatalf("csm.log = %q", got)
	}
}

func TestPruneIndividualLogsRemovesOnlyOldTimestampedLogs(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-individualLogMaxAge - time.Hour)
	for _, name := range []string{"2026-01-01_10-00-00_monitor.log", "csm.log", "notes.log"} {
		putFile(t, filepath.Join(dir, name), "x")
		_ = os.Chtimes(filepath.Join(dir, name), old, old)
	}
	putFile(t, filepath.Join(dir, "2026-10-14_10-00-00_start.log"), "x")
	pruneIndividualLogs(dir)
	if _, err := os.Stat(filepath.Join(dir, "2026-01-01_10-00-00_monitor.log")); err == nil {
		t.Fatal("old timestamped log kept")
	}
	for _, name := range []string{"csm.log", "notes.log", "2026-10-14_10-00-00_start.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s removed", name)
		}
	}
}
