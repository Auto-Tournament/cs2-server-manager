package csm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/hostagent"
	"github.com/sivert-io/cs2-server-manager/src/internal/license"
)

// Several platforms on one host
//
// A host can be linked to more than one Auto Tournament platform: a hosting
// provider runs servers for several clubs, each with its own platform. Each
// link has its own credentials and its own agent connection (`csm agent`
// runs one per link):
//
//	<csm root>/fleet/                the first link ("default"), as before
//	<csm root>/fleet/links/<name>/   every other link (`csm link --name <name> ...`)
//
// Every server belongs to one platform: the one that created it. Servers
// made before there was more than one link, or by the operator by hand,
// belong to the default link. A platform sees and controls only its own
// servers (LinkBackend), within the cap the host set for it
// (`csm license cap --link <name> <n>`). Each platform's license
// (host.license) goes to the Ready Up config of the servers it owns, unless
// the host has a key of its own, which covers every server.
//
// Ownership is <csm root>/fleet/owners.json; update holds per link are
// <csm root>/fleet/holds.json (the host holds updates while any platform
// asks it to).

var linkNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidLinkName reports whether name can name a platform link.
func ValidLinkName(name string) bool { return linkNameRe.MatchString(name) }

// LinkPaths is where a link's credentials and state live.
func LinkPaths(name string) hostagent.Paths {
	if name == "" || name == DefaultPlatformLink {
		return HostAgentPaths()
	}
	return hostagent.Paths{Dir: filepath.Join(HostAgentPaths().Dir, "links", name)}
}

