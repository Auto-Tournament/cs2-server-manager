package csm

import (
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
	orig := homeBaseDir
	homeBaseDir = home
	t.Cleanup(func() { homeBaseDir = orig })

	if got, want := OverridesDir("cs2"), filepath.Join(home, "cs2", "overrides"); got != want {
		t.Fatalf("OverridesDir = %q, want %q", got, want)
	}
	if got, want := OverridesGameDir("cs2"), filepath.Join(home, "cs2", "overrides", "game"); got != want {
		t.Fatalf("OverridesGameDir = %q, want %q", got, want)
	}
}
