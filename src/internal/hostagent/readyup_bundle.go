package hostagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/readyup"
)

// Where host.update_plugins gets Ready Up from
//
// Ready Up installs with its own install.sh. host.update_plugins {readyup:
// {version, bundle}} maps bundle "default" to essentials and "skins" to full.
//
//   - With `readyup_bundle` set in fleet/agent.json (a local zip, or an https
//     URL; {version} and {bundle} in it are replaced), csm installs that zip.
//   - Without it, csm resolves the GitHub release the same way `csm
//     update-plugins` does (package readyup): version "latest" means the
//     host's pinned version when the operator set one (csm plugins version),
//     else the newest release on the host's channel (csm plugins channel
//     stable|beta). csm downloads that bundle once, checks it against the
//     release's SHA256SUMS and runs the install.sh inside it with --zip on
//     every target. No such release gives host.result failed / no_release:
//     csm never reports an install that did not happen.
//
// `readyup_installer` (a local path or an https URL) replaces the install.sh
// from the bundle. The license answer is readyup_accept_license, else the
// host's `csm plugins license` answer (or AT_ACCEPT_LICENSE).

// Defaults for the Ready Up source.
const (
	DefaultReadyUpRepo      = "Auto-Tournament/ready-up"
	DefaultReadyUpInstaller = "https://raw.githubusercontent.com/Auto-Tournament/ready-up/master/install.sh"
	maxDownloadBytes        = 512 << 20
)

// AgentConfig is fleet/agent.json: settings that are not secrets.
type AgentConfig struct {
	// ReadyUpBundle is a local zip or an https URL of a Ready Up bundle zip.
	ReadyUpBundle string `json:"readyup_bundle,omitempty"`
	// ReadyUpInstaller is a local path or https URL of install.sh.
	ReadyUpInstaller string `json:"readyup_installer,omitempty"`
	// ReadyUpAcceptLicense (noncommercial | commercial) is passed to
	// install.sh as --accept-license. Unattended installs need a saved
	// choice; csm never picks one on the operator's behalf.
	ReadyUpAcceptLicense string `json:"readyup_accept_license,omitempty"`
	// ReadyUpRepo is owner/name for the releases lookup.
	ReadyUpRepo string `json:"readyup_repo,omitempty"`
}

// ConfigKeys are the keys `csm agent config` accepts.
var ConfigKeys = []string{"readyup_bundle", "readyup_installer", "readyup_accept_license", "readyup_repo"}

// LoadConfig reads fleet/agent.json (missing = defaults).
func LoadConfig(p Paths) (AgentConfig, error) {
	var c AgentConfig
	data, err := os.ReadFile(p.Config())
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", p.Config(), err)
	}
	return c, nil
}

// SetConfig sets one key ("" clears it) and saves.
func SetConfig(p Paths, key, value string) (AgentConfig, error) {
	c, err := LoadConfig(p)
	if err != nil {
		return c, err
	}
	value = strings.TrimSpace(value)
	switch key {
	case "readyup_bundle":
		if value != "" {
			if err := checkSource(value); err != nil {
				return c, err
			}
		}
		c.ReadyUpBundle = value
	case "readyup_installer":
		if value != "" {
			if err := checkSource(value); err != nil {
				return c, err
			}
		}
		c.ReadyUpInstaller = value
	case "readyup_accept_license":
		if value != "" && value != "noncommercial" && value != "commercial" {
			return c, errors.New("readyup_accept_license is noncommercial or commercial")
		}
		c.ReadyUpAcceptLicense = value
	case "readyup_repo":
		if value != "" && strings.Count(value, "/") != 1 {
			return c, errors.New("readyup_repo is owner/name")
		}
		c.ReadyUpRepo = value
	default:
		return c, fmt.Errorf("unknown key %q (keys: %s)", key, strings.Join(ConfigKeys, ", "))
	}
	return c, writeFileAtomic(p.Config(), mustJSONIndent(c), 0o644)
}

// checkSource accepts an absolute local path or an https URL.
func checkSource(s string) error {
	if strings.HasPrefix(s, "https://") {
		_, err := url.Parse(s)
		return err
	}
	if strings.Contains(s, "://") {
		return errors.New("only https:// URLs or local paths")
	}
	if !filepath.IsAbs(s) && !strings.HasPrefix(s, "/") {
		return errors.New("use an absolute path")
	}
	return nil
}

// PlanError is a Ready Up source that cannot be used, with its result code.
type PlanError struct {
	Code string
	Msg  string
}

func (e *PlanError) Error() string { return e.Msg }

// ReadyUpDefaults are the operator's Ready Up settings on this host (csm
// plugins ...): the channel, a pinned version, the license answer.
type ReadyUpDefaults struct {
	Channel       string
	Version       string
	AcceptLicense string
	// API overrides the GitHub API base URL (mirrors, tests).
	API string
}

// Fetcher downloads Ready Up sources. HTTPClient and GitHubAPI are
// overridable for tests; Defaults supplies the host's Ready Up settings.
type Fetcher struct {
	HTTPClient *http.Client
	GitHubAPI  string
	TempDir    string
	Defaults   func() ReadyUpDefaults
}

