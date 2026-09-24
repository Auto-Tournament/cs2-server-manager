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

// The Auto Tournament CS2 doctor check: is every server on plugin 2.0.0 or
// newer (and not still carrying the old MatchZy-named plugin), and do servers
// that share one MySQL database each keep their own persistent config?

// scopingSupport is whether an installed plugin build is new enough: 2.0.0
// or newer, which also scopes persistent config per server.
type scopingSupport int

const (
	scopingUnknown scopingSupport = iota // AutoTournamentCS2.dll missing or unreadable
	scopingNo
	scopingYes
)

// atcs2ServerFacts is what the doctor observed about one server. It is
// gathered by collectATCS2ServerFacts and judged by evaluateATCS2Scope,
// which is pure so it can be unit tested.
type atcs2ServerFacts struct {
	Server int

	// DBEngine is DatabaseType from the server's database.json, lower-cased
	// ("mysql", "sqlite"), or "" when the file is missing or unreadable.
	DBEngine string
	// MySQLTarget identifies the MySQL database ("host:port/db") when DBEngine
	// is mysql. Servers with equal targets share one database.
	MySQLTarget string

	Scoping scopingSupport
	// PluginVersion is the plugin version Scoping was decided from, when one
	// could be read ("" when the DLL string check was used instead).
	PluginVersion string

	// ServerID is the last at_server_id the server logged in its
	// current run (since the last startup in its log), or "".
	ServerID string

	// Starting is true when the server is running and its log shows the
	// current run's startup, but the plugin has not logged its version yet.
	// Scoping then comes from the deployed release, not the log.
	Starting bool

	// Running is true when a cs2 process for this server's game port was
	// found; HasScopeArg is whether its command line has +at_config_scope.
	Running     bool
	HasScopeArg bool

	// LegacyPlugin is true when the server still has the pre-2.0.0 plugin
	// folder, addons/counterstrikesharp/plugins/MatchZy/.
	LegacyPlugin bool
}

// checkATCS2ConfigScope detects servers that still run the old plugin, and
// servers that share persistent config and so load each other's
// at_server_id / bootstrap URL.
func checkATCS2ConfigScope(meta DoctorMeta) DoctorCheck {
	return evaluateATCS2Scope(meta.CS2User, collectATCS2ServerFacts(meta))
}

func atcs2ServerDBConfigPath(user string, server int) string {
	return filepath.Join(atcs2ServerCsgoDir(user, server), "cfg", ATCS2CfgDirName, "database.json")
}

func atcs2ServerDLLPath(user string, server int) string {
	return filepath.Join(atcs2PluginDir(atcs2ServerCsgoDir(user, server)), ATCS2DLLName)
}

func atcs2ServerCsgoDir(user string, server int) string {
	return filepath.Join("/home", user, fmt.Sprintf("server-%d", server), "game", "csgo")
}

func collectATCS2ServerFacts(meta DoctorMeta) []atcs2ServerFacts {
	psOut := ""
	if out, err := exec.Command("ps", "-eo", "args=").Output(); err == nil {
		psOut = string(out)
	}

	facts := make([]atcs2ServerFacts, 0, len(meta.Servers))
	for _, n := range meta.Servers {
		f := atcs2ServerFacts{Server: n}

		if data, err := os.ReadFile(atcs2ServerDBConfigPath(meta.CS2User, n)); err == nil {
			f.DBEngine, f.MySQLTarget = parseATCS2DBEngine(data)
		}

		if _, err := os.Lstat(legacyATCS2PluginDir(atcs2ServerCsgoDir(meta.CS2User, n))); err == nil {
			f.LegacyPlugin = true
		}

		gamePort, _ := detectServerPorts(meta.CS2User, n)
		f.Running, f.HasScopeArg = scopeArgOnRunningServer(psOut, gamePort)

		dllPath := atcs2ServerDLLPath(meta.CS2User, n)
		logTail := ""
		logPath := filepath.Join("/home", meta.CS2User, "logs", fmt.Sprintf("server-%d.log", n))
		if tail, err := readFileTail(logPath, 8<<20); err == nil {
			logTail = tail
		}
		deployedVersion := ""
		if data, err := os.ReadFile(filepath.Join(filepath.Dir(dllPath), ATCS2ReleaseMarkerFile)); err == nil {
			deployedVersion = strings.TrimSpace(string(data))
		}
		applyATCS2LogFacts(&f, logTail, deployedVersion, func() scopingSupport {
			return dllScopingSupport(dllPath)
		})

		facts = append(facts, f)
	}
	return facts
}