// PlatformLinks lists the links that have credentials: "default" first
// (when linked), then the others by name.
func PlatformLinks() []string {
	var out []string
	if _, err := hostagent.LoadCredentials(LinkPaths(DefaultPlatformLink)); err == nil {
		out = append(out, DefaultPlatformLink)
	}
	entries, _ := os.ReadDir(filepath.Join(HostAgentPaths().Dir, "links"))
	var names []string
	for _, e := range entries {
		if !e.IsDir() || !ValidLinkName(e.Name()) || e.Name() == DefaultPlatformLink {
			continue
		}
		if _, err := hostagent.LoadCredentials(LinkPaths(e.Name())); err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return append(out, names...)
}

// --- ownership ---------------------------------------------------------------

var ownersMu sync.Mutex

type ownersFile struct {
	Servers map[string]string `json:"servers"`
}

func ownersPath() string { return filepath.Join(HostAgentPaths().Dir, "owners.json") }

func loadOwners() ownersFile {
	var o ownersFile
	if data, err := os.ReadFile(ownersPath()); err == nil {
		_ = json.Unmarshal(data, &o)
	}
	if o.Servers == nil {
		o.Servers = map[string]string{}
	}
	return o
}

func saveOwners(o ownersFile) error {
	if err := os.MkdirAll(filepath.Dir(ownersPath()), 0o700); err != nil {
		return err
	}
	return writeJSONAtomicMode(ownersPath(), o, 0o600)
}

// ServerOwner is the platform link that owns server n ("default" when none
// was recorded).
func ServerOwner(n int) string {
	ownersMu.Lock()
	defer ownersMu.Unlock()
	if l, ok := loadOwners().Servers[strconv.Itoa(n)]; ok && l != "" {
		return l
	}
	return DefaultPlatformLink
}

// SetServerOwner records which link owns server n ("" or default: forget).
func SetServerOwner(n int, link string) error {
	ownersMu.Lock()
	defer ownersMu.Unlock()
	o := loadOwners()
	if link == "" || link == DefaultPlatformLink {
		delete(o.Servers, strconv.Itoa(n))
	} else {
		o.Servers[strconv.Itoa(n)] = link
	}
	return saveOwners(o)
}

// ServersOwnedBy counts how many of `servers` belong to link.
func ServersOwnedBy(link string, servers []int) []int {
	var out []int
	for _, n := range servers {
		if ServerOwner(n) == link {
			out = append(out, n)
		}
	}
	return out
}

// ReleaseLinkServers gives a removed link's servers back to the default link,
// and forgets its update hold.
func ReleaseLinkServers(link string) error {
	holdsMu.Lock()
	holds := map[string]string{}
	if data, err := os.ReadFile(holdsPath()); err == nil {
		_ = json.Unmarshal(data, &holds)
	}
	delete(holds, link)
	_ = writeJSONAtomicMode(holdsPath(), holds, 0o600)
	holdsMu.Unlock()
	ownersMu.Lock()
	defer ownersMu.Unlock()
	o := loadOwners()
	for k, v := range o.Servers {
		if v == link {
			delete(o.Servers, k)
		}
	}
	return saveOwners(o)
}

// --- update holds per link -------------------------------------------------

var holdsMu sync.Mutex

func holdsPath() string { return filepath.Join(HostAgentPaths().Dir, "holds.json") }

// CombinedHold records link's hold mode and returns the host's: on while any
// platform says on, else auto while any says auto, else off.
func CombinedHold(link, mode string) string {
	holdsMu.Lock()
	defer holdsMu.Unlock()
	holds := map[string]string{}
	if data, err := os.ReadFile(holdsPath()); err == nil {
		_ = json.Unmarshal(data, &holds)
	}
	holds[link] = mode
	_ = writeJSONAtomicMode(holdsPath(), holds, 0o600)
	combined := ""
	for _, m := range holds {
		switch {
		case m == HoldModeOn:
			return HoldModeOn
		case m == HoldModeAuto:
			combined = HoldModeAuto
		case combined == "":
			combined = m
		}
	}
	if combined == "" {
		combined = mode
	}
	return combined
}

// --- the license each link's platform sent ------------------------------------

// linkLicense is <link dir>/license.json: what the platform sent in host.license.
type linkLicense struct {
	Key        string        `json:"key,omitempty"`
	Lease      string        `json:"lease,omitempty"`
	State      *CheckinState `json:"state,omitempty"`
	Use        string        `json:"use,omitempty"`
	ReceivedAt string        `json:"received_at,omitempty"`
}

func linkLicensePath(link string) string { return filepath.Join(LinkPaths(link).Dir, "license.json") }

func loadLinkLicense(link string) linkLicense {
	var l linkLicense
	if data, err := os.ReadFile(linkLicensePath(link)); err == nil {
		_ = json.Unmarshal(data, &l)
	}
	return l
}

func saveLinkLicense(link string, l linkLicense) error {
	if err := os.MkdirAll(LinkPaths(link).Dir, 0o700); err != nil {
		return err
	}
	return writeJSONAtomicMode(linkLicensePath(link), l, 0o600)
}

// hostOwnKey is this host's own genuine paid key (`csm license set`), or "".
func hostOwnKey() string {
	s, err := LoadLicenseSettings()
	if err != nil || s.Key == "" || s.Source == LicenseSourcePlatform || !hostOwnsLicense(s.Key) {
		return ""
	}
	return s.Key
}

// serverLicense is what server n's Ready Up gets: the host's own key when it
// has one; otherwise its owning platform's (a link's host.license, or for the
// default link `defaultKey` with the stored lease and state).
func serverLicense(n int, defaultKey string) (key, lease string, state *CheckinState) {
	c := loadCheckinFile()
	if own := hostOwnKey(); own != "" {
		return own, c.Lease, c.State
	}
	if owner := ServerOwner(n); owner != DefaultPlatformLink {
		l := loadLinkLicense(owner)
		return l.Key, l.Lease, l.State
	}
	return defaultKey, c.Lease, c.State
}

// linkStanding is where a link's platform license stands, for its creates.
func linkStanding(link string) LicenseStanding {
	if hostOwnKey() != "" || link == DefaultPlatformLink {
		return CurrentStanding()
	}
	l := loadLinkLicense(link)
	return StandingFor(l.Key, l.Lease, l.State, time.Now(), nil)
}

// receiveLinkLicense stores a platform's host.license and rewrites the Ready
// Up config of the servers it owns. The default link goes the way of the
// hold-poll hand-off (license_platform.go), so both paths agree.
func receiveLinkLicense(link string, cmd hostagent.LicenseCmd, servers []int) error {
	key := ""
	if cmd.Key != nil {
		key = *cmd.Key
	}
	if key != "" {
		if p, ok := license.Peek(key); ok && p.Lease {
			return fmt.Errorf("the platform sent a lease as its key")
		}
	}
	state := (*CheckinState)(nil)
	if cmd.State != nil {
		state = &CheckinState{Status: cmd.State.Status}
		if cmd.State.StopsOn != nil {
			state.StopsOn = *cmd.State.StopsOn
		}
		if cmd.State.ValidUntil != nil {
			state.ValidUntil = *cmd.State.ValidUntil
		}
	}
	if link == DefaultPlatformLink {
		rev := cmd.Revision
		if rev == "" {
			rev = licenseRevision(key, cmd.Lease, state)
		}
		lic := &PlatformLicense{Key: cmd.Key, Lease: cmd.Lease, Use: cmd.Use, Revision: rev}
		if state != nil {
			lic.State = &PlatformLicenseState{Status: state.Status, StopsOn: strPtr(state.StopsOn), ValidUntil: strPtr(state.ValidUntil)}
		}
		var log discardWriter
		SyncPlatformLicense(&log, lic)
		return nil
	}
	l := linkLicense{Key: key, State: state, Use: cmd.Use, ReceivedAt: time.Now().UTC().Format(time.RFC3339)}
	if cmd.Lease != nil {
		if p := verifyQuiet(key, nil, time.Now()); p != nil {
			l.Lease = leaseFor(p, cmd.Lease)
		}
	}
	if err := saveLinkLicense(link, l); err != nil {
		return err
	}
	user := licenseCS2User()
	for _, n := range servers {
		applyStoredLicenseToServer(discardWriter{}, user, n)
	}
	return nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// licenseRevision names a key + lease + state, so a re-sent unchanged
// license changes nothing.
func licenseRevision(key string, lease *string, state *CheckinState) string {
	l := ""
	if lease != nil {
		l = *lease
	}
	b, _ := json.Marshal([]any{key, l, state})
	return "host:" + shortHash(string(b))
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// LinkServerSummary is "3 (server-4, server-5, server-6)" for `csm link status`.
func LinkServerSummary(link string) string {
	n := LicenseServerCount()
	if n <= 0 {
		return "0"
	}
	nums := make([]int, n)
	for i := range nums {
		nums[i] = i + 1
	}
	owned := ServersOwnedBy(link, nums)
	if len(owned) == 0 {
		return "0"
	}
	parts := make([]string, len(owned))
	for i, s := range owned {
		parts[i] = fmt.Sprintf("server-%d", s)
	}
	return fmt.Sprintf("%d (%s)", len(owned), joinComma(parts))
}

func joinComma(p []string) string {
	out := ""
	for i, s := range p {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
