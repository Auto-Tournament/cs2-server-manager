package license

import (
	"os"
	"time"
)

// lineDate is the release date (YYYY-MM-DD) of this build's major.minor.0.
// The release workflow sets it (scripts/release.sh, via scripts/line-date.sh):
//
//	go build -ldflags "-X github.com/sivert-io/cs2-server-manager/src/internal/license.lineDate=2026-09-28"
//
// A license covers this build when lineDate <= updates_until, so later
// patches of a covered line stay covered. Empty in dev builds.
var lineDate string

// LineDate is this build's version line date: the baked-in value, or for a
// dev build the build date (the binary's modification time), or today.
func LineDate() string {
	var built time.Time
	if exe, err := os.Executable(); err == nil {
		if fi, err := os.Stat(exe); err == nil {
			built = fi.ModTime()
		}
	}
	return ResolveLineDate(lineDate, built, time.Now())
}

// ResolveLineDate picks the line date: baked (when it is a real
// YYYY-MM-DD), else the build time, else now. Pure, for tests.
func ResolveLineDate(baked string, built, now time.Time) string {
	if d, ok := isDate(baked); ok {
		return d
	}
	if !built.IsZero() {
		return built.UTC().Format(dateLayout)
	}
	return now.UTC().Format(dateLayout)
}

// BakedLineDate reports whether this binary carries a release line date.
func BakedLineDate() bool {
	_, ok := isDate(lineDate)
	return ok
}