func (f *Fetcher) client() *http.Client {
	if f.HTTPClient != nil {
		return f.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (f *Fetcher) defaults() ReadyUpDefaults {
	if f.Defaults == nil {
		return ReadyUpDefaults{}
	}
	return f.Defaults()
}

// componentFor maps the protocol's bundle to install.sh's name.
func componentFor(bundle string) string {
	b, err := readyup.NormalizeBundle(bundle)
	if err != nil {
		return readyup.BundleEssentials
	}
	return b
}

// Prepare resolves a Ready Up plan: a verified bundle zip and an installer
// in temp files (removed by the returned cleanup). A missing release is a
// *PlanError with CodeNoRelease.
func (f *Fetcher) Prepare(ctx context.Context, cfg AgentConfig, version, bundle string) (ReadyUpPlan, func(), error) {
	var tmpFiles []string
	var tmpDirs []string
	cleanup := func() {
		for _, p := range tmpFiles {
			_ = os.Remove(p)
		}
		for _, d := range tmpDirs {
			_ = os.RemoveAll(d)
		}
	}
	def := f.defaults()
	plan := ReadyUpPlan{Component: componentFor(bundle), AcceptLicense: cfg.ReadyUpAcceptLicense}
	if plan.AcceptLicense == "" {
		plan.AcceptLicense = def.AcceptLicense
	}

	if src := strings.TrimSpace(cfg.ReadyUpBundle); src != "" {
		src = strings.NewReplacer("{version}", version, "{bundle}", plan.Component).Replace(src)
		path, tmp, err := f.fetch(ctx, src, "readyup-bundle-*.zip")
		if tmp {
			tmpFiles = append(tmpFiles, path)
		}
		if err != nil {
			cleanup()
			return plan, func() {}, &PlanError{Code: CodeNoRelease, Msg: "Ready Up bundle " + Redact(src) + ": " + err.Error()}
		}
		plan.Zip = path
	} else {
		b, err := f.download(ctx, cfg, def, version, plan.Component)
		if err != nil {
			return plan, func() {}, err
		}
		tmpDirs = append(tmpDirs, b.Dir)
		plan.Version = b.Tag
		plan.Zip = b.Zip
		plan.Installer = b.Installer
	}

	inst := strings.TrimSpace(cfg.ReadyUpInstaller)
	if inst == "" && plan.Installer != "" {
		return plan, cleanup, nil
	}
	if inst == "" {
		inst = DefaultReadyUpInstaller
	}
	path, tmp, err := f.fetch(ctx, inst, "readyup-install-*.sh")
	if tmp {
		tmpFiles = append(tmpFiles, path)
	}
	if err != nil {
		cleanup()
		return plan, func() {}, &PlanError{Code: CodeInstallFailed, Msg: "Ready Up installer " + Redact(inst) + ": " + err.Error()}
	}
	plan.Installer = path
	return plan, cleanup, nil
}

// download resolves the release (version, else the host's pin, else the
// newest on the host's channel) and downloads the verified bundle.
func (f *Fetcher) download(ctx context.Context, cfg AgentConfig, def ReadyUpDefaults, version, component string) (*readyup.Bundle, error) {
	api := f.GitHubAPI
	if api == "" {
		api = def.API
	}
	c := &readyup.Client{HTTP: f.client(), API: api, Repo: cfg.ReadyUpRepo, UserAgent: "csm-host-agent"}
	v := strings.TrimSpace(version)
	if v == "" || strings.EqualFold(v, "latest") {
		v = def.Version
	}
	rel, err := c.Resolve(ctx, def.Channel, v)
	if err != nil {
		code := CodeNoRelease
		var re *readyup.Error
		if errors.As(err, &re) && re.Code != readyup.CodeNoRelease {
			code = CodeInstallFailed
		}
		msg := err.Error()
		if code == CodeNoRelease {
			msg += " (or point csm at a bundle: csm agent config readyup_bundle <zip path or https URL>)"
		}
		return nil, &PlanError{Code: code, Msg: msg}
	}
	b, err := c.Download(ctx, rel, component, f.TempDir)
	if err != nil {
		code := CodeInstallFailed
		if readyup.IsNoRelease(err) {
			code = CodeNoRelease
		}
		return nil, &PlanError{Code: code, Msg: err.Error()}
	}
	return b, nil
}

// fetch returns a local path for src: the path itself, or a temp download of
// an https URL (tmp = true).
func (f *Fetcher) fetch(ctx context.Context, src, pattern string) (string, bool, error) {
	if !strings.HasPrefix(src, "https://") {
		if strings.Contains(src, "://") {
			return "", false, errors.New("only https:// URLs or local paths")
		}
		fi, err := os.Stat(src)
		if err != nil {
			return "", false, err
		}
		if fi.IsDir() {
			return "", false, errors.New("is a directory")
		}
		return src, false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("User-Agent", "csm-host-agent")
	resp, err := f.client().Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("download answered %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(f.TempDir, pattern)
	if err != nil {
		return "", false, err
	}
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, maxDownloadBytes+1))
	cerr := tmp.Close()
	if err == nil {
		err = cerr
	}
	if err == nil && n > maxDownloadBytes {
		err = errors.New("download is over 512 MiB")
	}
	if err == nil && n == 0 {
		err = errors.New("download is empty")
	}
	if err != nil {
		return tmp.Name(), true, err
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	return tmp.Name(), true, nil
}
