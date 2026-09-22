package csm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Asking Auto Tournament whether to hold updates
//
// The idle check in auto_update.go is a local judgement: nobody connected, no
// match loaded. It is right up to the moment the tournament platform allocates
// that server the next match. A server that restarts for a Valve update in the
// two minutes between two rounds is, from the players' side, a server that
// fell over.
//
// The platform knows. csm asks it:
//
//	GET <base>/api/servers/update-hold
//	X-MatchZy-Token: <server token>
//	-> {"success":true,"hold":true,"reason":"tournament \"X\" is in progress ..."}
//
// csm polls instead of the platform pushing, because a game host is behind
// whatever firewall the provider gives it and usually behind NAT: a push would
// need an inbound port and a listener here. The credential is the same
// fleet-wide server token the plugin already uses for event webhooks and demo
// uploads (`SERVER_TOKEN` on the platform), so this adds no new secret.
//
// **A failed poll holds.** If the platform cannot be reached, or answers
// something csm cannot read, csm does not know whether a tournament is running
// — and the cost of the two answers is not symmetric. Holding wrongly delays an
// update until the next cron cycle, five minutes later, or until the host runs
// `sudo csm update-server N` by hand. Updating wrongly restarts a live match.
// So "I cannot tell" is treated as "hold", and the monitor log says which it
// was.

// PlatformSettings points csm at an Auto Tournament instance.
type PlatformSettings struct {
	// BaseURL is the platform's public base URL, e.g. https://cs.example.io.
	BaseURL string `json:"base_url,omitempty"`
	// Token is the platform's SERVER_TOKEN, sent as X-MatchZy-Token. It is the
	// same fleet-wide token the plugin uses, not a per-host secret.
	Token string `json:"token,omitempty"`
}

// Environment overrides, for hosts that keep secrets out of files.
const (
	EnvPlatformURL   = "CSM_PLATFORM_URL"
	EnvPlatformToken = "CSM_PLATFORM_TOKEN"
)

// platformHoldPath is the endpoint on the platform. See the Auto Tournament
// API reference; it is server-token authenticated and read-only.
const platformHoldPath = "/api/servers/update-hold"

// platformHoldTimeout bounds one poll. The monitor runs from cron every few
// minutes, so a slow platform must not wedge the cycle; a timeout holds.
const platformHoldTimeout = 10 * time.Second

// Resolved returns the settings with the environment taking precedence.
func (p PlatformSettings) Resolved() PlatformSettings {
	return PlatformSettings{
		BaseURL: strings.TrimSpace(getenvDefault(EnvPlatformURL, p.BaseURL)),
		Token:   strings.TrimSpace(getenvDefault(EnvPlatformToken, p.Token)),
	}
}

// Configured reports whether there is enough to ask the platform anything.
func (p PlatformSettings) Configured() bool {
	r := p.Resolved()
	return r.BaseURL != "" && r.Token != ""
}

// holdURL builds the endpoint URL, rejecting anything that is not an http(s)
// URL so a typo becomes a clear error instead of a silent hold forever.
func (p PlatformSettings) holdURL() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("no platform URL is set (csm updates platform <url> <token>)")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("platform URL %q is not a URL: %w", base, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("platform URL %q must start with http:// or https://", base)
	}
	if u.Host == "" {
		return "", fmt.Errorf("platform URL %q has no host", base)
	}
	return base + platformHoldPath, nil
}

// PlainHTTPToRemoteHost reports whether the URL would send the token
// unencrypted to somewhere other than this machine. Callers warn; nothing is
// refused, because a reverse proxy on the same box is a legitimate setup.
func PlainHTTPToRemoteHost(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" || host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return false
	}
	return true
}

// PlatformHoldAnswer is what the platform replied.
type PlatformHoldAnswer struct {
	Success bool   `json:"success"`
	Hold    bool   `json:"hold"`
	Reason  string `json:"reason"`
	// TournamentStatus is informational ("in_progress", "setup", ...).
	TournamentStatus string `json:"tournamentStatus"`
}

// maxHoldBody bounds what is read from the platform, so a wrong URL that
// happens to answer cannot make csm read a large body into memory.
const maxHoldBody = 64 << 10

