package csm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNearestExistingDir(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "opt", "cs2-server-manager")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(existing, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{"existing dir", existing, existing},
		{"missing game_files/game on a fresh install", filepath.Join(existing, "game_files", "game"), existing},
		{"file is not a dir", filepath.Join(file, "sub"), existing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := nearestExistingDir(tt.path)
			if err != nil {
				t.Fatalf("nearestExistingDir(%q) error: %v", tt.path, err)
			}
			if got != tt.want {
				t.Fatalf("nearestExistingDir(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// A missing game_files/game directory must not fail the plugin pre-check,
// and must not create the directory as a side effect.
func TestCheckDiskSpaceForPluginUpdateMissingDir(t *testing.T) {
	gameDir := filepath.Join(t.TempDir(), "cs2-server-manager", "game_files", "game")
	if err := CheckDiskSpaceForPluginUpdate(gameDir); err != nil {
		t.Fatalf("CheckDiskSpaceForPluginUpdate on missing dir: %v", err)
	}
	if _, err := os.Stat(gameDir); !os.IsNotExist(err) {
		t.Fatalf("pre-check created %s (stat err = %v)", gameDir, err)
	}
}

func TestRequireFreeDiskGBReportsShortageOnlyWhenShort(t *testing.T) {
	dir := t.TempDir()
	if err := requireFreeDiskGB(filepath.Join(dir, "missing"), 0); err != nil {
		t.Fatalf("0 GB required: %v", err)
	}
	err := requireFreeDiskGB(dir, 1e12)
	if err == nil {
		t.Fatal("1e12 GB required: expected an error")
	}
	if !errors.Is(err, ErrInsufficientDiskSpace) {
		t.Fatalf("shortage error does not wrap ErrInsufficientDiskSpace: %v", err)
	}
	if !strings.Contains(err.Error(), "insufficient disk space") {
		t.Fatalf("shortage error text = %q", err.Error())
	}
}
