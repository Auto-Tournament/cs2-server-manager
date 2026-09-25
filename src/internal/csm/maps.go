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

// convertVtexWithPython converts one .vtex_c into PNG + WEBP variants. On
// failure the error carries the last line Python printed, so the progress
// line can say why.
func convertVtexWithPython(ctx context.Context, py, vtexFile, outPath string) error {

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

	cmd := exec.CommandContext(ctx, py, "-c", script, vtexFile, outPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if last := lastLine(string(out)); last != "" {
			return fmt.Errorf("%s (%v)", last, err)
		}
		return fmt.Errorf("python vtex conversion failed: %w", err)
	}
	return nil
}

// lastLine returns the last non-empty line of s.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
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
