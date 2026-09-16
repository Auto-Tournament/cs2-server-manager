package csm

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestEstimateInstallDisk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		masterGB      float64
		servers       int
		hardlink      bool
		wantMaster    float64
		wantPerServer float64
		wantTotal     float64
	}{
		{"hardlinked, nothing measured", 0, 4, true, DefaultMasterDiskGB, DefaultPerServerLinkedDiskGB, DefaultMasterDiskGB + 4*DefaultPerServerLinkedDiskGB},
		{"full copies, nothing measured", 0, 4, false, DefaultMasterDiskGB, DefaultMasterDiskGB, 5 * DefaultMasterDiskGB},
		{"hardlinked uses measured master", 68, 10, true, 68, DefaultPerServerLinkedDiskGB, 68 + 10*DefaultPerServerLinkedDiskGB},
		{"full copies are master sized", 68, 3, false, 68, 68, 4 * 68},
		{"no servers", 0, 0, true, DefaultMasterDiskGB, DefaultPerServerLinkedDiskGB, DefaultMasterDiskGB},
		{"negative servers clamp to 0", 50, -2, false, 50, 50, 50},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := EstimateInstallDisk(tt.masterGB, tt.servers, tt.hardlink)
			if !approx(got.MasterGB, tt.wantMaster) || !approx(got.PerServerGB, tt.wantPerServer) || !approx(got.TotalGB, tt.wantTotal) || got.Hardlinked != tt.hardlink {
				t.Fatalf("EstimateInstallDisk(%v, %d, %v) = %+v, want master %v per-server %v total %v",
					tt.masterGB, tt.servers, tt.hardlink, got, tt.wantMaster, tt.wantPerServer, tt.wantTotal)
			}
		})
	}

	// Hardlinking is what makes many servers fit: 10 servers need well under
	// a fifth of the space full copies do.
	linked := EstimateInstallDisk(0, 10, true).TotalGB
	full := EstimateInstallDisk(0, 10, false).TotalGB
	if linked*5 > full {
		t.Fatalf("hardlinked estimate %.1f GB is not far below full copies %.1f GB", linked, full)
	}
}

func writeSizedFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i*31 + 7) // non-zero so no filesystem stores it sparse
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMeasureInstallFootprintCountsHardlinksOnce(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	const vpk = 1 << 20
	const cfg = 64 << 10
	masterVPK := filepath.Join(home, "master-install", "game", "csgo", "pak01_000.vpk")
	writeSizedFile(t, masterVPK, vpk)
	writeSizedFile(t, filepath.Join(home, "master-install", "game", "csgo", "gameinfo.gi"), cfg)

	for _, s := range []string{"server-1", "server-2"} {
		dst := filepath.Join(home, s, "game", "csgo", "pak01_000.vpk")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(masterVPK, dst); err != nil {
			t.Skipf("hardlinks not supported here: %v", err)
		}
		writeSizedFile(t, filepath.Join(home, s, "game", "csgo", "gameinfo.gi"), cfg)
	}
	// server-3 is a full copy (CSM_VPK_HARDLINK=0 or a pre-1.7.7 install).
	writeSizedFile(t, filepath.Join(home, "server-3", "game", "csgo", "pak01_000.vpk"), vpk)
	// Not a server directory.
	writeSizedFile(t, filepath.Join(home, "server-backup", "big.vpk"), vpk)

	fp, err := MeasureInstallFootprint(home)
	if err != nil {
		t.Fatal(err)
	}
	if fp.Servers != 3 {
		t.Fatalf("Servers = %d, want 3", fp.Servers)
	}
	if fp.MasterBytes < vpk+cfg {
		t.Fatalf("MasterBytes = %d, want at least %d", fp.MasterBytes, vpk+cfg)
	}
	// Linked servers only add their gameinfo.gi; the full copy adds a VPK.
	// Allow for block rounding and directory entries, but a second or third
	// counting of the shared VPK must not fit.
	min := int64(2*cfg + vpk)
	max := int64(2*cfg+vpk) + vpk/2
	if fp.ServersBytes < min || fp.ServersBytes > max {
		t.Fatalf("ServersBytes = %d, want between %d and %d (shared VPK counted once, under master)", fp.ServersBytes, min, max)
	}

	// Missing home is not an error.
	empty, err := MeasureInstallFootprint(filepath.Join(home, "nope"))
	if err != nil || empty != (InstallFootprint{}) {
		t.Fatalf("missing home = %+v, %v; want zero, nil", empty, err)
	}
}
