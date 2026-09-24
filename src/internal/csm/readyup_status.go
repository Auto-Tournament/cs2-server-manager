package csm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Ready Up's local status endpoint
//
// Ready Up (github.com/Auto-Tournament/ready-up) runs a small read-only HTTP
// server next to each CS2 server (docs/FLEET.md §17). csm reads it to show a
// live fleet table and to refuse restarts and updates while a match is live:
//
//	GET http://127.0.0.1:<port>/status   -> one JSON document (ReadyUpStatus)
//	GET http://127.0.0.1:<port>/stream   -> Server-Sent Events (readyup_stream.go)
//
// The port and a read-only token are in game/csgo/readyup/status.json, which
// Ready Up writes on start. Without that file csm tries the default port, game
// port + 50. A server that runs the Auto Tournament CS2 plugin instead of Ready
// Up has neither, and csm treats it exactly as before: no fleet data, no
// gating.

// ReadyUpPortOffset is the default distance between a server's game port and
// its Ready Up status port (status_http_port, FLEET.md §17.3).
const ReadyUpPortOffset = 50

// readyUpStatusTimeout bounds one /status request. The endpoint is on
// loopback and answers from a prebuilt buffer, so anything slower than this is
// a server that is not answering.
const readyUpStatusTimeout = 2 * time.Second

// ReadyUpDiscovery is game/csgo/readyup/status.json.
type ReadyUpDiscovery struct {
	Port      int    `json:"port"`
	Bind      string `json:"bind,omitempty"`
	Token     string `json:"token,omitempty"`
	PID       int    `json:"pid,omitempty"`
	GamePort  int    `json:"game_port,omitempty"`
	StartedAt int64  `json:"started_at,omitempty"`

	// FromFile is false when the file was missing and Port is the default.
	FromFile bool `json:"-"`
}

// readyUpDiscoveryPath is where Ready Up writes its discovery file, relative
// to a server-N directory.
func readyUpDiscoveryPath(serverDir string) string {
	return filepath.Join(serverDir, "game", "csgo", "readyup", "status.json")
}

// DiscoverReadyUp reads a server's status.json. When the file does not exist
// (Ready Up not installed, or older than the status endpoint) it returns the
// default port for gamePort with FromFile=false. A file that exists but
// cannot be parsed is an error, so a broken install is not mistaken for "no
// Ready Up".
func DiscoverReadyUp(serverDir string, gamePort int) (ReadyUpDiscovery, error) {
	def := ReadyUpDiscovery{Port: gamePort + ReadyUpPortOffset, GamePort: gamePort}
	data, err := os.ReadFile(readyUpDiscoveryPath(serverDir))
	if err != nil {
		if os.IsNotExist(err) {
			return def, nil
		}
		return def, fmt.Errorf("read %s: %w", readyUpDiscoveryPath(serverDir), err)
	}
	var d ReadyUpDiscovery
	if err := json.Unmarshal(data, &d); err != nil {
		return def, fmt.Errorf("parse %s: %w", readyUpDiscoveryPath(serverDir), err)
	}
	if d.Port <= 0 || d.Port > 65535 {
		return def, fmt.Errorf("%s has no valid port", readyUpDiscoveryPath(serverDir))
	}
	d.FromFile = true
	if d.GamePort == 0 {
		d.GamePort = gamePort
	}
	return d, nil
}

// BaseURL is where to reach the endpoint. A wildcard or empty bind address is
// reached on loopback, which needs no token; a specific address is used as is.
func (d ReadyUpDiscovery) BaseURL() string {
	host := strings.TrimSpace(d.Bind)
	switch host {
	case "", "0.0.0.0", "::", "[::]", "*", "localhost":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(d.Port))
}

// newRequest builds a GET with the token, when there is one. Loopback does
// not need it, but sending it costs nothing and keeps a non-loopback bind
// working.
func (d ReadyUpDiscovery) newRequest(ctx context.Context, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.BaseURL()+path, nil)
	if err != nil {
		return nil, err
	}
	if t := strings.TrimSpace(d.Token); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	req.Header.Set("User-Agent", "csm")
	return req, nil
}

// flexString decodes a JSON string or number into a string, so version and
// build fields keep working whichever of the two Ready Up sends.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*f = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	*f = flexString(string(b))
	return nil
}

