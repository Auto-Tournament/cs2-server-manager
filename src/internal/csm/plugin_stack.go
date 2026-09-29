package csm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/hostagent"
	"github.com/sivert-io/cs2-server-manager/src/internal/readyup"
)

// Which plugin stack csm installs
//
// Two stacks:
//
//   - readyup: Ready Up (github.com/Auto-Tournament/ready-up), a native CS2
//     plugin suite. No Metamod, no CounterStrikeSharp. What the Auto
//     Tournament platform 3.x talks to.
//   - legacy: Metamod:Source + CounterStrikeSharp + the Auto Tournament CS2
//     plugin (MatchZy-era, matchzy_* cvars). What platform 2.x talks to.
//
// The choice is <csm root>/plugins.json (`csm plugins stack ...`), or
// CSM_PLUGIN_STACK. Without a choice:
//
//   - a host that already has the legacy stack (CounterStrikeSharp in
//     cs2-config or on a server) stays legacy: csm never switches an existing
//     install by itself (README, "Moving to Ready Up");
//   - a fresh install gets Ready Up when Ready Up has a stable release, and
//     the legacy stack until then (with a note on how to opt into a Ready Up
//     pre-release).
//
// Ready Up settings in the same file: the channel (stable | beta), a pinned
// version (overrides the channel), the bundle (essentials | full) and the
// license answer install.sh needs for an unattended install (noncommercial |
// commercial; AT_ACCEPT_LICENSE overrides it, like on the platform). csm never
// picks the license for the operator: without an answer the install stops and
// says how to give one.

// Plugin stacks.
const (
	PluginStackReadyUp = "readyup"
	PluginStackLegacy  = "legacy"
)

// Environment overrides.
const (
	EnvPluginStack     = "CSM_PLUGIN_STACK"
	EnvReadyUpChannel  = "CSM_READYUP_CHANNEL"
	EnvReadyUpVersion  = "CSM_READYUP_VERSION"
	EnvReadyUpBundle   = "CSM_READYUP_BUNDLE"
	EnvReadyUpAPI      = "CSM_READYUP_API"
	EnvReadyUpRepo     = "CSM_READYUP_REPO"
	EnvAcceptLicense   = "AT_ACCEPT_LICENSE"
	readyUpInstallWait = 15 * time.Minute
)

// PluginSettings is <csm root>/plugins.json.
type PluginSettings struct {
	// Stack is readyup, legacy or "" (not chosen).
	Stack string `json:"stack,omitempty"`
	// StackSetBy is "operator" or "fresh-install".
	StackSetBy string `json:"stack_set_by,omitempty"`
	// ReadyUpChannel is stable or beta ("" = stable).
	ReadyUpChannel string `json:"readyup_channel,omitempty"`
	// ReadyUpVersion pins a release (vX.Y.Z or vX.Y.Z-beta.N); "" follows the channel.
	ReadyUpVersion string `json:"readyup_version,omitempty"`
	// ReadyUpBundle is essentials or full ("" = essentials).
	ReadyUpBundle string `json:"readyup_bundle,omitempty"`
	// AcceptLicense is noncommercial or commercial, passed to install.sh.
	AcceptLicense   string `json:"readyup_accept_license,omitempty"`
	AcceptLicenseAt string `json:"readyup_accept_license_at,omitempty"`
	// AcceptLicenseBy is "operator" or "platform" (taken from the platform's
	// license hand-off because the operator gave no answer).
	AcceptLicenseBy string `json:"readyup_accept_license_by,omitempty"`
	// AutoUpdate keeps Ready Up on its channel from `csm monitor` (nil = on).
	AutoUpdate *bool `json:"readyup_auto_update,omitempty"`
}

// PluginSettingKeys are the keys `csm plugins set` accepts.
var PluginSettingKeys = []string{"stack", "channel", "version", "bundle", "license", "auto"}

func pluginSettingsPath() string { return filepath.Join(ResolveRoot(), "plugins.json") }

// LoadPluginSettings reads plugins.json (missing = nothing chosen).
func LoadPluginSettings() (PluginSettings, error) {
	var s PluginSettings
	data, err := os.ReadFile(pluginSettingsPath())
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parse %s: %w", pluginSettingsPath(), err)
	}
	return s, nil
}