// applyATCS2LogFacts fills ServerID, Starting, Scoping and PluginVersion
// from a server's log tail and the release csm last deployed. f.Running must
// already be set. Only the current run of the server is read: server logs are
// appended across restarts, so older runs would report a plugin version or
// server id the running process no longer has.
func applyATCS2LogFacts(f *atcs2ServerFacts, logTail, deployedVersion string, dllProbe func() scopingSupport) {
	run, sawStartup := currentServerRun(logTail)
	f.ServerID = lastATCS2ServerID(run)
	logVersion := lastATCS2PluginVersion(run)
	f.Starting = f.Running && sawStartup && logVersion == ""
	f.Scoping, f.PluginVersion = resolveScopingSupport(logVersion, deployedVersion, dllProbe)
}

// serverStartupMarkers are lines printed exactly once per cs2 process start,
// before any plugin loads:
//
//	command line arguments:
//	[03:25:53.870] CSSharp: Initializing with command line: "/home/.../cs2" ...
//
// The first is the engine's own startup dump and appears even when
// CounterStrikeSharp fails to load; the second follows it about 25 lines
// later. Either one marks the start of a run.
var serverStartupMarkers = []string{
	"command line arguments:",
	"CSSharp: Initializing with command line:",
}

// currentServerRun returns the part of a server log written by the most
// recent server start: everything after the last startup marker. When no
// marker is found (the run started before the tail being read), the whole
// log is the current run and sawStartup is false.
func currentServerRun(log string) (run string, sawStartup bool) {
	cut := -1
	for _, m := range serverStartupMarkers {
		for from := len(log); from > 0; {
			i := strings.LastIndex(log[:from], m)
			if i < 0 {
				break
			}
			// Only count the marker at the start of a line, allowing for the
			// colour code and timestamp in front of the CSSharp line.
			lineStart := strings.LastIndexByte(log[:i], '\n') + 1
			if startupLinePrefixPattern.MatchString(log[lineStart:i]) {
				if end := i + len(m); end > cut {
					cut = end
				}
				break
			}
			from = i
		}
	}
	if cut < 0 {
		return log, false
	}
	return log[cut:], true
}

// startupLinePrefixPattern is what may precede a startup marker on its line:
// nothing, or ANSI colour codes and a "[hh:mm:ss.mmm] " timestamp.
var startupLinePrefixPattern = regexp.MustCompile(`^(?:\x1b\[[0-9;]*m)*(?:\[[0-9:.]+\] )?$`)

// parseATCS2DBEngine returns the lower-cased DatabaseType and, for MySQL, a
// "host:port/db" target. Hosts that all mean "this machine" compare equal.
func parseATCS2DBEngine(data []byte) (engine, mysqlTarget string) {
	var c atcs2DBConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return "", ""
	}
	engine = strings.ToLower(strings.TrimSpace(c.DatabaseType))
	if engine != ATCS2DBEngineMySQL {
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

// resolveScopingSupport decides whether a server's plugin is 2.0.0 or newer
// (and so scopes persistent config per server). A version is preferred when
// one can be read: first the version the running plugin logged (what is
// actually loaded), then the release csm last deployed. Only when neither
// parses does it fall back to probing AutoTournamentCS2.dll for the convar
// name. It returns the version used, or ""
// for the DLL fallback.
func resolveScopingSupport(logVersion, deployedVersion string, dllProbe func() scopingSupport) (scopingSupport, string) {
	for _, v := range []string{logVersion, deployedVersion} {
		if cmp, ok := compareVersions(v, ATCS2MinVersion); ok {
			if cmp >= 0 {
				return scopingYes, normalizeVersion(v)
			}
			return scopingNo, normalizeVersion(v)
		}
	}
	return dllProbe(), ""
}

// atcs2PluginVersionPattern matches the ways the plugin logs its version:
//
//	[Auto Tournament CS2 v2.0.0 LOADED] ...
//	{"server_id":"s_1","plugin_version":"2.0.0",...}
//
// The event form is also what a pre-2.0.0 build sends, so an old plugin
// that is still loaded is recognised by its version too.
var atcs2PluginVersionPattern = regexp.MustCompile(`(?:\[Auto Tournament CS2 v|"plugin_version"\s*:\s*"v?)([0-9]+(?:\.[0-9]+)+)`)

// lastATCS2PluginVersion returns the most recent plugin version in a server
// log, or "" when there is none.
func lastATCS2PluginVersion(log string) string {
	m := atcs2PluginVersionPattern.FindAllStringSubmatch(log, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "v"), "V")
}

