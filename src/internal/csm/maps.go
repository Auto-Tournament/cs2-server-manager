package csm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ExtractMapThumbnails locates CS2 VPKs that contain the 1080p map
// screenshot assets, extracts them into the repo-local "extracted_csgo/"
// directory and converts the *.vtex_c thumbnails into a small image set in
// "map_thumbnails/":
//
//   - original PNG (source-resolution)
//   - full-size WEBP
//   - 1280px-wide WEBP thumbnail (height is scaled to preserve aspect ratio)
//
// It mirrors the behaviour of the previous extract_map_data.sh +
// convert_map_thumbnails.sh pipeline, with WEBP variants added for web
// consumption.
func ExtractMapThumbnails() (string, error) {
	return ExtractMapThumbnailsWithContext(context.Background())
}

// ExtractMapThumbnailsWithContext is like ExtractMapThumbnails but accepts a
// context so long-running Python and filesystem operations can be cancelled
// from the TUI.
func ExtractMapThumbnailsWithContext(ctx context.Context) (string, error) {
	var buf bytes.Buffer
	var fileLog *os.File
	if logPath := strings.TrimSpace(os.Getenv("CSM_THUMBS_LOG")); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			fileLog = f
			defer func() {
				if cerr := fileLog.Close(); cerr != nil {
					fmt.Fprintf(os.Stderr, "CSM_THUMBS_LOG close failed: %v\n", cerr)
				}
			}()
		}
	}
	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !strings.HasSuffix(format, "\n") {
			buf.WriteByte('\n')
		}
		if fileLog != nil {
			fmt.Fprintf(fileLog, format, args...)
			if !strings.HasSuffix(format, "\n") {
				_, _ = fileLog.Write([]byte{'\n'})
			}
		}
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
	targetFolder := filepath.Join("panorama", "images", "map_icons", "screenshots", "1080p")

	if fi, err := os.Stat(masterDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("master install not found at %s (CS2_USER=%s)", masterDir, cs2User)
	}
	if fi, err := os.Stat(csgoDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("CSGO directory not found at %s", csgoDir)
	}

	log("════════════════════════════════════════════════════════")
	log("  Extract & Convert Map Thumbnails")
	log("════════════════════════════════════════════════════════")
	log("")

	// Ensure Python + vpk + Pillow are available up front so users get a single
	// actionable install command instead of one failure per missing module.
	var missing []string
	if err := ensurePythonModule("vpk"); err != nil {
		log("Python vpk module check failed: %v", err)
		missing = append(missing, "vpk")
	}
	if err := ensurePythonModule("PIL.Image"); err != nil {
		log("Python Pillow (PIL) check failed: %v", err)
		missing = append(missing, "Pillow")
	}
	if len(missing) > 0 {
		log("")
		log("One or more required Python modules are missing for map thumbnail extraction:")
		for _, mname := range missing {
			log("  - %s", mname)
		}
		log("")
		log("Install them with (Debian/Ubuntu with PEP 668):")
		log("  sudo pip3 install --break-system-packages vpk Pillow")
		return buf.String(), fmt.Errorf("missing Python modules: %s", strings.Join(missing, ", "))
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(thumbsDir, 0o755); err != nil {
		return "", err
	}

	log("Master install:          %s", masterDir)
	log("CSGO directory:          %s", csgoDir)
	log("Extracted VPK contents:  %s", outputDir)
	log("Thumbnail output (PNG + WEBP): %s", thumbsDir)
	log("")

	// For now we only target pak01_dir.vpk which contains the working thumbnails.
	targetVPK := filepath.Join(csgoDir, "pak01_dir.vpk")
	if fi, err := os.Stat(targetVPK); err != nil || fi.IsDir() {
		return buf.String(), fmt.Errorf("target VPK not found at %s", targetVPK)
	}

	extractPath := filepath.Join(outputDir, "pak01_dir")
	reusedExtraction := false
	if _, err := os.Stat(extractPath); err == nil {
		log("[i] Extraction directory already exists at %s (will reuse)", extractPath)
		reusedExtraction = true
	} else {
		log("[EXTRACT] pak01_dir.vpk ...")
		// Stream VPK extraction progress/logs into both the in-memory buffer
		// and, when configured, the CSM_THUMBS_LOG file so the TUI can tail
		// progress in real time.
		var w io.Writer = &buf
		if fileLog != nil {
			w = &teeWriter{buf: &buf, file: fileLog}
		}
		if err := extractVPKWithPython(targetVPK, extractPath, w); err != nil {
			log("[!] VPK extraction failed: %v", err)
			return buf.String(), err
		}
		log("[OK] Extracted pak01_dir.vpk")
	}

	// Find all vtex_c files in the 1080p screenshot folders.
	findVtex := func() ([]string, error) {
		var files []string
		err := filepath.WalkDir(extractPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if filepath.Ext(path) == ".vtex_c" && strings.Contains(path, targetFolder) {
				files = append(files, path)
			}
			return nil
		})
		return files, err
	}

	// Honour cancellation before expensive directory walks.
	select {
	case <-ctx.Done():
		return buf.String(), ctx.Err()
	default:
	}

	vtexFiles, err := findVtex()
	if err != nil {
		return buf.String(), err
	}

	// If we reused a previous extraction but found no thumbnails, it's very
	// likely that a previous run failed partway through (for example, due to
	// disk space) and left a partial tree behind. In that case, re-extract
	// pak01_dir.vpk from scratch once before giving up.
	if reusedExtraction && len(vtexFiles) == 0 {
		log("[i] No vtex_c thumbnail files found under %s in existing extraction.", targetFolder)
		log("[i] Re-extracting pak01_dir.vpk from scratch in case a previous run was incomplete...")
		if err := os.RemoveAll(extractPath); err != nil {
			log("[!] Failed to remove existing extraction directory: %v", err)
			return buf.String(), err
		}
		log("[EXTRACT] pak01_dir.vpk ...")
		var w io.Writer = &buf
		if fileLog != nil {
			w = &teeWriter{buf: &buf, file: fileLog}
		}
		if err := extractVPKWithPython(targetVPK, extractPath, w); err != nil {
			log("[!] VPK extraction failed: %v", err)
			return buf.String(), err
		}
		log("[OK] Extracted pak01_dir.vpk")

		vtexFiles, err = findVtex()
		if err != nil {
			return buf.String(), err
		}
	}

	if len(vtexFiles) == 0 {
		log("[i] No vtex_c thumbnail files found under %s", targetFolder)
		return buf.String(), nil
	}

	// Filter out numbered variants up-front so our progress bar accurately
	// reflects the number of distinct thumbnails we expect to generate.
	var targets []string
	for _, v := range vtexFiles {
		base := strings.TrimSuffix(filepath.Base(v), ".vtex_c")
		if numberedVariant(base) {
			continue
		}
		targets = append(targets, v)
	}
	if len(targets) == 0 {
		log("[i] Only numbered variant thumbnails found under %s; nothing to convert.", targetFolder)
		return buf.String(), nil
	}

	log("[*] Found %d thumbnail files to convert", len(targets))
	log("")

	// Temporary workspace for fresh conversions so we can compare against any
	// existing thumbnails and avoid rewriting files that are byte-identical.
	tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("csm-thumbnails-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return buf.String(), fmt.Errorf("failed to create temp thumbnail dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	var convertSuccess, convertFail int
	total := len(targets)

	for i, vtex := range targets {
		select {
		case <-ctx.Done():
			return buf.String(), ctx.Err()
		default:
		}
		base := strings.TrimSuffix(filepath.Base(vtex), ".vtex_c")

		outName := strings.ReplaceAll(base, "_png", "") + ".png"
		finalPNG := filepath.Join(thumbsDir, outName)
		finalWEBP := strings.TrimSuffix(finalPNG, ".png") + ".webp"
		finalThumbWEBP := strings.TrimSuffix(finalPNG, ".png") + "_thumb.webp"

		tmpPNG := filepath.Join(tmpDir, outName)
		tmpWEBP := strings.TrimSuffix(tmpPNG, ".png") + ".webp"
		tmpThumbWEBP := strings.TrimSuffix(tmpPNG, ".png") + "_thumb.webp"

		// Generate fresh PNG + WEBP variants into the temp workspace.
		log("[CONVERT] %s -> %s (temp workspace)...", base, filepath.Base(tmpPNG))
		if err := convertVtexWithPython(vtex, tmpPNG, &buf); err != nil {
			log("[FAIL] %s: %v", base, err)
			convertFail++
			continue
		}

		// Sync each derived asset into the final thumbnails directory only
		// when the bytes differ, so unchanged images don't get rewritten (and
		// won't show up as noisy changes in downstream tooling).
		changed := false

		if ch, err := syncIfDifferent(tmpPNG, finalPNG); err != nil {
			log("[FAIL] %s (sync PNG): %v", base, err)
			convertFail++
			continue
		} else if ch {
			changed = true
		}

		if ch, err := syncIfDifferent(tmpWEBP, finalWEBP); err != nil {
			log("[FAIL] %s (sync WEBP): %v", base, err)
			convertFail++
			continue
		} else if ch {
			changed = true
		}

		if ch, err := syncIfDifferent(tmpThumbWEBP, finalThumbWEBP); err != nil {
			log("[FAIL] %s (sync thumb WEBP): %v", base, err)
			convertFail++
			continue
		} else if ch {
			changed = true
		}

		if changed {
			log("[OK] %s (updated thumbnails written)", base)
		} else {
			log("[UNCHANGED] %s (existing thumbnails identical; no updates written)", base)
		}
		convertSuccess++

		// Emit a coarse-grained progress line that the TUI can parse into a
		// log-based progress bar (via parsePercentFromLine).
		processed := i + 1
		pct := int(float64(processed) / float64(total) * 100.0)
		if pct > 100 {
			pct = 100
		}
		log("[THUMBS] Progress: %d%% (%d/%d)", pct, processed, total)
	}

	log("")
	log("Converted thumbnails: %d, Failed: %d", convertSuccess, convertFail)
	log("Thumbnails (PNG + WEBP + 1280px WEBP) saved to: %s", thumbsDir)

	// Clean up numbered variant PNGs (e.g. de_dust2_1.png).
	removed := 0
	_ = filepath.WalkDir(thumbsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".png" {
			return nil
		}
		name := strings.TrimSuffix(filepath.Base(path), ".png")
		if isNumberedSuffix(name) {
			_ = os.Remove(path)
			removed++
		}
		return nil
	})
	if removed > 0 {
		log("Removed %d numbered variant PNGs", removed)
	}

	// Best-effort cleanup of the extracted VPK contents to free disk space;
	// we only keep the small derived thumbnails in map_thumbnails/.
	log("[i] Cleaning up extracted VPK contents at %s", extractPath)
	if err := os.RemoveAll(extractPath); err != nil {
		log("[!] Failed to clean up extracted VPK contents: %v", err)
	}

	return buf.String(), nil
}

