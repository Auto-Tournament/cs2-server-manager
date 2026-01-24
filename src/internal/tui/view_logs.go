package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

// runViewRecentLogsGo shows a list of recent command log files with a
// quick preview of each, making it easy to debug what went wrong.
func runViewRecentLogsGo() tea.Cmd {
	return func() tea.Msg {
		// Get the log directory path for display
		logDir := csm.ResolveRoot()
		if d := os.Getenv("CSM_LOG_DIR"); d != "" {
			logDir = d
		} else {
			logDir = filepath.Join(logDir, "logs")
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Recent Command Logs\n")
		fmt.Fprintf(&b, "===================\n\n")
		fmt.Fprintf(&b, "Log directory: %s\n\n", logDir)

		// Get the 20 most recent log files
		logs, err := csm.ListRecentLogs(20)
		if err != nil {
			return commandFinishedMsg{
				item: menuItem{
					title: "View recent command logs",
					kind:  itemViewRecentLogsGo,
				},
				output: fmt.Sprintf("Failed to list logs: %v", err),
				err:    err,
			}
		}

		if len(logs) == 0 {
			fmt.Fprintf(&b, "No command logs found yet.\n\n")
			fmt.Fprintf(&b, "Run some commands and their logs will appear here for easy debugging.\n")
		} else {
			fmt.Fprintf(&b, "Recent commands (newest first):\n\n")

			for i, logName := range logs {
				// Parse the log filename to extract timestamp and action
				// Format: 2026-01-24_15-30-45_action-name.log
				parts := strings.SplitN(logName, "_", 3)
				timestamp := "unknown"
				action := logName

				if len(parts) >= 3 {
					timestamp = parts[0] + " " + strings.ReplaceAll(parts[1], "-", ":")
					action = strings.TrimSuffix(parts[2], ".log")
					action = strings.ReplaceAll(action, "-", " ")
				}

				fmt.Fprintf(&b, "%2d. [%s] %s\n", i+1, timestamp, action)

				// Show a quick preview of the log (first few lines or error)
				logPath := filepath.Join(logDir, logName)
				content, err := os.ReadFile(logPath)
				if err == nil {
					lines := strings.Split(string(content), "\n")
					// Look for error lines to highlight issues
					hasError := false
					for _, line := range lines {
						if strings.Contains(line, "Error:") {
							hasError = true
							// Show the error line
							trimmed := strings.TrimSpace(line)
							if len(trimmed) > 80 {
								trimmed = trimmed[:77] + "..."
							}
							fmt.Fprintf(&b, "    ⚠️  %s\n", trimmed)
							break
						}
					}
					if !hasError {
						// Show "completed successfully" if no errors
						for _, line := range lines {
							if strings.Contains(line, "completed") || strings.Contains(line, "successfully") || strings.Contains(line, "SUCCESS") {
								fmt.Fprintf(&b, "    ✓ Success\n")
								break
							}
						}
					}
				}
				fmt.Fprintln(&b)
			}

			fmt.Fprintf(&b, "\nTo view a full log file, use:\n")
			fmt.Fprintf(&b, "  cat %s/<filename>\n", logDir)
			fmt.Fprintf(&b, "\nOr open in your text editor:\n")
			fmt.Fprintf(&b, "  nano %s/<filename>\n", logDir)
		}

		return commandFinishedMsg{
			item: menuItem{
				title: "View recent command logs",
				kind:  itemViewRecentLogsGo,
			},
			output: b.String(),
			err:    nil,
		}
	}
}
