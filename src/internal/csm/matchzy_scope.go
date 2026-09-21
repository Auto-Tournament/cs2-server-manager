package csm

import (
	"fmt"
	"os"
	"strings"
)

// MatchZy persistent config scoping.
//
// MatchZy stores per-server settings (matchzy_server_id, bootstrap URL/token,
// remote log URL, demo upload URL, ...) in its database. Before
// Auto-Tournament/cs2-plugin#17 those rows were keyed by setting name only, so
// several servers sharing one MySQL database overwrote each other and all
// loaded the last writer's values on startup.
//
// #17 scopes the rows per server. Its automatic identity is bind address +
// game port, but CSM starts every server with `-ip 0.0.0.0`, which identifies
// nothing, so the plugin falls back to the machine name, which is the same for
// every server on one box. CSM therefore passes an explicit scope on each
// server's start command line.

const (
	// MatchzyConfigScopeArg is the start argument MatchZy-Enhanced reads to pin
	// a server's persistent config scope.
	MatchzyConfigScopeArg = "+matchzy_config_scope"

	// MatchzyConfigScopeConVar is the convar name behind the start argument. It
	// is also the string the doctor looks for in MatchZy.dll to tell whether an
	// installed build supports scoping.
	MatchzyConfigScopeConVar = "matchzy_config_scope"

	// MatchzyScopingPR is the MatchZy-Enhanced change that adds per-server
	// config scoping.
	MatchzyScopingPR = "https://github.com/Auto-Tournament/cs2-plugin/pull/17"

	// MatchzyScopingMinVersion is the first MatchZy-Enhanced release that
	// contains MatchzyScopingPR (released as v1.4.26).
	MatchzyScopingMinVersion = "1.4.26"

	// MatchzyReleaseMarkerFile is written next to MatchZy.dll by
	// `csm update-plugins` and holds the deployed release tag.
	MatchzyReleaseMarkerFile = ".csm-release"

	// matchzyScopeMaxLen keeps scopes well under the plugin's own 180-char cap
	// so the plugin never truncates (and so never changes) what CSM passes.
	matchzyScopeMaxLen = 64
)

// MatchzyScopingRequirement describes the plugin build shared MySQL needs, for
// use in wizard text, doctor output and logs.
func MatchzyScopingRequirement() string {
	return fmt.Sprintf("Auto Tournament CS2 (formerly MatchZy Enhanced) %s or newer", MatchzyScopingMinVersion)
}

// MatchzyConfigScope returns the persistent config scope for server-N on this
// machine: "<hostname>-server-<N>", for example "cs2-server-1".
//
//   - server-N is the server's directory name. It is unique per server on one
//     machine and never changes across restarts, game updates, plugin updates,
//     reinstalls or port changes.
//   - The hostname prefix keeps two machines that share one MySQL database
//     apart; without it both would write "server-1" and collide, which is the
//     original bug.
//
// Operators who rename the machine, or run several machines with the same
// hostname against one database, can pin the prefix with
// CSM_MATCHZY_SCOPE_PREFIX.
func MatchzyConfigScope(serverNum int) string {
	prefix := strings.TrimSpace(os.Getenv("CSM_MATCHZY_SCOPE_PREFIX"))
	if prefix == "" {
		if h, err := os.Hostname(); err == nil {
			prefix = h
		}
	}
	return matchzyConfigScopeFor(prefix, serverNum)
}

// matchzyConfigScopeFor builds the scope from an explicit prefix. It is split
// out from MatchzyConfigScope so it can be tested without depending on the
// test machine's hostname or environment.
func matchzyConfigScopeFor(prefix string, serverNum int) string {
	suffix := fmt.Sprintf("server-%d", serverNum)
	p := sanitizeScopeToken(prefix)
	maxPrefix := matchzyScopeMaxLen - len(suffix) - 1
	if len(p) > maxPrefix {
		p = strings.Trim(p[:maxPrefix], "-.")
	}
	if p == "" {
		return suffix
	}
	return p + "-" + suffix
}

// sanitizeScopeToken reduces s to lower-case [a-z0-9._-], turning every other
// byte into '-', collapsing runs of '-' and trimming leading/trailing '-' and
// '.'.
//
// This is what makes the scope safe on the start command line. The launch
// command is nested inside up to three shell layers (`su -c "..."`, tmux's
// single-quoted command, and for Steam Runtime a `bash -lc "..."` string), and
// no single quoting style survives all of them. A token with no shell
// metacharacters needs no quoting in any of them. It is also already in the
// form the plugin normalises to (trimmed, lower-case, no whitespace), so the
// plugin stores exactly what CSM passed. A leading '-' is trimmed so the value
// can never be mistaken for another start flag.
func sanitizeScopeToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	lastDash := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-'
		if !ok {
			c = '-'
		}
		if c == '-' {
			if lastDash {
				continue
			}
			lastDash = true
		} else {
			lastDash = false
		}
		b.WriteByte(c)
	}
	return strings.Trim(b.String(), "-.")
}

// matchzyScopeLaunchArg returns " +matchzy_config_scope <scope>" for appending
// to a launch command, or "" when the scope sanitises to nothing.
func matchzyScopeLaunchArg(scope string) string {
	s := sanitizeScopeToken(scope)
	if s == "" {
		return ""
	}
	return " " + MatchzyConfigScopeArg + " " + s
}