// compareVersions compares dotted numeric versions ("1.4.26", "v1.4.26").
// Missing components count as 0, and a pre-release/build suffix on a
// component ("26-rc1") is ignored. ok is false when either side has no
// numeric components.
func compareVersions(a, b string) (cmp int, ok bool) {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return 0, false
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

func parseVersion(v string) ([]int, bool) {
	v = normalizeVersion(v)
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		end := 0
		for end < len(p) && p[end] >= '0' && p[end] <= '9' {
			end++
		}
		if end == 0 {
			return nil, false
		}
		n, err := strconv.Atoi(p[:end])
		if err != nil {
			return nil, false
		}
		out = append(out, n)
		if end < len(p) {
			break // suffix such as "-rc1": stop comparing here
		}
	}
	return out, true
}

// dllScopingSupport looks for the at_config_scope convar name in
// AutoTournamentCS2.dll. .NET stores C# string literals as UTF-16LE and attribute
// arguments as UTF-8, so both encodings are checked. No match means the
// build predates 2.0.0.
func dllScopingSupport(path string) scopingSupport {
	data, err := os.ReadFile(path)
	if err != nil {
		return scopingUnknown
	}
	return scopingSupportInBytes(data)
}

func scopingSupportInBytes(data []byte) scopingSupport {
	needle := []byte(ATCS2ConfigScopeConVar)
	if bytes.Contains(data, needle) || bytes.Contains(data, utf16le(ATCS2ConfigScopeConVar)) {
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

// atcs2ServerIDPattern matches the ways the plugin logs its server id:
//
//	[SaveConfigValue] Saved config: at_server_id = s_3
//	[LoadPersistentConfig] Loaded at_server_id: s_3
//	"server_id":"s_3"
var atcs2ServerIDPattern = regexp.MustCompile(`(?:\bat_server_id\s*[=:]\s*"?|"server_id"\s*:\s*")([A-Za-z0-9_.:-]+)`)

// lastATCS2ServerID returns the most recent server id in a plugin log, or
// "" when there is none.
func lastATCS2ServerID(log string) string {
	m := atcs2ServerIDPattern.FindAllStringSubmatch(log, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

// scopeArgOnRunningServer looks through `ps -eo args=` output for the cs2
// process bound to gamePort and reports whether it was started with
// +at_config_scope. Wrapper processes (tmux, bash -lc, su) repeat the
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
		if strings.Contains(line, ATCS2ConfigScopeArg) {
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

// evaluateATCS2Scope turns the per-server facts into a doctor result.
//
// It fails when a server still runs the old plugin: a build older than
// ATCS2MinVersion, or the pre-2.0.0 plugins/MatchZy/ folder still installed.
// It also fails when servers are known to be overwriting each other's
// persistent config: two or more servers report the same at_server_id, or
// two or more servers share one MySQL database and are running without
// +at_config_scope (the plugin then falls back to the machine name, which is
// the same for every server here).
func evaluateATCS2Scope(user string, facts []atcs2ServerFacts) DoctorCheck {
	const (
		id    = "at-cs2-plugin"
		title = "Auto Tournament CS2 plugin (version, per-server config)"
	)
	if user == "" {
		user = DefaultCS2User
	}

	byID := map[string][]int{}
	byTarget := map[string][]int{}
	factsByServer := map[int]atcs2ServerFacts{}
	sqlite := 0
	for _, f := range facts {
		factsByServer[f.Server] = f
		if f.ServerID != "" {
			byID[f.ServerID] = append(byID[f.ServerID], f.Server)
		}
		switch f.DBEngine {
		case ATCS2DBEngineMySQL:
			byTarget[f.MySQLTarget] = append(byTarget[f.MySQLTarget], f.Server)
		case ATCS2DBEngineSQLite:
			sqlite++
		}
	}

	var failures, warnings []string
	var noScopeArg, unknownScoping []int
	dupIDs := false

	// The old plugin, on any server and whatever its database.
	var outdated []string
	for _, f := range facts {
		var why []string
		if f.Scoping == scopingNo {
			if f.PluginVersion != "" {
				why = append(why, "plugin "+f.PluginVersion)
			} else {
				why = append(why, "a build without "+ATCS2ConfigScopeConVar)
			}
		}
		if f.LegacyPlugin {
			why = append(why, fmt.Sprintf("old plugins/%s/ folder", legacyATCS2PluginDirName))
		}
		if len(why) > 0 {
			outdated = append(outdated, fmt.Sprintf("server-%d (%s)", f.Server, strings.Join(why, ", ")))
		}
	}
	if len(outdated) > 0 {
		failures = append(failures, fmt.Sprintf("%s still run the old plugin. csm needs %s, and the old plugin must be replaced, not kept next to it: it does not read %s or cfg/%s/, and it cannot talk to Auto Tournament 3.0.",
			strings.Join(outdated, ", "), ATCS2Requirement(), ATCS2ConfigScopeArg, ATCS2CfgDirName))
	}

	ids := make([]string, 0, len(byID))
	for k := range byID {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, sid := range ids {
		if servers := byID[sid]; len(servers) > 1 {
			dupIDs = true
			failures = append(failures, fmt.Sprintf("%s all report at_server_id %q; a tournament manager sees them as one server.", serverList(servers), sid))
		}
	}

	var starting []int
	for _, f := range facts {
		if f.Starting {
			starting = append(starting, f.Server)
		}
	}
	if len(starting) > 0 {
		warnings = append(warnings, fmt.Sprintf("%s: server is still starting (the plugin has not logged its version since the last start, so the deployed release was checked instead). Run sudo csm doctor again in a minute.", serverList(starting)))
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
		var groupNoScopeArg, groupUnknown []int
		for _, n := range servers {
			f := factsByServer[n]
			if f.Scoping == scopingUnknown && !f.LegacyPlugin {
				groupUnknown = append(groupUnknown, n)
			}
			if f.Running && !f.HasScopeArg {
				groupNoScopeArg = append(groupNoScopeArg, n)
			}
		}
		if len(groupNoScopeArg) > 0 {
			failures = append(failures, fmt.Sprintf("%s share MySQL %s, and %s are running without %s (started by an older CSM), so they all fall back to the same machine-name scope.", serverList(servers), t, serverList(groupNoScopeArg), ATCS2ConfigScopeArg))
		}
		if len(groupUnknown) > 0 {
			warnings = append(warnings, fmt.Sprintf("%s share MySQL %s, but %s was not found for %s, so the plugin version could not be checked.", serverList(servers), t, ATCS2DLLName, serverList(groupUnknown)))
		}
		noScopeArg = append(noScopeArg, groupNoScopeArg...)
		unknownScoping = append(unknownScoping, groupUnknown...)
	}

	var steps []string
	if len(outdated) > 0 || len(unknownScoping) > 0 {
		steps = append(steps,
			fmt.Sprintf("Replace the old plugin with %s and restart every server: sudo csm update-plugins", ATCS2Requirement()),
		)
	}
	if len(noScopeArg) > 0 && len(outdated) == 0 && len(unknownScoping) == 0 {
		steps = append(steps, fmt.Sprintf("Restart the servers so they start with %s: sudo csm restart", ATCS2ConfigScopeArg))
	}
	if dupIDs {
		steps = append(steps, "Then reconfigure every server from your tournament manager (for Auto Tournament, re-save or re-bootstrap each server) so each one writes its own at_server_id and bootstrap URL.")
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
		detail = fmt.Sprintf("Servers share one MySQL database, run %s and start with %s.", ATCS2Requirement(), ATCS2ConfigScopeArg)
	case sqlite == len(facts):
		detail = "Every server uses its own SQLite database."
	default:
		detail = "No servers share a MySQL database and no duplicate at_server_id was found."
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
