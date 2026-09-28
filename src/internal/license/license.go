// Package license verifies Auto Tournament license keys offline.
//
// It is a Go port of the website's reference verifier
// (Auto-Tournament/website: scripts/license-verify.mjs, format in the README
// section "License keys"). Nothing here ever blocks: a key is ok, ok with
// warnings, or invalid, and the caller only shows the result.
//
// Token: ATL1.<payload>.<signature>
//
//	payload   = base64url (no padding) of the UTF-8 JSON payload
//	signature = base64url (no padding) of the 64-byte Ed25519 signature over
//	            the ASCII bytes of "ATL1.<payload>"
//
// The payload's kid picks the public key (see PublicKeys).
package license

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

const (
	// TokenPrefix starts every v1 key.
	TokenPrefix = "ATL1"
	// MaxTokenLength bounds what is parsed.
	MaxTokenLength = 4096
	// Lifetime is updates_until for founder licenses.
	Lifetime = "9999-12-31"
	// dateLayout is the YYYY-MM-DD format used for every date in a payload.
	dateLayout = "2006-01-02"
)

// Status is the overall result. There is deliberately no "blocked".
type Status string

const (
	StatusOK      Status = "ok"
	StatusWarning Status = "warning"
	StatusInvalid Status = "invalid"
)

// Warning / invalid codes, the same as the reference verifier's.
const (
	CodeMalformed          = "malformed"
	CodeUnknownKid         = "unknown_kid"
	CodeBadSignature       = "bad_signature"
	CodeUnsupportedVersion = "unsupported_version"
	CodeUpdatesExpired     = "updates_expired"
	CodeTooManyServers     = "too_many_servers"
	CodePeriodEnded        = "period_ended"
	CodePeriodNotStarted   = "period_not_started"
)

// Payload is a v1 license. Unknown fields are ignored.
type Payload struct {
	V            int    `json:"v"`
	Kid          string `json:"kid"`
	ID           string `json:"id"`
	Customer     string `json:"customer"`
	Licensee     string `json:"licensee,omitempty"`
	Product      string `json:"product"` // servers | platform
	Pack         string `json:"pack"`    // S | M | L
	MaxServers   int    `json:"max_servers"`
	Kind         string `json:"kind"` // event | year | founder
	IssuedAt     string `json:"issued_at"`
	UpdatesUntil string `json:"updates_until"`
	ValidFrom    string `json:"valid_from,omitempty"`
	ValidTo      string `json:"valid_to,omitempty"`
}

// Message is one warning, or the reason a key is invalid.
type Message struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Result is what Verify returns.
type Result struct {
	Valid    bool      `json:"valid"`
	Status   Status    `json:"status"`
	Warnings []Message `json:"warnings"`
	License  *Payload  `json:"license"`
}

// Options are what the running product knows about itself.
type Options struct {
	// PublicKeys overrides the embedded PublicKeys (tests).
	PublicKeys map[string]string
	// LineDate is the release date (YYYY-MM-DD) of this build's x.y.0 line.
	// Empty means today.
	LineDate string
	// ServerCount is how many game servers are set up; < 0 means not checked.
	ServerCount int
	// Now is today; the zero time means time.Now().
	Now time.Time
}

var b64url = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func invalid(code, msg string) Result {
	return Result{Valid: false, Status: StatusInvalid, Warnings: []Message{{Code: code, Message: msg}}}
}

// decoded is a split token, signature not yet checked.
type decoded struct {
	raw       map[string]any
	signature []byte
	signed    []byte
}

