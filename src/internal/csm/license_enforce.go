package csm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/license"
)

// Paid license enforcement (pricing v4, Commercial License Terms sections 4
// and 8). It only ever applies to a genuine paid key; with no key (free,
// non-commercial use) or a key that doesn't verify, nothing is limited.
//
//   - The server limit: no game server is created above the key's
//     max_servers, however it is asked for (the TUI, `csm`, the platform
//     through the host agent). Servers that already exist keep running.
//   - Late payment: a monthly or yearly license that isn't paid is past due
//     (a warning) and, after 14 days of grace, expired: csm won't create or
//     start servers, and Ready Up stops loading matches, until it is paid.
//
// The standing comes from the key itself (offline) and the last check-in
// answer (license_checkin.go), so the license server being unreachable
// changes nothing: the last answer and the key's own dates hold. A monthly
// key whose last paid day is more than 14 days ago is expired even offline;
// the check-in brings the renewed key.

// LicenseGraceDays is how long a late subscription keeps working.
const LicenseGraceDays = 14

// Standing statuses.
const (
	StandingFree    = "free"
	StandingInvalid = "invalid"
	StandingActive  = "active"
	StandingPastDue = "past_due"
	StandingExpired = "expired"
)

// LicenseConsoleURL is where a license is paid and servers are added.
const LicenseConsoleURL = "https://console.autotournament.gg/billing"

// LicenseStanding is where this host's license stands today.
type LicenseStanding struct {
	Status string
	// Paid: a genuine key, so its server limit applies.
	Paid       bool
	MaxServers int
	LicenseID  string
	// StopsOn is the day csm and Ready Up stop unless it is paid (YYYY-MM-DD), or "".
	StopsOn string
}

// CheckinState is what the license server last said about the license.
type CheckinState struct {
	Status     string `json:"status"`
	ValidUntil string `json:"valid_until,omitempty"`
	StopsOn    string `json:"stops_on,omitempty"`
}

func addDaysISO(day string, days int) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}

var standingRank = map[string]int{StandingActive: 0, StandingPastDue: 1, StandingExpired: 2}

// StandingFor is the standing for a key and the last check-in answer on
// `now`. Pure apart from the embedded public keys (opts for tests).
func StandingFor(key string, server *CheckinState, now time.Time, keys map[string]string) LicenseStanding {
	key = strings.TrimSpace(key)
	if key == "" {
		return LicenseStanding{Status: StandingFree}
	}
	r := license.Verify(key, license.Options{PublicKeys: keys, LineDate: "0000-01-01", ServerCount: -1, Now: now})
	if !r.Valid || r.License == nil {
		return LicenseStanding{Status: StandingInvalid}
	}
	p := r.License
	today := now.UTC().Format("2006-01-02")
	st := LicenseStanding{Status: StandingActive, Paid: true, MaxServers: p.MaxServers, LicenseID: p.ID}
	if p.Kind == "month" {
		stops := addDaysISO(p.UpdatesUntil, LicenseGraceDays)
		switch {
		case today > stops:
			st.Status, st.StopsOn = StandingExpired, stops
		case today > p.UpdatesUntil:
			st.Status, st.StopsOn = StandingPastDue, stops
		}
	}
	// The license server only ever makes it stricter; a renewal arrives as a newer key.
	// An answer about an older period than the key covers (the key was renewed since) doesn't count.
	if server != nil && server.ValidUntil != "" && server.ValidUntil < p.UpdatesUntil {
		server = nil
	}
	if server != nil && (server.Status == StandingPastDue || server.Status == StandingExpired) {
		if standingRank[server.Status] > standingRank[st.Status] {
			st.Status = server.Status
		}
		if server.StopsOn != "" {
			st.StopsOn = server.StopsOn
		}
	}
	return st
}

// LicenseLimitError is a refused create or start.
type LicenseLimitError struct {
	Code    string // server_limit | license_expired
	Message string
}

func (e *LicenseLimitError) Error() string { return e.Message }

// CheckCreate refuses creating `adding` servers when `current` are set up
// and that would go above the paid limit, or when the license has expired.
func CheckCreate(st LicenseStanding, current, adding int) error {
	if !st.Paid {
		return nil
	}
	if st.Status == StandingExpired {
		return expiredError()
	}
	if current < 0 {
		current = 0
	}
	if current+adding > st.MaxServers {
		plural := "s"
		if st.MaxServers == 1 {
			plural = ""
		}
		return &LicenseLimitError{Code: "server_limit", Message: fmt.Sprintf(
			"your license covers %d game server%s and %d are set up; add servers to your license at %s to create more",
			st.MaxServers, plural, current, LicenseConsoleURL)}
	}
	return nil
}

// CheckStart refuses starting servers once the license has expired.
func CheckStart(st LicenseStanding) error {
	if st.Paid && st.Status == StandingExpired {
		return expiredError()
	}
	return nil
}

func expiredError() error {
	return &LicenseLimitError{Code: "license_expired", Message: "your Auto Tournament license has expired because it wasn't paid; pay it at " +
		LicenseConsoleURL + " and servers can be created and started again (or `csm license clear` for free, non-commercial use)"}
}

// CurrentStanding is this host's standing now. Errors reading the files
// count as no key: enforcement never fails closed on a broken file.
func CurrentStanding() LicenseStanding {
	s, err := LoadLicenseSettings()
	if err != nil {
		return LicenseStanding{Status: StandingFree}
	}
	c := loadCheckinFile()
	return StandingFor(s.Key, c.State, time.Now(), nil)
}

// GateCreate is CheckCreate for this host: `adding` more servers on top of
// the servers set up now.
func GateCreate(adding int) error {
	st := CurrentStanding()
	if !st.Paid {
		return nil
	}
	return CheckCreate(st, LicenseServerCount(), adding)
}

// GateStart is CheckStart for this host.
func GateStart() error { return CheckStart(CurrentStanding()) }

// checkinFile is <csm root>/license_checkin.json (mode 600): this host's
// random instance id and the last check-in answer. Kept apart from
// license.json, which the platform hand-off rewrites.
type checkinFile struct {
	InstanceID string        `json:"instance_id,omitempty"`
	LastAt     string        `json:"last_at,omitempty"`
	LicenseID  string        `json:"license_id,omitempty"`
	State      *CheckinState `json:"state,omitempty"`
	Notice     string        `json:"notice,omitempty"`
}

func checkinFilePath() string { return filepath.Join(ResolveRoot(), "license_checkin.json") }

func loadCheckinFile() checkinFile {
	var c checkinFile
	data, err := os.ReadFile(checkinFilePath())
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, &c)
	// An answer about another license (the key was replaced) doesn't count.
	if s, err := LoadLicenseSettings(); err == nil {
		if p, ok := license.Peek(s.Key); !ok || p.ID != c.LicenseID {
			c.State = nil
		}
	}
	return c
}

func saveCheckinFile(c checkinFile) error {
	if err := writeJSONAtomicMode(checkinFilePath(), c, 0o600); err != nil {
		return err
	}
	if canChown() {
		_ = ensureOwnedByUser(licenseCS2User(), checkinFilePath())
	}
	return nil
}

// StandingLine is the one-line warning for status output, or "" when there is nothing to say.
func (st LicenseStanding) StandingLine() string {
	switch st.Status {
	case StandingPastDue:
		return fmt.Sprintf("License payment is late: csm and Ready Up stop on %s unless it is paid (%s).", st.StopsOn, LicenseConsoleURL)
	case StandingExpired:
		return "License expired (not paid): servers can't be created or started, and Ready Up won't load matches, until it is paid (" + LicenseConsoleURL + ")."
	}
	return ""
}
