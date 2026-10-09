package csm

import (
	"context"
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
//   - One limit per license: every install using the key (csm hosts, the
//     platform) shares the license's max_servers. Before creating servers,
//     csm asks the license server whether they fit the whole license right
//     now (license_checkin.go, Reserve). If it can't be asked, the last
//     check-in decides, but only when it is at most 3 days old.
//   - Late payment, a replaced key, a key in use on another platform
//     install: csm won't create or start servers, and Ready Up stops loading
//     matches, until it is sorted. Servers already running are never stopped.
//
// The key never changes on renewal or more servers. The license's current
// terms come as a lease (signed like a key, marked "lease") in the check-in
// answer, or from the platform with its key. The standing comes from the
// key, the lease and the last answer, so the license server being
// unreachable changes nothing: they hold.

// LicenseGraceDays is how long a late subscription keeps working.
const LicenseGraceDays = 14

// checkinFresh is how recent the last check-in must be to create servers
// with a paid key when the license server can't be asked.
const checkinFresh = 3 * 24 * time.Hour

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
	// StopsOn is the first day csm and Ready Up no longer work unless it is sorted (YYYY-MM-DD), or "".
	StopsOn string
	// Reason is why it is past due or expired: unpaid, replaced or in_use_elsewhere.
	Reason string
	// ServersElsewhere is the servers other installs use on this license (last answer).
	ServersElsewhere int
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

func verifyQuiet(token string, keys map[string]string, now time.Time) *license.Payload {
	r := license.Verify(token, license.Options{PublicKeys: keys, LineDate: "0000-01-01", ServerCount: -1, Now: now})
	if !r.Valid || r.License == nil {
		return nil
	}
	return r.License
}

// StandingFor is the standing for a key, a lease and the last answer on
// `now`. Pure apart from the embedded public keys (keys != nil in tests).
func StandingFor(key, lease string, server *CheckinState, now time.Time, keys map[string]string) LicenseStanding {
	key = strings.TrimSpace(key)
	if key == "" {
		return LicenseStanding{Status: StandingFree}
	}
	p := verifyQuiet(key, keys, now)
	if p == nil || p.Lease {
		return LicenseStanding{Status: StandingInvalid}
	}
	if lease = strings.TrimSpace(lease); lease != "" {
		if l := verifyQuiet(lease, keys, now); l != nil && l.Lease && l.ID == p.ID && l.Kind == p.Kind && l.IssuedAt >= p.IssuedAt {
			p = l
		}
	}
	today := now.UTC().Format("2006-01-02")
	st := LicenseStanding{Status: StandingActive, Paid: true, MaxServers: p.MaxServers, LicenseID: p.ID}

	if server != nil {
		switch server.Status {
		case "in_use_elsewhere":
			st.Status, st.Reason, st.StopsOn = StandingExpired, "in_use_elsewhere", server.StopsOn
			return st
		case "replaced":
			st.StopsOn, st.Reason = server.StopsOn, "replaced"
			if st.StopsOn == "" || today >= st.StopsOn {
				st.Status = StandingExpired
			} else {
				st.Status = StandingPastDue
			}
			return st
		case "revoked":
			return LicenseStanding{Status: StandingInvalid}
		}
	}

	if p.Kind == "month" {
		// Works through the 14th day after the last paid day; stops on the 15th.
		stops := addDaysISO(p.UpdatesUntil, LicenseGraceDays+1)
		switch {
		case today >= stops:
			st.Status, st.StopsOn = StandingExpired, stops
		case today > p.UpdatesUntil:
			st.Status, st.StopsOn = StandingPastDue, stops
		}
	}
	// The license server only ever makes it stricter; an answer about an
	// earlier period than the lease covers (paid since) doesn't count.
	if server != nil && !(server.ValidUntil != "" && server.ValidUntil < p.UpdatesUntil) &&
		(server.Status == StandingPastDue || server.Status == StandingExpired) {
		if standingRank[server.Status] > standingRank[st.Status] {
			st.Status = server.Status
		}
		if server.StopsOn != "" {
			st.StopsOn = server.StopsOn
		}
	}
	if st.Status != StandingActive {
		st.Reason = "unpaid"
	}
	return st
}

// LicenseLimitError is a refused create or start.
type LicenseLimitError struct {
	Code    string // server_limit | license_expired | checkin_stale | platform_cap
	Message string
}

func (e *LicenseLimitError) Error() string { return e.Message }

// CheckCreate refuses creating `adding` servers when `current` are set up
// here, the license's other installs use ServersElsewhere, and that would go
// above the paid limit; or when the license has stopped.
func CheckCreate(st LicenseStanding, current, adding int) error {
	if !st.Paid {
		return nil
	}
	if st.Status == StandingExpired {
		return stoppedError(st.Reason)
	}
	if current < 0 {
		current = 0
	}
	if current+st.ServersElsewhere+adding > st.MaxServers {
		plural := "s"
		if st.MaxServers == 1 {
			plural = ""
		}
		elsewhere := ""
		if st.ServersElsewhere > 0 {
			elsewhere = fmt.Sprintf(" (%d of them on other installs using this key)", st.ServersElsewhere)
		}
		return &LicenseLimitError{Code: "server_limit", Message: fmt.Sprintf(
			"your license covers %d game server%s and %d are set up%s; add servers to your license at %s to create more",
			st.MaxServers, plural, current+st.ServersElsewhere, elsewhere, LicenseConsoleURL)}
	}
	return nil
}

// CheckStart refuses starting servers once the license has stopped.
func CheckStart(st LicenseStanding) error {
	if st.Paid && st.Status == StandingExpired {
		return stoppedError(st.Reason)
	}
	return nil
}

func stoppedError(reason string) error {
	switch reason {
	case "in_use_elsewhere":
		return &LicenseLimitError{Code: "license_expired", Message: "this license key is in use on another Auto Tournament install; use \"Move to another install\" at " +
			LicenseConsoleURL + ", or set this host's own key (`csm license set`)"}
	case "replaced":
		return &LicenseLimitError{Code: "license_expired", Message: "this license key was replaced by a new one in the console; set the new key (`csm license set`, or in the platform's license settings)"}
	}
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
	st := StandingFor(s.Key, c.Lease, c.State, time.Now(), nil)
	st.ServersElsewhere = c.ServersElsewhere
	return st
}

// GateCreate decides whether `adding` more servers may be created on this
// host now. For a key set on this host it asks the license server (one limit
// across every install using the key); a key from the platform was already
// checked by the platform, so only the local view applies.
func GateCreate(ctx context.Context, adding int) error {
	st := CurrentStanding()
	if !st.Paid {
		return nil
	}
	current := LicenseServerCount()
	if err := CheckCreate(st, current, adding); err != nil {
		return err
	}
	s, _ := LoadLicenseSettings()
	if s.Source == LicenseSourcePlatform {
		return nil
	}
	if current < 0 {
		current = 0
	}
	ans := Reserve(ctx, current, adding)
	switch ans.Result {
	case ReserveRefused:
		if ans.Reason == "server_limit" {
			st.MaxServers, st.ServersElsewhere = ans.MaxServers, ans.Elsewhere
			if err := CheckCreate(st, current, adding); err != nil {
				return err
			}
			return &LicenseLimitError{Code: "server_limit", Message: "your license has no free servers left; add servers to your license at " + LicenseConsoleURL}
		}
		reason := ans.Reason
		if reason != "in_use_elsewhere" && reason != "replaced" {
			reason = "unpaid"
		}
		return stoppedError(reason)
	case ReserveUnreachable:
		if !CheckedInRecently() {
			return &LicenseLimitError{Code: "checkin_stale", Message: "csm hasn't reached autotournament.gg for 3 days, so new servers can't be created with a paid license; servers already set up keep working. Check the internet connection and try again"}
		}
	}
	return nil
}

// GateStart is CheckStart for this host.
func GateStart() error { return CheckStart(CurrentStanding()) }

// checkinFile is <csm root>/license_checkin.json (mode 600): this host's
// random instance id, the last check-in (or platform hand-off) answer and
// the lease. Kept apart from license.json, which the platform hand-off
// rewrites.
type checkinFile struct {
	InstanceID       string        `json:"instance_id,omitempty"`
	LastAt           string        `json:"last_at,omitempty"`
	LicenseID        string        `json:"license_id,omitempty"`
	State            *CheckinState `json:"state,omitempty"`
	Lease            string        `json:"lease,omitempty"`
	ServersElsewhere int           `json:"servers_elsewhere,omitempty"`
	Notice           string        `json:"notice,omitempty"`
}

func checkinFilePath() string { return filepath.Join(ResolveRoot(), "license_checkin.json") }

func loadCheckinFile() checkinFile {
	var c checkinFile
	data, err := os.ReadFile(checkinFilePath())
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, &c)
	// What was said about another license (the key was replaced) doesn't count.
	if s, err := LoadLicenseSettings(); err == nil {
		if p, ok := license.Peek(s.Key); !ok || p.ID != c.LicenseID {
			c.State, c.Lease, c.ServersElsewhere = nil, "", 0
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

// CheckedInRecently reports whether the last successful check-in (or
// platform hand-off) is at most 3 days old.
func CheckedInRecently() bool {
	c := loadCheckinFile()
	last, err := time.Parse(time.RFC3339, c.LastAt)
	return err == nil && time.Since(last) < checkinFresh
}

// StandingLine is the one-line warning for status output, or "" when there is nothing to say.
func (st LicenseStanding) StandingLine() string {
	switch {
	case st.Status == StandingPastDue && st.Reason == "replaced":
		return fmt.Sprintf("License key replaced by a new one: set the new key before %s, when this one stops.", st.StopsOn)
	case st.Status == StandingPastDue:
		return fmt.Sprintf("License payment is late: csm and Ready Up stop on %s unless it is paid (%s).", st.StopsOn, LicenseConsoleURL)
	case st.Status == StandingExpired:
		return "License stopped: " + stoppedError(st.Reason).Error() + "."
	}
	return ""
}