// PublicIP resolves the machine's public IP address by querying a small set
// of external services. It returns a single IP string or an error.
func PublicIP() (string, error) {
	services := []string{
		"https://api4.ipify.org",        // IPv4-only
		"https://ipv4.icanhazip.com",    // IPv4-only
		"https://ifconfig.me/ip",        // May return v4 or v6
		"https://checkip.amazonaws.com", // May return v4 or v6
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	var anyIP string

	for _, svc := range services {
		req, err := http.NewRequest("GET", svc, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		body := string(data)

		// Prefer an IPv4 address if possible.
		if ip4 := extractIPv4(body); ip4 != "" {
			return ip4, nil
		}

		// Otherwise remember any valid IP as a fallback.
		if anyIP == "" {
			if ipAny := extractIP(body); ipAny != "" {
				anyIP = ipAny
			}
		}
	}

	if anyIP != "" {
		return anyIP, nil
	}

	return "", fmt.Errorf("failed to resolve public IP from known services")
}

// extractIP scans a response body and returns the first substring that parses
// as a valid IP address (IPv4 or IPv6).
func extractIP(body string) string {
	// Fast path: a clean body with just the IP.
	trimmed := strings.TrimSpace(body)
	if ip := net.ParseIP(trimmed); ip != nil {
		return ip.String()
	}

	// Fallback: scan tokens and strip common delimiters.
	for _, tok := range strings.Fields(body) {
		clean := strings.Trim(tok, " \t\r\n<>\",;:'[](){}")
		if ip := net.ParseIP(clean); ip != nil {
			return ip.String()
		}
	}
	return ""
}

// extractIPv4 scans a response body and returns the first substring that parses
// specifically as an IPv4 address.
func extractIPv4(body string) string {
	trimmed := strings.TrimSpace(body)
	if ip := net.ParseIP(trimmed); ip != nil && ip.To4() != nil {
		return ip.String()
	}

	for _, tok := range strings.Fields(body) {
		clean := strings.Trim(tok, " \t\r\n<>\",;:'[](){}")
		if ip := net.ParseIP(clean); ip != nil && ip.To4() != nil {
			return ip.String()
		}
	}
	return ""
}

func ensurePythonModule(mod string) error {
	py, err := exec.LookPath("python3")
	if err != nil {
		if py, err = exec.LookPath("python"); err != nil {
			return fmt.Errorf("python3/python not found in PATH")
		}
	}
	code := fmt.Sprintf("import %s", mod)
	cmd := exec.Command(py, "-c", code)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("python module %q not available", mod)
	}
	return nil
}

func extractVPKWithPython(vpkFile, outDir string, w io.Writer) error {
	py, err := exec.LookPath("python3")
	if err != nil {
		if py, err = exec.LookPath("python"); err != nil {
			return fmt.Errorf("python3/python not found in PATH")
		}
	}

	script := `
import vpk
import os
import sys

vpk_file = sys.argv[1]
output_path = sys.argv[2]

pak = vpk.open(vpk_file)
file_count = 0
for filepath in pak:
    data = pak[filepath]
    full_output_path = os.path.join(output_path, filepath)
    os.makedirs(os.path.dirname(full_output_path), exist_ok=True)
    with open(full_output_path, 'wb') as f:
        f.write(data.read())
    file_count += 1

print(f"Extracted {file_count} files from {vpk_file}")
`

	cmd := exec.Command(py, "-c", script, vpkFile, outDir)
	// Stream Python stdout/stderr directly into the provided writer so that
	// long-running VPK extraction progress is visible in real time to both
	// CLI and TUI (via CSM_THUMBS_LOG when set).
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("python vpk extraction failed: %w", err)
	}
	return nil
}

