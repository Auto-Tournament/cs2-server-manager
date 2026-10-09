package csm

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/license"
)

// Auto Tournament license key
//
// The operator pastes one key into csm (`csm license set`). csm checks it
// offline, shows its status, and hands the same key to Ready Up on every
// server it manages, so nobody pastes it per server.
//
// Nothing here blocks, disables or degrades anything: every problem is a
// warning, and a missing key is one quiet line. Free non-commercial use needs
// no key.

const (
	// LicensePricingURL is where a commercial license is bought.
	LicensePricingURL = "https://autotournament.gg/pricing"
	// LicenseVerifyURL + license id is the public check page.
	LicenseVerifyURL = "https://autotournament.gg/verify/"

	// ReadyUpLicenseCvar is the Ready Up setting that receives the key.
	ReadyUpLicenseCvar = "readyup_license_key"
	// readyUpLicenseCfg is the per-server file (game/csgo/cfg/<name>) that
	// sets the cvar. It is owner-only; server.cfg execs it.
	readyUpLicenseCfg = "readyup_license.cfg"
	// readyUpLicenseExec is the line csm adds to server.cfg.
	readyUpLicenseExec = "exec " + readyUpLicenseCfg + " // csm: Auto Tournament license key for Ready Up (csm license set / clear)"
)

// LicenseSettings is what csm stores: <csm root>/license.json, mode 600.
type LicenseSettings struct {
	Key   string `json:"key,omitempty"`
	SetAt string `json:"set_at,omitempty"`
	// Source is LicenseSourcePlatform when the key came from the Auto
	// Tournament platform (license_platform.go), and empty when an operator
	// ran `csm license set`.
	Source string `json:"source,omitempty"`
	// PlatformRevision is the platform's license revision csm last applied to
	// every server. Empty until then, and after a partial failure, so the
	// next poll tries again.
	PlatformRevision string `json:"platform_revision,omitempty"`
}

func licenseSettingsPath() string {
	return filepath.Join(ResolveRoot(), "license.json")
}

// LoadLicenseSettings reads the stored key. A missing file means no key.
func LoadLicenseSettings() (LicenseSettings, error) {
	var s LicenseSettings
	data, err := os.ReadFile(licenseSettingsPath())
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parse %s: %w", licenseSettingsPath(), err)
	}
	s.Key = strings.TrimSpace(s.Key)
	return s, nil
}

func saveLicenseSettings(s LicenseSettings, cs2User string) error {
	path := licenseSettingsPath()
	if err := writeJSONAtomicMode(path, s, 0o600); err != nil {
		return err
	}
	// Written as root: hand it to the CS2 user, who runs csm day to day.
	if canChown() {
		_ = ensureOwnedByUser(cs2User, path)
	}
	return nil
}

// LicenseSummary is the checked key, ready to print.
type LicenseSummary struct {
	Set         bool
	Result      license.Result
	ServerCount int
	// ID is the license id, also for a key that did not verify (when readable).
	ID string
	// FromPlatform: the key was handed over by the Auto Tournament platform.
	FromPlatform bool
}

// CheckLicense verifies a key for this build and this host's server count
// (serverCount < 0: not known, not checked).
func CheckLicense(key string, serverCount int, now time.Time) LicenseSummary {
	key = strings.TrimSpace(key)
	if key == "" {
		return LicenseSummary{ServerCount: serverCount}
	}
	r := license.Verify(key, license.Options{LineDate: license.LineDate(), ServerCount: serverCount, Now: now})
	s := LicenseSummary{Set: true, Result: r, ServerCount: serverCount}
	if r.License != nil {
		s.ID = r.License.ID
	} else if p, ok := license.Peek(key); ok {
		s.ID = p.ID
	}
	return s
}

