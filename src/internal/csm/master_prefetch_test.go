package csm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMasterHasGame(t *testing.T) {
	master := t.TempDir()
	if masterHasGame(master) {
		t.Fatal("an empty master-install has no game")
	}
	bin := filepath.Join(master, "game", "bin", "linuxsteamrt64")
	if err := os.MkdirAll(filepath.Join(bin, "cs2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if masterHasGame(master) {
		t.Fatal("a directory named cs2 is not the game")
	}
	if err := os.Remove(filepath.Join(bin, "cs2")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "cs2"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !masterHasGame(master) {
		t.Fatal("the cs2 binary means CS2 is installed")
	}
}
