package tui

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type selfUpdateFinishedMsg struct {
	newVersion string
	err        error
}

func runSelfUpdate(targetVersion string) tea.Cmd {
	return func() tea.Msg {
		asset, err := selectAssetForCurrentPlatform()
		if err != nil {
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}

		url := fmt.Sprintf("https://github.com/sivert-io/cs2-server-manager/releases/download/%s/%s", targetVersion, asset)

		client := http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return selfUpdateFinishedMsg{newVersion: "", err: fmt.Errorf("download failed with status %d", resp.StatusCode)}
		}

		exePath, err := os.Executable()
		if err != nil {
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}

		// Write to a temporary file in the same directory, then atomically replace.
		dir := filepath.Dir(exePath)
		tmpPath := filepath.Join(dir, ".csm.tmp")

		f, err := os.Create(tmpPath)
		if err != nil {
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}

		if _, err := io.Copy(f, resp.Body); err != nil {
			f.Close()
			_ = os.Remove(tmpPath)
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}
		if err := f.Chmod(0755); err != nil {
			f.Close()
			_ = os.Remove(tmpPath)
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}
		f.Close()

		if err := os.Rename(tmpPath, exePath); err != nil {
			_ = os.Remove(tmpPath)
			return selfUpdateFinishedMsg{newVersion: "", err: err}
		}

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


