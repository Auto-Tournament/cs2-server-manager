package csm

import (
	"fmt"
	"sync"
)

type copyStats struct {
	mu sync.Mutex

	reflinkSuccess  int
	reflinkFallback int
	reflinkSkipped  int

	rsyncTuned  int
	rsyncLegacy int

	lastNote string
}

var globalCopyStats copyStats

func ResetCopyStats() {
	globalCopyStats.mu.Lock()
	defer globalCopyStats.mu.Unlock()
	globalCopyStats.reflinkSuccess = 0
	globalCopyStats.reflinkFallback = 0
	globalCopyStats.reflinkSkipped = 0
	globalCopyStats.rsyncTuned = 0
	globalCopyStats.rsyncLegacy = 0
	globalCopyStats.lastNote = ""
}

func recordCopyNote(note string) {
	globalCopyStats.mu.Lock()
	defer globalCopyStats.mu.Unlock()
	globalCopyStats.lastNote = note
}

func RecordCopyReflinkSuccess(note string) {
	globalCopyStats.mu.Lock()
	defer globalCopyStats.mu.Unlock()
	globalCopyStats.reflinkSuccess++
	globalCopyStats.lastNote = note
}

func RecordCopyReflinkFallback(note string) {
	globalCopyStats.mu.Lock()
	defer globalCopyStats.mu.Unlock()
	globalCopyStats.reflinkFallback++
	globalCopyStats.lastNote = note
}

func RecordCopyReflinkSkipped(note string) {
	globalCopyStats.mu.Lock()
	defer globalCopyStats.mu.Unlock()
	globalCopyStats.reflinkSkipped++
	globalCopyStats.lastNote = note
}

func RecordCopyRsyncTuned(note string) {
	globalCopyStats.mu.Lock()
	defer globalCopyStats.mu.Unlock()
	globalCopyStats.rsyncTuned++
	globalCopyStats.lastNote = note
}

func RecordCopyRsyncLegacy(note string) {
	globalCopyStats.mu.Lock()
	defer globalCopyStats.mu.Unlock()
	globalCopyStats.rsyncLegacy++
	globalCopyStats.lastNote = note
}

// CopyStatsSummary returns a human-readable summary of what happened during
// the current process's last copy operations. This is intended for display
// in the install wizard summary and CLI output.
func CopyStatsSummary() string {
	globalCopyStats.mu.Lock()
	defer globalCopyStats.mu.Unlock()

	// Keep this stable and compact.
	return fmt.Sprintf(
		"reflink: %d ok, %d fallback, %d skipped; rsync: %d tuned, %d legacy%s",
		globalCopyStats.reflinkSuccess,
		globalCopyStats.reflinkFallback,
		globalCopyStats.reflinkSkipped,
		globalCopyStats.rsyncTuned,
		globalCopyStats.rsyncLegacy,
		func() string {
			if globalCopyStats.lastNote == "" {
				return ""
			}
			return fmt.Sprintf(" (last: %s)", globalCopyStats.lastNote)
		}(),
	)
}
