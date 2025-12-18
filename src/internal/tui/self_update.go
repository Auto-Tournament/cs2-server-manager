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

// selfUpdateProgressWriter wraps the target file so that as bytes are written
// we can emit selfUpdateProgressMsg messages into the TUI, giving the user a
// sense of download progress while the update runs.
type selfUpdateProgressWriter struct {
	w       io.Writer
	total   int64
	written int64
}

func (pw *selfUpdateProgressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	if n > 0 {
		pw.written += int64(n)
		if pw.total > 0 {
			percent := int(pw.written * 100 / pw.total)
			if percent > 100 {
				percent = 100
			}
			send(selfUpdateProgressMsg{Percent: percent})
		} else {
			// Unknown total size; just signal that data is flowing.
			send(selfUpdateProgressMsg{Percent: -1})
		}
	}
	return n, err
}

func runSelfUpdate(targetVersion string) tea.Cmd {
	return func() tea.Msg {
		asset, err := selectAssetForCurrentPlatform()
		if err != nil {
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}

		exePath, err := os.Executable()
		if err != nil {
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}

		// Write to a temporary file in the same directory, then atomically replace.
		dir := filepath.Dir(exePath)
		tmpPath := filepath.Join(dir, ".csm.tmp")

		// Pre-flight permission check: if we can't create a temp file next to the
		// binary (e.g. global install in /usr/local/bin), surface a friendly
		// message so users know they should rerun with sudo or update manually.
		if f, err := os.CreateTemp(dir, ".csm-perm-check-*"); err != nil {
			// Keep the detailed guidance in the log output but make the actual
			// error string concise to satisfy linter guidance about punctuation
			// and avoid embedding multi-line help text in error values.
			detail := fmt.Sprintf(
				"CSM cannot write to %s to perform a self-update.\n\n"+
					"If CSM is installed globally (for example in /usr/local/bin), "+
					"please restart it with sudo and run the update again:\n\n"+
					"  sudo csm\n\n"+
					"Alternatively, download the new binary from GitHub Releases and replace it manually.\n",
				dir,
			)
			_ = os.WriteFile(filepath.Join(dir, "csm-self-update-permissions.txt"), []byte(detail), 0o644)

			return selfUpdateFinishedMsg{
				newVersion: "",
				err:        fmt.Errorf("cannot write to %s to perform a self-update", dir),
			}
		} else {
			f.Close()
			_ = os.Remove(f.Name())
		}

		url := fmt.Sprintf("https://github.com/sivert-io/cs2-server-manager/releases/download/%s/%s", targetVersion, asset)

		// Allow for slow connections: give the download up to 5 minutes before
		// timing out.
		client := http.Client{Timeout: 5 * time.Minute}
		resp, err := client.Get(url)
		if err != nil {
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return selfUpdateFinishedMsg{newVersion: "", err: fmt.Errorf("download failed with status %d", resp.StatusCode)}
		}

		f, err := os.Create(tmpPath)
		if err != nil {
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}

		// Wrap the file in a progress writer so we can emit selfUpdateProgressMsg
		// events while the binary is downloading.
		pw := &selfUpdateProgressWriter{
			w:       f,
			total:   resp.ContentLength,
			written: 0,
		}

		if _, err := io.Copy(pw, resp.Body); err != nil {
			_ = f.Close()
			_ = os.Remove(tmpPath)
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}
		if err := f.Chmod(0755); err != nil {
			_ = f.Close()
			_ = os.Remove(tmpPath)
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(tmpPath)
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}

		if err := os.Rename(tmpPath, exePath); err != nil {
			_ = os.Remove(tmpPath)
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
