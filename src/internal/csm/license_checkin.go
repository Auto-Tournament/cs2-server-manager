package csm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/license"
)

// The license check-in for a key set on this host (`csm license set`)
//
// Like the platform, a csm host with its own paid key tells
// autotournament.gg once a day how many game servers it has set up, and gets
// back where the license stands, the servers other installs use on the same
// key, and the lease: the license's current terms. The key itself never
// changes. Before creating servers, csm also asks (Reserve) whether they fit
// the whole license right now.
//
// A key the platform handed over is left to the platform: it checks in and
// reserves itself, and passes the lease and the license state on
// (license_platform.go), so its servers are never counted twice.
//
// What is sent, and nothing else: the key, a random instance id made once,
// the number of game servers, the csm version, the time and the product
// ("csm"). Failures are one log line; the last answer and the key's own
// dates hold.
//
// CSM_LICENSE_CHECKIN_URL replaces the endpoint (https, or http to
// localhost) and `off` turns check-ins and reservations off.

// DefaultCheckinURL is the license server's check-in endpoint.
const DefaultCheckinURL = "https://autotournament.gg/api/licenses/checkin"

const checkinInterval = 24 * time.Hour

// CheckinVersion is the csm version sent with the check-in (set by main).
var CheckinVersion = "dev"

func checkinURL() string {
	raw := strings.TrimSpace(os.Getenv("CSM_LICENSE_CHECKIN_URL"))
	if raw == "" {
		return DefaultCheckinURL
	}
	switch strings.ToLower(raw) {
	case "off", "false", "0", "no", "none", "disabled":
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return u.String()
	}
	return ""
}

func reserveURL(checkin string) string {
	if strings.HasSuffix(checkin, "/checkin") {
		return strings.TrimSuffix(checkin, "/checkin") + "/reserve"
	}
	return ""
}

func newInstanceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type checkinBody struct {
	Token           string  `json:"token"`
	KeyID           string  `json:"key_id"`
	InstanceID      string  `json:"instance_id"`
	ServerCount     int     `json:"server_count"`
	PlatformVersion string  `json:"platform_version"`
	SentAt          string  `json:"sent_at"`
	Product         string  `json:"product"`
	PublicURL       *string `json:"public_url"`
	Adding          int     `json:"adding,omitempty"`
}

type checkinAnswer struct {
	OK               bool    `json:"ok"`
	Lease            *string `json:"lease"`
	Notice           *string `json:"notice"`
	ServersElsewhere *int    `json:"servers_elsewhere"`
	License          *struct {
		Status     string  `json:"status"`
		ValidUntil *string `json:"valid_until"`
		StopsOn    *string `json:"stops_on"`
	} `json:"license"`
}

// checkinVersion fits the license server's version pattern.
func checkinVersion() string {
	v := strings.TrimPrefix(strings.TrimSpace(CheckinVersion), "v")
	out := make([]rune, 0, len(v))
	for _, r := range v {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || strings.ContainsRune(".+_-", r) {
			out = append(out, r)
		}
	}
	if len(out) == 0 || !((out[0] >= '0' && out[0] <= '9') || (out[0] >= 'a' && out[0] <= 'z') || (out[0] >= 'A' && out[0] <= 'Z')) {
		return "dev"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return string(out)
}

// ownKey is the key set on this host (not from the platform) when it is a
// genuine key, with its payload; otherwise "".
func ownKey() (string, *license.Payload) {
	s, err := LoadLicenseSettings()
	if err != nil || s.Key == "" || s.Source == LicenseSourcePlatform {
		return "", nil
	}
	p := verifyQuiet(s.Key, nil, time.Now())
	if p == nil || p.Lease {
		return "", nil
	}
	return s.Key, p
}

func postJSON(ctx context.Context, endpoint string, body any, out any) (int, error) {
	data, _ := json.Marshal(body)
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return res.StatusCode, nil
	}
	return res.StatusCode, json.NewDecoder(io.LimitReader(res.Body, 64*1024)).Decode(out)
}

// leaseFor is the answer's lease when it is genuine current terms for this
// key's license; otherwise "".
func leaseFor(p *license.Payload, candidate *string) string {
	if candidate == nil {
		return ""
	}
	l := verifyQuiet(*candidate, nil, time.Now())
	if l == nil || !l.Lease || l.ID != p.ID || l.Kind != p.Kind || l.IssuedAt < p.IssuedAt {
		return ""
	}
	return strings.TrimSpace(*candidate)
}

