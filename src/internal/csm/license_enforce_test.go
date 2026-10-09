package csm

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/license"
)

type testSigner struct {
	priv ed25519.PrivateKey
	kid  string
	keys map[string]string
}

func newTestSigner(t *testing.T) testSigner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	kid := license.KidFor(pub)
	return testSigner{priv: priv, kid: kid, keys: map[string]string{kid: base64.RawURLEncoding.EncodeToString(pub)}}
}

func (s testSigner) sign(t *testing.T, over map[string]any) string {
	t.Helper()
	p := map[string]any{
		"v": 1, "kid": s.kid, "id": "L-test", "customer": "cus_1", "product": "platform", "pack": "M",
		"max_servers": 10, "kind": "month", "issued_at": "2026-10-01T10:00:00Z", "updates_until": "2026-11-01",
	}
	for k, v := range over {
		p[k] = v
	}
	b, _ := json.Marshal(p)
	body := base64.RawURLEncoding.EncodeToString(b)
	sig := ed25519.Sign(s.priv, []byte(license.TokenPrefix+"."+body))
	return license.TokenPrefix + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t.Add(12 * time.Hour)
}

func TestStandingFreeInvalidAndPaid(t *testing.T) {
	s := newTestSigner(t)
	if st := StandingFor("", "", nil, day("2026-10-10"), s.keys); st.Status != StandingFree || st.Paid {
		t.Fatalf("no key: %+v", st)
	}
	key := s.sign(t, nil)
	if st := StandingFor(key[:len(key)-4]+"AAAA", "", nil, day("2026-10-10"), s.keys); st.Status != StandingInvalid || st.Paid {
		t.Fatalf("forged: %+v", st)
	}
	st := StandingFor(key, "", nil, day("2026-10-10"), s.keys)
	if st.Status != StandingActive || !st.Paid || st.MaxServers != 10 {
		t.Fatalf("paid: %+v", st)
	}
	if err := CheckCreate(st, 9, 1); err != nil {
		t.Fatal(err)
	}
	if err := CheckCreate(st, 9, 2); err == nil {
		t.Fatal("over the limit was allowed")
	}
	st.ServersElsewhere = 6
	if err := CheckCreate(st, 4, 1); err == nil || !strings.Contains(err.Error(), "6 of them on other installs") {
		t.Fatalf("pool: %v", err)
	}
}

func TestStandingLateMonthlyAndLease(t *testing.T) {
	s := newTestSigner(t)
	key := s.sign(t, nil)
	if st := StandingFor(key, "", nil, day("2026-11-02"), s.keys); st.Status != StandingPastDue || st.StopsOn != "2026-11-15" || st.Reason != "unpaid" {
		t.Fatalf("late: %+v", st)
	}
	if st := StandingFor(key, "", nil, day("2026-11-16"), s.keys); st.Status != StandingExpired {
		t.Fatalf("expired: %+v", st)
	}
	lease := s.sign(t, map[string]any{"lease": true, "max_servers": 20, "updates_until": "2026-12-01", "issued_at": "2026-11-01T10:00:00Z"})
	st := StandingFor(key, lease, nil, day("2026-11-16"), s.keys)
	if st.Status != StandingActive || st.MaxServers != 20 {
		t.Fatalf("lease: %+v", st)
	}
	// A lease is never the key; terms without the lease mark are no lease.
	if st := StandingFor(lease, "", nil, day("2026-10-10"), s.keys); st.Paid {
		t.Fatalf("lease as key: %+v", st)
	}
	unmarked := s.sign(t, map[string]any{"max_servers": 20, "updates_until": "2026-12-01", "issued_at": "2026-11-01T10:00:00Z"})
	if st := StandingFor(key, unmarked, nil, day("2026-10-10"), s.keys); st.MaxServers != 10 {
		t.Fatalf("unmarked lease used: %+v", st)
	}
	// An answer about an earlier period than the lease covers doesn't count.
	old := &CheckinState{Status: StandingPastDue, ValidUntil: "2026-11-01", StopsOn: "2026-11-15"}
	if st := StandingFor(key, lease, old, day("2026-11-10"), s.keys); st.Status != StandingActive {
		t.Fatalf("stale answer: %+v", st)
	}
}

func TestStandingReplacedAndElsewhere(t *testing.T) {
	s := newTestSigner(t)
	key := s.sign(t, nil)
	replaced := &CheckinState{Status: "replaced", StopsOn: "2026-10-11"}
	if st := StandingFor(key, "", replaced, day("2026-10-11"), s.keys); st.Status != StandingPastDue || st.Reason != "replaced" {
		t.Fatalf("replaced, within its day: %+v", st)
	}
	st := StandingFor(key, "", replaced, day("2026-10-12"), s.keys)
	if st.Status != StandingExpired || CheckStart(st) == nil || !strings.Contains(CheckCreate(st, 0, 1).Error(), "replaced") {
		t.Fatalf("replaced, after its day: %+v", st)
	}
	st = StandingFor(key, "", &CheckinState{Status: "in_use_elsewhere", StopsOn: "2026-10-10"}, day("2026-10-10"), s.keys)
	if st.Status != StandingExpired || !strings.Contains(CheckStart(st).Error(), "another Auto Tournament install") {
		t.Fatalf("elsewhere: %+v", st)
	}
}

func TestPlatformCap(t *testing.T) {
	t.Setenv("CSM_ROOT", t.TempDir())
	if err := CheckPlatformCap(DefaultPlatformLink, 50, 1); err != nil {
		t.Fatalf("no cap: %v", err)
	}
	if err := SetPlatformCap(DefaultPlatformLink, 5); err != nil {
		t.Fatal(err)
	}
	if err := CheckPlatformCap(DefaultPlatformLink, 4, 1); err != nil {
		t.Fatal(err)
	}
	err := CheckPlatformCap(DefaultPlatformLink, 5, 1)
	if le, ok := err.(*LicenseLimitError); !ok || le.Code != "platform_cap" {
		t.Fatalf("cap: %v", err)
	}
	_ = SetPlatformCap(DefaultPlatformLink, -1)
	if PlatformCap(DefaultPlatformLink) != -1 {
		t.Fatal("cap not removed")
	}
}
