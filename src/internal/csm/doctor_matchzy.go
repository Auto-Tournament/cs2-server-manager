package csm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// scopingSupport is whether an installed MatchZy build scopes persistent
// config per server.
type scopingSupport int

const (
	scopingUnknown scopingSupport = iota // MatchZy.dll missing or unreadable
	scopingNo
	scopingYes
)

// matchzyServerFacts is what the doctor observed about one server. It is
// gathered by collectMatchzyServerFacts and judged by evaluateMatchzyScope,
// which is pure so it can be unit tested.
type matchzyServerFacts struct {
	Server int

	// DBEngine is DatabaseType from the server's database.json, lower-cased
	// ("mysql", "sqlite"), or "" when the file is missing or unreadable.
	DBEngine string
	// MySQLTarget identifies the MySQL database ("host:port/db") when DBEngine
	// is mysql. Servers with equal targets share one database.
	MySQLTarget string

	Scoping scopingSupport

	// ServerID is the last matchzy_server_id the server logged, or "".
	ServerID string

	// Running is true when a cs2 process for this server's game port was
	// found; HasScopeArg is whether its command line has
	// +matchzy_config_scope.
	Running     bool
	HasScopeArg bool
}

// checkMatchzyConfigScope detects servers that share MatchZy persistent config
// and so load each other's matchzy_server_id / bootstrap URL.
func checkMatchzyConfigScope(meta DoctorMeta) DoctorCheck {
	return evaluateMatchzyScope(meta.CS2User, collectMatchzyServerFacts(meta))
}

func matchzyServerDBConfigPath(user string, server int) string {
	return filepath.Join("/home", user, fmt.Sprintf("server-%d", server), "game", "csgo", "cfg", "MatchZy", "database.json")
}

func matchzyServerDLLPath(user string, server int) string {
	return filepath.Join("/home", user, fmt.Sprintf("server-%d", server), "game", "csgo", "addons", "counterstrikesharp", "plugins", "MatchZy", "MatchZy.dll")
}

func collectMatchzyServerFacts(meta DoctorMeta) []matchzyServerFacts {
	psOut := ""
	if out, err := exec.Command("ps", "-eo", "args=").Output(); err == nil {
		psOut = string(out)
	}

	facts := make([]matchzyServerFacts, 0, len(meta.Servers))
	for _, n := range meta.Servers {
		f := matchzyServerFacts{Server: n}

		if data, err := os.ReadFile(matchzyServerDBConfigPath(meta.CS2User, n)); err == nil {
			f.DBEngine, f.MySQLTarget = parseMatchzyDBEngine(data)
		}

		f.Scoping = dllScopingSupport(matchzyServerDLLPath(meta.CS2User, n))

		logPath := filepath.Join("/home", meta.CS2User, "logs", fmt.Sprintf("server-%d.log", n))
		if tail, err := readFileTail(logPath, 8<<20); err == nil {
			f.ServerID = lastMatchzyServerID(tail)
		}

		gamePort, _ := detectServerPorts(meta.CS2User, n)
		f.Running, f.HasScopeArg = scopeArgOnRunningServer(psOut, gamePort)

		facts = append(facts, f)
	}
	return facts
}

// parseMatchzyDBEngine returns the lower-cased DatabaseType and, for MySQL, a
// "host:port/db" target. Hosts that all mean "this machine" compare equal.
func parseMatchzyDBEngine(data []byte) (engine, mysqlTarget string) {
	var c matchzyDBConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return "", ""
	}
	engine = strings.ToLower(strings.TrimSpace(c.DatabaseType))
	if engine != MatchzyDBEngineMySQL {
		return engine, ""
	}
	host := strings.ToLower(strings.TrimSpace(c.MySQLHost))
	switch host {
	case "", "localhost", "127.0.0.1", "::1":
		host = "localhost"
	}
	port := c.MySQLPort
	if port <= 0 {
		port = 3306
	}
	return engine, fmt.Sprintf("%s:%d/%s", host, port, strings.TrimSpace(c.MySQLDatabase))
}

// dllScopingSupport looks for the matchzy_config_scope convar name in
// MatchZy.dll. .NET stores C# string literals as UTF-16LE and attribute
// arguments as UTF-8, so both encodings are checked. No match means the
// build predates per-server config scoping.
func dllScopingSupport(path string) scopingSupport {
	data, err := os.ReadFile(path)
	if err != nil {
		return scopingUnknown
	}
	return scopingSupportInBytes(data)
}

