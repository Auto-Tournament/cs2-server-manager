package csm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// DiskEstimate is the space a master-install plus a number of servers needs.
type DiskEstimate struct {
	MasterGB    float64 // full master-install
	PerServerGB float64 // what each server-N adds on top of master
	Servers     int
	TotalGB     float64 // MasterGB + PerServerGB*Servers
	Hardlinked  bool    // VPKs are hardlinked from master (CSM_VPK_HARDLINK not 0)
}

// EstimateInstallDisk returns the disk space for master-install plus servers
// server directories. masterGB is the measured size of master-install (<= 0
// uses DefaultMasterDiskGB). With VPK hardlinks (the default) each server only
// adds its non-VPK files, about DefaultPerServerLinkedDiskGB; with
// CSM_VPK_HARDLINK=0 each server is a full copy of master.
func EstimateInstallDisk(masterGB float64, servers int, hardlink bool) DiskEstimate {
	if masterGB <= 0 {
		masterGB = DefaultMasterDiskGB
	}
	if servers < 0 {
		servers = 0
	}
	per := masterGB
	if hardlink {
		per = DefaultPerServerLinkedDiskGB
	}
	return DiskEstimate{
		MasterGB:    masterGB,
		PerServerGB: per,
		Servers:     servers,
		TotalGB:     masterGB + per*float64(servers),
		Hardlinked:  hardlink,
	}
}

// InstallFootprint is the space master-install and the server-N directories
// under a CS2 user's home use today.
type InstallFootprint struct {
	MasterBytes int64
	// ServersBytes counts only what the servers add on top of master: a VPK
	// hardlinked to master (or between servers) is counted once, under the
	// first tree that has it.
	ServersBytes int64
	Servers      int
}

// MeasureInstallFootprint measures master-install and every server-N under
// home. Each inode is counted once, so hardlinked VPKs are attributed to
// master-install and not charged to every server again (running du on each
// directory separately would count them once per server).
func MeasureInstallFootprint(home string) (InstallFootprint, error) {
	var fp InstallFootprint
	seen := make(map[inodeKey]struct{})

	master := filepath.Join(home, "master-install")
	if fi, err := os.Stat(master); err == nil && fi.IsDir() {
		b, err := allocatedBytes(master, seen)
		if err != nil {
			return fp, err
		}
		fp.MasterBytes = b
	}

	entries, err := os.ReadDir(home)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fp, nil
		}
		return fp, err
	}
	var servers []int
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "server-") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "server-")); err == nil && n > 0 {
			servers = append(servers, n)
		}
	}
	sort.Ints(servers)
	for _, n := range servers {
		b, err := allocatedBytes(filepath.Join(home, "server-"+strconv.Itoa(n)), seen)
		if err != nil {
			return fp, err
		}
		fp.ServersBytes += b
		fp.Servers++
	}
	return fp, nil
}

type inodeKey struct{ dev, ino uint64 }

// allocatedBytes returns the allocated bytes under root for inodes not yet in
// seen, and adds them to seen. Symlinks are not followed.
func allocatedBytes(root string, seen map[inodeKey]struct{}) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			total += fi.Size()
			return nil
		}
		k := inodeKey{uint64(st.Dev), uint64(st.Ino)}
		if _, dup := seen[k]; dup {
			return nil
		}
		seen[k] = struct{}{}
		total += int64(st.Blocks) * 512
		return nil
	})
	return total, err
}