func convertVtexWithPython(vtexFile, outPath string, w *bytes.Buffer) error {
	py, err := exec.LookPath("python3")
	if err != nil {
		if py, err = exec.LookPath("python"); err != nil {
			return fmt.Errorf("python3/python not found in PATH")
		}
	}

	script := `
import io
import os
import sys
from PIL import Image


def save_webp_variants(img, png_output_file, thumb_width=1280):
    """
    Save a full-size WEBP next to the PNG plus a 1280px-wide WEBP thumbnail.
    Failures here are logged but do not cause the overall conversion to fail
    as long as the PNG was written successfully.
    """
    try:
        base, _ = os.path.splitext(png_output_file)
        webp_path = base + ".webp"
        thumb_path = base + "_thumb.webp"

        # Work in RGB to avoid palette/alpha edge cases when saving as WEBP.
        full = img.convert("RGB")
        full.save(webp_path, "WEBP")

        # Build a 1280px-wide thumbnail while preserving aspect ratio.
        w, h = full.size
        if w > 0 and h > 0:
            if w <= thumb_width:
                thumb = full
            else:
                new_h = int(h * (thumb_width / float(w)))
                thumb = full.resize((thumb_width, new_h), Image.LANCZOS)
            thumb.save(thumb_path, "WEBP")
    except Exception as e:
        print(f"Warning: WEBP conversion failed for {png_output_file}: {e}")


def extract_vtex_image(vtex_file, output_file):
    with open(vtex_file, "rb") as f:
        data = f.read()

    # Try to find embedded PNG and preserve it byte-for-byte.
    png_start = data.find(b"\x89PNG")
    if png_start != -1:
        png_data = data[png_start:]
        end_idx = png_data.find(b"IEND")
        if end_idx != -1:
            png_data = png_data[: end_idx + 8]
            try:
                # Write the original embedded PNG bytes directly so we keep the
                # exact asset shipped by the game (no recompression).
                with open(output_file, "wb") as f:
                    f.write(png_data)

                # Use the decoded image only for derived WEBP variants.
                img = Image.open(io.BytesIO(png_data))
                save_webp_variants(img, output_file)
                return True
            except Exception as e:
                print(f"ERROR: embedded PNG could not be decoded for {vtex_file}: {e}")
                return False

    # Fallback: try to read the whole blob as an image (TGA/other).
    if len(data) > 18:
        try:
            img = Image.open(io.BytesIO(data))
            img.save(output_file, "PNG")
            save_webp_variants(img, output_file)
            return True
        except Exception as e:
            print(f"ERROR: VTEX payload is not a supported image format for {vtex_file}: {e}")

    print(f"ERROR: no embedded PNG or decodable image found in {vtex_file}")
    return False


vtex_file = sys.argv[1]
output_file = sys.argv[2]

if extract_vtex_image(vtex_file, output_file):
    print(f"Converted: {vtex_file} -> {output_file} (+ WEBP variants)")
    sys.exit(0)
else:
    sys.exit(1)
`

	cmd := exec.Command(py, "-c", script, vtexFile, outPath)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		w.Write(out)
	}
	if err != nil {
		return fmt.Errorf("python vtex conversion failed: %w", err)
	}
	return nil
}