func savePluginSettings(s PluginSettings) error {
	path := pluginSettingsPath()
	if err := writeJSONAtomic(path, s); err != nil {
		return err
	}
	if canChown() {
		_ = ensureOwnedByUser(configuredCS2User(), path)
	}
	return nil
}

// Resolved applies the environment and the defaults (channel stable, bundle
// essentials). Stack stays "" when nothing chose one.
func (s PluginSettings) Resolved() PluginSettings {
	r := s
	r.Stack = strings.ToLower(strings.TrimSpace(getenvDefault(EnvPluginStack, s.Stack)))
	if ch, err := readyup.NormalizeChannel(getenvDefault(EnvReadyUpChannel, s.ReadyUpChannel)); err == nil {
		r.ReadyUpChannel = ch
	} else {
		r.ReadyUpChannel = readyup.ChannelStable
	}
	if v, err := readyup.NormalizeVersion(getenvDefault(EnvReadyUpVersion, s.ReadyUpVersion)); err == nil {
		r.ReadyUpVersion = v
	}
	if b, err := readyup.NormalizeBundle(getenvDefault(EnvReadyUpBundle, s.ReadyUpBundle)); err == nil {
		r.ReadyUpBundle = b
	} else {
		r.ReadyUpBundle = readyup.BundleEssentials
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv(EnvAcceptLicense))); v == "noncommercial" || v == "commercial" {
		r.AcceptLicense = v
	}
	return r
}

// LayerBundle is the bundle instance mode builds its shared Ready Up layer
// with: the operator's `csm plugins bundle` (or CSM_READYUP_BUNDLE) when one
// was chosen, else full. Instances share one layer, so it carries every
// plugin and the platform picks each server's plugins by enabling or
// disabling them (cmd plugins.set). Classic servers keep Resolved's default
// (essentials).
func (s PluginSettings) LayerBundle() string {
	v := strings.TrimSpace(getenvDefault(EnvReadyUpBundle, s.ReadyUpBundle))
	if v == "" {
		return readyup.BundleFull
	}
	if b, err := readyup.NormalizeBundle(v); err == nil {
		return b
	}
	return readyup.BundleFull
}

// LayerBundleFor is the bundle an instance layer gets when the platform asks
// for `requested` (host.update_plugins): full stays full; its default
// (essentials) becomes LayerBundle, so a platform install does not shrink
// the shared layer back to essentials.
func (s PluginSettings) LayerBundleFor(requested string) string {
	if b, err := readyup.NormalizeBundle(requested); err == nil && b == readyup.BundleFull {
		return b
	}
	return s.LayerBundle()
}

// SetPluginSetting changes one setting and saves. value "" (or "latest" for
// version, "clear" for license) resets it.
func SetPluginSetting(key, value string) (PluginSettings, error) {
	s, err := LoadPluginSettings()
	if err != nil {
		return s, err
	}
	value = strings.TrimSpace(value)
	switch key {
	case "stack":
		v := strings.ToLower(value)
		switch v {
		case PluginStackReadyUp, PluginStackLegacy:
			s.Stack, s.StackSetBy = v, "operator"
		case "", "auto":
			s.Stack, s.StackSetBy = "", ""
		default:
			return s, fmt.Errorf("stack is readyup or legacy (got %q)", value)
		}
	case "channel":
		ch, err := readyup.NormalizeChannel(value)
		if err != nil {
			return s, err
		}
		s.ReadyUpChannel = ch
	case "version":
		v, err := readyup.NormalizeVersion(value)
		if err != nil {
			return s, err
		}
		s.ReadyUpVersion = v
	case "bundle":
		b, err := readyup.NormalizeBundle(value)
		if err != nil {
			return s, err
		}
		s.ReadyUpBundle = b
	case "license":
		v := strings.ToLower(value)
		switch v {
		case "noncommercial", "commercial":
			s.AcceptLicense = v
			s.AcceptLicenseAt = time.Now().UTC().Format(time.RFC3339)
			s.AcceptLicenseBy = "operator"
		case "", "clear":
			s.AcceptLicense, s.AcceptLicenseAt, s.AcceptLicenseBy = "", "", ""
		default:
			return s, fmt.Errorf("license is noncommercial or commercial (got %q)", value)
		}
	case "auto":
		switch strings.ToLower(value) {
		case "on", "true", "yes", "":
			s.AutoUpdate = nil
		case "off", "false", "no":
			off := false
			s.AutoUpdate = &off
		default:
			return s, fmt.Errorf("auto is on or off (got %q)", value)
		}
	default:
		return s, fmt.Errorf("unknown setting %q (settings: %s)", key, strings.Join(PluginSettingKeys, ", "))
	}
	return s, savePluginSettings(s)
}

