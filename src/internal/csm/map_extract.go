package csm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// thumbnailVPKFolder is where pak01_dir.vpk keeps the 1080p map screenshots.
const thumbnailVPKFolder = "panorama/images/map_icons/screenshots/1080p/"

// MapDataOptions configures ExtractMapData.
type MapDataOptions struct {
	// Progress, when set, receives every log line as soon as it is written
	// (the CLI passes os.Stdout). The TUI tails CSM_THUMBS_LOG instead.
	Progress io.Writer
}

// MapDataResult summarises an ExtractMapData run.
type MapDataResult struct {
	ThumbsDir    string
	PatchVersion string
	Maps         int
	ActiveDuty   []string
}

// ExtractMapThumbnails runs ExtractMapData without live progress.
func ExtractMapThumbnails() (string, error) {
	return ExtractMapThumbnailsWithContext(context.Background())
}

// ExtractMapThumbnailsWithContext is like ExtractMapThumbnails but accepts a
// context so the TUI can cancel it.
func ExtractMapThumbnailsWithContext(ctx context.Context) (string, error) {
	out, _, err := ExtractMapData(ctx, MapDataOptions{})
	return out, err
}

// multiLogWriter writes to every writer and ignores their errors, so a broken
// log file or closed stdout never stops the extraction. It is safe for the
// concurrent writes os/exec makes from a child's stdout and stderr.
type multiLogWriter struct {
	mu      sync.Mutex
	writers []io.Writer
}

func (m *multiLogWriter) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.writers {
		_, _ = w.Write(p)
	}
	return len(p), nil
}

