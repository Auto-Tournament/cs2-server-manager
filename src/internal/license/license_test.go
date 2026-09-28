package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// testKey is a throwaway key pair made for the test run; the real private
// key is never needed.
type testKey struct {
	priv ed25519.PrivateKey
	kid  string
	x    string
}

func newTestKey(t *testing.T) testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{priv: priv, kid: KidFor(pub), x: base64.RawURLEncoding.EncodeToString(pub)}
}

func (k testKey) keys() map[string]string { return map[string]string{k.kid: k.x} }

// signRaw signs any payload value, like the website's signLicense.
func (k testKey) signRaw(t *testing.T, payload any) string {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	input := TokenPrefix + "." + base64.RawURLEncoding.EncodeToString(data)
	sig := ed25519.Sign(k.priv, []byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (k testKey) payload(overrides map[string]any) map[string]any {
	p := map[string]any{
		"v":             1,
		"kid":           k.kid,
		"id":            "L-3kq8Zx0bQ1aR",
		"customer":      "cus_123",
		"licensee":      "NTLAN",
		"product":       "servers",
		"pack":          "S",
		"max_servers":   6,
		"kind":          "year",
		"issued_at":     "2026-09-28T10:11:12Z",
		"updates_until": "2027-09-28",
	}
	for key, v := range overrides {
		if v == nil {
			delete(p, key)
			continue
		}
		p[key] = v
	}
	return p
}

func day(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d.Add(12 * time.Hour)
}

func codes(r Result) []string {
	var out []string
	for _, w := range r.Warnings {
		out = append(out, w.Code)
	}
	return out
}

func TestEmbeddedKidMatchesKey(t *testing.T) {
	for kid, x := range PublicKeys {
		pub, err := base64.RawURLEncoding.DecodeString(x)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			t.Fatalf("kid %s: x is not a raw 32-byte key: %v", kid, err)
		}
		if got := KidFor(pub); got != kid {
			t.Fatalf("kid %s: derived kid is %s", kid, got)
		}
	}
	if _, ok := PublicKeys["tWl_YS3_AzLgqdkm"]; !ok {
		t.Fatal("the production key is missing")
	}
}

func TestFreshKeyVerifies(t *testing.T) {
	k := newTestKey(t)
	token := k.signRaw(t, k.payload(nil))
	r := Verify(token, Options{PublicKeys: k.keys(), LineDate: "2027-03-01", ServerCount: 6, Now: day("2027-03-01")})
	if !r.Valid || r.Status != StatusOK || len(r.Warnings) != 0 {
		t.Fatalf("got %+v", r)
	}
	if r.License.Licensee != "NTLAN" || r.License.MaxServers != 6 || r.License.ID != "L-3kq8Zx0bQ1aR" {
		t.Fatalf("payload %+v", r.License)
	}
	// Surrounding whitespace (a paste) is fine.
	if r := Verify("  "+token+"\n", Options{PublicKeys: k.keys(), LineDate: "2027-03-01", ServerCount: -1, Now: day("2027-03-01")}); r.Status != StatusOK {
		t.Fatalf("trimmed: %+v", r)
	}
}

func TestTamperedSignatureIsInvalid(t *testing.T) {
	k := newTestKey(t)
	token := k.signRaw(t, k.payload(nil))
	parts := strings.Split(token, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sig[0] ^= 1
	bad := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sig)
	r := Verify(bad, Options{PublicKeys: k.keys()})
	if r.Valid || r.Status != StatusInvalid || codes(r)[0] != CodeBadSignature {
		t.Fatalf("got %+v", r)
	}
}

func TestTamperedPayloadIsInvalid(t *testing.T) {
	k := newTestKey(t)
	token := k.signRaw(t, k.payload(nil))
	parts := strings.Split(token, ".")
	changed, _ := json.Marshal(k.payload(map[string]any{"max_servers": 500}))
	bad := parts[0] + "." + base64.RawURLEncoding.EncodeToString(changed) + "." + parts[2]
	if r := Verify(bad, Options{PublicKeys: k.keys()}); codes(r)[0] != CodeBadSignature {
		t.Fatalf("got %+v", r)
	}
}

func TestUnknownKid(t *testing.T) {
	k := newTestKey(t)
	other := newTestKey(t)
	token := k.signRaw(t, k.payload(nil))
	if r := Verify(token, Options{PublicKeys: other.keys()}); codes(r)[0] != CodeUnknownKid {
		t.Fatalf("got %+v", r)
	}
	// A kid pointing at another key's public key.
	if r := Verify(token, Options{PublicKeys: map[string]string{k.kid: other.x}}); codes(r)[0] != CodeBadSignature {
		t.Fatalf("got %+v", r)
	}
	// The embedded table does not know a throwaway key.
	if r := Verify(token, Options{}); codes(r)[0] != CodeUnknownKid {
		t.Fatalf("embedded: got %+v", r)
	}
}

func TestLineCoverage(t *testing.T) {
	k := newTestKey(t)
	token := k.signRaw(t, k.payload(map[string]any{"updates_until": "2027-10-01"}))
	r := Verify(token, Options{PublicKeys: k.keys(), LineDate: "2027-11-15", Now: day("2028-01-01"), ServerCount: -1})
	if !r.Valid || r.Status != StatusWarning || strings.Join(codes(r), ",") != CodeUpdatesExpired {
		t.Fatalf("newer line: %+v", r)
	}
	// A line from before updates_until stays covered after it (later patches too).
	if r := Verify(token, Options{PublicKeys: k.keys(), LineDate: "2027-09-01", Now: day("2028-06-01"), ServerCount: -1}); r.Status != StatusOK {
		t.Fatalf("covered line: %+v", r)
	}
	// Inclusive.
	if r := Verify(token, Options{PublicKeys: k.keys(), LineDate: "2027-10-01", Now: day("2027-10-01"), ServerCount: -1}); r.Status != StatusOK {
		t.Fatalf("same day: %+v", r)
	}
}

func TestFounderCoversEveryLine(t *testing.T) {
	k := newTestKey(t)
	token := k.signRaw(t, k.payload(map[string]any{"kind": "founder", "updates_until": Lifetime}))
	if r := Verify(token, Options{PublicKeys: k.keys(), LineDate: "2040-01-01", Now: day("2041-01-01"), ServerCount: -1}); r.Status != StatusOK {
		t.Fatalf("got %+v", r)
	}
	// Even if a founder payload carried an earlier date.
	token = k.signRaw(t, k.payload(map[string]any{"kind": "founder", "updates_until": "2027-01-01"}))
	if r := Verify(token, Options{PublicKeys: k.keys(), LineDate: "2040-01-01", Now: day("2041-01-01"), ServerCount: -1}); r.Status != StatusOK {
		t.Fatalf("founder with a date: %+v", r)
	}
}

func TestEventWindowAndServersOnlyWarn(t *testing.T) {
	k := newTestKey(t)
	token := k.signRaw(t, k.payload(map[string]any{
		"kind": "event", "updates_until": "2026-10-05", "valid_from": "2026-10-03", "valid_to": "2026-10-05", "max_servers": 15, "pack": "M",
	}))
	r := Verify(token, Options{PublicKeys: k.keys(), LineDate: "2026-09-01", Now: day("2026-10-09"), ServerCount: 20})
	if !r.Valid || r.Status != StatusWarning || strings.Join(codes(r), ",") != "period_ended,too_many_servers" {
		t.Fatalf("got %+v", r)
	}
	if !strings.Contains(r.Warnings[1].Message, "20 servers set up; license covers 15.") ||
		!strings.Contains(r.Warnings[1].Message, "Every server running CS2 Server Manager or Ready Up counts") {
		t.Fatalf("server warning wording: %q", r.Warnings[1].Message)
	}
	if r := Verify(token, Options{PublicKeys: k.keys(), LineDate: "2026-09-01", Now: day("2026-10-01"), ServerCount: -1}); strings.Join(codes(r), ",") != CodePeriodNotStarted {
		t.Fatalf("before: %+v", r)
	}
	if r := Verify(token, Options{PublicKeys: k.keys(), LineDate: "2026-09-01", Now: day("2026-10-04"), ServerCount: 15}); r.Status != StatusOK {
		t.Fatalf("during: %+v", r)
	}
}

func TestBothProductsCoverCSM(t *testing.T) {
	k := newTestKey(t)
	for _, product := range []string{"servers", "platform"} {
		token := k.signRaw(t, k.payload(map[string]any{"product": product}))
		if r := Verify(token, Options{PublicKeys: k.keys(), LineDate: "2027-01-01", Now: day("2027-01-01"), ServerCount: 1}); r.Status != StatusOK {
			t.Fatalf("%s: %+v", product, r)
		}
	}
}

func TestGarbageIsInvalidNeverPanics(t *testing.T) {
	k := newTestKey(t)
	for _, bad := range []string{
		"", "ATL1", "ATL1..", "ATL2.abc.def", "ATL1.abc.def", "ATL1.a b.c", "ATL1.!!!.AAAA",
		"ATL1." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + "." + strings.Repeat("A", 86),
		"ATL1.e30." + strings.Repeat("A", 10),
		strings.Repeat("A", MaxTokenLength+1),
	} {
		r := Verify(bad, Options{PublicKeys: k.keys()})
		if r.Valid || r.Status != StatusInvalid || codes(r)[0] != CodeMalformed {
			t.Fatalf("%q: %+v", bad, r)
		}
	}
	v2 := base64.RawURLEncoding.EncodeToString([]byte(`{"v":2}`))
	if r := Verify("ATL1."+v2+"."+strings.Repeat("A", 86), Options{PublicKeys: k.keys()}); codes(r)[0] != CodeUnsupportedVersion {
		t.Fatalf("v2: %+v", r)
	}
	// JSON null / array payloads have no kid.
	for _, body := range []string{"null", "[1]", "3"} {
		b := base64.RawURLEncoding.EncodeToString([]byte(body))
		if r := Verify("ATL1."+b+"."+strings.Repeat("A", 86), Options{PublicKeys: k.keys()}); codes(r)[0] != CodeUnknownKid {
			t.Fatalf("%s: %+v", body, r)
		}
	}
}

func TestSignedButIncompleteIsInvalid(t *testing.T) {
	k := newTestKey(t)
	cases := map[string]map[string]any{
		"no id":              {"id": nil},
		"bad product":        {"product": "other"},
		"bad pack":           {"pack": "XL"},
		"zero servers":       {"max_servers": 0},
		"fractional servers": {"max_servers": 1.5},
		"string servers":     {"max_servers": "6"},
		"bad kind":           {"kind": "month"},
		"bad date":           {"updates_until": "2027-02-30"},
		"bad issued_at":      {"issued_at": "yesterday"},
		"half window":        {"valid_from": "2026-10-03"},
		"reversed window":    {"valid_from": "2026-10-05", "valid_to": "2026-10-03"},
		"bad licensee":       {"licensee": 7},
	}
	for name, override := range cases {
		token := k.signRaw(t, k.payload(override))
		r := Verify(token, Options{PublicKeys: k.keys()})
		if r.Valid || codes(r)[0] != CodeMalformed {
			t.Fatalf("%s: %+v", name, r)
		}
	}
	// Unknown fields are ignored; licensee is optional.
	token := k.signRaw(t, k.payload(map[string]any{"licensee": nil, "future_field": true}))
	if r := Verify(token, Options{PublicKeys: k.keys(), LineDate: "2027-01-01", Now: day("2027-01-01"), ServerCount: -1}); r.Status != StatusOK {
		t.Fatalf("extra field: %+v", r)
	}
}

func TestLooksLikeKeyAndPeek(t *testing.T) {
	k := newTestKey(t)
	token := k.signRaw(t, k.payload(nil))
	if !LooksLikeKey(token) {
		t.Fatal("a real key does not look like one")
	}
	for _, bad := range []string{"", "hello", `ATL1.abc.def"; rcon_password x`, "ATL1.abc", "ATL1.a b.c"} {
		if LooksLikeKey(bad) {
			t.Fatalf("%q looks like a key", bad)
		}
	}
	p, ok := Peek(token)
	if !ok || p.ID != "L-3kq8Zx0bQ1aR" {
		t.Fatalf("peek: %+v %v", p, ok)
	}
}

func TestResolveLineDate(t *testing.T) {
	now := time.Date(2026, 9, 28, 23, 0, 0, 0, time.UTC)
	built := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		baked string
		built time.Time
		want  string
	}{
		{"2026-09-01", built, "2026-09-01"},
		{"", built, "2026-09-20"},
		{"not-a-date", built, "2026-09-20"},
		{"2026-02-30", time.Time{}, "2026-09-28"},
		{"", time.Time{}, "2026-09-28"},
	}
	for _, c := range cases {
		if got := ResolveLineDate(c.baked, c.built, now); got != c.want {
			t.Fatalf("ResolveLineDate(%q, %v) = %s, want %s", c.baked, c.built, got, c.want)
		}
	}
}
