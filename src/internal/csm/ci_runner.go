package csm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// `csm ci` sets up a CI test host for Ready Up's real-server compatibility
// check: a dedicated CS2 install plus a GitHub Actions self-hosted runner
// (label readyup-live) that runs as the unprivileged CS2 user under
// `systemd --user`. The Ready Up workflow finds the install through the
// runner's environment (CS2_CI_DIR, CS2_CI_PORT) and starts and stops the CI
// server itself.
//
// The CI install is not one of the numbered servers: it does not live in a
// server-N directory, so the server list, `csm monitor`, auto-update and
// start/stop/restart never see it, and nothing here starts, stops, restarts
// or updates a numbered server.

const (
	// CIRunnerLabel is the runner label the Ready Up workflow targets.
	CIRunnerLabel = "readyup-live"
	// CIDefaultRepo is the repository the runner registers with.
	CIDefaultRepo = "Auto-Tournament/ready-up"
	// CIDefaultDir is the default CI install directory (~ is the CS2 user's home).
	CIDefaultDir = "~/ru-ci"
	// CIDefaultPort is the default game port of the CI server.
	CIDefaultPort = 27095
	// CIRunnerDirName is the runner directory in the CS2 user's home.
	CIRunnerDirName = "actions-runner-readyup"
	// CIRunnerUnit is the systemd user unit that runs the runner.
	CIRunnerUnit = "actions.runner.readyup.service"

	// ciMarkerFile marks a directory csm ci created; --purge only deletes a
	// directory that has it.
	ciMarkerFile        = ".csm-ci-install"
	ciRunnerReleaseAPI  = "https://api.github.com/repos/actions/runner/releases/latest"
	ciRunnerAssetPrefix = "actions-runner-linux-x64-"
	ciInstallMinFreeGB  = 40
)

// CICommand is a parsed `csm ci` command line.
type CICommand struct {
	Action string // setup, status, update or remove
	Token  string // registration (setup) or removal (remove) token; never stored
	Repo   string
	Dir    string // raw --dir value; "" means from the runner's .env or the default
	Port   int
	Purge  bool
}

// CIUsage is the help text for `csm ci`.
const CIUsage = `Usage:
  csm ci setup --token <registration token> [--repo Auto-Tournament/ready-up] [--dir ~/ru-ci] [--port 27095]
  csm ci status [--dir <dir>]
  csm ci update [--dir <dir>]
  csm ci remove [--token <removal token>] [--purge] [--dir <dir>]

Sets up this host as the CI test host for Ready Up's real-server check: a
dedicated CS2 install (not one of the numbered servers: csm monitor,
auto-update and start/stop never touch it) and a GitHub Actions self-hosted
runner labelled readyup-live, running as the CS2 user under systemd --user.
Run as the CS2 user (not root) after a one-time ` + "`sudo csm setup-host`" + `.

  setup   register the runner, install CS2 into --dir, enable the service.
          The token (repo Settings > Actions > Runners > New self-hosted
          runner) is only passed to config.sh; it is never written to disk.
  status  runner service state, runner name/labels, CI install build, disk use
  update  SteamCMD update of the CI install only
  remove  stop and disable the runner; --token unregisters it on GitHub,
          --purge also deletes the runner directory and the CI install
`

