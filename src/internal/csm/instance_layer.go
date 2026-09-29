package csm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/readyup"
)

// The Ready Up layer
//
// A layer is what Ready Up's install.sh adds to a CS2 install, captured once
// and shared read-only by every instance:
//
//  1. A copy of the master install's directory skeleton, owned by the CS2
//     user. Overlayfs copies a lower directory up (with its owner) the first
//     time an instance writes below it; a directory owned by a uid the user
//     namespace cannot map would fail that, so the layer provides a mapped
//     copy of every directory.
//  2. install.sh run against a temporary overlay (lower = master install,
//     upper = the new layer), so exactly what it writes lands in the layer:
//     game/csgo/readyup/, cfg/ReadyUp/ and the patched gameinfo.gi.
//
// Layers live in instances/layers/<id>/ with <id>.json (what is in it) and
// <id>.src/ (the install.sh + bundle it was built from, so a CS2 update can
// rebuild the same Ready Up on the new gameinfo.gi). layers/current points
// at the one new starts use.

// LayerSource is what a layer is built from: install.sh plus a bundle zip
// (or a release tag install.sh downloads).
type LayerSource struct {
	Installer     string
	Zip           string
	Bundle        string // essentials | full
	Version       string // release tag, only without Zip
	AcceptLicense string
}