func decode(token string) (decoded, error) {
	var d decoded
	t := strings.TrimSpace(token)
	if len(t) > MaxTokenLength {
		return d, fmt.Errorf("too long")
	}
	parts := strings.Split(t, ".")
	if len(parts) != 3 || parts[0] != TokenPrefix {
		return d, fmt.Errorf("not an %s key", TokenPrefix)
	}
	body, sig := parts[1], parts[2]
	if !b64url.MatchString(body) || !b64url.MatchString(sig) {
		return d, fmt.Errorf("not base64url")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return d, fmt.Errorf("not base64url")
	}
	dec := json.NewDecoder(strings.NewReader(string(payloadBytes)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return d, fmt.Errorf("payload is not JSON")
	}
	if dec.More() {
		return d, fmt.Errorf("payload is not JSON")
	}
	d.raw, _ = v.(map[string]any) // null, arrays and scalars have no kid: unknown_kid below
	d.signature, err = base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return d, fmt.Errorf("not base64url")
	}
	if len(d.signature) != ed25519.SignatureSize {
		return d, fmt.Errorf("bad signature length")
	}
	d.signed = []byte(TokenPrefix + "." + body)
	return d, nil
}

// LooksLikeKey reports whether s has the shape of a key (ATL1.<b64url>.<b64url>),
// without checking anything else. A key that passes is safe to write into a
// config file: it has no spaces, quotes or semicolons.
func LooksLikeKey(s string) bool {
	parts := strings.Split(strings.TrimSpace(s), ".")
	return len(s) <= MaxTokenLength && len(parts) == 3 && parts[0] == TokenPrefix &&
		b64url.MatchString(parts[1]) && b64url.MatchString(parts[2])
}

// Peek returns the payload without checking the signature, for showing the
// license id of a key that did not verify. Never trust it.
func Peek(token string) (*Payload, bool) {
	d, err := decode(token)
	if err != nil || d.raw == nil {
		return nil, false
	}
	p, problem := parsePayload(d.raw)
	if problem != "" {
		if id, ok := d.raw["id"].(string); ok && id != "" {
			return &Payload{ID: id}, true
		}
		return nil, false
	}
	return p, true
}

func isDate(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || len(s) != len(dateLayout) {
		return "", false
	}
	if _, err := time.Parse(dateLayout, s); err != nil {
		return "", false
	}
	return s, true
}

func nonEmptyString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && s != ""
}

func oneOf(v any, allowed ...string) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	for _, a := range allowed {
		if s == a {
			return s, true
		}
	}
	return "", false
}

// safeInt reads a JSON number that is a whole number within JavaScript's
// safe integer range (Number.isSafeInteger).
func safeInt(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) || math.Abs(f) > (1<<53)-1 {
		return 0, false
	}
	return int64(f), true
}

