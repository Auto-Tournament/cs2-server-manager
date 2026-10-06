package hostagent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Payloads of the hosts channel (FLEET.md §18.2). protocol/host-v1/messages
// holds the normative JSON Schemas; these structs follow them field for field.

// --- transport --------------------------------------------------------------

// HelloPayload is the host's first message (host -> platform, ephemeral).
type HelloPayload struct {
	HostID       string        `json:"host_id"`
	MachineID    string        `json:"machine_id"`
	TenantID     string        `json:"tenant_id"`
	Protocol     ProtocolRange `json:"protocol"`
	Versions     HostVersions  `json:"versions"`
	Capabilities []string      `json:"capabilities"`
	Hostname     string        `json:"hostname"`
	BootID       string        `json:"boot_id"`
	Stream       StreamState   `json:"stream"`
}

// ProtocolRange is hello.protocol.
type ProtocolRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// HostVersions is hello.versions.
type HostVersions struct {
	CSM string `json:"csm"`
	OS  string `json:"os,omitempty"`
}

// StreamState is hello.stream (FLEET.md §6.4).
type StreamState struct {
	ID        string `json:"id"`
	LastTxSeq int64  `json:"last_tx_seq"`
	LastRxSeq int64  `json:"last_rx_seq"`
}

// WelcomePayload is the platform's answer to hello.
type WelcomePayload struct {
	SessionID string `json:"session_id"`
	Protocol  int    `json:"protocol"`
	HostID    string `json:"host_id,omitempty"`
	Heartbeat struct {
		IntervalMS int64 `json:"interval_ms"`
		TimeoutMS  int64 `json:"timeout_ms"`
	} `json:"heartbeat"`
	Resume struct {
		Result            string `json:"result"`
		PlatformLastRxSeq int64  `json:"platform_last_rx_seq"`
	} `json:"resume"`
}

func (w *WelcomePayload) validate() error {
	switch {
	case w.SessionID == "":
		return fmt.Errorf("welcome without session_id")
	case w.Protocol != ProtocolVersion:
		return fmt.Errorf("welcome picked protocol %d, this agent speaks %d", w.Protocol, ProtocolVersion)
	case w.Heartbeat.IntervalMS < 1000 || w.Heartbeat.TimeoutMS < 1000:
		return fmt.Errorf("welcome heartbeat below 1000 ms")
	case w.Resume.Result != "resumed" && w.Resume.Result != "reset":
		return fmt.Errorf("welcome resume.result %q", w.Resume.Result)
	case w.Resume.PlatformLastRxSeq < 0:
		return fmt.Errorf("welcome resume.platform_last_rx_seq < 0")
	}
	return nil
}

// PingPayload is ping and pong.
type PingPayload struct {
	T int64 `json:"t"`
}

// ErrorPayload is the ephemeral protocol error.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Type    string `json:"type,omitempty"`
}

// AuthRotatePayload is auth.rotate (FLEET.md §4.3) for a host token.
type AuthRotatePayload struct {
	Token         string `json:"token"`
	OldValidUntil int64  `json:"old_valid_until"`
}

// --- platform -> host commands ---------------------------------------------

// Force is present on a disruptive command the platform wants run although a
// match is in progress (root admins only, audited on the platform).
type Force struct {
	By     string `json:"by"`
	Reason string `json:"reason"`
}

func (f *Force) validate() error {
	if f == nil {
		return nil
	}
	if strings.TrimSpace(f.By) == "" || len(f.By) > 128 {
		return fmt.Errorf("force.by must be 1-128 characters")
	}
	if len(f.Reason) > 512 {
		return fmt.Errorf("force.reason is over 512 characters")
	}
	return nil
}

