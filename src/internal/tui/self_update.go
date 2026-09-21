package tui

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type selfUpdateFinishedMsg struct {
	newVersion string
	err        error
}

// progressWriter wraps the target file and reports download progress as bytes
// are written: a percentage, or -1 when the total size is unknown.
type progressWriter struct {
	w          io.Writer
	total      int64
	written    int64
	onProgress func(percent int)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	if n > 0 && pw.onProgress != nil {
		pw.written += int64(n)
		if pw.total > 0 {
			percent := int(pw.written * 100 / pw.total)
			if percent > 100 {
				percent = 100
			}
			pw.onProgress(percent)
		} else {
			pw.onProgress(-1)
		}
	}
	return n, err
}

func runSelfUpdate(targetVersion string) tea.Cmd {
	return func() tea.Msg {
		exePath, err := downloadAndReplace(targetVersion, func(percent int) {
			send(selfUpdateProgressMsg{Percent: percent})
		})
		if err != nil {
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}

		// Try to restart CSM in-place with the new binary. On success this call
		// does not return. If it fails for some reason, fall back to telling the
		// user to restart manually via the selfUpdateFinishedMsg.
		if err := syscall.Exec(exePath, os.Args, os.Environ()); err != nil {
			return selfUpdateFinishedMsg{
				newVersion: targetVersion,
				err:        fmt.Errorf("update installed, but failed to restart automatically: %w", err),
			}
		}

		// Not reached on success.
		return selfUpdateFinishedMsg{newVersion: targetVersion, err: nil}
	}
}

// SelfUpdateCLI is `csm self-update`: it checks GitHub for the latest release
// and, when it is newer than this binary, downloads it over the running one.
// Progress and the result are written to w.
func SelfUpdateCLI(w io.Writer) error {
	latest, err := fetchLatestVersion()
	if err != nil {
		return fmt.Errorf("could not check for updates: %w", err)
	}
	if !isNewerVersion(currentVersion, latest) {
		fmt.Fprintf(w, "csm %s is the latest version.\n", currentVersion)
		return nil
	}
	fmt.Fprintf(w, "Updating csm %s -> %s...\n", currentVersion, latest)
	last := -2
	exePath, err := downloadAndReplace(latest, func(percent int) {
		// Print every 10 % so logs stay short.
		if percent < 0 || percent/10 == last/10 {
			return
		}
		last = percent
		fmt.Fprintf(w, "  downloaded %d%%\n", percent)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Updated %s to csm %s.\n", exePath, latest)
	return nil
}

// downloadAndReplace downloads the release asset for this platform and
// atomically replaces the running binary with it. onProgress gets a
// percentage, or -1 when the size is unknown.
func downloadAndReplace(targetVersion string, onProgress func(percent int)) (string, error) {
	asset, err := selectAssetForCurrentPlatform()
	if err != nil {
		return "", err
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}

	// Write to a temporary file in the same directory, then atomically replace.
	dir := filepath.Dir(exePath)
	tmpPath := filepath.Join(dir, ".csm.tmp")

	// Pre-flight permission check: if we can't create a temp file next to the
	// binary (e.g. global install in /usr/local/bin), surface a friendly
	// message so users know they should rerun with sudo or update manually.
	if f, err := os.CreateTemp(dir, ".csm-perm-check-*"); err != nil {
		return "", fmt.Errorf("cannot write to %s to perform a self-update (run it with sudo, or download the new binary from GitHub Releases)", dir)
	} else {
		f.Close()
		_ = os.Remove(f.Name())
	}

	url := fmt.Sprintf("https://github.com/Auto-Tournament/cs2-server-manager/releases/download/%s/%s", targetVersion, asset)

	// Allow for slow connections: give the download up to 5 minutes before
	// timing out.
	client := http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	f, err := os.Create(tmpPath)
	if err != nil {
		return "", err
	}

	pw := &progressWriter{w: f, total: resp.ContentLength, onProgress: onProgress}
	if _, err := io.Copy(pw, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := f.Chmod(0755); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	if err := os.Rename(tmpPath, exePath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return exePath, nil
}

func selectAssetForCurrentPlatform() (string, error) {
	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return "csm-linux-amd64", nil
		case "arm64":
			return "csm-linux-arm64", nil
		}
	}
	return "", fmt.Errorf("auto-update is only available for linux/amd64 and linux/arm64 (detected: %s/%s)", runtime.GOOS, runtime.GOARCH)
}