// legacyStackPresent reports whether this host already runs the legacy stack.
func legacyStackPresent(user string) bool {
	home := filepath.Join("/home", user)
	paths := []string{filepath.Join(home, "cs2-config", "game", "csgo", "addons", "counterstrikesharp")}
	if entries, err := os.ReadDir(home); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "server-") {
				paths = append(paths, filepath.Join(home, e.Name(), "game", "csgo", "addons", "counterstrikesharp"))
			}
		}
	}
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// PluginStack returns the stack to install and why. It may ask GitHub (a
// fresh install without a choice) and then saves the answer.
func PluginStack(ctx context.Context, w io.Writer, user string) (string, string) {
	s, _ := LoadPluginSettings()
	r := s.Resolved()
	switch r.Stack {
	case PluginStackReadyUp, PluginStackLegacy:
		if os.Getenv(EnvPluginStack) != "" {
			return r.Stack, EnvPluginStack
		}
		return r.Stack, "csm plugins stack"
	}
	if legacyStackPresent(user) {
		return PluginStackLegacy, "existing install (switch with: csm plugins stack readyup)"
	}
	// Fresh install: Ready Up once it has a stable release.
	rel, err := readyUpClient().Resolve(ctx, readyup.ChannelStable, "")
	if err != nil {
		fmt.Fprintf(w, "[plugins] Fresh install: Ready Up has no stable release yet, so csm installs the legacy stack.\n")
		fmt.Fprintf(w, "[plugins]   (%v)\n", err)
		fmt.Fprintf(w, "[plugins]   For a Ready Up pre-release instead: csm plugins stack readyup; csm plugins channel beta\n")
		return PluginStackLegacy, "fresh install, no stable Ready Up release"
	}
	s.Stack, s.StackSetBy = PluginStackReadyUp, "fresh-install"
	if err := savePluginSettings(s); err != nil {
		fmt.Fprintf(w, "[plugins] [WARN] could not save the plugin stack choice: %v\n", err)
	}
	return PluginStackReadyUp, "fresh install, Ready Up " + rel.TagName
}

// readyUpClient is the GitHub client (CSM_READYUP_API / _REPO for mirrors and tests).
var readyUpClient = func() *readyup.Client {
	return &readyup.Client{
		API:       strings.TrimSpace(os.Getenv(EnvReadyUpAPI)),
		Repo:      strings.TrimSpace(os.Getenv(EnvReadyUpRepo)),
		UserAgent: "csm",
	}
}

// ReadyUpLicenseError is an install without a license answer.
type ReadyUpLicenseError struct{}

func (ReadyUpLicenseError) Error() string {
	return "Ready Up needs a license choice before csm can install it unattended. Run one of:\n" +
		"  csm plugins license noncommercial   personal / noncommercial use (PolyForm Noncommercial 1.0.0)\n" +
		"  csm plugins license commercial      only with a paid license (" + LicensePricingURL + "); add the key with csm license set\n" +
		"or set " + EnvAcceptLicense + "=noncommercial|commercial. csm update-plugins in a terminal also asks."
}

// ReadyUpLicenseSummary is the text shown before asking (install.sh's words, shortened).
const ReadyUpLicenseSummary = `Ready Up license
Ready Up is free for noncommercial use under the PolyForm Noncommercial License 1.0.0:
your own servers, friends, a community or club, a school, a charity, or a free event.
Commercial use (a business, a profit-making event, a paid server operator, selling it or a
service built on it) needs a separate paid license: ` + LicensePricingURL + `
It comes with no warranty. Full text: https://polyformproject.org/licenses/noncommercial/1.0.0/
`

