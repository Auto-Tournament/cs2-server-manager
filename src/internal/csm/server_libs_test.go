package csm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var bootLibs = []string{
	filepath.Join("bin", "linuxsteamrt64", "cs2"),
	filepath.Join("csgo", "bin", "linuxsteamrt64", "libserver.so"),
	filepath.Join("bin", "linuxsteamrt64", "libv8.so"),
}

func writeBootLibs(t *testing.T, gameRoot string, skip ...string) {
	t.Helper()
	for _, rel := range bootLibs {
		if contains(skip, filepath.Base(rel)) {
			continue
		}
		writeFile(t, filepath.Join(gameRoot, rel), "elf", testMtime)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestMissingServerLibs(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, root string)
		want  []string
	}{
		{"complete", func(t *testing.T, root string) { writeBootLibs(t, root) }, nil},
		{"empty tree", func(t *testing.T, root string) {}, []string{"cs2", "libserver.so", "libv8.so"}},
		{"libv8 missing (the reported crash)", func(t *testing.T, root string) { writeBootLibs(t, root, "libv8.so") }, []string{"libv8.so"}},
		{"libserver missing", func(t *testing.T, root string) { writeBootLibs(t, root, "libserver.so") }, []string{"libserver.so"}},
		{"libv8 under csgo/bin also counts", func(t *testing.T, root string) {
			writeBootLibs(t, root, "libv8.so")
			writeFile(t, filepath.Join(root, "csgo", "bin", "linuxsteamrt64", "libv8.so"), "elf", testMtime)
		}, nil},
		{"broken symlink counts as missing", func(t *testing.T, root string) {
			writeBootLibs(t, root, "libserver.so")
			if err := os.MkdirAll(filepath.Join(root, "csgo", "bin", "linuxsteamrt64"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "nope.so"), filepath.Join(root, "csgo", "bin", "linuxsteamrt64", "libserver.so")); err != nil {
				t.Fatal(err)
			}
		}, []string{"libserver.so"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.setup(t, root)
			if got := missingServerLibs(root); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("missingServerLibs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServerLibRepairEnsure(t *testing.T) {
	tests := []struct {
		name          string
		serverOK      bool
		masterOK      bool
		validateFixes bool
		syncFails     bool
		wantErr       string
		wantValidate  int
		wantSync      int
	}{
		{name: "already complete", serverOK: true, masterOK: true},
		{name: "server incomplete, master fine: re-copy", masterOK: true, wantSync: 1},
		{name: "master incomplete: validate then re-copy", validateFixes: true, wantValidate: 1, wantSync: 1},
		{name: "validate does not help", wantErr: "even after SteamCMD validate", wantValidate: 1},
		{name: "re-copy fails", masterOK: true, syncFails: true, wantErr: "re-copy from master-install failed", wantSync: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			masterDir := filepath.Join(root, "master-install")
			masterGame := filepath.Join(masterDir, "game")
			serverGame := filepath.Join(root, "server-1", "game")
			if tt.masterOK {
				writeBootLibs(t, masterGame)
			}
			if tt.serverOK {
				writeBootLibs(t, serverGame)
			} else {
				writeBootLibs(t, serverGame, "libv8.so")
			}

			validates, syncs := 0, 0
			r := &serverLibRepair{
				masterDir: masterDir,
				validateMaster: func(context.Context, io.Writer) error {
					validates++
					if tt.validateFixes {
						writeBootLibs(t, masterGame)
					}
					return nil
				},
				syncServer: func(_ context.Context, _ io.Writer, dst string) error {
					syncs++
					if tt.syncFails {
						return errors.New("rsync died")
					}
					writeBootLibs(t, dst)
					return nil
				},
			}
			var out bytes.Buffer
			err := r.ensure(context.Background(), &out, "server-1", serverGame)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v\n%s", err, out.String())
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if validates != tt.wantValidate || syncs != tt.wantSync {
				t.Fatalf("validate=%d sync=%d, want %d/%d", validates, syncs, tt.wantValidate, tt.wantSync)
			}
		})
	}
}

// Master is validated at most once per repair, even across servers.
func TestServerLibRepairValidatesMasterOnce(t *testing.T) {
	root := t.TempDir()
	validates := 0
	r := &serverLibRepair{
		masterDir:      filepath.Join(root, "master-install"),
		validateMaster: func(context.Context, io.Writer) error { validates++; return nil },
		syncServer:     func(context.Context, io.Writer, string) error { return nil },
	}
	for i := 1; i <= 3; i++ {
		_ = r.ensure(context.Background(), io.Discard, "server", filepath.Join(root, "server", "game"))
	}
	if validates != 1 {
		t.Fatalf("validated %d times, want 1", validates)
	}
}