var (
	ciRepoRe     = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?/[A-Za-z0-9._-]+$`)
	ciServerDir  = regexp.MustCompile(`^server-[0-9]+$`)
	ciNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// ParseCIArgs parses the arguments after `csm ci`. It returns flag.ErrHelp
// for -h/--help/help.
func ParseCIArgs(args []string) (CICommand, error) {
	cmd := CICommand{Repo: CIDefaultRepo, Port: CIDefaultPort}
	if len(args) == 0 {
		return cmd, fmt.Errorf("missing subcommand (setup, status, update or remove)")
	}
	cmd.Action = args[0]
	switch cmd.Action {
	case "-h", "--help", "help":
		return cmd, flag.ErrHelp
	case "setup", "status", "update", "remove":
	default:
		return cmd, fmt.Errorf("unknown subcommand %q (setup, status, update or remove)", cmd.Action)
	}

	fs := flag.NewFlagSet("ci "+cmd.Action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cmd.Dir, "dir", "", "CI install directory")
	switch cmd.Action {
	case "setup":
		fs.StringVar(&cmd.Token, "token", "", "runner registration token")
		fs.StringVar(&cmd.Repo, "repo", CIDefaultRepo, "repository (owner/name)")
		fs.IntVar(&cmd.Port, "port", CIDefaultPort, "CI server game port")
	case "remove":
		fs.StringVar(&cmd.Token, "token", "", "runner removal token")
		fs.BoolVar(&cmd.Purge, "purge", false, "also delete the runner directory and the CI install")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cmd, err
		}
		// flag errors can echo a bad value; keep a stray token out of them.
		return cmd, errors.New(RedactSecret(err.Error(), cmd.Token))
	}
	if fs.NArg() > 0 {
		return cmd, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	cmd.Token = strings.TrimSpace(cmd.Token)
	cmd.Repo = strings.TrimSpace(cmd.Repo)
	cmd.Dir = strings.TrimSpace(cmd.Dir)

	if cmd.Action == "setup" {
		if cmd.Token == "" {
			return cmd, fmt.Errorf("--token is required (GitHub: %s > Settings > Actions > Runners > New self-hosted runner)", cmd.Repo)
		}
		if !ciRepoRe.MatchString(cmd.Repo) {
			return cmd, fmt.Errorf("--repo %q is not owner/name", cmd.Repo)
		}
		if cmd.Port < 1024 || cmd.Port > 65535 {
			return cmd, fmt.Errorf("--port %d is out of range (1024-65535)", cmd.Port)
		}
	}
	if strings.ContainsAny(cmd.Token, " \t\r\n") {
		return cmd, fmt.Errorf("--token contains whitespace")
	}
	return cmd, nil
}

// RedactSecret replaces every occurrence of secret in s with ***.
func RedactSecret(s, secret string) string {
	if strings.TrimSpace(secret) == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

// redactWriter passes output through line by line with secret redacted, so a
// secret split across two writes is still caught. Call Flush at the end.
type redactWriter struct {
	w      io.Writer
	secret string
	buf    []byte
}

func newRedactWriter(w io.Writer, secret string) *redactWriter {
	return &redactWriter{w: w, secret: secret}
}

func (r *redactWriter) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexAny(r.buf, "\r\n")
		if i < 0 {
			break
		}
		if _, err := io.WriteString(r.w, RedactSecret(string(r.buf[:i+1]), r.secret)); err != nil {
			return 0, err
		}
		r.buf = r.buf[i+1:]
	}
	return len(p), nil
}

func (r *redactWriter) Flush() {
	if len(r.buf) > 0 {
		_, _ = io.WriteString(r.w, RedactSecret(string(r.buf), r.secret))
		r.buf = nil
	}
}

// resolveCIDir expands ~ against home and checks that dir is a sensible
// place for the CI install: absolute, not the home directory itself, not a
// numbered server or the master install, not the csm state directory and not
// inside the runner directory.
func resolveCIDir(raw, home, stateRoot string) (string, error) {
	dir := strings.TrimSpace(raw)
	if dir == "" {
		dir = CIDefaultDir
	}
	if dir == "~" {
		dir = home
	} else if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(home, dir[2:])
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("CI directory %q must be absolute (or start with ~/)", raw)
	}
	if strings.ContainsAny(dir, " \t\r\n\"'\\") {
		return "", fmt.Errorf("CI directory %q must not contain spaces, quotes or backslashes", dir)
	}
	dir = filepath.Clean(dir)
	home = filepath.Clean(home)
	if dir == "/" || dir == home {
		return "", fmt.Errorf("CI directory %q must be a directory of its own, not / or the home directory", dir)
	}
	for _, part := range strings.Split(dir, string(filepath.Separator)) {
		if ciServerDir.MatchString(part) || part == "master-install" {
			return "", fmt.Errorf("CI directory %q must not be (or be inside) a numbered server or the master install", dir)
		}
	}
	if within(dir, filepath.Join(home, CIRunnerDirName)) {
		return "", fmt.Errorf("CI directory %q must not be inside the runner directory", dir)
	}
	if stateRoot != "" && within(dir, filepath.Clean(stateRoot)) {
		return "", fmt.Errorf("CI directory %q must not be inside the csm state directory %s", dir, stateRoot)
	}
	return dir, nil
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// ciRunnerName is the runner name for a host: <hostname>-readyup-live.
func ciRunnerName(hostname string) string {
	h := strings.Trim(ciNameUnsafe.ReplaceAllString(strings.TrimSpace(hostname), "-"), "-.")
	if h == "" {
		h = "csm"
	}
	return h + "-" + CIRunnerLabel
}

// RenderCIRunnerUnit returns the systemd user unit that runs the runner. It
// mirrors what the runner's svc.sh installs for a system service.
func RenderCIRunnerUnit(runnerDir string) string {
	return fmt.Sprintf(`# Written by csm ci setup. GitHub Actions runner for Ready Up's live CS2 checks.
[Unit]
Description=GitHub Actions runner (%s) for Ready Up live CS2 checks
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s
KillMode=process
KillSignal=SIGTERM
TimeoutStopSec=5min
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
`, CIRunnerLabel, runnerDir, filepath.Join(runnerDir, "run.sh"))
}

// RenderCIRunnerEnv returns the runner's .env with CS2_CI_DIR and CS2_CI_PORT
// set, keeping whatever else config.sh put there (LANG, JAVA_HOME, ...).
func RenderCIRunnerEnv(existing, ciDir string, port int) string {
	var b strings.Builder
	for _, line := range strings.Split(existing, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "CS2_CI_DIR=") || strings.HasPrefix(trimmed, "CS2_CI_PORT=") {
			continue
		}
		b.WriteString(strings.TrimRight(line, "\r"))
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "CS2_CI_DIR=%s\nCS2_CI_PORT=%d\n", ciDir, port)
	return b.String()
}

// parseEnvFile reads KEY=value lines.
func parseEnvFile(content string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// userBusEnv makes `systemctl --user` work from `sudo -iu <user>`, which has
// no logind session and so no XDG_RUNTIME_DIR: with lingering enabled the
// user manager listens in /run/user/<uid>.
func userBusEnv(environ []string, uid int) []string {
	out := append([]string(nil), environ...)
	runtime := ""
	hasBus := false
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "XDG_RUNTIME_DIR="); ok && v != "" {
			runtime = v
		}
		if strings.HasPrefix(kv, "DBUS_SESSION_BUS_ADDRESS=") {
			hasBus = true
		}
	}
	if runtime == "" {
		runtime = fmt.Sprintf("/run/user/%d", uid)
		out = append(out, "XDG_RUNTIME_DIR="+runtime)
	}
	if !hasBus {
		out = append(out, "DBUS_SESSION_BUS_ADDRESS=unix:path="+runtime+"/bus")
	}
	return out
}

// runnerRelease is the part of the GitHub releases API response csm uses.
type runnerRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

var ciRunnerSHARe = regexp.MustCompile(`<!-- BEGIN SHA linux-x64 -->\s*([0-9a-fA-F]{64})\s*<!-- END SHA linux-x64 -->`)

// pickRunnerAsset returns the linux-x64 tarball of a runner release and its
// published SHA256 (from the release notes, or the asset digest), or "" if
// neither publishes one.
func pickRunnerAsset(rel runnerRelease) (name, url, sha string, err error) {
	want := ciRunnerAssetPrefix + strings.TrimPrefix(rel.TagName, "v") + ".tar.gz"
	for _, a := range rel.Assets {
		if a.Name != want {
			continue
		}
		if m := ciRunnerSHARe.FindStringSubmatch(rel.Body); m != nil {
			sha = strings.ToLower(m[1])
		} else if d, ok := strings.CutPrefix(a.Digest, "sha256:"); ok && len(d) == 64 {
			sha = strings.ToLower(d)
		}
		return a.Name, a.URL, sha, nil
	}
	return "", "", "", fmt.Errorf("release %s has no %s", rel.TagName, want)
}

// ciPrivilegeError refuses root (the runner must not run as root) and any
// user other than the CS2 user.
func ciPrivilegeError(euid int, currentUser, cs2User string) error {
	if euid == 0 {
		return fmt.Errorf("csm ci must not run as root (the runner would run as root). "+
			"Run it as the CS2 user, for example `sudo -iu %s csm ci ...` (after a one-time `sudo csm setup-host`)", cs2User)
	}
	return privilegeError(euid, currentUser, cs2User, "csm ci")
}

// ciEnv is where things live for the running CS2 user.
type ciEnv struct {
	cs2User   string
	home      string
	runnerDir string
	unitPath  string
}

func newCIEnv() (ciEnv, error) {
	cs2User := configuredCS2User()
	if err := ciPrivilegeError(os.Geteuid(), currentUsername(), cs2User); err != nil {
		return ciEnv{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = filepath.Join("/home", cs2User)
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	return ciEnv{
		cs2User:   cs2User,
		home:      home,
		runnerDir: filepath.Join(home, CIRunnerDirName),
		unitPath:  filepath.Join(cfg, "systemd", "user", CIRunnerUnit),
	}, nil
}

// ciDirFor picks the CI directory: --dir, else the runner's .env, else the default.
func (e ciEnv) ciDirFor(raw string) (string, error) {
	if raw == "" {
		if data, err := os.ReadFile(filepath.Join(e.runnerDir, ".env")); err == nil {
			raw = parseEnvFile(string(data))["CS2_CI_DIR"]
		}
	}
	return resolveCIDir(raw, e.home, ResolveRoot())
}

func (e ciEnv) systemctl(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	cmd.Env = userBusEnv(os.Environ(), os.Getuid())
	return cmd
}

func lingerEnabled(user string) bool {
	_, err := os.Stat(filepath.Join("/var/lib/systemd/linger", user))
	return err == nil
}

// RunCI runs a parsed `csm ci` command, writing progress to w. The token is
// redacted from everything written to w and from the returned error.
func RunCI(ctx context.Context, w io.Writer, cmd CICommand) error {
	rw := newRedactWriter(w, cmd.Token)
	defer rw.Flush()
	err := runCI(ctx, rw, cmd)
	if err != nil {
		return errors.New(RedactSecret(err.Error(), cmd.Token))
	}
	return nil
}

func runCI(ctx context.Context, w io.Writer, cmd CICommand) error {
	env, err := newCIEnv()
	if err != nil {
		return err
	}
	switch cmd.Action {
	case "setup":
		return ciSetup(ctx, w, env, cmd)
	case "status":
		return ciStatus(ctx, w, env, cmd)
	case "update":
		dir, err := env.ciDirFor(cmd.Dir)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(dir, ciMarkerFile)); err != nil {
			return fmt.Errorf("%s is not a csm ci install (no %s); run `csm ci setup` first", dir, ciMarkerFile)
		}
		return ciSteamUpdate(ctx, w, env.cs2User, dir)
	case "remove":
		return ciRemove(ctx, w, env, cmd)
	}
	return fmt.Errorf("unknown ci subcommand %q", cmd.Action)
}

func ciSetup(ctx context.Context, w io.Writer, env ciEnv, cmd CICommand) error {
	dir, err := resolveCIDir(cmd.Dir, env.home, ResolveRoot())
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "=== csm ci setup (user %s, repo %s) ===\n", env.cs2User, cmd.Repo)
	fmt.Fprintln(w, "The numbered servers are not started, stopped, restarted or updated.")
	fmt.Fprintln(w)

	fmt.Fprintln(w, "[1/5] Checks")
	if !lingerEnabled(env.cs2User) {
		return fmt.Errorf("lingering is off for %s, so its systemd --user services stop when it logs out. "+
			"Enable it once as root: `sudo loginctl enable-linger %s` (or `sudo csm setup-host`)", env.cs2User, env.cs2User)
	}
	for _, bin := range []string{"steamcmd", "systemctl", "tar"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found in PATH (install the dependencies once: `sudo csm install-deps`)", bin)
		}
	}
	if err := checkCIDirUsable(dir); err != nil {
		return err
	}
	if !ciInstallComplete(dir) {
		if err := requireFreeDiskGB(dir, ciInstallMinFreeGB); err != nil {
			return fmt.Errorf("not enough disk for the CI install: %w", err)
		}
	}
	fmt.Fprintf(w, "  [✓] user %s, linger on, CI install %s, port %d\n\n", env.cs2User, dir, cmd.Port)

	// The runner is registered before the (slow) CS2 download because
	// registration tokens expire after an hour. The service is only enabled
	// at the end, so no job runs against a half-installed server.
	fmt.Fprintf(w, "[2/5] GitHub Actions runner in %s\n", env.runnerDir)
	if err := ensureRunnerFiles(ctx, w, env.runnerDir); err != nil {
		return err
	}
	if err := configureRunner(ctx, w, env.runnerDir, cmd); err != nil {
		return err
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "[3/5] Runner environment (.env)")
	envPath := filepath.Join(env.runnerDir, ".env")
	existing, _ := os.ReadFile(envPath)
	if err := os.WriteFile(envPath, []byte(RenderCIRunnerEnv(string(existing), dir, cmd.Port)), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", envPath, err)
	}
	fmt.Fprintf(w, "  [✓] CS2_CI_DIR=%s CS2_CI_PORT=%d\n\n", dir, cmd.Port)

	fmt.Fprintf(w, "[4/5] CS2 install in %s (copy of the master install when there is one, then SteamCMD, anonymous)\n", dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	marker := "Created by `csm ci setup` for Ready Up's CI. Not a numbered server; csm monitor/auto-update/start/stop never touch it.\n"
	if err := os.WriteFile(filepath.Join(dir, ciMarkerFile), []byte(marker), 0o644); err != nil {
		return fmt.Errorf("writing marker in %s: %w", dir, err)
	}
	if !ciInstallComplete(dir) {
		// A local copy of the master install is far faster than a fresh
		// download; SteamCMD below then only fetches what differs.
		masterDir := filepath.Join("/home", env.cs2User, "master-install")
		if err := ciSeedFromMaster(ctx, w, masterDir, dir); err != nil {
			fmt.Fprintf(w, "  [!] %v; downloading instead\n", err)
		}
	}
	if err := ciSteamUpdate(ctx, w, env.cs2User, dir); err != nil {
		return err
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "[5/5] systemd --user service %s\n", CIRunnerUnit)
	if err := os.MkdirAll(filepath.Dir(env.unitPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(env.unitPath, []byte(RenderCIRunnerUnit(env.runnerDir)), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", env.unitPath, err)
	}
	// restart (not just --now) so a re-run picks up a changed .env or unit.
	for _, args := range [][]string{{"daemon-reload"}, {"enable", CIRunnerUnit}, {"restart", CIRunnerUnit}} {
		c := env.systemctl(ctx, args...)
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	fmt.Fprintf(w, "  [✓] %s enabled and running\n\n", CIRunnerUnit)
	fmt.Fprintf(w, "Done. The runner shows up as %s (label %s) in %s > Settings > Actions > Runners.\n",
		ciRunnerName(hostnameOrEmpty()), CIRunnerLabel, cmd.Repo)
	fmt.Fprintln(w, "Check it any time with `csm ci status`.")
	return nil
}

func hostnameOrEmpty() string {
	h, _ := os.Hostname()
	return h
}

// checkCIDirUsable refuses an existing, non-empty directory that csm ci did
// not create, so setup never installs into (and --purge never deletes)
// something else.
func checkCIDirUsable(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, ciMarkerFile)); err == nil {
		return nil
	}
	return fmt.Errorf("%s exists, is not empty and was not created by csm ci; pick another --dir", dir)
}

// ciInstallComplete reports whether Steam marks the CS2 install in dir as
// fully installed (StateFlags bit 4). An interrupted download leaves an
// appmanifest without it; such a dir is seeded from the master again.
func ciInstallComplete(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "steamapps", "appmanifest_730.acf"))
	if err != nil {
		return false
	}
	return acfFullyInstalled(string(data))
}

func acfFullyInstalled(acf string) bool {
	nodes, err := parseKeyValues(acf)
	if err != nil {
		return false
	}
	n := findKV(nodes, func(n *kvNode) bool {
		return !n.IsBlock && strings.EqualFold(n.Key, "StateFlags")
	})
	if n == nil {
		return false
	}
	flags, err := strconv.Atoi(strings.TrimSpace(n.Value))
	return err == nil && flags&4 != 0
}

func ciInstallPresent(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "steamapps", "appmanifest_730.acf"))
	return err == nil
}

// ciSeedFromMaster copies csm's master CS2 install into an empty CI dir.
// It is a real copy (reflinked where the filesystem supports it), never
// hardlinks: SteamCMD updates the CI install on its own and must not be able
// to change the master's files. It returns an error, and the caller falls
// back to a download, when there is no usable master install.
func ciSeedFromMaster(ctx context.Context, w io.Writer, masterDir, dir string) error {
	if !ciInstallComplete(masterDir) {
		return fmt.Errorf("no fully installed master install at %s", masterDir)
	}
	// An interrupted download leaves its staging data here; SteamCMD would
	// resume it instead of checking the copied files.
	for _, sub := range []string{"downloading", "temp"} {
		if err := os.RemoveAll(filepath.Join(dir, "steamapps", sub)); err != nil {
			return fmt.Errorf("clearing %s: %w", filepath.Join(dir, "steamapps", sub), err)
		}
	}
	fmt.Fprintf(w, "  Copying the master install %s (%s) to %s...\n", masterDir, ciBuildString(masterDir), dir)
	c := exec.CommandContext(ctx, "cp", "-a", "--reflink=auto", masterDir+"/.", dir+"/")
	c.Stdout = w
	c.Stderr = w
	total := treeBytes(masterDir)
	done := make(chan struct{})
	go reportCopyProgress(w, dir, total, 5*time.Second, done)
	err := c.Run()
	close(done)
	if err != nil {
		return fmt.Errorf("copying %s failed: %w", masterDir, err)
	}
	fmt.Fprintln(w, "  [✓] Copied; SteamCMD now only updates what differs")
	return nil
}

// ciSteamUpdate installs or updates CS2 in dir with SteamCMD (anonymous).
func ciSteamUpdate(ctx context.Context, w io.Writer, cs2User, dir string) error {
	steamArgs := []string{"+force_install_dir", dir, "+login", "anonymous", "+app_update", "730"}
	if SteamcmdShouldValidate() {
		steamArgs = append(steamArgs, "validate")
	}
	steamArgs = append(steamArgs, "+quit")
	argv := steamcmdAsUser(cs2User, false, steamArgs...)
	fmt.Fprintf(w, "  Running: %s\n", strings.Join(argv, " "))
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Stdout = w
	c.Stderr = w
	if err := c.Run(); err != nil {
		return fmt.Errorf("SteamCMD update of the CI install %s failed: %w", dir, err)
	}
	if !ciInstallPresent(dir) {
		return fmt.Errorf("SteamCMD finished but %s has no appmanifest_730.acf", dir)
	}
	fmt.Fprintf(w, "  [✓] CI install %s is up to date (%s)\n", dir, ciBuildString(dir))
	return nil
}

func ciBuildString(dir string) string {
	build, patch := "?", "?"
	if data, err := os.ReadFile(filepath.Join(dir, "steamapps", "appmanifest_730.acf")); err == nil {
		if v := parseBuildID(string(data)); v != "" {
			build = v
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "game", "csgo", "steam.inf")); err == nil {
		if v := parsePatchVersion(string(data)); v != "" {
			patch = v
		}
	}
	return "buildid " + build + ", PatchVersion " + patch
}

// ensureRunnerFiles downloads and unpacks the latest linux-x64 runner into
// runnerDir unless it is already there.
func ensureRunnerFiles(ctx context.Context, w io.Writer, runnerDir string) error {
	if _, err := os.Stat(filepath.Join(runnerDir, "config.sh")); err == nil {
		fmt.Fprintln(w, "  [i] Runner already unpacked (it updates itself)")
		return nil
	}
	client := &http.Client{Timeout: 30 * time.Second}
	data, err := RetryHTTPRead(client, ciRunnerReleaseAPI, DefaultRetryConfig())
	if err != nil {
		return fmt.Errorf("looking up the latest actions/runner release: %w", err)
	}
	var rel runnerRelease
	if err := json.Unmarshal(data, &rel); err != nil {
		return fmt.Errorf("parsing the actions/runner release: %w", err)
	}
	name, url, sha, err := pickRunnerAsset(rel)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "  Downloading %s\n", name)
	if err := os.MkdirAll(runnerDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(runnerDir), ".actions-runner-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: HTTP %s", name, resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		return fmt.Errorf("downloading %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if sha == "" {
		fmt.Fprintf(w, "  [!] The release publishes no SHA256; downloaded file has %s\n", got)
	} else if got != sha {
		return fmt.Errorf("SHA256 mismatch for %s: got %s, release says %s", name, got, sha)
	} else {
		fmt.Fprintln(w, "  [✓] SHA256 matches the release")
	}
	if out, err := exec.CommandContext(ctx, "tar", "-xzf", tmp.Name(), "-C", runnerDir).CombinedOutput(); err != nil {
		return fmt.Errorf("unpacking %s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(w, "  [✓] Runner %s unpacked\n", rel.TagName)
	return nil
}

// configureRunner registers the runner with GitHub unless it already is.
// The token goes to config.sh on its command line only.
func configureRunner(ctx context.Context, w io.Writer, runnerDir string, cmd CICommand) error {
	if _, err := os.Stat(filepath.Join(runnerDir, ".runner")); err == nil {
		fmt.Fprintln(w, "  [i] Runner already registered; token not used. To re-register: `csm ci remove --token <removal token>`, then setup again")
		return nil
	}
	name := ciRunnerName(hostnameOrEmpty())
	c := exec.CommandContext(ctx, "./config.sh",
		"--unattended",
		"--url", "https://github.com/"+cmd.Repo,
		"--token", cmd.Token,
		"--labels", CIRunnerLabel,
		"--name", name,
		"--work", "_work",
		"--replace",
	)
	c.Dir = runnerDir
	c.Stdout = w
	c.Stderr = w
	if err := c.Run(); err != nil {
		return fmt.Errorf("config.sh failed: %w. An expired token (they last an hour) needs a new one; "+
			"missing .NET dependencies (libicu) are installed once as root with `sudo %s`",
			err, filepath.Join(runnerDir, "bin", "installdependencies.sh"))
	}
	fmt.Fprintf(w, "  [✓] Registered as %s with label %s\n", name, CIRunnerLabel)
	return nil
}

// runnerInfo reads the runner's .runner file (JSON, sometimes with a BOM).
func runnerInfo(runnerDir string) (name, url string, ok bool) {
	data, err := os.ReadFile(filepath.Join(runnerDir, ".runner"))
	if err != nil {
		return "", "", false
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var info struct {
		AgentName string `json:"agentName"`
		GitHubURL string `json:"gitHubUrl"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return "", "", true
	}
	return info.AgentName, info.GitHubURL, true
}