// ExtractMapData pulls the 1080p map screenshots out of pak01_dir.vpk and
// converts them into map_thumbnails/ under the working directory:
//
//   - original PNG (source resolution)
//   - full-size WEBP
//   - 1280px-wide WEBP thumbnail
//
// It then writes map_thumbnails/maps.json listing every playable map (map
// VPKs in game/csgo/maps plus anything with a screenshot) and the Active Duty
// pool from gamemodes.txt. Every step is logged as it happens.
func ExtractMapData(ctx context.Context, opts MapDataOptions) (string, *MapDataResult, error) {
	start := time.Now()
	elapsed := func() string { return time.Since(start).Round(time.Second).String() }

	var buf bytes.Buffer
	out := &multiLogWriter{writers: []io.Writer{&buf}}
	if logPath := strings.TrimSpace(os.Getenv("CSM_THUMBS_LOG")); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			defer func() { _ = f.Close() }()
			out.writers = append(out.writers, f)
		}
	}
	if opts.Progress != nil {
		out.writers = append(out.writers, opts.Progress)
	}
	log := func(format string, args ...any) {
		s := fmt.Sprintf(format, args...)
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		_, _ = io.WriteString(out, s)
	}

	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	cs2User := getenvDefault("CS2_USER", DefaultCS2User)
	masterDir := filepath.Join("/home", cs2User, "master-install")
	csgoDir := filepath.Join(masterDir, "game", "csgo")
	outputDir := filepath.Join(root, "extracted_csgo")
	thumbsDir := filepath.Join(root, "map_thumbnails")

	if fi, err := os.Stat(masterDir); err != nil || !fi.IsDir() {
		err := fmt.Errorf("master install not found at %s (CS2_USER=%s)", masterDir, cs2User)
		log("[!] %v", err)
		return buf.String(), nil, err
	}
	if fi, err := os.Stat(csgoDir); err != nil || !fi.IsDir() {
		err := fmt.Errorf("CSGO directory not found at %s", csgoDir)
		log("[!] %v", err)
		return buf.String(), nil, err
	}

	log("════════════════════════════════════════════════════════")
	log("  Extract map thumbnails + maps.json")
	log("════════════════════════════════════════════════════════")
	log("Master install:  %s", masterDir)
	log("Output:          %s", thumbsDir)
	log("")

	// Find (or, as root, set up) a Python that has vpk + Pillow before doing
	// any work, so a missing module is one clear error.
	log("[1/4] Checking Python (vpk + Pillow) ...")
	py, err := resolveThumbnailPython(ctx, out, defaultPythonEnv())
	if err != nil {
		log("[!] Map thumbnail extraction needs Python with the vpk and Pillow modules: %v", err)
		return buf.String(), nil, err
	}
	log("      Python: %s", py)

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return buf.String(), nil, err
	}
	if err := os.MkdirAll(thumbsDir, 0o755); err != nil {
		return buf.String(), nil, err
	}

	targetVPK := filepath.Join(csgoDir, "pak01_dir.vpk")
	if fi, err := os.Stat(targetVPK); err != nil || fi.IsDir() {
		err := fmt.Errorf("target VPK not found at %s", targetVPK)
		log("[!] %v", err)
		return buf.String(), nil, err
	}

	// Only the screenshots and gamemodes.txt are extracted, not the whole
	// VPK, so a leftover directory from an older run is just stale.
	extractPath := filepath.Join(outputDir, "pak01_dir")
	_ = os.RemoveAll(extractPath)
	defer func() { _ = os.RemoveAll(extractPath) }()

	log("[2/4] Scanning %s (%s) ...", targetVPK, elapsed())
	if err := extractVPKWithPython(ctx, py, targetVPK, extractPath, out); err != nil {
		log("[!] VPK extraction failed: %v", err)
		return buf.String(), nil, err
	}

	var targets []string
	_ = filepath.WalkDir(extractPath, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".vtex_c" {
			return nil
		}
		if !strings.Contains(filepath.ToSlash(path), thumbnailVPKFolder) {
			return nil
		}
		if numberedVariant(strings.TrimSuffix(filepath.Base(path), ".vtex_c")) {
			return nil
		}
		targets = append(targets, path)
		return nil
	})
	log("      Found %d thumbnail sources (%s)", len(targets), elapsed())
	log("")

	var updated, unchanged, failed int
	if len(targets) > 0 {
		log("[3/4] Converting thumbnails ...")
		tmpDir, err := os.MkdirTemp("", "csm-thumbnails-")
		if err != nil {
			return buf.String(), nil, fmt.Errorf("failed to create temp thumbnail dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		total := len(targets)
		for i, vtex := range targets {
			if err := ctx.Err(); err != nil {
				log("[!] Cancelled after %d/%d (%s)", i, total, elapsed())
				return buf.String(), nil, err
			}
			base := strings.TrimSuffix(filepath.Base(vtex), ".vtex_c")
			name := strings.ReplaceAll(base, "_png", "")
			status := convertOneThumbnail(ctx, py, vtex, name, tmpDir, thumbsDir)
			switch {
			case strings.HasPrefix(status, "failed"):
				failed++
			case status == "unchanged":
				unchanged++
			default:
				updated++
			}
			// The NN% token feeds the TUI progress bar (parsePercentFromLine).
			log("[%d/%d] %s … %s · %d%% · %s", i+1, total, name, status, (i+1)*100/total, elapsed())
		}
		removeNumberedPNGs(thumbsDir)
		log("")
	} else {
		log("[3/4] No thumbnails to convert; writing maps.json from the map VPKs only")
	}

	log("[4/4] Writing %s ...", MapsManifestFile)
	in, warnings := readMapsManifestInput(masterDir, csgoDir, filepath.Join(extractPath, "gamemodes.txt"), thumbsDir)
	for _, w := range warnings {
		log("      [!] %s", w)
	}
	manifest := buildMapsManifest(in)
	written, err := writeMapsManifest(thumbsDir, manifest)
	if err != nil {
		log("[!] Writing %s failed: %v", MapsManifestFile, err)
		return buf.String(), nil, err
	}
	state := "updated"
	if !written {
		state = "unchanged"
	}
	log("      %s %s: %d maps, patch %s", MapsManifestFile, state, len(manifest.Maps), orUnknown(manifest.PatchVersion))
	log("      Active Duty: %s", orUnknown(strings.Join(manifest.ActiveDuty, ", ")))
	log("")
	log("Done in %s: %d updated, %d unchanged, %d failed; %d maps, %d in Active Duty.",
		elapsed(), updated, unchanged, failed, len(manifest.Maps), len(manifest.ActiveDuty))
	log("Output: %s", thumbsDir)

	return buf.String(), &MapDataResult{
		ThumbsDir:    thumbsDir,
		PatchVersion: manifest.PatchVersion,
		Maps:         len(manifest.Maps),
		ActiveDuty:   manifest.ActiveDuty,
	}, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// convertOneThumbnail converts vtex into tmpDir and syncs the PNG and WEBPs
// into thumbsDir when their bytes differ. It returns "updated", "unchanged"
// or "failed: <reason>".
func convertOneThumbnail(ctx context.Context, py, vtex, name, tmpDir, thumbsDir string) string {
	tmpPNG := filepath.Join(tmpDir, name+".png")
	if err := convertVtexWithPython(ctx, py, vtex, tmpPNG); err != nil {
		return "failed: " + strings.ReplaceAll(err.Error(), vtex, filepath.Base(vtex))
	}
	suffixes := []string{".png", ".webp", "_thumb.webp"}
	if isNumberedSuffix(name) {
		// Numbered variants (de_dust2_1) only keep their WEBPs.
		suffixes = suffixes[1:]
	}
	changed := false
	for _, suffix := range suffixes {
		ch, err := syncIfDifferent(filepath.Join(tmpDir, name+suffix), filepath.Join(thumbsDir, name+suffix))
		if err != nil {
			return fmt.Sprintf("failed: sync %s%s: %v", name, suffix, err)
		}
		changed = changed || ch
	}
	if changed {
		return "updated"
	}
	return "unchanged"
}

// removeNumberedPNGs deletes numbered variant PNGs (de_dust2_1.png) left by
// older csm versions; only their WEBPs are kept.
func removeNumberedPNGs(thumbsDir string) {
	entries, err := os.ReadDir(thumbsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && filepath.Ext(n) == ".png" && isNumberedSuffix(strings.TrimSuffix(n, ".png")) {
			_ = os.Remove(filepath.Join(thumbsDir, n))
		}
	}
}

// extractVPKScript extracts only the map screenshots and gamemodes.txt from a
// VPK. Extracting the whole pak01 took many minutes with no output, which is
// why this filters first and prints (flushed) progress as it goes.
const extractVPKScript = `
import os
import sys
import vpk

vpk_file, output_path, folder = sys.argv[1], sys.argv[2], sys.argv[3]
name = os.path.basename(vpk_file)

print(f"      Reading the {name} index ...", flush=True)
pak = vpk.open(vpk_file)
wanted = []
entries = 0
for path in pak:
    entries += 1
    p = path.replace("\\", "/")
    if (folder in p and p.endswith(".vtex_c")) or p.lower() == "gamemodes.txt":
        wanted.append(path)
print(f"      {name}: {entries} entries, {len(wanted)} to extract", flush=True)

total = len(wanted)
for i, path in enumerate(wanted, 1):
    dst = os.path.join(output_path, path)
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    with open(dst, "wb") as f:
        f.write(pak.get_file(path).read())
    if i % 25 == 0 or i == total:
        print(f"      extracted {i}/{total}", flush=True)
`

// extractVPKWithPython streams the Python child's stdout/stderr into w line
// by line as it runs.
func extractVPKWithPython(ctx context.Context, py, vpkFile, outDir string, w io.Writer) error {
	cmd := exec.CommandContext(ctx, py, "-u", "-c", extractVPKScript, vpkFile, outDir, thumbnailVPKFolder)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("python vpk extraction failed: %w", err)
	}
	return nil
}
