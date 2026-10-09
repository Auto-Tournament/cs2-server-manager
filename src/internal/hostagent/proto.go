// Package hostagent is csm's host agent: the machine's link to the Auto
// Tournament platform over the hosts channel (Ready Up docs/FLEET.md §18, D17).
//
// csm enrolls the machine once (`csm link`), keeps one outbound WebSocket to
// <platform>/api/fleet/host and lets the platform start, stop, restart, create
// and update the server-N folders on it. The transport is the server channel's
// (FLEET.md §5 envelope, §6 hello/welcome, ping, seq/ack, backoff, close
// codes); only the identity and the messages differ. The JSON Schemas for
// every host message are in protocol/host-v1/ at the repository root.
//
// This package holds everything that does not touch the machine: the
// protocol, enrollment, credentials, the session and the message handlers.
// The machine itself (tmux, SteamCMD, server-N folders) is behind Backend,
// which the csm package implements. That keeps this package portable and its
// tests runnable anywhere.
package hostagent

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ProtocolVersion is the only protocol major this agent speaks (FLEET.md §14.1).
const ProtocolVersion = 1

// MaxFrameBytes is the largest WebSocket frame either side sends (FLEET.md §5).
const MaxFrameBytes = 1 << 20

// Paths on the platform (FLEET.md §3, §18.1).
const (
	EnrollPath = "/api/fleet/enroll"
	HostWSPath = "/api/fleet/host"
)

// WebSocket close codes (FLEET.md §6.3).
const (
	CloseNormal              = 1000
	CloseGoingAway           = 1001
	CloseProtocolError       = 4400
	CloseBadToken            = 4401
	CloseRevoked             = 4403
	CloseReplaced            = 4409
	CloseUnsupportedProtocol = 4426
	CloseRateLimited         = 4429
	ClosePlatformDraining    = 4503
)

// Message types of the hosts channel. The transport ones are shared with the
// server channel.
const (
	TypeHello   = "hello"
	TypeWelcome = "welcome"
	TypePing    = "ping"
	TypePong    = "pong"
	TypeAck     = "ack"
	TypeError   = "error"

	TypeAuthRotate  = "auth.rotate"
	TypeAuthRotated = "auth.rotated"

	// platform -> host (reliable)
	TypeServersList     = "host.servers.list"
	TypeServerStart     = "server.start"
	TypeServerStop      = "server.stop"
	TypeServerRestart   = "server.restart"
	TypeServerCreate    = "server.create"
	TypeServerRemove    = "server.remove"
	TypeServerSetLaunch = "server.set_launch_args"
	TypeUpdateGame      = "host.update_game"
	TypeUpdatePlugins   = "host.update_plugins"
	TypeLogsTail        = "logs.tail"
	TypeLogsStop        = "logs.stop"
	TypeUpdatesHold     = "host.updates_hold"
	TypeLicense         = "host.license"

	// host -> platform
	TypeInventory = "host.inventory"
	TypeHealth    = "host.health"
	TypeResult    = "host.result"
	TypeProgress  = "host.progress"
	TypeLogsChunk = "logs.chunk"
)

// Capabilities this agent advertises in hello (FLEET.md §14.2 style).
var Capabilities = []string{
	"host.v1",
	"servers.start_stop",
	"servers.create",
	"servers.remove_last",
	"update.game",
	"update.plugins",
	"updates.hold",
	"logs.tail",
	"logs.follow",
	"health.v1",
}

