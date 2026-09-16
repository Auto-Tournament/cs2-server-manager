package csm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// MatchZy database engines the install wizard can write into database.json.
const (
	// MatchzyDBEngineMySQL is one MySQL database shared by every server (the
	// default; shared stats are the point). Safe with several servers only
	// when the plugin scopes persistent config per server, see
	// MatchzyScopingRequirement.
	MatchzyDBEngineMySQL = "mysql"

	// MatchzyDBEngineSQLite gives each server its own SQLite file. MatchZy
	// creates matchzy.db inside each server's own plugin directory, so one
	// shared database.json with DatabaseType=SQLite already yields one
	// database per server. Stats are not shared, but it is safe with any
	// plugin build.
	MatchzyDBEngineSQLite = "sqlite"
)

// csmManagedDBNote is written into database.json as __CSM_NOTE. Only files
// carrying it are rewritten by CSM.
const csmManagedDBNote = "This file is managed by CSM's install wizard. Manual edits may be overwritten."

// matchzyDBFile is database.json as CSM writes it.
type matchzyDBFile struct {
	matchzyDBConfig
	CSMNote string `json:"__CSM_NOTE,omitempty"`
	DBMode  string `json:"__CSM_DB_MODE,omitempty"`
}

// NormalizeMatchzyDBEngine maps user input to a MatchzyDBEngine* constant.
// It returns "" for empty input (meaning "not specified") and an error for
// anything unrecognised.
func NormalizeMatchzyDBEngine(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return "", nil
	case "mysql", "shared", "shared-mysql":
		return MatchzyDBEngineMySQL, nil
	case "sqlite", "sqlite-per-server", "per-server":
		return MatchzyDBEngineSQLite, nil
	}
	return "", fmt.Errorf("unknown MatchZy database engine %q (want %q or %q)", s, MatchzyDBEngineMySQL, MatchzyDBEngineSQLite)
}

// isCSMManagedDBNote reports whether a __CSM_NOTE value is CSM's own marker.
// An operator who edits the file and replaces the note with their own text has
// taken the file over, so only CSM's wording counts.
func isCSMManagedDBNote(note string) bool {
	return strings.Contains(strings.ToLower(note), "managed by csm")
}

// inspectMatchzyDBConfig reports whether database.json exists at path and
// whether CSM manages it. A file that exists but can't be parsed counts as
// not managed, so CSM never overwrites something it doesn't understand.
func inspectMatchzyDBConfig(path string) (exists, managed bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return true, false, err
	}
	var f struct {
		CSMNote string `json:"__CSM_NOTE"`
	}
	if json.Unmarshal(data, &f) != nil {
		return true, false, nil
	}
	return true, isCSMManagedDBNote(f.CSMNote), nil
}

// writeManagedMatchzyDBConfig writes cfg to path, tagged with CSM's managed
// note, unless path already exists and is not CSM-managed. It returns whether
// it wrote the file. The caller creates the parent directory.
func writeManagedMatchzyDBConfig(path string, cfg matchzyDBConfig, dbMode string) (bool, error) {
	exists, managed, err := inspectMatchzyDBConfig(path)
	if err != nil {
		return false, err
	}
	if exists && !managed {
		return false, nil
	}
	data, err := json.MarshalIndent(matchzyDBFile{
		matchzyDBConfig: cfg,
		CSMNote:         csmManagedDBNote,
		DBMode:          dbMode,
	}, "", "  ")
	if err != nil {
		return false, fmt.Errorf("failed to marshal database config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o664); err != nil {
		return false, err
	}
	return true, nil
}

// wizardMatchzyDBConfig returns the database.json contents and __CSM_DB_MODE
// marker for the install wizard's choices.
func wizardMatchzyDBConfig(cfg BootstrapConfig) (matchzyDBConfig, string) {
	engine, _ := NormalizeMatchzyDBEngine(cfg.DBEngine)
	mode := strings.ToLower(strings.TrimSpace(cfg.DBMode))

	if engine == MatchzyDBEngineSQLite {
		// The MySQL fields are ignored by MatchZy in SQLite mode. Keeping the
		// defaults means switching back to shared MySQL is a one-word edit.
		return matchzyDBConfig{
			DatabaseType:  "SQLite",
			MySQLHost:     "127.0.0.1",
			MySQLPort:     3306,
			MySQLDatabase: DefaultMatchzyDBName,
			MySQLUsername: DefaultMatchzyDBUser,
			MySQLPassword: DefaultMatchzyDBPassword,
		}, MatchzyDBEngineSQLite
	}

	if mode == "external" || cfg.MatchzySkipDocker {
		host := strings.TrimSpace(cfg.ExternalDBHost)
		if host == "" {
			host = "127.0.0.1"
		}
		port := cfg.ExternalDBPort
		if port <= 0 {
			port = 3306
		}
		name := strings.TrimSpace(cfg.ExternalDBName)
		if name == "" {
			name = DefaultMatchzyDBName
		}
		user := strings.TrimSpace(cfg.ExternalDBUser)
		if user == "" {
			user = DefaultMatchzyDBUser
		}
		pass := cfg.ExternalDBPassword
		if strings.TrimSpace(pass) == "" {
			pass = DefaultMatchzyDBPassword
		}
		return matchzyDBConfig{
			DatabaseType:  "MySQL",
			MySQLHost:     host,
			MySQLPort:     port,
			MySQLDatabase: name,
			MySQLUsername: user,
			MySQLPassword: pass,
		}, "external"
	}

	return matchzyDBConfig{
		DatabaseType:  "MySQL",
		MySQLHost:     "127.0.0.1",
		MySQLPort:     3306,
		MySQLDatabase: DefaultMatchzyDBName,
		MySQLUsername: DefaultMatchzyDBUser,
		MySQLPassword: DefaultMatchzyDBPassword,
	}, "docker"
}

// sharedMySQLScopingNotice is logged when several servers are set up against
// one shared MySQL database.
func sharedMySQLScopingNotice(numServers int) string {
	return fmt.Sprintf(
		"%d servers will share one MySQL database. This needs %s; older builds make every server load the same matchzy_server_id. CSM passes %s <hostname>-server-N on each server's start command. On an older plugin build, rerun the wizard with SQLite per server (or MATCHZY_DB_ENGINE=sqlite).",
		numServers, MatchzyScopingRequirement(), MatchzyConfigScopeArg,
	)
}
