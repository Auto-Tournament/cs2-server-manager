package hostagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Token formats (FLEET.md §4.2, §18.1): <prefix>_<12 base32>_<43 base64url>.
var (
	hostTokenRe = regexp.MustCompile(`^rhs_[0-9a-z]{12}_[A-Za-z0-9_-]{43}$`)
	fleetKeyRe  = regexp.MustCompile(`^rfk_[0-9a-z]{12}_[A-Za-z0-9_-]{43}$`)
)

// IsFleetKey reports whether s looks like a fleet enrollment key.
func IsFleetKey(s string) bool { return fleetKeyRe.MatchString(s) }

// Credentials is fleet/credentials.json in csm's state dir (mode 0600). It
// is the host's identity on the platform.
type Credentials struct {
	// PlatformURL is the base URL the host enrolled with (https://…).
	PlatformURL string `json:"platform_url"`
	// WSURL is where the hosts channel connects (wss://…/api/fleet/host).
	WSURL    string `json:"ws_url"`
	HostID   string `json:"host_id"`
	TenantID string `json:"tenant_id"`
	// Token is the host token (rhs_…). Never logged, never in a URL.
	Token string `json:"token"`
	// FleetKey is the fleet enrollment key (rfk_…) when the host enrolled
	// with one. It is reused to re-enroll after a revoked token and handed
	// to the servers csm creates (FLEET.md §18.1). Empty after a code.
	FleetKey   string `json:"fleet_key,omitempty"`
	MachineID  string `json:"machine_id"`
	EnrolledAt string `json:"enrolled_at"`
	RotatedAt  string `json:"rotated_at,omitempty"`
	// InsecureDev allows ws:// and http:// (csm link --insecure), to any host.
	InsecureDev bool `json:"insecure_dev,omitempty"`
	// CAFile is an extra CA bundle (PEM) for a platform behind a private CA.
	CAFile string `json:"ca_file,omitempty"`
}

// Validate checks a loaded or received credentials document.
func (c *Credentials) Validate() error {
	switch {
	case c.HostID == "" || len(c.HostID) > 64:
		return errors.New("credentials: host_id missing")
	case !hostTokenRe.MatchString(c.Token):
		return errors.New("credentials: token is not a host token")
	case c.FleetKey != "" && !fleetKeyRe.MatchString(c.FleetKey):
		return errors.New("credentials: fleet_key is not a fleet key")
	}
	if _, err := CheckURL(c.PlatformURL, c.InsecureDev, "https", "http"); err != nil {
		return fmt.Errorf("credentials: platform_url: %w", err)
	}
	if _, err := CheckURL(c.WSURL, c.InsecureDev, "wss", "ws"); err != nil {
		return fmt.Errorf("credentials: ws_url: %w", err)
	}
	return nil
}

// Paths is where the agent keeps its files: <csm root>/fleet/.
type Paths struct {
	Dir string
}

// NewPaths returns the fleet dir under csm's state root.
func NewPaths(csmRoot string) Paths { return Paths{Dir: filepath.Join(csmRoot, "fleet")} }

// Credentials is fleet/credentials.json.
func (p Paths) Credentials() string { return filepath.Join(p.Dir, "credentials.json") }

// State is fleet/state.json (the last platform seq processed).
func (p Paths) State() string { return filepath.Join(p.Dir, "state.json") }

// Config is fleet/agent.json (non-secret agent settings).
func (p Paths) Config() string { return filepath.Join(p.Dir, "agent.json") }

// ErrNotLinked is returned when there are no credentials.
var ErrNotLinked = errors.New("this machine is not linked to a platform (run `csm link <url> <code|key>`)")

// LoadCredentials reads and validates the credentials file.
func LoadCredentials(p Paths) (*Credentials, error) {
	data, err := os.ReadFile(p.Credentials())
	if os.IsNotExist(err) {
		return nil, ErrNotLinked
	}
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p.Credentials(), err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// SaveCredentials writes the file atomically (temp + rename, 0600) in a 0700
// directory.
func SaveCredentials(p Paths, c *Credentials) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return writeFileAtomic(p.Credentials(), mustJSONIndent(c), 0o600)
}