// PromptReadyUpLicense asks once (1 = noncommercial, 2 = commercial, then
// "I AGREE") and saves the answer. It returns the answer, or "" when the
// operator did not agree.
func PromptReadyUpLicense(in io.Reader, out io.Writer) (string, error) {
	rd := bufio.NewReader(in)
	fmt.Fprint(out, ReadyUpLicenseSummary)
	fmt.Fprintln(out, "How will you use Ready Up?")
	fmt.Fprintln(out, "  1) personal / noncommercial (free)")
	fmt.Fprintln(out, "  2) commercial (needs a paid license; add its key with csm license set)")
	var use string
	for use == "" {
		fmt.Fprint(out, "Choose 1 or 2: ")
		line, err := rd.ReadString('\n')
		switch strings.TrimSpace(line) {
		case "1":
			use = "noncommercial"
		case "2":
			use = "commercial"
		case "":
			return "", nil
		default:
			if err != nil {
				return "", nil
			}
			fmt.Fprintln(out, "  please enter 1 or 2")
		}
	}
	fmt.Fprint(out, "Type I AGREE to accept these terms (anything else stops): ")
	line, _ := rd.ReadString('\n')
	if strings.ToUpper(strings.Join(strings.Fields(line), " ")) != "I AGREE" {
		return "", nil
	}
	if _, err := SetPluginSetting("license", use); err != nil {
		return use, err
	}
	return use, nil
}

// runReadyUpInstaller runs one install.sh command line as the CS2 user.
var runReadyUpInstaller = func(ctx context.Context, user, cmdline string, w io.Writer) error {
	cctx, cancel := context.WithTimeout(ctx, readyUpInstallWait)
	defer cancel()
	cmd := userShellCommand(user, cmdline)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-cctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return fmt.Errorf("install.sh did not finish in %s", readyUpInstallWait)
	}
}

// readyUpTarget is one server to install on.
type readyUpTarget struct {
	Num int
	Dir string // /home/<user>/server-N
}

// ReadyUpPlanFor resolves and downloads the configured Ready Up release. The
// caller removes bundle.Dir. It fails before anything is downloaded when
// there is no license answer.
func ReadyUpPlanFor(ctx context.Context, w io.Writer, s PluginSettings) (*readyup.Bundle, error) {
	r := s.Resolved()
	if r.AcceptLicense == "" {
		return nil, ReadyUpLicenseError{}
	}
	what := "channel " + r.ReadyUpChannel
	if r.ReadyUpVersion != "" {
		what = "pinned " + r.ReadyUpVersion
	}
	fmt.Fprintf(w, "[Ready Up] Resolving the release (%s, bundle %s)...\n", what, r.ReadyUpBundle)
	c := readyUpClient()
	rel, err := c.Resolve(ctx, r.ReadyUpChannel, r.ReadyUpVersion)
	if err != nil {
		return nil, err
	}
	kind := "stable"
	if rel.Prerelease {
		kind = "pre-release"
	}
	fmt.Fprintf(w, "[Ready Up] Target: Ready Up %s (%s)\n", rel.TagName, kind)
	b, err := c.Download(ctx, rel, r.ReadyUpBundle, "")
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(w, "[Ready Up] Downloaded %s, SHA256SUMS ok\n", filepath.Base(b.Zip))
	return b, nil
}

// installReadyUpOn runs install.sh --zip on every target. It stops at the
// first failure and says which servers were done.
func installReadyUpOn(ctx context.Context, w io.Writer, user string, b *readyup.Bundle, accept string, targets []readyUpTarget) error {
	var done []string
	for _, t := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprintf(w, "[Ready Up] server-%d: installing %s %s...\n", t.Num, b.Name, b.Tag)
		args := readyup.InstallArgs{Installer: b.Installer, Bundle: b.Name, Dir: t.Dir, Zip: b.Zip, AcceptLicense: accept}
		if err := runReadyUpInstaller(ctx, user, args.Cmdline(), w); err != nil {
			msg := fmt.Sprintf("Ready Up install on server-%d failed: %v", t.Num, err)
			if len(done) > 0 {
				msg += " (already done: " + strings.Join(done, ", ") + ")"
			}
			return fmt.Errorf("%s", msg)
		}
		applyStoredLicenseToServer(w, user, t.Num)
		done = append(done, fmt.Sprintf("server-%d", t.Num))
	}
	return nil
}

