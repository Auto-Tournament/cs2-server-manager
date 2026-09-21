package csm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOverridesDirFor(t *testing.T) {
	cases := []struct {
		name, user, want string
	}{
		{"user home", "cs2servermanager", "/home/cs2servermanager/overrides"},
		{"trims user", "  cs2 ", "/home/cs2/overrides"},
		{"no user falls back to root", "", "/opt/cs2-server-manager/overrides"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := overridesDirFor("/home", "/opt/cs2-server-manager", tc.user); got != tc.want {
				t.Fatalf("overridesDirFor(%q) = %q, want %q", tc.user, got, tc.want)
			}
		})
	}
}

func TestOverridesDirHelpers(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	orig := homeBaseDir
	homeBaseDir = home
	t.Cleanup(func() { homeBaseDir = orig })
	t.Setenv("CSM_ROOT", root)

	if got, want := OverridesDir("cs2"), filepath.Join(home, "cs2", "overrides"); got != want {
		t.Fatalf("OverridesDir = %q, want %q", got, want)
	}
	if got, want := OverridesGameDir("cs2"), filepath.Join(home, "cs2", "overrides", "game"); got != want {
		t.Fatalf("OverridesGameDir = %q, want %q", got, want)
	}
	if got, want := LegacyOverridesDir(), filepath.Join(root, "overrides"); got != want {
		t.Fatalf("LegacyOverridesDir = %q, want %q", got, want)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const testCfgRel = "game/csgo/cfg/MatchZy/config.cfg"

func TestMigrateLegacyOverridesCopiesMissing(t *testing.T) {
	oldDir, newDir := t.TempDir(), filepath.Join(t.TempDir(), "overrides")
	writeTestFile(t, filepath.Join(oldDir, testCfgRel), "old edit")

	got, err := MigrateLegacyOverrides(oldDir, newDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != filepath.FromSlash(testCfgRel) {
		t.Fatalf("migrated = %v, want [%s]", got, testCfgRel)
	}
	if c := readTestFile(t, filepath.Join(newDir, testCfgRel)); c != "old edit" {
		t.Fatalf("new file content = %q", c)
	}
	if _, err := os.Stat(filepath.Join(oldDir, testCfgRel)); err != nil {
		t.Fatalf("old file must be kept: %v", err)
	}
}

func TestMigrateLegacyOverridesKeepsExisting(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(oldDir, testCfgRel), "old edit")
	writeTestFile(t, filepath.Join(newDir, testCfgRel), "current")
	writeTestFile(t, filepath.Join(oldDir, "game/csgo/cfg/other.cfg"), "extra")

	got, err := MigrateLegacyOverrides(oldDir, newDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != filepath.FromSlash("game/csgo/cfg/other.cfg") {
		t.Fatalf("migrated = %v, want only other.cfg", got)
	}
	if c := readTestFile(t, filepath.Join(newDir, testCfgRel)); c != "current" {
		t.Fatalf("existing file overwritten: %q", c)
	}
}

func TestMigrateLegacyOverridesNoop(t *testing.T) {
	base := t.TempDir()
	oldDir, newDir := filepath.Join(base, "old"), filepath.Join(base, "new")

	got, err := MigrateLegacyOverrides(oldDir, newDir)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want no-op", got, err)
	}
	if _, err := os.Stat(newDir); !os.IsNotExist(err) {
		t.Fatalf("new dir must not be created, stat err = %v", err)
	}

	// Same folder on both sides is also a no-op.
	writeTestFile(t, filepath.Join(oldDir, testCfgRel), "x")
	if got, err := MigrateLegacyOverrides(oldDir, oldDir); err != nil || len(got) != 0 {
		t.Fatalf("same dir: got %v, %v; want no-op", got, err)
	}
}
