package csm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sivert-io/cs2-server-manager/src/internal/gamefiles"
)

// CS2_MASTER points at a master install; OUT, when set, gets maps.json and skins.json.
func TestCatalogsFromInstall(t *testing.T) {
	master := os.Getenv("CS2_MASTER")
	if master == "" {
		t.Skip("CS2_MASTER not set")
	}
	csgo := filepath.Join(master, "game", "csgo")
	v, err := gamefiles.OpenVPK(filepath.Join(csgo, "pak01_dir.vpk"))
	if err != nil {
		t.Fatal(err)
	}
	for name, build := range Catalogs {
		data, err := build(csgo, master, v)
		if err != nil || len(data) < 1000 {
			t.Fatalf("%s: %d bytes, %v", name, len(data), err)
		}
		if out := os.Getenv("OUT"); out != "" {
			_ = os.WriteFile(filepath.Join(out, name), data, 0o644)
		}
	}
}
