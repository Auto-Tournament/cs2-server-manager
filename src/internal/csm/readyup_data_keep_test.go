package csm

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// excludesDir reports whether rsync args exclude dir/ before the source and
// destination arguments (the last two).
func excludesDir(args []string, dir string) bool {
	if len(args) < 2 {
		return false
	}
	opts := args[:len(args)-2]
	for i := 0; i+1 < len(opts); i++ {
		if opts[i] == "--exclude" && opts[i+1] == dir+"/" {
			return true
		}
	}
	return false
}

func TestMasterSyncExcludesPluginOwnedDirs(t *testing.T) {
	for name, args := range map[string][]string{
		"legacy":     rsyncArgsLegacyCopy("m/", "s/", false, false),
		"legacy-vpk": rsyncArgsLegacyCopy("m/", "s/", true, true, "--exclude", "/csgo/cfg/server.cfg"),
		"tuned":      rsyncArgsTunedCopy("m/", "s/", false, "u", false),
		"tuned-vpk":  rsyncArgsTunedCopy("m/", "s/", true, "u", true),
	} {
		for _, dir := range []string{"csgo/addons", "csgo/readyup"} {
			if !excludesDir(args, dir) {
				t.Errorf("%s: %s/ is not excluded: %v", name, dir, args)
			}
		}
		if args[len(args)-2] != "m/" || args[len(args)-1] != "s/" {
			t.Errorf("%s: source/destination must stay last: %v", name, args)
		}
	}
}

func TestVPKPruneLeavesReadyUpAlone(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")
	copyTree(t, master, server)
	ruFile := filepath.Join(server, "csgo", "readyup", "plugins", "x", "pack.vpk")
	writeFile(t, ruFile, "ru", testMtime)

	if _, err := newVPKLinker(io.Discard).relinkServer(context.Background(), master, server); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ruFile); err != nil {
		t.Fatalf("a file under csgo/readyup must not be pruned: %v", err)
	}
}

// TestUpdateGameKeepsReadyUpData runs the real master -> server copy (rsync)
// against a server that has Ready Up and its data files.
func TestUpdateGameKeepsReadyUpData(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}
	for _, mode := range []string{"legacy", "rsync"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CSM_COPY_MODE", mode)
			t.Setenv("CSM_VPK_HARDLINK", "0")
			root := t.TempDir()
			masterDir := filepath.Join(root, "master-install")
			master := newMaster(t, root)
			writeFile(t, filepath.Join(master, "csgo", "new_in_update.txt"), "new", testMtime)
			server := filepath.Join(root, "server-1", "game")
			copyTree(t, master, server)

			ru := filepath.Join(server, "csgo", "readyup")
			kept := map[string]string{
				"bin/linuxsteamrt64/libserver.so":       "shim",
				"bin/linuxsteamrt64/readyup.cfg":        "chat_prefix=[PUG]",
				"plugins/essentials/admins.json":        `{"version":1}`,
				"plugins/match/state.json":              `{"version":1}`,
				"plugins/fleet/credentials.json":        `{"k":"v"}`,
				"license-acceptance.json":               `{"use":"noncommercial"}`,
				"installed.json":                        `{"components":{}}`,
				"manifests/core.json":                   `{"files":[]}`,
				"cfg-templates/ReadyUp/match.cfg":       "// tpl",
				"tools/patch_gameinfo.py":               "# py",
				"plugins/skins/loadouts.json":           `{"version":1}`,
				"plugins/match/state.json.corrupt-1700": "old",
			}
			for rel, content := range kept {
				writeFile(t, filepath.Join(ru, filepath.FromSlash(rel)), content, testMtime)
			}
			stale := filepath.Join(server, "csgo", "stale_from_old_build.txt")
			writeFile(t, stale, "old", testMtime)

			if err := copyMasterGameToServerGame(context.Background(), io.Discard, "", masterDir, server, false, false); err != nil {
				t.Fatal(err)
			}
			for rel, want := range kept {
				if got := readFile(t, filepath.Join(ru, filepath.FromSlash(rel))); got != want {
					t.Errorf("readyup/%s = %q, want %q", rel, got, want)
				}
			}
			// The sync itself still works: new files arrive, stale ones go.
			if got := readFile(t, filepath.Join(server, "csgo", "new_in_update.txt")); got != "new" {
				t.Errorf("new file not synced: %q", got)
			}
			if _, err := os.Stat(stale); !os.IsNotExist(err) {
				t.Errorf("stale file should be deleted by the sync")
			}
		})
	}
}