// ReadyUpStatus is the /status document (FLEET.md §17.2). Only the fields
// csm uses are typed; State stays raw.
type ReadyUpStatus struct {
	ServerID    string           `json:"server_id,omitempty"`
	Hostname    string           `json:"hostname"`
	GamePort    int              `json:"game_port"`
	UptimeS     float64          `json:"uptime_s"`
	GeneratedAt int64            `json:"generated_at"`
	Versions    ReadyUpVersions  `json:"versions"`
	Selftest    *ReadyUpSelftest `json:"selftest,omitempty"`
	Platform    ReadyUpPlatform  `json:"platform"`
	UpdateSafe  *bool            `json:"update_safe"`
	Summary     ReadyUpSummary   `json:"summary"`
	State       json.RawMessage  `json:"state,omitempty"`
}

// ReadyUpVersions is status.versions.
type ReadyUpVersions struct {
	Core      flexString            `json:"core"`
	PluginAPI flexString            `json:"plugin_api"`
	Plugins   map[string]flexString `json:"plugins,omitempty"`
	CS2Build  flexString            `json:"cs2_build"`
	CS2Patch  flexString            `json:"cs2_patch"`
}

// ReadyUpSelftest is status.selftest.
type ReadyUpSelftest struct {
	Pass     bool     `json:"pass"`
	Passed   int      `json:"passed"`
	Total    int      `json:"total"`
	Failures []string `json:"failures,omitempty"`
	RanAt    int64    `json:"ran_at,omitempty"`
}

// ReadyUpPlatform is status.platform: the server's own link to the platform.
type ReadyUpPlatform struct {
	Mode         string `json:"mode"`  // fleet | standalone
	State        string `json:"state"` // online | offline | enrolling | rejected
	Since        int64  `json:"since,omitempty"`
	Reconnects   int    `json:"reconnects,omitempty"`
	SpoolMsgs    int    `json:"spool_msgs,omitempty"`
	AutoPauseInS *int   `json:"auto_pause_in_s,omitempty"`
}

// TeamScore is a team1/team2 pair.
type TeamScore struct {
	Team1 int `json:"team1"`
	Team2 int `json:"team2"`
}

// ReadyUpSummary is status.summary: flat fields for a table row.
type ReadyUpSummary struct {
	Mode        string    `json:"mode"` // idle | scrim | match | practice
	Phase       string    `json:"phase"`
	Map         string    `json:"map"`
	MapNumber   int       `json:"map_number"`
	NumMaps     int       `json:"num_maps"`
	Round       int       `json:"round"`
	Score       TeamScore `json:"score"`
	SeriesScore TeamScore `json:"series_score"`
	Players     struct {
		Connected int `json:"connected"`
		Expected  int `json:"expected"`
	} `json:"players"`
	MatchID flexString `json:"match_id,omitempty"`
	Teams   *struct {
		Team1 string `json:"team1"`
		Team2 string `json:"team2"`
	} `json:"teams,omitempty"`
	Paused bool `json:"paused,omitempty"`
}

// ReadyUpState says what csm knows about Ready Up on one server.
type ReadyUpState string

const (
	// ReadyUpOK: /status answered.
	ReadyUpOK ReadyUpState = "ok"
	// ReadyUpNone: no status.json and nothing on the default port. The server
	// runs without Ready Up (for example with the Auto Tournament CS2 plugin).
	ReadyUpNone ReadyUpState = "none"
	// ReadyUpNoResponse: Ready Up is installed (status.json exists) or the
	// port answered, but /status did not give a usable answer.
	ReadyUpNoResponse ReadyUpState = "no_response"
	// ReadyUpStopped: the server process is not running; nothing was asked.
	ReadyUpStopped ReadyUpState = "stopped"
)

// FleetTarget is one server-N as csm sees it on disk and in tmux.
type FleetTarget struct {
	Server   int
	Dir      string // /home/<user>/server-N
	GamePort int
	Running  bool
	Updating bool // csm's own UPDATING marker is set
}

// FleetTargets lists every server of this install with its process state.
func (m *TmuxManager) FleetTargets() []FleetTarget {
	out := make([]FleetTarget, 0, m.NumServers)
	for i := 1; i <= m.NumServers; i++ {
		out = append(out, m.fleetTarget(i))
	}
	return out
}

func (m *TmuxManager) fleetTarget(server int) FleetTarget {
	gamePort, _ := detectServerPorts(m.CS2User, server)
	t := FleetTarget{Server: server, Dir: m.serverDir(server), GamePort: gamePort, Running: m.IsRunning(server)}
	if data, err := os.ReadFile(m.serverStatusFile(server)); err == nil && strings.TrimSpace(string(data)) == "UPDATING" {
		t.Updating = true
	}
	return t
}

