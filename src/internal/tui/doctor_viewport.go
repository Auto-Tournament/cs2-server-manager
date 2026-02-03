package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

func runDoctorViewport(applyFixes bool) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()

		meta, checks, scanErr := csm.DoctorScan(ctx, csm.DoctorOptions{Server: 0})
		report := csm.FormatDoctorReport(meta, checks)

		var b strings.Builder
		b.WriteString(report)

		if scanErr != nil {
			b.WriteString("\nDoctor scan error:\n")
			b.WriteString(scanErr.Error())
			b.WriteString("\n")
		}

		if !applyFixes {
			b.WriteString("\nNotes:\n")
			b.WriteString("  - This was a read-only run (no fixes applied).\n")
			b.WriteString("  - Re-run and choose \"apply fixes\" to attempt automated repairs.\n")
			return viewportFinishedMsg{
				title:   "Doctor (report)",
				content: b.String(),
				err:     scanErr,
			}
		}

		// Apply all available fixes (no per-issue prompting inside the viewport).
		b.WriteString("\nApplying fixes:\n")
		anyFix := false
		for _, chk := range checks {
			if chk.Status == csm.DoctorOK || chk.Fix == nil {
				continue
			}
			anyFix = true
			b.WriteString("\n--- ")
			b.WriteString(chk.Title)
			b.WriteString(" ---\n")
			out, err := chk.Fix(ctx)
			if strings.TrimSpace(out) != "" {
				b.WriteString(out)
				if !strings.HasSuffix(out, "\n") {
					b.WriteString("\n")
				}
			}
			if err != nil {
				b.WriteString(fmt.Sprintf("\nFix failed: %v\n", err))
				return viewportFinishedMsg{
					title:   "Doctor (failed)",
					content: b.String(),
					err:     err,
				}
			}
		}

		if !anyFix {
			b.WriteString("\nNo applicable fixes were found.\n")
		}

		b.WriteString("\nRescan:\n")
		meta2, checks2, scanErr2 := csm.DoctorScan(ctx, csm.DoctorOptions{Server: 0})
		b.WriteString(csm.FormatDoctorReport(meta2, checks2))
		if scanErr2 != nil {
			b.WriteString("\nRescan error:\n")
			b.WriteString(scanErr2.Error())
			b.WriteString("\n")
		}

		return viewportFinishedMsg{
			title:   "Doctor",
			content: b.String(),
			err:     scanErr2,
		}
	}
}