// LayerInfo is layers/<id>.json.
type LayerInfo struct {
	ID          string `json:"id"`
	Core        string `json:"core"`
	Bundle      string `json:"bundle,omitempty"`
	Tag         string `json:"tag,omitempty"`
	BuiltAt     string `json:"built_at"`
	MasterBuild int64  `json:"master_build,omitempty"`
	Zip         string `json:"zip,omitempty"` // file name inside <id>.src/
	Installer   string `json:"installer,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// errNoLayer is returned while no layer has been built yet.
var errNoLayer = errors.New("no Ready Up layer yet: build one with `csm instance layer build` (or `csm instance update`)")

// CurrentLayer is the directory layers/current points at.
func (m *InstanceManager) CurrentLayer() (string, error) {
	p, err := filepath.EvalSymlinks(m.L.CurrentLayerLink())
	if err != nil {
		return "", errNoLayer
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		return "", errNoLayer
	}
	return p, nil
}

// layerCore is the Ready Up core version installed in a layer ("" = none).
func layerCore(dir string) string {
	in, err := readyup.ReadInstalled(dir)
	if err != nil || in == nil {
		return ""
	}
	return in.Components["core"]
}

// ReadLayerInfo reads layers/<id>.json (falling back to installed.json).
func (m *InstanceManager) ReadLayerInfo(dir string) LayerInfo {
	info := LayerInfo{ID: filepath.Base(dir)}
	if data, err := os.ReadFile(dir + ".json"); err == nil {
		_ = json.Unmarshal(data, &info)
	}
	if info.Core == "" {
		info.Core = layerCore(dir)
	}
	return info
}

// ListLayers returns every built layer directory, oldest first.
func (m *InstanceManager) ListLayers() []string {
	entries, err := os.ReadDir(m.L.LayersDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".src") || name == "current" {
			continue
		}
		out = append(out, filepath.Join(m.L.LayersDir(), name))
	}
	sort.Strings(out)
	return out
}

// copySkeleton recreates every directory of src under dst (mode kept, plus
// owner rwx so the build can write into it). Files and symlinks are skipped.
func copySkeleton(src, dst string) (int, error) {
	n := 0
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != src {
				return fs.SkipDir // unreadable: nothing below it to mirror
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o755)
		if fi, err := d.Info(); err == nil {
			mode = fi.Mode().Perm() | 0o700
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(target, mode); err != nil {
			return err
		}
		_ = os.Chmod(target, mode)
		n++
		return nil
	})
	return n, err
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// lockLayerBuild serializes layer builds (CLI, monitor and agent).
func (m *InstanceManager) lockLayerBuild() (func(), error) {
	if err := m.mkdirs(m.L.LayersDir()); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(m.L.LayersDir(), ".build.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another Ready Up layer build is running")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// newLayerID is a sortable, unique layer name.
func (m *InstanceManager) newLayerID(now time.Time) string {
	base := now.UTC().Format("20060102-150405")
	id := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(filepath.Join(m.L.LayersDir(), id)); os.IsNotExist(err) {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

// layerBuildCmdline mounts a temporary overlay (lower master, upper the new
// layer) inside a fresh namespace and runs install.sh against it.
func layerBuildCmdline(master, upper, work, mnt string, install readyup.InstallArgs) (string, error) {
	opts, err := overlayMountOptions([]string{master}, upper, work)
	if err != nil {
		return "", err
	}
	if err := overlayPathOK(mnt); err != nil {
		return "", err
	}
	install.Dir = mnt
	inner := fmt.Sprintf("set -e; mount -t overlay overlay -o %s %s; %s", shellQuote(opts), shellQuote(mnt), install.Cmdline())
	return strings.Join(unshareArgv(), " ") + " bash -c " + shellQuote(inner), nil
}

// BuildLayer builds a new layer from src and makes it current. Running
// instances keep their layer until they restart.
func (m *InstanceManager) BuildLayer(ctx context.Context, w io.Writer, src LayerSource, reason string) (string, error) {
	if err := m.requireInstancePrivileges("instance layer build"); err != nil {
		return "", err
	}
	if src.Installer == "" {
		return "", errors.New("no install.sh to build the layer with")
	}
	if src.Zip == "" && src.Version == "" {
		return "", errors.New("no Ready Up bundle zip or version to build the layer from")
	}
	if src.Bundle == "" {
		src.Bundle = readyup.BundleEssentials
	}
	if _, err := os.Stat(filepath.Join(m.L.Master, "game", "csgo")); err != nil {
		return "", fmt.Errorf("no CS2 install at %s: %w", m.L.Master, err)
	}
	unlock, err := m.lockLayerBuild()
	if err != nil {
		return "", err
	}
	defer unlock()

	id := m.newLayerID(time.Now())
	layers := m.L.LayersDir()
	final := filepath.Join(layers, id)
	build := filepath.Join(layers, ".build-"+id)
	srcDir := final + ".src"
	upper, work, mnt := filepath.Join(build, "upper"), filepath.Join(build, "work"), filepath.Join(build, "mnt")
	defer func() { _ = removeTreeForce(build) }()
	if err := m.mkdirs(upper, work, mnt, srcDir); err != nil {
		return "", err
	}

	// Keep what the layer is built from, so a CS2 update can rebuild it.
	info := LayerInfo{ID: id, Bundle: src.Bundle, Tag: src.Version, BuiltAt: time.Now().UTC().Format(time.RFC3339),
		MasterBuild: m.MasterBuild(), Reason: reason, Installer: "install.sh"}
	if err := copyFile(src.Installer, filepath.Join(srcDir, "install.sh"), 0o755); err != nil {
		return "", fmt.Errorf("copy install.sh: %w", err)
	}
	install := readyup.InstallArgs{Installer: filepath.Join(srcDir, "install.sh"), Bundle: src.Bundle, Version: src.Version, AcceptLicense: src.AcceptLicense}
	if src.Zip != "" {
		info.Zip = filepath.Base(src.Zip)
		if err := copyFile(src.Zip, filepath.Join(srcDir, info.Zip), 0o644); err != nil {
			return "", fmt.Errorf("copy bundle: %w", err)
		}
		if sums := filepath.Join(filepath.Dir(src.Zip), "SHA256SUMS"); fileExists(sums) {
			_ = copyFile(sums, filepath.Join(srcDir, "SHA256SUMS"), 0o644)
		}
		install.Zip = filepath.Join(srcDir, info.Zip)
		install.Version = ""
	}

	fmt.Fprintf(w, "[Ready Up layer] %s: copying the directory skeleton of %s...\n", id, m.L.Master)
	nDirs, err := copySkeleton(m.L.Master, upper)
	if err != nil {
		return "", fmt.Errorf("skeleton: %w", err)
	}
	fmt.Fprintf(w, "[Ready Up layer] %d directories\n", nDirs)
	// install.sh patches gameinfo.gi and chowns the result to the original's
	// owner; a master file owned by an unmapped uid would make that fail, so
	// the layer starts with the user's own copies (patched on top).
	for _, gi := range []string{"gameinfo.gi", "gameinfo_branchspecific.gi"} {
		from := filepath.Join(m.L.Master, "game", "csgo", gi)
		fi, err := os.Stat(from)
		if err != nil {
			continue
		}
		to := filepath.Join(upper, "game", "csgo", gi)
		if err := copyFile(from, to, fi.Mode().Perm()|0o600); err != nil {
			return "", fmt.Errorf("copy %s: %w", gi, err)
		}
		_ = os.Chtimes(to, fi.ModTime(), fi.ModTime())
	}
	if canChown() {
		if out, err := exec.Command("chown", "-R", m.L.User+":"+m.L.User, build, srcDir).CombinedOutput(); err != nil {
			return "", fmt.Errorf("chown %s: %v %s", build, err, strings.TrimSpace(string(out)))
		}
	}

	cmdline, err := layerBuildCmdline(m.L.Master, upper, work, mnt, install)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(w, "[Ready Up layer] %s: running install.sh (%s) in a temporary overlay...\n", id, src.Bundle)
	if err := runReadyUpInstaller(ctx, m.L.User, cmdline, w); err != nil {
		_ = removeTreeForce(srcDir)
		return "", fmt.Errorf("install.sh in the layer build failed: %w", err)
	}
	info.Core = layerCore(upper)
	if info.Core == "" {
		_ = removeTreeForce(srcDir)
		return "", errors.New("install.sh finished but the layer has no readyup/installed.json")
	}
	if gi, err := os.ReadFile(filepath.Join(upper, "game", "csgo", "gameinfo.gi")); err != nil || !bytes.Contains(gi, []byte("csgo/readyup")) {
		_ = removeTreeForce(srcDir)
		return "", errors.New("install.sh finished but gameinfo.gi in the layer has no csgo/readyup line")
	}
	if err := os.Rename(upper, final); err != nil {
		_ = removeTreeForce(srcDir)
		return "", err
	}
	if err := writeJSONAtomic(final+".json", info); err != nil {
		return "", err
	}
	m.own(final + ".json")
	if err := m.setCurrentLayer(id); err != nil {
		return "", err
	}
	fmt.Fprintf(w, "[✓] Ready Up layer %s (core %s) is current. Instances pick it up when they restart.\n", id, info.Core)
	m.gcLayers(w)
	return final, nil
}

// setCurrentLayer points layers/current at id (atomic rename of a symlink).
func (m *InstanceManager) setCurrentLayer(id string) error {
	if _, err := os.Stat(filepath.Join(m.L.LayersDir(), id)); err != nil {
		return fmt.Errorf("layer %s: %w", id, err)
	}
	tmp := m.L.CurrentLayerLink() + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(id, tmp); err != nil {
		return err
	}
	if canChown() {
		_ = exec.Command("chown", "-h", m.L.User+":"+m.L.User, tmp).Run()
	}
	return os.Rename(tmp, m.L.CurrentLayerLink())
}

// UseLayer makes an existing layer current (a rollback).
func (m *InstanceManager) UseLayer(id string) error {
	if err := m.requireInstancePrivileges("instance layer use"); err != nil {
		return err
	}
	if id == "" || strings.ContainsAny(id, "/\\") || strings.HasPrefix(id, ".") {
		return fmt.Errorf("not a layer id: %q", id)
	}
	return m.setCurrentLayer(id)
}

// layersInUse are the layers running instances were mounted with.
func (m *InstanceManager) layersInUse() map[string]bool {
	used := map[string]bool{}
	for _, n := range m.List() {
		if p := m.LayerInUse(n); p != "" && m.IsRunning(n) {
			used[filepath.Clean(p)] = true
		}
	}
	return used
}

// gcLayers removes layers nothing uses: not current, not mounted by a
// running instance, and not the newest previous one (kept for rollback).
func (m *InstanceManager) gcLayers(w io.Writer) {
	current, _ := m.CurrentLayer()
	used := m.layersInUse()
	all := m.ListLayers()
	keptPrevious := false
	for i := len(all) - 1; i >= 0; i-- {
		d := filepath.Clean(all[i])
		if d == filepath.Clean(current) || used[d] {
			continue
		}
		if !keptPrevious {
			keptPrevious = true
			continue
		}
		if err := removeTreeForce(d); err == nil {
			_ = removeTreeForce(d + ".src")
			_ = os.Remove(d + ".json")
			fmt.Fprintf(w, "[Ready Up layer] removed unused layer %s\n", filepath.Base(d))
		}
	}
}

// RebuildLayer builds a fresh layer from the current layer's own sources
// (same Ready Up), e.g. after a CS2 update replaced gameinfo.gi.
func (m *InstanceManager) RebuildLayer(ctx context.Context, w io.Writer, reason string) (string, error) {
	cur, err := m.CurrentLayer()
	if err != nil {
		return "", err
	}
	info := m.ReadLayerInfo(cur)
	src := LayerSource{Installer: filepath.Join(cur+".src", "install.sh"), Bundle: info.Bundle, Version: info.Tag}
	if info.Zip != "" {
		src.Zip = filepath.Join(cur+".src", info.Zip)
		src.Version = ""
	}
	if !fileExists(src.Installer) || (src.Zip != "" && !fileExists(src.Zip)) {
		return "", fmt.Errorf("layer %s has no saved sources to rebuild from; run `csm instance update`", info.ID)
	}
	if s, err := LoadPluginSettings(); err == nil {
		src.AcceptLicense = s.Resolved().AcceptLicense
	}
	return m.BuildLayer(ctx, w, src, reason)
}

// BuildLayerFromRelease downloads the configured Ready Up release (csm
// plugins channel/version/bundle/license) and builds a layer from it.
func (m *InstanceManager) BuildLayerFromRelease(ctx context.Context, w io.Writer, reason string) (string, error) {
	s, err := LoadPluginSettings()
	if err != nil {
		return "", err
	}
	b, err := ReadyUpPlanFor(ctx, w, s)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(b.Dir)
	return m.BuildLayer(ctx, w, LayerSource{Installer: b.Installer, Zip: b.Zip, Bundle: b.Name, AcceptLicense: s.Resolved().AcceptLicense}, reason+" "+b.Tag)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// --- shadowed update files -------------------------------------------------------

// LayerShadow is a layer file an instance hides with its own copy (or
// deleted), so a new layer's version of it never reaches that instance.
type LayerShadow struct {
	Path    string // relative to the server root, e.g. game/csgo/cfg/ReadyUp/live.cfg
	Deleted bool   // the instance deleted it (an overlay whiteout)
}

// instanceOwnedFiles are layer files an instance is meant to override.
var instanceOwnedFiles = map[string]bool{
	"game/csgo/cfg/ReadyUp/fleet.cfg": true, // per-instance fleet link (csm host agent)
}

// isWhiteout: overlayfs marks a deleted lower file with a 0:0 char device.
func isWhiteout(mode fs.FileMode, rdev uint64) bool {
	return mode&fs.ModeCharDevice != 0 && mode&fs.ModeDevice != 0 && rdev == 0
}

// findShadows lists the regular files of layer that upper overrides with
// different content (or deletes). Files csm keeps per instance are skipped.
func findShadows(layer, upper string) ([]LayerShadow, error) {
	var out []LayerShadow
	err := filepath.WalkDir(layer, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(layer, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if instanceOwnedFiles[rel] {
			return nil
		}
		up := filepath.Join(upper, filepath.FromSlash(rel))
		fi, err := os.Lstat(up)
		if err != nil {
			return nil
		}
		if fi.Mode().IsRegular() {
			if !sameFileContent(p, up) {
				out = append(out, LayerShadow{Path: rel})
			}
			return nil
		}
		var rdev uint64
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			rdev = uint64(st.Rdev)
		}
		if isWhiteout(fi.Mode(), rdev) {
			out = append(out, LayerShadow{Path: rel, Deleted: true})
		}
		return nil
	})
	return out, err
}

func sameFileContent(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil || fa.Size() != fb.Size() {
		return false
	}
	da, err1 := os.ReadFile(a)
	db, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && bytes.Equal(da, db)
}

// Shadows lists instance n's shadowing of the current layer.
func (m *InstanceManager) Shadows(n int) ([]LayerShadow, error) {
	cur, err := m.CurrentLayer()
	if err != nil {
		return nil, err
	}
	return findShadows(cur, m.L.Upper(n))
}

// describeShadows is the warning for an instance's shadowed files.
func describeShadows(n int, sh []LayerShadow) string {
	if len(sh) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[!] instance %d has its own copy of %d Ready Up file(s); the shared layer's version (and its updates) does not reach it:\n", n, len(sh))
	for _, s := range sh {
		if s.Deleted {
			fmt.Fprintf(&b, "      %s (deleted in the instance)\n", s.Path)
		} else {
			fmt.Fprintf(&b, "      %s\n", s.Path)
		}
	}
	b.WriteString("    Keep instance settings in cfg/" + instanceCustomCfgName + " instead, and remove the copies from the instance's upper/ to follow updates.\n")
	return b.String()
}
