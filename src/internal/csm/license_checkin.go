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

// The daily license check-in for a key set with `csm license set`
//
// Like the platform, a csm host with a paid key tells autotournament.gg once
// a day that the key is in use, and gets back where the license stands and,
// after a renewal or a server-count change, the license's newest key, which
// csm stores and hands to Ready Up like `csm license set` would.
//
// A key the platform handed over is left to the platform: it checks in
// itself and passes renewed keys on (license_platform.go).
//
// What is sent, and nothing else: the key, a random instance id made once,
// the number of game servers, the csm version, the time and the product
// ("csm"). It runs from the auto-update monitor, at most once a day.
// Failures are one log line; the last answer and the key's own dates hold.
//
// CSM_LICENSE_CHECKIN_URL replaces the endpoint (https, or http to
// localhost) and `off` turns the check-in off.

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
}

type checkinAnswer struct {
	OK      bool    `json:"ok"`
	Token   *string `json:"token"`
	Notice  *string `json:"notice"`
	License *struct {
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

// newerKey is the answer's token when it is a genuine key for the same
// license, of the same kind, issued no earlier than ours.
func newerKey(current, candidate string) (string, bool) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || candidate == current || !license.LooksLikeKey(candidate) {
		return "", false
	}
	now := time.Now()
	cur := license.Verify(current, license.Options{ServerCount: -1, Now: now})
	next := license.Verify(candidate, license.Options{ServerCount: -1, Now: now})
	if !cur.Valid || !next.Valid || cur.License == nil || next.License == nil {
		return "", false
	}
	if next.License.ID != cur.License.ID || next.License.Kind != cur.License.Kind || next.License.IssuedAt < cur.License.IssuedAt {
		return "", false
	}
	return candidate, true
}

// MaybeCheckIn checks in when a hand-set paid key is stored and the last
// check-in is a day old. Everything it has to say goes to w; never an error.
func MaybeCheckIn(ctx context.Context, w io.Writer) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(w, "License check-in: skipped (%v).\n", r)
		}
	}()
	endpoint := checkinURL()
	if endpoint == "" {
		return
	}
	s, err := LoadLicenseSettings()
	if err != nil || s.Key == "" || s.Source == LicenseSourcePlatform {
		return
	}
	r := license.Verify(s.Key, license.Options{ServerCount: -1})
	if !r.Valid || r.License == nil {
		return
	}
	c := loadCheckinFile()
	if last, err := time.Parse(time.RFC3339, c.LastAt); err == nil && time.Since(last) < checkinInterval && c.LicenseID == r.License.ID {
		return
	}
	if c.InstanceID == "" {
		c.InstanceID = newInstanceID()
	}
	servers := LicenseServerCount()
	if servers < 0 {
		servers = 0
	}
	body, _ := json.Marshal(checkinBody{
		Token: s.Key, KeyID: r.License.ID, InstanceID: c.InstanceID, ServerCount: servers,
		PlatformVersion: checkinVersion(), SentAt: time.Now().UTC().Format(time.RFC3339), Product: "csm",
	})
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(w, "License check-in: the license server didn't answer (%v); the last answer holds.\n", err)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(w, "License check-in: the license server answered %d; the last answer holds.\n", res.StatusCode)
		return
	}
	var a checkinAnswer
	if err := json.NewDecoder(io.LimitReader(res.Body, 64*1024)).Decode(&a); err != nil || !a.OK {
		fmt.Fprintln(w, "License check-in: the answer couldn't be read; the last answer holds.")
		return
	}
	c.LastAt = time.Now().UTC().Format(time.RFC3339)
	c.LicenseID = r.License.ID
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
	c.Notice = ""
	if a.Notice != nil {
		c.Notice = strings.TrimSpace(*a.Notice)
		if len(c.Notice) > 300 {
			c.Notice = c.Notice[:300]
		}
	}
	if err := saveCheckinFile(c); err != nil {
		fmt.Fprintf(w, "License check-in: could not save the answer (%v).\n", err)
	}
	if a.Token != nil {
		if next, ok := newerKey(s.Key, *a.Token); ok {
			if _, err := SetLicenseKey(w, next); err != nil {
				fmt.Fprintf(w, "License check-in: could not store the renewed key (%v); will try again tomorrow.\n", err)
			} else {
				fmt.Fprintf(w, "License check-in: stored the renewed key for %s.\n", r.License.ID)
			}
		}
	}
	if c.State != nil {
		fmt.Fprintf(w, "License check-in: %s (%s).\n", r.License.ID, c.State.Status)
	}
}