func ciStatus(ctx context.Context, w io.Writer, env ciEnv, cmd CICommand) error {
	state := func(verb string) string {
		out, _ := env.systemctl(ctx, verb, CIRunnerUnit).Output()
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
		return "unknown"
	}
	fmt.Fprintf(w, "Runner service:  %s: %s (%s)\n", CIRunnerUnit, state("is-active"), state("is-enabled"))
	if !lingerEnabled(env.cs2User) {
		fmt.Fprintf(w, "                 warning: lingering is off; `sudo loginctl enable-linger %s`\n", env.cs2User)
	}
	if name, url, ok := runnerInfo(env.runnerDir); ok {
		fmt.Fprintf(w, "Runner:          %s -> %s\n", name, url)
		fmt.Fprintf(w, "Labels:          %s\n", CIRunnerLabel)
	} else {
		fmt.Fprintln(w, "Runner:          not registered (csm ci setup --token <token>)")
	}
	fmt.Fprintf(w, "Runner dir:      %s\n", env.runnerDir)

	vars := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(env.runnerDir, ".env")); err == nil {
		vars = parseEnvFile(string(data))
	}
	dir, err := env.ciDirFor(cmd.Dir)
	if err != nil {
		return err
	}
	port := vars["CS2_CI_PORT"]
	if port == "" {
		port = strconv.Itoa(CIDefaultPort) + " (default; not in .env)"
	}
	fmt.Fprintf(w, "CI install:      %s (port %s)\n", dir, port)
	if ciInstallPresent(dir) {
		fmt.Fprintf(w, "CS2 build:       %s\n", ciBuildString(dir))
		if out, err := exec.CommandContext(ctx, "du", "-sh", dir).Output(); err == nil {
			if f := strings.Fields(string(out)); len(f) > 0 {
				fmt.Fprintf(w, "Disk used:       %s\n", f[0])
			}
		}
	} else {
		fmt.Fprintln(w, "CS2 build:       not installed")
	}
	if free, checked, err := freeDiskGB(dir); err == nil {
		fmt.Fprintf(w, "Disk free:       %.1f GB on %s\n", free, checked)
	}
	return nil
}