// commandBase is carried by every command.
type commandBase struct {
	// ExpiresAt (ms since epoch) is optional: a command replayed after it is
	// rejected with code "expired" and not run.
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

// ServersListCmd is host.servers.list.
type ServersListCmd struct{ commandBase }

// ServerStartCmd is server.start.
type ServerStartCmd struct {
	commandBase
	Server     string `json:"server"`
	LaunchMode string `json:"launch_mode,omitempty"`
}

// ServerStopCmd is server.stop.
type ServerStopCmd struct {
	commandBase
	Server string `json:"server"`
	GraceS *int   `json:"grace_s,omitempty"`
	Force  *Force `json:"force,omitempty"`
}

// ServerRestartCmd is server.restart.
type ServerRestartCmd struct {
	commandBase
	Server string `json:"server"`
	Reason string `json:"reason"`
	Force  *Force `json:"force,omitempty"`
}

// ServerCreateCmd is server.create.
type ServerCreateCmd struct {
	commandBase
	Count      *int   `json:"count,omitempty"`
	NamePrefix string `json:"name_prefix,omitempty"`
	GamePort   *int   `json:"game_port,omitempty"`
	Enroll     *bool  `json:"enroll"`
	// EnrollKey (optional, csm addition) is the fleet enrollment key to write
	// into the new servers' fleet.cfg. Without it the key the host linked
	// with is used.
	EnrollKey string `json:"enroll_key,omitempty"`
	// AcceptLicense (optional, csm addition) is the Ready Up license use the
	// platform's admin accepted: noncommercial or commercial. Used when the
	// host has no answer of its own (LicenseAnswerAdopter).
	AcceptLicense string `json:"accept_license,omitempty"`
}

// ServerRemoveCmd is server.remove.
type ServerRemoveCmd struct {
	commandBase
	Server    string `json:"server"`
	KeepFiles bool   `json:"keep_files,omitempty"`
	Force     *Force `json:"force,omitempty"`
}

// ServerSetLaunchArgsCmd is server.set_launch_args.
type ServerSetLaunchArgsCmd struct {
	commandBase
	Server string   `json:"server"`
	Args   []string `json:"args"`
	Force  *Force   `json:"force,omitempty"`
}

// UpdateGameCmd is host.update_game.
type UpdateGameCmd struct {
	commandBase
	Servers []string `json:"servers,omitempty"`
	Force   *Force   `json:"force,omitempty"`
}

// UpdatePluginsCmd is host.update_plugins.
type UpdatePluginsCmd struct {
	commandBase
	Servers []string `json:"servers,omitempty"`
	ReadyUp *struct {
		Version string `json:"version"`
		Bundle  string `json:"bundle"`
	} `json:"readyup"`
	Force *Force `json:"force,omitempty"`
	// AcceptLicense: as on server.create.
	AcceptLicense string `json:"accept_license,omitempty"`
}

// LogsTailCmd is logs.tail.
type LogsTailCmd struct {
	commandBase
	Server string `json:"server,omitempty"`
	Source string `json:"source"`
	Lines  *int   `json:"lines,omitempty"`
	Follow bool   `json:"follow,omitempty"`
	MaxS   *int   `json:"max_s,omitempty"`
}

// LogsStopCmd is logs.stop.
type LogsStopCmd struct {
	commandBase
	StreamID string `json:"stream_id"`
}

// UpdatesHoldCmd is host.updates_hold.
type UpdatesHoldCmd struct {
	commandBase
	Mode string `json:"mode"`
}

// --- host -> platform ------------------------------------------------------

// ResultError is host.result.error.
type ResultError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// ResultPayload is host.result: exactly one per reliable platform message,
// with ref = that message's id.
type ResultPayload struct {
	Status string       `json:"status"` // ok | rejected | failed
	Error  *ResultError `json:"error,omitempty"`
	Output string       `json:"output,omitempty"`
}

// ProgressPayload is host.progress for long jobs.
type ProgressPayload struct {
	Ref  string `json:"ref"`
	Step string `json:"step"`
	Pct  *int   `json:"pct,omitempty"`
}

// HealthPayload is host.health.
type HealthPayload struct {
	Server   string `json:"server"`
	Event    string `json:"event"` // crashed | exited | hung | recovered | restarted
	ExitCode *int   `json:"exit_code,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// LogsChunkPayload is logs.chunk.
type LogsChunkPayload struct {
	StreamID string   `json:"stream_id"`
	Server   string   `json:"server,omitempty"`
	Lines    []string `json:"lines"`
	EOF      bool     `json:"eof,omitempty"`
}

// Status values of host.result.
const (
	StatusOK       = "ok"
	StatusRejected = "rejected"
	StatusFailed   = "failed"
)

// Error codes of host.result.error (listed in protocol/host-v1/messages/host.result.json).
const (
	CodeBadArgs         = "bad_args"
	CodeUnknownServer   = "unknown_server"
	CodeMatchInProgress = "match_in_progress"
	CodeBusy            = "busy"
	CodeExpired         = "expired"
	CodeStale           = "stale"
	CodeUnsupported     = "unsupported"
	CodeNoEnrollKey     = "no_enroll_key"
	CodeNoRelease       = "no_release"
	CodeLicense         = "license_not_accepted"
	CodeInstallFailed   = "install_failed"
	CodeFailed          = "failed"
	CodeNotFound        = "not_found"
	CodeLimit           = "limit"
)

// --- validation helpers ----------------------------------------------------

var serverNameRe = regexp.MustCompile(`^server-([1-9][0-9]{0,3})$`)

// ParseServerName reads "server-N" and returns N.
func ParseServerName(s string) (int, error) {
	m := serverNameRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("server must look like server-N, not %q", truncate(s, 40))
	}
	n, _ := strconv.Atoi(m[1])
	return n, nil
}

// ServerName is the inverse of ParseServerName.
func ServerName(n int) string { return "server-" + strconv.Itoa(n) }

func parseServerList(list []string) ([]int, error) {
	if len(list) > 64 {
		return nil, fmt.Errorf("at most 64 servers per command")
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(list))
	for _, s := range list {
		n, err := ParseServerName(s)
		if err != nil {
			return nil, err
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

var readyUpVersionRe = regexp.MustCompile(`^(latest|v?[0-9]+\.[0-9]+\.[0-9]+([-+.][0-9A-Za-z.-]{1,32})?)$`)

// validateCommand checks a decoded command's fields beyond their JSON types.
func validateCommand(typ string, cmd any) error {
	switch c := cmd.(type) {
	case *ServersListCmd:
		return nil
	case *ServerStartCmd:
		if _, err := ParseServerName(c.Server); err != nil {
			return err
		}
		switch c.LaunchMode {
		case "", "default", "alternate", "binary":
		default:
			return fmt.Errorf("launch_mode must be default, alternate or binary")
		}
	case *ServerStopCmd:
		if _, err := ParseServerName(c.Server); err != nil {
			return err
		}
		if c.GraceS != nil && (*c.GraceS < 0 || *c.GraceS > 300) {
			return fmt.Errorf("grace_s must be 0-300")
		}
		return c.Force.validate()
	case *ServerRestartCmd:
		if _, err := ParseServerName(c.Server); err != nil {
			return err
		}
		if len(c.Reason) > 512 {
			return fmt.Errorf("reason is over 512 characters")
		}
		return c.Force.validate()
	case *ServerCreateCmd:
		if c.Enroll == nil {
			return fmt.Errorf("enroll is required")
		}
		if c.Count != nil && (*c.Count < 1 || *c.Count > 16) {
			return fmt.Errorf("count must be 1-16")
		}
		if c.GamePort != nil && (*c.GamePort < 1 || *c.GamePort > 65535) {
			return fmt.Errorf("game_port must be 1-65535")
		}
		if len(c.NamePrefix) > 64 {
			return fmt.Errorf("name_prefix is over 64 characters")
		}
		if c.EnrollKey != "" && !fleetKeyRe.MatchString(c.EnrollKey) {
			return fmt.Errorf("enroll_key is not a fleet enrollment key")
		}
	case *ServerRemoveCmd:
		if _, err := ParseServerName(c.Server); err != nil {
			return err
		}
		return c.Force.validate()
	case *ServerSetLaunchArgsCmd:
		if _, err := ParseServerName(c.Server); err != nil {
			return err
		}
		if len(c.Args) > 64 {
			return fmt.Errorf("at most 64 args")
		}
		return c.Force.validate()
	case *UpdateGameCmd:
		if _, err := parseServerList(c.Servers); err != nil {
			return err
		}
		return c.Force.validate()
	case *UpdatePluginsCmd:
		if _, err := parseServerList(c.Servers); err != nil {
			return err
		}
		if c.ReadyUp == nil {
			return fmt.Errorf("readyup is required")
		}
		if !readyUpVersionRe.MatchString(c.ReadyUp.Version) {
			return fmt.Errorf("readyup.version must be latest or X.Y.Z")
		}
		if c.ReadyUp.Bundle != "default" && c.ReadyUp.Bundle != "skins" {
			return fmt.Errorf("readyup.bundle must be default or skins")
		}
		return c.Force.validate()
	case *LogsTailCmd:
		if c.Server != "" {
			if _, err := ParseServerName(c.Server); err != nil {
				return err
			}
		}
		switch c.Source {
		case "console", "readyup":
			if c.Server == "" {
				return fmt.Errorf("source %s needs a server", c.Source)
			}
		case "csm", "monitor":
		default:
			return fmt.Errorf("source must be console, readyup, csm or monitor")
		}
		if c.Lines != nil && (*c.Lines < 0 || *c.Lines > 2000) {
			return fmt.Errorf("lines must be 0-2000")
		}
		if c.MaxS != nil && (*c.MaxS < 1 || *c.MaxS > 3600) {
			return fmt.Errorf("max_s must be 1-3600")
		}
	case *LogsStopCmd:
		if !ulidRe.MatchString(c.StreamID) {
			return fmt.Errorf("stream_id is not a stream id")
		}
	case *UpdatesHoldCmd:
		switch c.Mode {
		case "on", "off", "auto":
		default:
			return fmt.Errorf("mode must be on, off or auto")
		}
	default:
		return fmt.Errorf("no validator for %s", typ)
	}
	return nil
}

// newCommand returns an empty command struct for a platform -> host type, or
// nil when the type is not one.
func newCommand(typ string) any {
	switch typ {
	case TypeServersList:
		return &ServersListCmd{}
	case TypeServerStart:
		return &ServerStartCmd{}
	case TypeServerStop:
		return &ServerStopCmd{}
	case TypeServerRestart:
		return &ServerRestartCmd{}
	case TypeServerCreate:
		return &ServerCreateCmd{}
	case TypeServerRemove:
		return &ServerRemoveCmd{}
	case TypeServerSetLaunch:
		return &ServerSetLaunchArgsCmd{}
	case TypeUpdateGame:
		return &UpdateGameCmd{}
	case TypeUpdatePlugins:
		return &UpdatePluginsCmd{}
	case TypeLogsTail:
		return &LogsTailCmd{}
	case TypeLogsStop:
		return &LogsStopCmd{}
	case TypeUpdatesHold:
		return &UpdatesHoldCmd{}
	}
	return nil
}

// expiresAt returns the command's expires_at (0 = none).
func expiresAt(cmd any) int64 {
	type hasBase interface{ base() *commandBase }
	if b, ok := cmd.(hasBase); ok {
		return b.base().ExpiresAt
	}
	return 0
}

func (c *commandBase) base() *commandBase { return c }

// mustJSON marshals v; the payloads here always marshal.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