// Envelope is one frame (FLEET.md §5). Seq, Ack, Ref and Epoch are pointers
// so "absent" and "0" stay different.
type Envelope struct {
	V       int             `json:"v"`
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Seq     *int64          `json:"seq,omitempty"`
	Ack     *int64          `json:"ack,omitempty"`
	TS      int64           `json:"ts"`
	Ref     *string         `json:"ref,omitempty"`
	Epoch   *int64          `json:"epoch,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

// Reliable reports whether the frame carries a seq.
func (e *Envelope) Reliable() bool { return e.Seq != nil }

var (
	typeRe = regexp.MustCompile(`^[a-z]+(\.[a-z_]+)*$`)
	ulidRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
)

// rawEnvelope is used to check required fields on the way in.
type rawEnvelope struct {
	V       *int            `json:"v"`
	Type    *string         `json:"type"`
	ID      *string         `json:"id"`
	Seq     *int64          `json:"seq"`
	Ack     *int64          `json:"ack"`
	TS      *int64          `json:"ts"`
	Ref     *string         `json:"ref"`
	Epoch   *int64          `json:"epoch"`
	Payload json.RawMessage `json:"payload"`
}

// ParseEnvelope decodes and validates one inbound frame against the envelope
// schema: required fields, v = 1, the type and ULID patterns, no unknown
// top-level fields, an object payload, the frame size.
func ParseEnvelope(data []byte) (*Envelope, error) {
	if len(data) > MaxFrameBytes {
		return nil, fmt.Errorf("frame of %d bytes is over the %d byte limit", len(data), MaxFrameBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	var raw rawEnvelope
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("invalid envelope: %w", err)
	}
	if dec.More() {
		return nil, errors.New("invalid envelope: trailing data")
	}
	switch {
	case raw.V == nil || raw.Type == nil || raw.ID == nil || raw.TS == nil || raw.Payload == nil:
		return nil, errors.New("invalid envelope: v, type, id, ts and payload are required")
	case *raw.V != ProtocolVersion:
		return nil, fmt.Errorf("invalid envelope: v %d is not %d", *raw.V, ProtocolVersion)
	case !typeRe.MatchString(*raw.Type) || len(*raw.Type) > 128:
		return nil, fmt.Errorf("invalid envelope: bad type %q", truncate(*raw.Type, 64))
	case !ulidRe.MatchString(*raw.ID):
		return nil, errors.New("invalid envelope: id is not a ULID")
	case raw.Seq != nil && *raw.Seq < 1:
		return nil, errors.New("invalid envelope: seq must be >= 1")
	case raw.Ack != nil && *raw.Ack < 0:
		return nil, errors.New("invalid envelope: ack must be >= 0")
	case raw.Epoch != nil && *raw.Epoch < 1:
		return nil, errors.New("invalid envelope: epoch must be >= 1")
	}
	p := bytes.TrimSpace(raw.Payload)
	if len(p) == 0 || p[0] != '{' {
		return nil, errors.New("invalid envelope: payload must be an object")
	}
	return &Envelope{
		V: *raw.V, Type: *raw.Type, ID: *raw.ID, Seq: raw.Seq, Ack: raw.Ack, TS: *raw.TS,
		Ref: raw.Ref, Epoch: raw.Epoch, Payload: p,
	}, nil
}

// DecodePayload decodes a payload strictly: unknown fields are ignored (as
// the protocol says), but the types must match.
func DecodePayload(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}
	return nil
}

// --- ULID -------------------------------------------------------------------

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	ulidMu   sync.Mutex
	ulidLast int64
	ulidRand [10]byte
)

// NewULID returns a ULID (48-bit ms time + 80 random bits, Crockford base32).
// Within one millisecond the random part is incremented, so ids stay sortable.
func NewULID() string {
	ulidMu.Lock()
	defer ulidMu.Unlock()
	ms := time.Now().UnixMilli()
	if ms == ulidLast {
		for i := len(ulidRand) - 1; i >= 0; i-- {
			ulidRand[i]++
			if ulidRand[i] != 0 {
				break
			}
		}
	} else {
		ulidLast = ms
		if _, err := rand.Read(ulidRand[:]); err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
	}
	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	copy(b[6:], ulidRand[:])
	// 128 bits -> 26 chars of 5 bits, least significant first (the first
	// char carries the top 3 bits).
	var out [26]byte
	var acc uint64
	bits := 0
	idx := 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(b[i]) << bits
		bits += 8
		for bits >= 5 && idx >= 0 {
			out[idx] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			idx--
		}
	}
	if idx >= 0 {
		out[idx] = crockford[acc&31]
	}
	return string(out[:])
}

// --- secrets ----------------------------------------------------------------

var secretRe = regexp.MustCompile(`\b(rhs|rus|rfk)_[0-9A-Za-z]+_[A-Za-z0-9_-]+|\brst_[0-9A-Za-z]{16,}|\bRUE-[0-9A-Za-z]{4}(-[0-9A-Za-z]{4}){3}\b|\bATL1[.A-Za-z0-9_-]{16,}`)

// Redact replaces host and server tokens, fleet keys, status tokens,
// enrollment codes and license keys in s. Everything the agent logs or sends
// back as output goes through it (FLEET.md §15).
func Redact(s string) string {
	return secretRe.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "RUE-") {
			return "RUE-…"
		}
		if strings.HasPrefix(m, "ATL1") {
			return "ATL1…"
		}
		return m[:4] + "…"
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Do not cut a UTF-8 sequence in half.
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// tail keeps the last n bytes of s (on a line start when there is one).
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut++
	}
	t := s[cut:]
	if i := strings.IndexByte(t, '\n'); i >= 0 && i < len(t)-1 {
		t = t[i+1:]
	}
	return "…\n" + t
}