func scopingSupportInBytes(data []byte) scopingSupport {
	needle := []byte(MatchzyConfigScopeConVar)
	if bytes.Contains(data, needle) || bytes.Contains(data, utf16le(MatchzyConfigScopeConVar)) {
		return scopingYes
	}
	return scopingNo
}

func utf16le(s string) []byte {
	b := make([]byte, 0, len(s)*2)
	for i := 0; i < len(s); i++ {
		b = append(b, s[i], 0)
	}
	return b
}

// matchzyServerIDPattern matches the ways MatchZy logs its server id:
//
//	[SaveConfigValue] Saved config: matchzy_server_id = s_3
//	[LoadPersistentConfig] Loaded matchzy_server_id: s_3
//	"server_id":"s_3"
var matchzyServerIDPattern = regexp.MustCompile(`(?:matchzy_server_id\s*[=:]\s*"?|"server_id"\s*:\s*")([A-Za-z0-9_.:-]+)`)

// lastMatchzyServerID returns the most recent server id in a MatchZy log, or
// "" when there is none.
func lastMatchzyServerID(log string) string {
	m := matchzyServerIDPattern.FindAllStringSubmatch(log, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

// scopeArgOnRunningServer looks through `ps -eo args=` output for the cs2
// process bound to gamePort and reports whether it was started with
// +matchzy_config_scope. Wrapper processes (tmux, bash -lc, su) repeat the
// arguments, so any matching line with the argument counts.
func scopeArgOnRunningServer(psOut string, gamePort int) (running, hasScope bool) {
	portArg := "-port " + strconv.Itoa(gamePort)
	for _, line := range strings.Split(psOut, "\n") {
		if !strings.Contains(line, "cs2") {
			continue
		}
		idx := strings.Index(line, portArg)
		if idx < 0 {
			continue
		}
		// Make sure "-port 27015" isn't a prefix of "-port 270150".
		end := idx + len(portArg)
		if end < len(line) && line[end] >= '0' && line[end] <= '9' {
			continue
		}
		running = true
		if strings.Contains(line, MatchzyConfigScopeArg) {
			hasScope = true
		}
	}
	return running, hasScope
}

// readFileTail reads at most max bytes from the end of path.
func readFileTail(path string, max int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if fi.Size() > max {
		if _, err := f.Seek(-max, io.SeekEnd); err != nil {
			return "", err
		}
	}
	data, err := io.ReadAll(f)
	return string(data), err
}

func serverList(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = fmt.Sprintf("server-%d", n)
	}
	return strings.Join(parts, ", ")
}

// evaluateMatchzyScope turns the per-server facts into a doctor result.
//
// It fails when servers are known to be overwriting each other's persistent
// config: two or more servers report the same matchzy_server_id, or two or
// more servers share one MySQL database and either run a MatchZy build without
// scoping or are running without +matchzy_config_scope (a scoping build then
// falls back to the machine name, which is the same for every server here).
func evaluateMatchzyScope(user string, facts []matchzyServerFacts) DoctorCheck {
	const (
		id    = "matchzy-config-scope"
		title = "MatchZy per-server config (shared database)"
	)
	if user == "" {
		user = DefaultCS2User
	}

	byID := map[string][]int{}
	byTarget := map[string][]int{}
	factsByServer := map[int]matchzyServerFacts{}
	sqlite := 0
	for _, f := range facts {
		factsByServer[f.Server] = f
		if f.ServerID != "" {
			byID[f.ServerID] = append(byID[f.ServerID], f.Server)
		}
		switch f.DBEngine {
		case MatchzyDBEngineMySQL:
			byTarget[f.MySQLTarget] = append(byTarget[f.MySQLTarget], f.Server)
		case MatchzyDBEngineSQLite:
			sqlite++
		}
	}

	var failures, warnings []string
	var noScoping, noScopeArg, unknownScoping []int
	dupIDs := false

	ids := make([]string, 0, len(byID))
	for k := range byID {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, sid := range ids {
		if servers := byID[sid]; len(servers) > 1 {
			dupIDs = true
			failures = append(failures, fmt.Sprintf("%s all report matchzy_server_id %q; a tournament manager sees them as one server.", serverList(servers), sid))
		}
	}

	targets := make([]string, 0, len(byTarget))
	for k := range byTarget {
		targets = append(targets, k)
	}
	sort.Strings(targets)
	shared := false
	for _, t := range targets {
		servers := byTarget[t]
		if len(servers) < 2 {
			continue
		}
		shared = true
		var groupNoScoping, groupNoScopeArg, groupUnknown []int
		for _, n := range servers {
			f := factsByServer[n]
			switch f.Scoping {
			case scopingNo:
				groupNoScoping = append(groupNoScoping, n)
			case scopingUnknown:
				groupUnknown = append(groupUnknown, n)
			}
			if f.Running && !f.HasScopeArg {
				groupNoScopeArg = append(groupNoScopeArg, n)
			}
		}
		if len(groupNoScoping) > 0 {
			failures = append(failures, fmt.Sprintf("%s share MySQL %s, and %s run a MatchZy build without per-server config scoping, so they overwrite each other's matchzy_server_id and bootstrap URL.", serverList(servers), t, serverList(groupNoScoping)))
		}
		if len(groupNoScopeArg) > 0 {
			failures = append(failures, fmt.Sprintf("%s share MySQL %s, and %s are running without %s (started by an older CSM), so they all fall back to the same machine-name scope.", serverList(servers), t, serverList(groupNoScopeArg), MatchzyConfigScopeArg))
		}
		if len(groupUnknown) > 0 {
			warnings = append(warnings, fmt.Sprintf("%s share MySQL %s, but MatchZy.dll was not found for %s, so scoping support could not be checked.", serverList(servers), t, serverList(groupUnknown)))
		}
		noScoping = append(noScoping, groupNoScoping...)
		noScopeArg = append(noScopeArg, groupNoScopeArg...)
		unknownScoping = append(unknownScoping, groupUnknown...)
	}

	var steps []string
	if len(noScoping) > 0 || len(unknownScoping) > 0 {
		steps = append(steps,
			fmt.Sprintf("Shared MySQL needs %s. Update MatchZy and restart every server: sudo csm update-plugins", MatchzyScopingRequirement()),
			fmt.Sprintf("If that build isn't available yet, switch to SQLite per server instead: set \"DatabaseType\": \"SQLite\" in /home/%[1]s/overrides/game/csgo/cfg/MatchZy/database.json and /home/%[1]s/cs2-config/game/csgo/cfg/MatchZy/database.json, then run: sudo csm update-plugins", user),
		)
	}
	if len(noScopeArg) > 0 && len(noScoping) == 0 && len(unknownScoping) == 0 {
		steps = append(steps, fmt.Sprintf("Restart the servers so they start with %s: sudo csm restart", MatchzyConfigScopeArg))
	}
	if dupIDs {
		steps = append(steps, "Then reconfigure every server from your tournament manager (for MAT, re-save or re-bootstrap each server) so each one writes its own matchzy_server_id and bootstrap URL.")
	}
	if len(steps) > 0 {
		steps = append(steps, "Run sudo csm doctor again to confirm.")
	}

	switch {
	case len(failures) > 0:
		detail := strings.Join(append(failures, warnings...), "\n")
		return DoctorCheck{ID: id, Title: title, Status: DoctorFail, Detail: detail, FixHint: numberedSteps(steps)}
	case len(warnings) > 0:
		return DoctorCheck{ID: id, Title: title, Status: DoctorWarn, Detail: strings.Join(warnings, "\n"), FixHint: numberedSteps(steps)}
	}

	var detail string
	switch {
	case len(facts) == 0:
		detail = "No servers discovered; nothing to check."
	case shared:
		detail = fmt.Sprintf("Servers share one MySQL database, run a MatchZy build with per-server config scoping and start with %s.", MatchzyConfigScopeArg)
	case sqlite == len(facts):
		detail = "Every server uses its own SQLite database."
	default:
		detail = "No servers share a MySQL database and no duplicate matchzy_server_id was found."
	}
	return DoctorCheck{ID: id, Title: title, Status: DoctorOK, Detail: detail}
}

func numberedSteps(steps []string) string {
	if len(steps) == 0 {
		return ""
	}
	lines := make([]string, len(steps))
	for i, s := range steps {
		lines[i] = fmt.Sprintf("%d. %s", i+1, s)
	}
	return strings.Join(lines, "\n")
}