// UpdateReadyUpOnServers installs or updates Ready Up on the servers (nil =
// all): stops them, runs install.sh on each, and starts them again. It is what
// `csm update-plugins` and the TUI run on the readyup stack.
func UpdateReadyUpOnServers(ctx context.Context, servers []int) (string, error) {
	var buf strings.Builder
	w := io.Writer(&buf)
	if logPath := strings.TrimSpace(os.Getenv("CSM_PLUGINS_LOG")); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			defer f.Close()
			w = io.MultiWriter(&buf, f)
		}
	}
	err := updateReadyUpOnServers(ctx, w, servers)
	out := buf.String()
	AppendLog("update-readyup.log", out)
	return out, err
}

func updateReadyUpOnServers(ctx context.Context, w io.Writer, servers []int) error {
	return withPluginUpdateLock(func() error {
		mgr, err := NewTmuxManager()
		if err != nil {
			return err
		}
		if mgr.NumServers <= 0 {
			return fmt.Errorf("no CS2 servers found for user %s; run the install wizard first", mgr.CS2User)
		}
		if servers == nil {
			for i := 1; i <= mgr.NumServers; i++ {
				servers = append(servers, i)
			}
		}
		var targets []readyUpTarget
		for _, n := range servers {
			dir := mgr.serverDir(n)
			if fi, err := os.Stat(filepath.Join(dir, "game", "csgo")); err != nil || !fi.IsDir() {
				fmt.Fprintf(w, "[Ready Up] server-%d not found at %s, skipping\n", n, dir)
				continue
			}
			targets = append(targets, readyUpTarget{Num: n, Dir: dir})
		}
		fmt.Fprintln(w, "=== Update Ready Up ===")
		s, err := LoadPluginSettings()
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return fmt.Errorf("none of the servers exist")
		}
		if err := CheckDiskSpaceForPluginUpdate(targets[0].Dir); err != nil {
			return fmt.Errorf("plugin update pre-check failed: %w", err)
		}
		b, err := ReadyUpPlanFor(ctx, w, s)
		if err != nil {
			return err
		}
		defer os.RemoveAll(b.Dir)

		var wasRunning []int
		for _, t := range targets {
			if mgr.IsRunning(t.Num) {
				wasRunning = append(wasRunning, t.Num)
				if err := mgr.Stop(t.Num); err != nil {
					fmt.Fprintf(w, "[Ready Up] [WARN] could not stop server-%d: %v\n", t.Num, err)
				}
			}
		}
		ierr := installReadyUpOn(ctx, w, mgr.CS2User, b, s.Resolved().AcceptLicense, targets)
		for _, n := range wasRunning {
			if err := mgr.Start(n); err != nil {
				fmt.Fprintf(w, "[Ready Up] [WARN] server-%d did not start again: %v\n", n, err)
			}
		}
		if ierr != nil {
			return ierr
		}
		fmt.Fprintf(w, "[✓] Ready Up %s (%s) is on %d server(s). Run `ru selftest` in a server console to check.\n", b.Tag, b.Name, len(targets))
		return nil
	})
}

// installReadyUpOnNewServer installs Ready Up on a server csm just created,
// before its first start, when this host is on the readyup stack. Failures
// are returned for the caller to log; the server itself is fine.
func installReadyUpOnNewServer(ctx context.Context, w io.Writer, user string, serverNum int) error {
	s, err := LoadPluginSettings()
	if err != nil {
		return err
	}
	if s.Resolved().Stack != PluginStackReadyUp {
		return nil
	}
	b, err := ReadyUpPlanFor(ctx, w, s)
	if err != nil {
		return err
	}
	defer os.RemoveAll(b.Dir)
	dir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum))
	return installReadyUpOn(ctx, w, user, b, s.Resolved().AcceptLicense, []readyUpTarget{{Num: serverNum, Dir: dir}})
}