// MaybeCheckIn checks in when this host's own paid key is set and the last
// check-in is a day old (force: now). Everything it has to say goes to w.
func MaybeCheckIn(ctx context.Context, w io.Writer, force bool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(w, "License check-in: skipped (%v).\n", r)
		}
	}()
	endpoint := checkinURL()
	key, p := ownKey()
	if endpoint == "" || p == nil {
		return
	}
	c := loadCheckinFile()
	if last, err := time.Parse(time.RFC3339, c.LastAt); !force && err == nil && time.Since(last) < checkinInterval && c.LicenseID == p.ID {
		return
	}
	if c.InstanceID == "" {
		c.InstanceID = newInstanceID()
		_ = saveCheckinFile(c)
	}
	servers := LicenseServerCount()
	if servers < 0 {
		servers = 0
	}
	var a checkinAnswer
	status, err := postJSON(ctx, endpoint, checkinBody{
		Token: key, KeyID: p.ID, InstanceID: c.InstanceID, ServerCount: servers,
		PlatformVersion: checkinVersion(), SentAt: time.Now().UTC().Format(time.RFC3339), Product: "csm",
	}, &a)
	switch {
	case err != nil && status == 0:
		fmt.Fprintf(w, "License check-in: the license server didn't answer (%v); the last answer holds.\n", err)
		return
	case status != http.StatusOK:
		fmt.Fprintf(w, "License check-in: the license server answered %d; the last answer holds.\n", status)
		return
	case err != nil || !a.OK:
		fmt.Fprintln(w, "License check-in: the answer couldn't be read; the last answer holds.")
		return
	}
	c.LastAt = time.Now().UTC().Format(time.RFC3339)
	c.LicenseID = p.ID
	if a.License != nil {
		st := &CheckinState{Status: a.License.Status}
		if a.License.ValidUntil != nil {
			st.ValidUntil = *a.License.ValidUntil
		}
		if a.License.StopsOn != nil {
			st.StopsOn = *a.License.StopsOn
		}
		c.State = st
	}
	if l := leaseFor(p, a.Lease); l != "" {
		c.Lease = l
	}
	if a.ServersElsewhere != nil && *a.ServersElsewhere >= 0 {
		c.ServersElsewhere = *a.ServersElsewhere
	}
	c.Notice = ""
	if a.Notice != nil {
		c.Notice = strings.TrimSpace(*a.Notice)
		if len(c.Notice) > 300 {
			c.Notice = c.Notice[:300]
		}
	}
	before := loadCheckinFile()
	if err := saveCheckinFile(c); err != nil {
		fmt.Fprintf(w, "License check-in: could not save the answer (%v).\n", err)
		return
	}
	// Ready Up gets the new lease / state in its cfg (read at the next map load).
	if before.Lease != c.Lease || fmt.Sprint(before.State) != fmt.Sprint(c.State) {
		applyLicenseToAllServers(io.Discard, key)
	}
	state := "active"
	if c.State != nil {
		state = c.State.Status
	}
	fmt.Fprintf(w, "License check-in: %s (%s).\n", p.ID, state)
}

// Reservation results.
const (
	ReserveAllowed     = "allowed"
	ReserveRefused     = "refused"
	ReserveUnreachable = "unreachable"
	ReserveSkipped     = "skipped"
)

// ReserveAnswer is the license server's answer to "may I add N servers?".
type ReserveAnswer struct {
	Result     string
	Reason     string
	MaxServers int
	Elsewhere  int
}

// Reserve asks the license server whether `adding` more servers fit the
// whole license now, `current` being this host's servers. Only for this
// host's own key; skipped when check-ins are off or there is none.
func Reserve(ctx context.Context, current, adding int) ReserveAnswer {
	endpoint := reserveURL(checkinURL())
	key, p := ownKey()
	if endpoint == "" || p == nil {
		return ReserveAnswer{Result: ReserveSkipped}
	}
	c := loadCheckinFile()
	if c.InstanceID == "" {
		c.InstanceID = newInstanceID()
		_ = saveCheckinFile(c)
	}
	var a struct {
		OK               bool   `json:"ok"`
		Allowed          *bool  `json:"allowed"`
		Reason           string `json:"reason"`
		MaxServers       int    `json:"max_servers"`
		ServersElsewhere int    `json:"servers_elsewhere"`
	}
	status, err := postJSON(ctx, endpoint, checkinBody{
		Token: key, KeyID: p.ID, InstanceID: c.InstanceID, ServerCount: current, Adding: adding,
		PlatformVersion: checkinVersion(), SentAt: time.Now().UTC().Format(time.RFC3339), Product: "csm",
	}, &a)
	if err != nil || status != http.StatusOK || !a.OK || a.Allowed == nil {
		return ReserveAnswer{Result: ReserveUnreachable}
	}
	if *a.Allowed {
		return ReserveAnswer{Result: ReserveAllowed}
	}
	reason := a.Reason
	if reason == "" {
		reason = "server_limit"
	}
	return ReserveAnswer{Result: ReserveRefused, Reason: reason, MaxServers: a.MaxServers, Elsewhere: a.ServersElsewhere}
}