// RemoveCredentials deletes the credentials and the agent state.
func RemoveCredentials(p Paths) error {
	var errs []string
	for _, f := range []string{p.Credentials(), p.State()} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// agentState is fleet/state.json.
type agentState struct {
	// HostID the seq belongs to; a new identity starts from 0.
	HostID    string `json:"host_id"`
	LastRxSeq int64  `json:"last_rx_seq"`
}

func loadState(p Paths, hostID string) agentState {
	data, err := os.ReadFile(p.State())
	if err != nil {
		return agentState{HostID: hostID}
	}
	var s agentState
	if json.Unmarshal(data, &s) != nil || s.HostID != hostID || s.LastRxSeq < 0 {
		return agentState{HostID: hostID}
	}
	return s
}

func saveState(p Paths, s agentState) error {
	return writeFileAtomic(p.State(), mustJSONIndent(s), 0o600)
}

func mustJSONIndent(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

// writeFileAtomic writes data to path through a temp file in the same
// directory, with mode set before the rename, and creates the directory as
// 0700 when missing.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(mode); err != nil && !isChmodUnsupported(err) {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// isChmodUnsupported: Windows (tests only) cannot chmod an open file.
func isChmodUnsupported(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not supported")
}

// --- URLs --------------------------------------------------------------------

// CheckURL parses a platform URL and applies the transport rules (FLEET.md
// §4.4): the secure scheme always; the plain one only with insecure set
// (--insecure: any host, for platforms served over plain http://ip:port; the
// token then travels unencrypted). No user info, query or fragment (a token
// must never ride in a URL).
func CheckURL(raw string, insecure bool, secure, plain string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%q is not a URL", truncate(raw, 80))
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("the URL must not carry credentials, a query or a fragment")
	}
	switch strings.ToLower(u.Scheme) {
	case secure:
		return u, nil
	case plain:
		if !insecure {
			return nil, fmt.Errorf("%s:// is refused: use %s://, or add --insecure if the platform only serves plain %s:// (the token then travels unencrypted)", plain, secure, plain)
		}
		return u, nil
	}
	return nil, fmt.Errorf("scheme %q is not %s", u.Scheme, secure)
}

// IsPrivateHost reports loopback, RFC 1918 and localhost names.
func IsPrivateHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

// UpgradeSameHostWS turns a ws:// URL into wss:// when the platform was
// enrolled with over https at the same host (and the same or no port): a
// platform behind a TLS proxy that does not pass X-Forwarded-Proto sees
// plain http and answers ws://, though the host evidently speaks TLS. A ws://
// URL to any other host is left alone for CheckURL to refuse.
func UpgradeSameHostWS(base *url.URL, raw string) (string, bool) {
	if base == nil || !strings.EqualFold(base.Scheme, "https") {
		return "", false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "ws") || u.Host == "" {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), strings.TrimSuffix(base.Hostname(), ".")) {
		return "", false
	}
	switch p := u.Port(); p {
	case "", "80", base.Port():
	default:
		if !(p == "443" && base.Port() == "") {
			return "", false
		}
	}
	u.Scheme = "wss"
	u.Host = base.Host
	return u.String(), true
}

// DefaultWSURL derives wss://host/api/fleet/host from the platform base URL.
func DefaultWSURL(base *url.URL) string {
	u := *base
	if strings.EqualFold(u.Scheme, "http") {
		u.Scheme = "ws"
	} else {
		u.Scheme = "wss"
	}
	u.Path = strings.TrimRight(u.Path, "/") + HostWSPath
	u.RawPath = ""
	return u.String()
}

// --- machine id ---------------------------------------------------------------

// MachineID derives the host's stable id from /etc/machine-id. The raw id is
// never sent: systemd asks applications to use a keyed hash of it, so the
// platform gets sha256("auto-tournament-host:" + machine-id), first 32 hex
// characters. Re-enrolling the same machine sends the same value.
func MachineID(readFile func(string) ([]byte, error)) (string, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		data, err := readFile(p)
		if err != nil {
			continue
		}
		id := strings.TrimSpace(string(data))
		if len(id) < 16 {
			continue
		}
		sum := sha256.Sum256([]byte("auto-tournament-host:" + id))
		return hex.EncodeToString(sum[:])[:32], nil
	}
	return "", errors.New("no /etc/machine-id on this machine (systemd-machine-id-setup creates one)")
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
