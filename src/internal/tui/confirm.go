package tui

import (
	"fmt"
	"strings"
)

// viewCleanupConfirm renders the "Danger zone" confirmation screen before
// running the irreversible cleanup-all operation.
func (m model) viewCleanupConfirm() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Danger zone")) +
		"\n" +
		headerBorderStyle.Render("Wipe all servers and the CS2 user")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "This will permanently delete:")
	fmt.Fprintln(&b, "  • All CS2 server data and configs")
	fmt.Fprintln(&b, "  • The CS2 Linux user and its home directory")
	fmt.Fprintln(&b, "  • The MatchZy MySQL Docker container and volume (if present)")
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, warningStyle.Render("This cannot be undone. Make sure you have backups if needed."))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Press Enter or Y to run cleanup, or N/Esc to cancel.")

	return b.String()
}