// LicenseServerCount is the number of game servers csm has set up: every
// server-N directory, spares and test servers included (they can't be told
// apart). -1 when it can't be read.
func LicenseServerCount() int {
	n := -1
	if mgr, err := NewTmuxManager(); err == nil {
		n = mgr.NumServers
	}
	// Instance mode: every serving instance is a game server too.
	if m, err := NewInstanceManager(); err == nil {
		if k := len(m.Serving()); k > n {
			n = k
		}
	}
	return n
}

// CurrentLicense checks the stored key.
func CurrentLicense() (LicenseSummary, error) {
	s, err := LoadLicenseSettings()
	if err != nil {
		return LicenseSummary{ServerCount: -1}, err
	}
	sum := CheckLicense(s.Key, LicenseServerCount(), time.Now())
	sum.FromPlatform = sum.Set && s.Source == LicenseSourcePlatform
	return sum, nil
}

func productName(p string) string {
	switch p {
	case "servers":
		return "Servers"
	case "platform":
		return "Platform"
	}
	return p
}

func periodText(p *license.Payload) string {
	switch p.Kind {
	case "event":
		if p.ValidFrom == p.ValidTo {
			return "event " + p.ValidFrom
		}
		return fmt.Sprintf("event %s to %s", p.ValidFrom, p.ValidTo)
	case "month":
		return "monthly, paid until " + p.UpdatesUntil
	case "year":
		return "updates until " + p.UpdatesUntil
	case "founder":
		return "founder"
	}
	return p.Kind
}

// Line is the one-line status, e.g.
// "Licensed to NTLAN · Servers S (6 servers) · event 2026-10-16 · valid".
func (s LicenseSummary) Line() string {
	if !s.Set {
		return "No commercial license key set (free for non-commercial use: " + LicensePricingURL + ")"
	}
	if !s.Result.Valid {
		msg := "the key could not be read"
		if len(s.Result.Warnings) > 0 {
			msg = s.Result.Warnings[0].Message
		}
		return "License key not valid: " + msg
	}
	p := s.Result.License
	who := p.Licensee
	if strings.TrimSpace(who) == "" {
		who = "license " + p.ID
	}
	state := "valid"
	if n := len(s.Result.Warnings); n == 1 {
		state = "valid, 1 warning"
	} else if n > 1 {
		state = fmt.Sprintf("valid, %d warnings", n)
	}
	return fmt.Sprintf("Licensed to %s · %s %s (%d servers) · %s · %s",
		who, productName(p.Product), p.Pack, p.MaxServers, periodText(p), state)
}

// VerifyLink is the public check page for this license, or "".
func (s LicenseSummary) VerifyLink() string {
	if s.ID == "" {
		return ""
	}
	return LicenseVerifyURL + s.ID
}

// Report is the full `csm license status` text.
func (s LicenseSummary) Report() string {
	var b strings.Builder
	b.WriteString(s.Line() + "\n")
	if !s.Set {
		return b.String()
	}
	if s.Result.Valid {
		for _, w := range s.Result.Warnings {
			b.WriteString("  warning: " + w.Message + "\n")
		}
	}
	if s.ID != "" {
		b.WriteString("License id: " + s.ID + "\n")
	}
	if s.FromPlatform {
		b.WriteString("Source:     the Auto Tournament platform (Settings → License)\n")
	}
	if link := s.VerifyLink(); link != "" {
		b.WriteString("Check it:   " + link + "\n")
	}
	st := CurrentStanding()
	if st.Paid {
		b.WriteString(fmt.Sprintf("Servers:    %d on this host", max(s.ServerCount, 0)))
		if st.ServersElsewhere > 0 {
			b.WriteString(fmt.Sprintf(", %d on other installs with this key", st.ServersElsewhere))
		}
		b.WriteString(fmt.Sprintf("; the license covers %d\n", st.MaxServers))
		if line := st.StandingLine(); line != "" {
			b.WriteString(line + "\n")
		}
	}
	if !license.BakedLineDate() {
		b.WriteString("(dev build: version line date " + license.LineDate() + ", the build date)\n")
	}
	return b.String()
}

