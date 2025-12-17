package csm

import (
	"bytes"
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
	var buf bytes.Buffer
	var fileLog *os.File
	if logPath := strings.TrimSpace(os.Getenv("CSM_THUMBS_LOG")); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			fileLog = f
			defer fileLog.Close()
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

	masterDir := "/home/cs2servermanager/master-install"
	csgoDir := filepath.Join(masterDir, "game", "csgo")
	outputDir := filepath.Join(root, "extracted_csgo")
	thumbsDir := filepath.Join(root, "map_thumbnails")
	targetFolder := filepath.Join("panorama", "images", "map_icons", "screenshots", "1080p")

	if fi, err := os.Stat(masterDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("master install not found at %s", masterDir)
	}
	if fi, err := os.Stat(csgoDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("CSGO directory not found at %s", csgoDir)
	}

	log("════════════════════════════════════════════════════════")
	log("  Extract & Convert Map Thumbnails")
	log("════════════════════════════════════════════════════════")
	log("")

	// Ensure Python + vpk + Pillow are available.
	if err := ensurePythonModule("vpk"); err != nil {
		log("Python vpk module check failed: %v", err)
		log("Install with: pip3 install vpk --break-system-packages")
		return buf.String(), err
	}
	if err := ensurePythonModule("PIL.Image"); err != nil {
		log("Python Pillow (PIL) check failed: %v", err)
		log("Install with: pip3 install Pillow --break-system-packages")
		return buf.String(), err
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
	if _, err := os.Stat(extractPath); err == nil {
		log("[i] Extraction directory already exists at %s (will reuse)", extractPath)
	} else {
		log("[EXTRACT] pak01_dir.vpk ...")
		if err := extractVPKWithPython(targetVPK, extractPath, &buf); err != nil {
			log("[!] VPK extraction failed: %v", err)
			return buf.String(), err
		}
		log("[OK] Extracted pak01_dir.vpk")
	}

	// Find all vtex_c files in the 1080p screenshot folders.
	var vtexFiles []string
	err = filepath.WalkDir(extractPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".vtex_c" && strings.Contains(path, targetFolder) {
			vtexFiles = append(vtexFiles, path)
		}
		return nil
	})
	if err != nil {
		return buf.String(), err
	}

	if len(vtexFiles) == 0 {
		log("[i] No vtex_c thumbnail files found under %s", targetFolder)
		return buf.String(), nil
	}

	log("[*] Found %d thumbnail files to convert", len(vtexFiles))
	log("")

	var convertSuccess, convertFail int
	for _, vtex := range vtexFiles {
		base := strings.TrimSuffix(filepath.Base(vtex), ".vtex_c")

		// Skip numbered variants (e.g. de_dust2_1_png, or _png_<hash>).
		if numberedVariant(base) {
			continue
		}

		outName := strings.ReplaceAll(base, "_png", "") + ".png"
		pngPath := filepath.Join(thumbsDir, outName)
		webpPath := strings.TrimSuffix(pngPath, ".png") + ".webp"
		thumbWebpPath := strings.TrimSuffix(pngPath, ".png") + "_thumb.webp"

		// If all expected outputs already exist, skip this thumbnail
		// entirely; otherwise re-run the conversion so that any missing
		// WEBP variants are (re)generated alongside the PNG.
		if fileExists(pngPath) && fileExists(webpPath) && fileExists(thumbWebpPath) {
			log("[SKIP] %s (PNG + WEBP variants already present)", base)
			continue
		}

		log("[CONVERT] %s -> %s (and WEBP variants)...", base, filepath.Base(pngPath))
		if err := convertVtexWithPython(vtex, pngPath, &buf); err != nil {
			log("[FAIL] %s: %v", base, err)
			convertFail++
		} else {
			log("[OK] %s", base)
			convertSuccess++
		}
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

func extractVPKWithPython(vpkFile, outDir string, w *bytes.Buffer) error {
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
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		w.Write(out)
	}
	if err != nil {
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

    # Try to find embedded PNG.
    png_start = data.find(b"\x89PNG")
    if png_start != -1:
        png_data = data[png_start:]
        end_idx = png_data.find(b"IEND")
        if end_idx != -1:
            png_data = png_data[: end_idx + 8]
            img = Image.open(io.BytesIO(png_data))
            img.save(output_file, "PNG")
            save_webp_variants(img, output_file)
            return True

    # Fallback: try to read the whole blob as an image (TGA/other).
    if len(data) > 18:
        try:
            img = Image.open(io.BytesIO(data))
            img.save(output_file, "PNG")
            save_webp_variants(img, output_file)
            return True
        except Exception:
            pass

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

// fileExists returns true if the given path exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