// ensureReadyUpGameinfo puts Ready Up's line back into a server's
// gameinfo.gi. A CS2 update (master -> server sync) replaces gameinfo.gi with
// Valve's, which drops "Game csgo/readyup"; Ready Up then no longer loads. It
// is a no-op on servers without Ready Up.
func ensureReadyUpGameinfo(w io.Writer, user string, serverNum int) {
	csgo := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo")
	patcher := filepath.Join(csgo, "readyup", "tools", "patch_gameinfo.py")
	if _, err := os.Stat(patcher); err != nil {
		return
	}
	for _, gi := range []string{"gameinfo.gi", "gameinfo_branchspecific.gi"} {
		path := filepath.Join(csgo, gi)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		cmdline := "python3 " + shellQuote(patcher) + " " + shellQuote(path) + " --game csgo/readyup"
		out, err := userShellCommand(user, cmdline).CombinedOutput()
		text := strings.TrimSpace(string(out))
		switch {
		case err == nil && strings.HasPrefix(text, "Patched"):
			fmt.Fprintf(w, "  [✓] server-%d: Ready Up line restored in %s\n", serverNum, gi)
		case err == nil:
		case gi == "gameinfo_branchspecific.gi" && strings.HasPrefix(text, "Skipped"):
		default:
			fmt.Fprintf(w, "  [!] server-%d: could not add Ready Up to %s: %v %s\n", serverNum, gi, err, text)
		}
	}
}

// ReadyUpInstalledOn returns what install.sh recorded on a server.
func ReadyUpInstalledOn(user string, serverNum int) string {
	in, err := readyup.ReadInstalled(filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum)))
	if err != nil {
		return "unreadable installed.json: " + err.Error()
	}
	return in.Summary()
}

// PluginsReport is `csm plugins status`.
func PluginsReport(ctx context.Context, lookup bool) string {
	var b strings.Builder
	s, err := LoadPluginSettings()
	if err != nil {
		fmt.Fprintf(&b, "Could not read %s: %v\n", pluginSettingsPath(), err)
	}
	r := s.Resolved()
	user := configuredCS2User()
	stack := r.Stack
	switch {
	case stack == "" && legacyStackPresent(user):
		stack = "legacy (not chosen; this host has the legacy stack, so it stays)"
	case stack == "":
		stack = "not chosen (a fresh install gets Ready Up once it has a stable release)"
	case r.Stack != s.Stack:
		stack += " (" + EnvPluginStack + ")"
	}
	fmt.Fprintf(&b, "Plugin stack    : %s\n", stack)
	fmt.Fprintf(&b, "Ready Up channel: %s\n", r.ReadyUpChannel)
	if r.ReadyUpVersion != "" {
		fmt.Fprintf(&b, "Ready Up version: %s (pinned; overrides the channel)\n", r.ReadyUpVersion)
	} else {
		fmt.Fprintf(&b, "Ready Up version: newest on the channel\n")
	}
	if InstanceBackendOn() {
		fmt.Fprintf(&b, "Ready Up bundle : %s (instance layer; classic servers: %s)\n", s.LayerBundle(), r.ReadyUpBundle)
	} else {
		fmt.Fprintf(&b, "Ready Up bundle : %s\n", r.ReadyUpBundle)
	}
	switch {
	case r.AcceptLicense == "":
		fmt.Fprintf(&b, "Ready Up license: not answered (csm plugins license noncommercial|commercial)\n")
	case r.AcceptLicense != s.AcceptLicense:
		fmt.Fprintf(&b, "Ready Up license: %s (%s)\n", r.AcceptLicense, EnvAcceptLicense)
	default:
		fmt.Fprintf(&b, "Ready Up license: %s (answered %s)\n", r.AcceptLicense, s.AcceptLicenseAt)
	}
	if lookup {
		rel, err := readyUpClient().Resolve(ctx, r.ReadyUpChannel, r.ReadyUpVersion)
		if err != nil {
			fmt.Fprintf(&b, "Would install   : nothing: %v\n", err)
		} else {
			kind := "stable"
			if rel.Prerelease {
				kind = "pre-release"
			}
			fmt.Fprintf(&b, "Would install   : Ready Up %s (%s)\n", rel.TagName, kind)
		}
	}
	if mgr, err := NewTmuxManager(); err == nil && mgr.NumServers > 0 {
		fmt.Fprintln(&b, "Installed:")
		for i := 1; i <= mgr.NumServers; i++ {
			fmt.Fprintf(&b, "  server-%d: Ready Up %s\n", i, ReadyUpInstalledOn(mgr.CS2User, i))
		}
	}
	return b.String()
}