// SetLicenseKey stores the key and hands it to every server's Ready Up. The
// key only has to look like a key (so it is safe in a config file); a key
// that fails verification is stored anyway and reported, because a newer
// Ready Up may know a signing key this csm does not.
func SetLicenseKey(w io.Writer, key string) (LicenseSummary, error) {
	key = strings.TrimSpace(key)
	if !license.LooksLikeKey(key) {
		return LicenseSummary{}, fmt.Errorf("that is not an Auto Tournament license key (it starts with %s.)", license.TokenPrefix)
	}
	if p, ok := license.Peek(key); ok && p.Lease {
		return LicenseSummary{}, fmt.Errorf("that is a lease, not a license key: copy the key from the console")
	}
	user := licenseCS2User()
	if err := saveLicenseSettings(LicenseSettings{Key: key, SetAt: time.Now().UTC().Format(time.RFC3339)}, user); err != nil {
		return LicenseSummary{}, fmt.Errorf("could not store the key in %s: %w", licenseSettingsPath(), err)
	}
	applyLicenseToAllServers(w, key)
	return CheckLicense(key, LicenseServerCount(), time.Now()), nil
}

// ClearLicenseKey removes the stored key and takes it out of every server's
// config.
func ClearLicenseKey(w io.Writer) error {
	path := licenseSettingsPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not remove %s: %w", path, err)
	}
	applyLicenseToAllServers(w, "")
	return nil
}

func licenseCS2User() string {
	if mgr, err := NewTmuxManager(); err == nil && mgr.CS2User != "" {
		return mgr.CS2User
	}
	return DefaultCS2User
}

// applyLicenseToAllServers writes (or removes) the key on every server-N.
// Failures are warnings: the stored key stays, and the next config
// regeneration (update-config, bootstrap, reinstall) tries again. It returns
// how many servers were updated and how many failed.
func applyLicenseToAllServers(w io.Writer, key string) (done, failed int) {
	mgr, err := NewTmuxManager()
	if err != nil || mgr.NumServers <= 0 {
		return 0, 0
	}
	for i := 1; i <= mgr.NumServers; i++ {
		cfgDir := filepath.Join("/home", mgr.CS2User, fmt.Sprintf("server-%d", i), "game", "csgo", "cfg")
		if _, err := os.Stat(cfgDir); err != nil {
			continue
		}
		k, lease, state := serverLicense(i, key)
		if err := writeServerLicenseWith(cfgDir, k, lease, state, mgr.CS2User); err != nil {
			fmt.Fprintf(w, "  warning: server-%d: could not update its Ready Up license key: %v\n", i, err)
			failed++
			continue
		}
		done++
	}
	if key == "" {
		fmt.Fprintf(w, "Removed the key from %d server(s)' config.\n", done)
	} else {
		fmt.Fprintf(w, "Handed the key to Ready Up on %d server(s) (%s, read at the next map load or restart).\n", done, ReadyUpLicenseCvar)
	}
	return done, failed
}

// applyStoredLicenseToServer is called wherever csm (re)writes a server's
// server.cfg, so a regenerated config keeps the key. Best-effort.
func applyStoredLicenseToServer(w io.Writer, user string, serverNum int) {
	s, _ := LoadLicenseSettings()
	key, lease, state := serverLicense(serverNum, s.Key)
	if key == "" || !license.LooksLikeKey(key) {
		return
	}
	cfgDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "cfg")
	if err := writeServerLicenseWith(cfgDir, key, lease, state, user); err != nil {
		fmt.Fprintf(w, "  [i] Could not hand the license key to Ready Up on server-%d: %v\n", serverNum, err)
	}
}

// The lease and the license state go to Ready Up next to the key, so it stops
// loading matches together with csm and the platform (license_enforce.go).
const (
	readyUpLeaseCvar = "readyup_license_lease"
	readyUpStateCvar = "readyup_license_state"
)

