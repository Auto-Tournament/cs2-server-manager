package readyup

import (
	"strconv"
	"strings"
)

// Release order must come from version numbers, never from list position or
// publish time: GitHub lists releases by creation date, and a backported patch
// can be created after a newer minor.

type semver struct {
	core [3]uint64
	pre  []string
}

// parseSemver reads [v]MAJOR.MINOR.PATCH[-pre][+build].
func parseSemver(s string) (semver, bool) {
	var v semver
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return v, false
		}
		v.core[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" {
				return v, false
			}
		}
	}
	return v, true
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// CompareVersions compares two SemVer 2.0 versions (a leading "v" is allowed)
// and returns -1, 0 or 1. ok is false when either is not valid SemVer.
// Pre-release precedence follows the spec: 1.0.0 > 1.0.0-rc.1 > 1.0.0-beta.10 >
// 1.0.0-beta.2 > 1.0.0-beta > 1.0.0-alpha.
func CompareVersions(a, b string) (c int, ok bool) {
	x, aok := parseSemver(a)
	y, bok := parseSemver(b)
	if !aok || !bok {
		return 0, false
	}
	for i := range x.core {
		if c := cmpUint(x.core[i], y.core[i]); c != 0 {
			return c, true
		}
	}
	switch {
	case len(x.pre) == 0 && len(y.pre) == 0:
		return 0, true
	case len(x.pre) == 0:
		return 1, true
	case len(y.pre) == 0:
		return -1, true
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		p, q := x.pre[i], y.pre[i]
		pv, perr := strconv.ParseUint(p, 10, 64)
		qv, qerr := strconv.ParseUint(q, 10, 64)
		switch {
		case perr == nil && qerr == nil:
			if c := cmpUint(pv, qv); c != 0 {
				return c, true
			}
		case perr == nil:
			return -1, true // numeric identifiers rank below alphanumeric ones
		case qerr == nil:
			return 1, true
		case p != q:
			return strings.Compare(p, q), true
		}
	}
	return cmpUint(uint64(len(x.pre)), uint64(len(y.pre))), true
}

// releaseNewer reports whether a is a newer release than b. Tags that are not
// SemVer rank below valid ones and are ordered among themselves by publish time.
func releaseNewer(a, b Release) bool {
	if c, ok := CompareVersions(a.TagName, b.TagName); ok {
		if c != 0 {
			return c > 0
		}
		return a.time().After(b.time())
	}
	_, aok := parseSemver(a.TagName)
	_, bok := parseSemver(b.TagName)
	if aok != bok {
		return aok
	}
	return a.time().After(b.time())
}