func numberedVariant(base string) bool {
	// Skip names ending in _<number>_png or _png_<hex>.
	if strings.HasSuffix(base, "_png") {
		return false
	}
	if idx := strings.LastIndex(base, "_"); idx != -1 {
		suffix := base[idx+1:]
		allDigits := len(suffix) > 0
		for _, r := range suffix {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return true
		}
	}
	return false
}

func isNumberedSuffix(name string) bool {
	// Returns true if the name ends with _<number>.
	if idx := strings.LastIndex(name, "_"); idx != -1 {
		suffix := name[idx+1:]
		if suffix == "" {
			return false
		}
		for _, r := range suffix {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// syncIfDifferent moves or copies src into dst only if the file contents differ.
// It returns (true, nil) when dst was updated, (false, nil) when dst was left
// unchanged (including when src does not exist), or (false, err) on error.
func syncIfDifferent(src, dst string) (bool, error) {
	// If the source file doesn't exist (e.g. WEBP variants when conversion
	// failed), there's nothing to sync.
	if fi, err := os.Stat(src); err != nil || fi.IsDir() {
		return false, nil
	}

	// If the destination exists and is byte-identical, skip updating it to
	// avoid noisy changes.
	if equal, err := filesEqual(src, dst); err != nil {
		return false, err
	} else if equal {
		_ = os.Remove(src)
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}

	// Prefer a simple rename; if that fails due to cross-filesystem issues,
	// fall back to a copy+remove.
	if err := os.Rename(src, dst); err == nil {
		return true, nil
	}

	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = in.Close()
	}()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return false, err
	}
	_ = os.Remove(src)
	return true, nil
}

// filesEqual returns true when both files exist, are regular files, and have
// identical size and contents.
func filesEqual(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil || ai.IsDir() {
		return false, err
	}
	bi, err := os.Stat(b)
	if os.IsNotExist(err) || bi.IsDir() {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ai.Size() != bi.Size() {
		return false, nil
	}

	f1, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = f1.Close()
	}()

	f2, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = f2.Close()
	}()

	buf1 := make([]byte, 32*1024)
	buf2 := make([]byte, 32*1024)

	for {
		n1, e1 := f1.Read(buf1)
		n2, e2 := f2.Read(buf2)

		if n1 != n2 || !bytes.Equal(buf1[:n1], buf2[:n2]) {
			return false, nil
		}
		if e1 == io.EOF && e2 == io.EOF {
			break
		}
		if e1 != nil && e1 != io.EOF {
			return false, e1
		}
		if e2 != nil && e2 != io.EOF {
			return false, e2
		}
	}
	return true, nil
}
