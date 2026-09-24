package csm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Auto Tournament CS2 database engines the install wizard can write into database.json.
const (
	// ATCS2DBEngineMySQL is one MySQL database shared by every server (the
	// default; shared stats are the point). Safe with several servers only
	// when the plugin scopes persistent config per server, see
	// ATCS2Requirement.
	ATCS2DBEngineMySQL = "mysql"

	// ATCS2DBEngineSQLite gives each server its own SQLite file. The plugin
	// creates auto_tournament_cs2.db inside each server's own plugin
	// directory, so one shared database.json with DatabaseType=SQLite already
	// yields one database per server. Stats are not shared.
	ATCS2DBEngineSQLite = "sqlite"
)

// csmManagedDBNote is written into database.json as __CSM_NOTE. Only files
// carrying it are rewritten by CSM.
const csmManagedDBNote = "This file is managed by CSM's install wizard. Manual edits may be overwritten."

// atcs2DBFile is database.json as CSM writes it.
type atcs2DBFile struct {
	atcs2DBConfig
	CSMNote string `json:"__CSM_NOTE,omitempty"`
	DBMode  string `json:"__CSM_DB_MODE,omitempty"`
}

// NormalizeATCS2DBEngine maps user input to a ATCS2DBEngine* constant.
// It returns "" for empty input (meaning "not specified") and an error for
// anything unrecognised.
func NormalizeATCS2DBEngine(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return "", nil
	case "mysql", "shared", "shared-mysql":
		return ATCS2DBEngineMySQL, nil
	case "sqlite", "sqlite-per-server", "per-server":
		return ATCS2DBEngineSQLite, nil
	}
	return "", fmt.Errorf("unknown Auto Tournament CS2 database engine %q (want %q or %q)", s, ATCS2DBEngineMySQL, ATCS2DBEngineSQLite)
}

// isCSMManagedDBNote reports whether a __CSM_NOTE value is CSM's own marker.
// An operator who edits the file and replaces the note with their own text has
// taken the file over, so only CSM's wording counts.
func isCSMManagedDBNote(note string) bool {
	return strings.Contains(strings.ToLower(note), "managed by csm")
}

// inspectATCS2DBConfig reports whether database.json exists at path and
// whether CSM manages it. A file that exists but can't be parsed counts as
// not managed, so CSM never overwrites something it doesn't understand.
func inspectATCS2DBConfig(path string) (exists, managed bool, err error) {
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

// writeManagedATCS2DBConfig writes cfg to path, tagged with CSM's managed
// note, unless path already exists and is not CSM-managed. It returns whether
// it wrote the file. The caller creates the parent directory.
func writeManagedATCS2DBConfig(path string, cfg atcs2DBConfig, dbMode string) (bool, error) {
	exists, managed, err := inspectATCS2DBConfig(path)
	if err != nil {
		return false, err
	}
	if exists && !managed {
		return false, nil
	}
	data, err := json.MarshalIndent(atcs2DBFile{
		atcs2DBConfig: cfg,
		CSMNote:       csmManagedDBNote,
		DBMode:        dbMode,
	}, "", "  ")
	if err != nil {
		return false, fmt.Errorf("failed to marshal database config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o664); err != nil {
		return false, err
	}
	return true, nil
}

// wizardATCS2DBConfig returns the database.json contents and __CSM_DB_MODE
// marker for the install wizard's choices.
func wizardATCS2DBConfig(cfg BootstrapConfig) (atcs2DBConfig, string) {
	engine, _ := NormalizeATCS2DBEngine(cfg.DBEngine)
	mode := strings.ToLower(strings.TrimSpace(cfg.DBMode))

	if engine == ATCS2DBEngineSQLite {
		// The MySQL fields are ignored by the plugin in SQLite mode. Keeping the
		// defaults means switching back to shared MySQL is a one-word edit.
		return atcs2DBConfig{
			DatabaseType:  "SQLite",
			MySQLHost:     "127.0.0.1",
			MySQLPort:     3306,
			MySQLDatabase: DefaultATCS2DBName,
			MySQLUsername: DefaultATCS2DBUser,
			MySQLPassword: DefaultATCS2DBPassword,
		}, ATCS2DBEngineSQLite
	}

	if mode == "external" || cfg.ATCS2SkipDocker {
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
			name = DefaultATCS2DBName
		}
		user := strings.TrimSpace(cfg.ExternalDBUser)
		if user == "" {
			user = DefaultATCS2DBUser
		}
		pass := cfg.ExternalDBPassword
		if strings.TrimSpace(pass) == "" {
			pass = DefaultATCS2DBPassword
		}
		return atcs2DBConfig{
			DatabaseType:  "MySQL",
			MySQLHost:     host,
			MySQLPort:     port,
			MySQLDatabase: name,
			MySQLUsername: user,
			MySQLPassword: pass,
		}, "external"
	}

	return atcs2DBConfig{
		DatabaseType:  "MySQL",
		MySQLHost:     "127.0.0.1",
		MySQLPort:     3306,
		MySQLDatabase: DefaultATCS2DBName,
		MySQLUsername: DefaultATCS2DBUser,
		MySQLPassword: DefaultATCS2DBPassword,
	}, "docker"
}

// sharedMySQLScopingNotice is logged when several servers are set up against
// one shared MySQL database.
func sharedMySQLScopingNotice(numServers int) string {
	return fmt.Sprintf(
		"%d servers will share one MySQL database. CSM passes %s <hostname>-server-N on each server's start command so each server keeps its own at_server_id and settings. This needs %s. To keep the servers' data apart instead, rerun the wizard with SQLite per server (or AT_DB_ENGINE=sqlite).",
		numServers, ATCS2ConfigScopeArg, ATCS2Requirement(),
	)
}

// keepExistingDockerDBCredentials keeps the database name, user and password
// from an existing database.json when the wizard (re)writes it for the
// Docker-managed database. Those name what already exists in the container's
// volume; replacing them with today's defaults would point the plugin at a
// new, empty database. Only a file that already points at this machine is
// read, so an external database's settings are never copied into Docker.
func keepExistingDockerDBCredentials(path string, desired *atcs2DBConfig, dbMode string) {
	if dbMode != "docker" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var existing atcs2DBConfig
	if json.Unmarshal(data, &existing) != nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(existing.MySQLHost)) {
	case "127.0.0.1", "localhost", "::1":
	default:
		return
	}
	if strings.TrimSpace(existing.MySQLDatabase) == "" || strings.TrimSpace(existing.MySQLUsername) == "" || existing.MySQLPassword == "" {
		return
	}
	desired.MySQLDatabase = existing.MySQLDatabase
	desired.MySQLUsername = existing.MySQLUsername
	desired.MySQLPassword = existing.MySQLPassword
}