// FleetRow is one row of the fleet table.
type FleetRow struct {
	Target     FleetTarget
	StatusPort int
	State      ReadyUpState
	Status     *ReadyUpStatus // nil unless State == ReadyUpOK (or last known while reconnecting)
	Err        string         // why State is not ok, for the notes under the table
	Source     string         // "stream" | "poll" | "" (one-shot)
	UpdatedAt  time.Time
}

// UpdateSafe reports Ready Up's verdict. known is false when there is no
// verdict (no Ready Up, no answer, or a Ready Up without the field); callers
// then keep csm's behaviour from before Ready Up.
func (r FleetRow) UpdateSafe() (safe, known bool) {
	if r.State != ReadyUpOK || r.Status == nil || r.Status.UpdateSafe == nil {
		return true, false
	}
	return *r.Status.UpdateSafe, true
}

// errReadyUpAbsent marks answers that mean "nothing is listening here".
var errReadyUpAbsent = errors.New("nothing listening")

// FetchReadyUpStatus performs one GET /status and decodes it. It also
// returns the document as a generic map, which the stream watcher patches.
func FetchReadyUpStatus(ctx context.Context, client *http.Client, d ReadyUpDiscovery) (*ReadyUpStatus, map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, readyUpStatusTimeout)
	defer cancel()
	req, err := d.newRequest(ctx, "/status")
	if err != nil {
		return nil, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		if isConnRefused(err) {
			return nil, nil, fmt.Errorf("%w on port %d", errReadyUpAbsent, d.Port)
		}
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil, fmt.Errorf("%w: /status returned 404 on port %d", errReadyUpAbsent, d.Port)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, nil, fmt.Errorf("/status returned %d: the token in status.json was not accepted", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, nil, fmt.Errorf("/status returned %d", resp.StatusCode)
	}
	return decodeReadyUpStatus(body)
}

func decodeReadyUpStatus(body []byte) (*ReadyUpStatus, map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, nil, fmt.Errorf("/status is not JSON: %w", err)
	}
	st, err := statusFromDoc(doc)
	if err != nil {
		return nil, nil, err
	}
	return st, doc, nil
}

// statusFromDoc turns the generic document back into the typed view.
func statusFromDoc(doc map[string]any) (*ReadyUpStatus, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var st ReadyUpStatus
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("/status has an unexpected shape: %w", err)
	}
	return &st, nil
}

func isConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(strings.ToLower(err.Error()), "connection refused")
}

// ProbeReadyUp builds one fleet row for a target with a single /status
// request. csm status and the update gate use it.
func ProbeReadyUp(ctx context.Context, client *http.Client, t FleetTarget) FleetRow {
	row := FleetRow{Target: t, StatusPort: t.GamePort + ReadyUpPortOffset, UpdatedAt: time.Now()}
	if !t.Running {
		row.State = ReadyUpStopped
		return row
	}
	d, derr := DiscoverReadyUp(t.Dir, t.GamePort)
	row.StatusPort = d.Port
	if derr != nil {
		row.State = ReadyUpNoResponse
		row.Err = derr.Error()
		return row
	}
	st, _, err := FetchReadyUpStatus(ctx, client, d)
	applyProbeResult(&row, d, st, err)
	return row
}

// applyProbeResult sorts a /status outcome into a row state. Without a
// discovery file, "nothing there" and "something that is not Ready Up" both
// mean the server runs without Ready Up; with one, any failure is a Ready Up
// that does not answer.
func applyProbeResult(row *FleetRow, d ReadyUpDiscovery, st *ReadyUpStatus, err error) {
	row.StatusPort = d.Port
	if err == nil {
		row.State = ReadyUpOK
		row.Status = st
		row.Err = ""
		return
	}
	row.Status = nil
	row.Err = err.Error()
	if !d.FromFile {
		row.State = ReadyUpNone
		return
	}
	row.State = ReadyUpNoResponse
}

// ProbeFleet probes every target in parallel and returns the rows in server
// order.
func ProbeFleet(ctx context.Context, targets []FleetTarget) []FleetRow {
	client := &http.Client{}
	rows := make([]FleetRow, len(targets))
	done := make(chan struct{}, len(targets))
	for i := range targets {
		go func(i int) {
			rows[i] = ProbeReadyUp(ctx, client, targets[i])
			done <- struct{}{}
		}(i)
	}
	for range targets {
		<-done
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Target.Server < rows[b].Target.Server })
	return rows
}
