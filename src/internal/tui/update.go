package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type updateInfoMsg struct {
	latest string
	err    error
}

func checkForUpdates() tea.Cmd {
	return func() tea.Msg {
		client := http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("https://api.github.com/repos/sivert-io/cs2-server-manager/releases/latest")
		if err != nil {
			return updateInfoMsg{err: err}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return updateInfoMsg{err: fmt.Errorf("status %d", resp.StatusCode)}
		}

		var payload struct {
			TagName string `json:"tag_name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return updateInfoMsg{err: err}
		}

		return updateInfoMsg{latest: strings.TrimSpace(payload.TagName)}
	}
}

func isNewerVersion(current, latest string) bool {
	cur := parseSemver(current)
	lat := parseSemver(latest)

	if lat[0] != cur[0] {
		return lat[0] > cur[0]
	}
	if lat[1] != cur[1] {
		return lat[1] > cur[1]
	}
	return lat[2] > cur[2]
}

func parseSemver(v string) [3]int {
	var res [3]int
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			n = 0
		}
		res[i] = n
	}
	return res
}