// readyUpLicenseCfgContent is the per-server cfg that sets the cvars: the
// key, and the lease and license state csm last got for it.
func readyUpLicenseCfgContent(key string) string {
	c := loadCheckinFile()
	return readyUpLicenseCfgContentWith(key, c.Lease, c.State)
}

// readyUpLicenseCfgContentWith is the cfg for a key with its own lease and state.
func readyUpLicenseCfgContentWith(key, lease string, state *CheckinState) string {
	out := "// Written by csm: the Auto Tournament license key for Ready Up.\n" +
		"// Change it with `csm license set` / `csm license clear`; edits here are overwritten.\n" +
		fmt.Sprintf("%s \"%s\"\n", ReadyUpLicenseCvar, key)
	if lease != "" && license.LooksLikeKey(lease) {
		out += fmt.Sprintf("%s \"%s\"\n", readyUpLeaseCvar, lease)
	}
	if state != nil && safeCfgWord(state.Status) && (state.StopsOn == "" || safeCfgWord(state.StopsOn)) {
		out += fmt.Sprintf("%s \"%s\"\n", readyUpStateCvar, strings.TrimSpace(state.Status+" "+state.StopsOn))
	}
	return out
}

// safeCfgWord: letters, digits, '-' and '_' only (safe inside a quoted cfg value).
func safeCfgWord(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// isLicenseExecLine matches the exec line csm adds (with or without its comment).
func isLicenseExecLine(line string) bool {
	f := strings.Fields(strings.TrimSpace(line))
	return len(f) >= 2 && f[0] == "exec" &&
		(f[1] == readyUpLicenseCfg || f[1] == strings.TrimSuffix(readyUpLicenseCfg, ".cfg") ||
			f[1] == `"`+readyUpLicenseCfg+`"`)
}

// withLicenseExec returns server.cfg with exactly one exec line when on, and
// none when off.
func withLicenseExec(cfg string, on bool) string {
	lines := strings.Split(cfg, "\n")
	found := 0
	for _, l := range lines {
		if isLicenseExecLine(l) {
			found++
		}
	}
	if (on && found == 1) || (!on && found == 0) {
		return cfg
	}
	out := make([]string, 0, len(lines)+2)
	for _, l := range lines {
		if isLicenseExecLine(l) {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if on {
		out = append(out, "", readyUpLicenseExec)
	}
	return strings.Join(out, "\n") + "\n"
}

// writeServerLicense puts the key into one server's cfg dir (key == "":
// takes it out). It writes readyup_license.cfg (mode 600) and makes
// server.cfg exec it. A server without a server.cfg gets the file only; the
// exec line is added when csm writes its server.cfg.
func writeServerLicense(cfgDir, key, user string) error {
	c := loadCheckinFile()
	return writeServerLicenseWith(cfgDir, key, c.Lease, c.State, user)
}

// writeServerLicenseWith is writeServerLicense with the key's own lease and state.
func writeServerLicenseWith(cfgDir, key, lease string, state *CheckinState, user string) error {
	cfgFile := filepath.Join(cfgDir, readyUpLicenseCfg)
	serverCfg := filepath.Join(cfgDir, "server.cfg")

	if key == "" {
		if err := os.Remove(cfgFile); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		if !license.LooksLikeKey(key) {
			return fmt.Errorf("not a license key")
		}
		tmp := cfgFile + ".tmp"
		if err := os.WriteFile(tmp, []byte(readyUpLicenseCfgContentWith(key, lease, state)), 0o600); err != nil {
			return err
		}
		if err := os.Chmod(tmp, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, cfgFile); err != nil {
			return err
		}
		if canChown() {
			_ = ensureOwnedByUser(user, cfgFile)
		}
	}

	data, err := os.ReadFile(serverCfg)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	updated := withLicenseExec(string(data), key != "")
	if updated == string(data) {
		return nil
	}
	fi, err := os.Stat(serverCfg)
	if err != nil {
		return err
	}
	return os.WriteFile(serverCfg, []byte(updated), fi.Mode().Perm())
}