func ciRemove(ctx context.Context, w io.Writer, env ciEnv, cmd CICommand) error {
	// Resolve the CI dir before .env goes away with the runner directory.
	dir, dirErr := env.ciDirFor(cmd.Dir)

	fmt.Fprintf(w, "Stopping %s\n", CIRunnerUnit)
	if out, err := env.systemctl(ctx, "disable", "--now", CIRunnerUnit).CombinedOutput(); err != nil {
		fmt.Fprintf(w, "  [i] systemctl --user disable --now: %s\n", strings.TrimSpace(string(out)))
	}
	if err := os.Remove(env.unitPath); err == nil {
		fmt.Fprintf(w, "  [✓] Removed %s\n", env.unitPath)
	}
	_ = env.systemctl(ctx, "daemon-reload").Run()

	_, url, registered := runnerInfo(env.runnerDir)
	switch {
	case registered && cmd.Token != "":
		c := exec.CommandContext(ctx, "./config.sh", "remove", "--token", cmd.Token)
		c.Dir = env.runnerDir
		c.Stdout = w
		c.Stderr = w
		if err := c.Run(); err != nil {
			return fmt.Errorf("config.sh remove failed: %w (removal tokens last an hour)", err)
		}
		fmt.Fprintln(w, "  [✓] Runner unregistered from GitHub")
	case registered:
		if url == "" {
			url = "the repository"
		}
		fmt.Fprintf(w, "  [i] Runner still registered on GitHub (no --token). Remove it in %s > Settings > Actions > Runners, "+
			"or run `csm ci remove --token <removal token>`\n", url)
	default:
		fmt.Fprintln(w, "  [i] Runner not registered")
	}

	if !cmd.Purge {
		fmt.Fprintln(w, "Runner files and the CI install are kept (--purge deletes them).")
		return nil
	}
	if _, err := os.Stat(filepath.Join(env.runnerDir, "config.sh")); err == nil {
		if err := os.RemoveAll(env.runnerDir); err != nil {
			return fmt.Errorf("deleting %s: %w", env.runnerDir, err)
		}
		fmt.Fprintf(w, "  [✓] Deleted %s\n", env.runnerDir)
	}
	if dirErr != nil {
		return dirErr
	}
	if _, err := os.Stat(filepath.Join(dir, ciMarkerFile)); err != nil {
		fmt.Fprintf(w, "  [i] %s has no %s marker; not deleting it\n", dir, ciMarkerFile)
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("deleting %s: %w", dir, err)
	}
	fmt.Fprintf(w, "  [✓] Deleted the CI install %s\n", dir)
	return nil
}

// treeBytes is the total size of the regular files under root.
func treeBytes(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if fi, ierr := d.Info(); ierr == nil {
			n += fi.Size()
		}
		return nil
	})
	return n
}

// reportCopyProgress prints how much of total has arrived in dir every
// interval, with an estimate of the time left, until done is closed.
func reportCopyProgress(w io.Writer, dir string, total int64, interval time.Duration, done <-chan struct{}) {
	if total <= 0 {
		return
	}
	start := time.Now()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			fmt.Fprintf(w, "  %s\n", copyProgressLine(treeBytes(dir), total, time.Since(start)))
		}
	}
}

func copyProgressLine(copied, total int64, elapsed time.Duration) string {
	if copied > total {
		copied = total
	}
	pct := float64(copied) * 100 / float64(total)
	line := fmt.Sprintf("%5.1f%%  %.1f / %.1f GB", pct, float64(copied)/1e9, float64(total)/1e9)
	if copied > 0 && elapsed > 0 {
		rate := float64(copied) / elapsed.Seconds()
		left := time.Duration(float64(total-copied)/rate) * time.Second
		line += fmt.Sprintf("  %.0f MB/s  ~%s left", rate/1e6, left.Round(time.Second))
	}
	return line
}