// HostReadyUpDefaults feeds the host agent (host.update_plugins) the
// operator's Ready Up settings.
func HostReadyUpDefaults() hostagent.ReadyUpDefaults {
	s, _ := LoadPluginSettings()
	r := s.Resolved()
	return hostagent.ReadyUpDefaults{
		Channel:       r.ReadyUpChannel,
		Version:       r.ReadyUpVersion,
		AcceptLicense: r.AcceptLicense,
		API:           strings.TrimSpace(os.Getenv(EnvReadyUpAPI)),
	}
}

// --- legacy stack and the platform version ------------------------------------

// platformVersionPath is the platform's public version endpoint.
const platformVersionPath = "/api/settings/version"

// platformBaseURL is the Auto Tournament platform this host talks to: the
// update-hold settings (csm updates platform), else the host agent link.
func platformBaseURL() string {
	if s, err := LoadAutoUpdateSettings(); err == nil {
		if u := s.Platform.Resolved().BaseURL; u != "" {
			return strings.TrimRight(u, "/")
		}
	}
	if c, err := hostagent.LoadCredentials(HostAgentPaths()); err == nil && c != nil && c.PlatformURL != "" {
		return strings.TrimRight(c.PlatformURL, "/")
	}
	return ""
}

// fetchPlatformVersion asks the platform for its version ("" = no answer).
func fetchPlatformVersion(ctx context.Context, base string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, base+platformVersionPath, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "csm")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %d", platformVersionPath, resp.StatusCode)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(body.Version), "v"), nil
}

func majorVersion(v string) int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '.'); i > 0 {
		v = v[:i]
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return -1
	}
	return n
}

// checkLegacyPlatform refuses the legacy stack for a 3.x platform, which
// talks to Ready Up only. An unknown platform (none configured, unreachable)
// is allowed with a note.
func checkLegacyPlatform(ctx context.Context, w io.Writer, base string) error {
	if base == "" {
		return nil
	}
	v, err := fetchPlatformVersion(ctx, base)
	if err != nil || v == "" {
		fmt.Fprintf(w, "[MatchZy] Could not read the platform version from %s (%v); installing the legacy plugin anyway.\n", base, err)
		return nil
	}
	if majorVersion(v) >= 3 {
		return fmt.Errorf("the Auto Tournament platform at %s is %s, which only talks to Ready Up; "+
			"the legacy MatchZy-era plugin stack does not work with it. Switch this host to Ready Up: "+
			"csm plugins stack readyup (README: \"Moving to Ready Up\")", base, v)
	}
	fmt.Fprintf(w, "[MatchZy] Platform %s is %s: the legacy plugin stack fits.\n", base, v)
	return nil
}

// AutoUpdateOn reports whether `csm monitor` keeps Ready Up up to date.
func (s PluginSettings) AutoUpdateOn() bool { return s.AutoUpdate == nil || *s.AutoUpdate }

// adoptReadyUpStack records the readyup stack when nothing chose a stack yet
// and Ready Up was just installed from elsewhere (the platform's "Update
// Ready Up"), so new servers and `csm monitor` follow it from then on.
func adoptReadyUpStack(w io.Writer, by string) {
	s, err := LoadPluginSettings()
	if err != nil || s.Stack != "" {
		return
	}
	s.Stack, s.StackSetBy = PluginStackReadyUp, by
	if err := savePluginSettings(s); err == nil {
		fmt.Fprintf(w, "csm now keeps this host on Ready Up (plugin stack set by the %s; csm plugins status).\n", by)
	}
}

// ConfiguredCS2User is the CS2 user csm manages (CS2_USER, else detected).
func ConfiguredCS2User() string { return configuredCS2User() }