func validIssuedAt(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, dateLayout} {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

// parsePayload checks the payload's shape, like the reference verifier's
// payloadProblem. It returns the payload, or a problem description.
func parsePayload(raw map[string]any) (*Payload, string) {
	var p Payload
	if v, ok := safeInt(raw["v"]); !ok || v != 1 {
		return nil, fmt.Sprintf("unsupported version %v", raw["v"])
	}
	p.V = 1
	var ok bool
	if p.Kid, ok = nonEmptyString(raw["kid"]); !ok {
		return nil, "missing kid"
	}
	if p.ID, ok = nonEmptyString(raw["id"]); !ok {
		return nil, "missing id"
	}
	if p.Customer, ok = nonEmptyString(raw["customer"]); !ok {
		return nil, "missing customer"
	}
	if p.Product, ok = oneOf(raw["product"], "servers", "platform"); !ok {
		return nil, "bad product"
	}
	if p.Pack, ok = oneOf(raw["pack"], "S", "M", "L"); !ok {
		return nil, "bad pack"
	}
	max, ok := safeInt(raw["max_servers"])
	if !ok || max < 1 || max > math.MaxInt32 {
		return nil, "bad max_servers"
	}
	p.MaxServers = int(max)
	if p.Kind, ok = oneOf(raw["kind"], "event", "year", "founder"); !ok {
		return nil, "bad kind"
	}
	if !validIssuedAt(raw["issued_at"]) {
		return nil, "bad issued_at"
	}
	p.IssuedAt = raw["issued_at"].(string)
	if p.UpdatesUntil, ok = isDate(raw["updates_until"]); !ok {
		return nil, "bad updates_until"
	}
	_, hasFrom := raw["valid_from"]
	_, hasTo := raw["valid_to"]
	if hasFrom != hasTo {
		return nil, "valid_from and valid_to go together"
	}
	if hasFrom {
		from, okFrom := isDate(raw["valid_from"])
		to, okTo := isDate(raw["valid_to"])
		if !okFrom || !okTo || from > to {
			return nil, "bad valid_from/valid_to"
		}
		p.ValidFrom, p.ValidTo = from, to
	}
	if v, has := raw["licensee"]; has {
		s, isString := v.(string)
		if !isString {
			return nil, "bad licensee"
		}
		p.Licensee = s
	}
	return &p, ""
}

// Verify checks a key offline. It never returns an error and never blocks:
// a bad format or signature is StatusInvalid, everything else is at most a
// warning.
func Verify(token string, opts Options) Result {
	keys := opts.PublicKeys
	if keys == nil {
		keys = PublicKeys
	}

	d, err := decode(token)
	if err != nil {
		return invalid(CodeMalformed, fmt.Sprintf("This is not a valid license key (%v).", err))
	}

	if n, isNum := d.raw["v"].(json.Number); isNum && n.String() != "1" {
		if v, ok := safeInt(n); !ok || v != 1 {
			return invalid(CodeUnsupportedVersion, fmt.Sprintf("License key version %s needs a newer release.", n.String()))
		}
	}

	kid, _ := d.raw["kid"].(string)
	x, known := keys[kid]
	if kid == "" || !known {
		return invalid(CodeUnknownKid, "This license key was signed with a key this release does not know.")
	}
	pub, err := base64.RawURLEncoding.DecodeString(x)
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(pub), d.signed, d.signature) {
		return invalid(CodeBadSignature, "The license key signature does not match: the key was changed or mistyped.")
	}

	p, problem := parsePayload(d.raw)
	if problem != "" {
		return invalid(CodeMalformed, fmt.Sprintf("The license key content is not valid (%s).", problem))
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	today := now.UTC().Format(dateLayout)
	line := opts.LineDate
	if _, ok := isDate(line); !ok {
		line = today
	}

	var warnings []Message
	// Founder licenses cover every release line (updates_until is 9999-12-31
	// anyway; this keeps it true whatever the payload says).
	if p.Kind != "founder" && line > p.UpdatesUntil {
		warnings = append(warnings, Message{CodeUpdatesExpired, fmt.Sprintf(
			"This release line came out on %s, after the license's updates ended on %s. Renew updates, or stay on a release line from before then.",
			line, p.UpdatesUntil)})
	}
	if p.ValidTo != "" && today > p.ValidTo {
		warnings = append(warnings, Message{CodePeriodEnded, fmt.Sprintf("The event license window ended on %s.", p.ValidTo)})
	}
	if p.ValidFrom != "" && today < p.ValidFrom {
		warnings = append(warnings, Message{CodePeriodNotStarted, fmt.Sprintf("The event license window starts on %s.", p.ValidFrom)})
	}
	if opts.ServerCount >= 0 && opts.ServerCount > p.MaxServers {
		warnings = append(warnings, Message{CodeTooManyServers, fmt.Sprintf(
			"%d servers set up; license covers %d. Every server running CS2 Server Manager or Ready Up counts, spares and test servers included.",
			opts.ServerCount, p.MaxServers)})
	}
	// csm is covered by both a Servers and a Platform license, so there is
	// no wrong_product warning here.

	status := StatusOK
	if len(warnings) > 0 {
		status = StatusWarning
	}
	return Result{Valid: true, Status: status, Warnings: warnings, License: p}
}

// KidFor derives the kid of a public key: the first 16 base64url characters
// of SHA-256 over the raw 32-byte key.
func KidFor(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:])[:16]
}