// FetchPlatformHold asks the platform once. Any error means "cannot tell".
func FetchPlatformHold(ctx context.Context, p PlatformSettings) (PlatformHoldAnswer, error) {
	var answer PlatformHoldAnswer
	p = p.Resolved()
	endpoint, err := p.holdURL()
	if err != nil {
		return answer, err
	}
	if p.Token == "" {
		return answer, fmt.Errorf("no platform token is set (csm updates platform <url> <token>)")
	}

	ctx, cancel := context.WithTimeout(ctx, platformHoldTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return answer, err
	}
	req.Header.Set("X-MatchZy-Token", p.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: platformHoldTimeout}).Do(req)
	if err != nil {
		return answer, fmt.Errorf("%s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHoldBody))
	if err != nil {
		return answer, fmt.Errorf("%s: reading the answer: %w", endpoint, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return answer, fmt.Errorf("%s: the platform rejected the server token (HTTP %d); "+
			"check it matches SERVER_TOKEN on the platform", endpoint, resp.StatusCode)
	case http.StatusNotFound:
		return answer, fmt.Errorf("%s: no such endpoint (HTTP 404); "+
			"the platform is older than the update hold, or the URL is wrong", endpoint)
	default:
		return answer, fmt.Errorf("%s: HTTP %d", endpoint, resp.StatusCode)
	}

	if err := json.Unmarshal(body, &answer); err != nil {
		return answer, fmt.Errorf("%s: the answer is not JSON: %w", endpoint, err)
	}
	if !answer.Success {
		return answer, fmt.Errorf("%s: the platform could not answer", endpoint)
	}
	return answer, nil
}

// HoldSource says who decided the hold, for the monitor log and `csm updates status`.
type HoldSource string

const (
	// HoldSourceManual: the host set `csm updates hold on|off`.
	HoldSourceManual HoldSource = "manual"
	// HoldSourcePlatform: the platform answered.
	HoldSourcePlatform HoldSource = "platform"
	// HoldSourceUnreachable: the platform could not be asked, so csm holds.
	HoldSourceUnreachable HoldSource = "platform-unreachable"
	// HoldSourceNone: nothing holds updates (no platform configured).
	HoldSourceNone HoldSource = "none"
)

// UpdateHold is the monitor's answer to "may a server restart for an update?".
type UpdateHold struct {
	On     bool
	Source HoldSource
	// Reason is one clause, written to be read in auto_update_monitor.log.
	Reason string
}

// Describe renders the hold for a log line or `csm updates status`.
func (h UpdateHold) Describe() string {
	state := "off"
	if h.On {
		state = "ON"
	}
	if h.Reason == "" {
		return fmt.Sprintf("%s (%s)", state, h.Source)
	}
	return fmt.Sprintf("%s (%s: %s)", state, h.Source, h.Reason)
}

// ResolveUpdateHold decides whether updates are held right now.
//
// The manual setting wins both ways: `hold on` holds whatever the platform
// says, and `hold off` allows updates without asking it — an operator who has
// turned the automatic hold off has said they will manage the timing. Only
// `hold auto` (the default) consults the platform, and only when one is
// configured.
func ResolveUpdateHold(ctx context.Context, s AutoUpdateSettings) UpdateHold {
	switch s.Mode() {
	case HoldModeOn:
		return UpdateHold{On: true, Source: HoldSourceManual,
			Reason: "`csm updates hold on` is set"}
	case HoldModeOff:
		return UpdateHold{On: false, Source: HoldSourceManual,
			Reason: "`csm updates hold off` is set, so the platform is not consulted"}
	}

	platform := s.Platform.Resolved()
	if !platform.Configured() {
		return UpdateHold{On: false, Source: HoldSourceNone,
			Reason: "no platform is configured (csm updates platform <url> <token>)"}
	}

	answer, err := FetchPlatformHold(ctx, platform)
	if err != nil {
		// Fail safe: not knowing is not permission to restart a server.
		return UpdateHold{On: true, Source: HoldSourceUnreachable,
			Reason: fmt.Sprintf("could not ask the platform (%v), so updates stay held until it answers", err)}
	}
	reason := strings.TrimSpace(answer.Reason)
	if reason == "" {
		reason = "the platform gave no reason"
	}
	return UpdateHold{On: answer.Hold, Source: HoldSourcePlatform, Reason: reason}
}
