package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"

	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
	"github.com/sivert-io/cs2-server-manager/src/internal/hostagent"
	"github.com/sivert-io/cs2-server-manager/src/internal/tui"
)

// csm link / unlink / agent: the host agent (Ready Up docs/FLEET.md §18).
// Codes, keys and tokens are never printed.

const linkUsage = `usage:
  csm link <platform-url> <code|key|-> [--insecure] [--ca-file PATH]
      Link this machine to an Auto Tournament platform as a host. <code> is the
      one-time code from Settings -> Hosts -> Add host (RUE-XXXX-...), <key> a
      fleet enrollment key (rfk_...). "-" reads it from stdin, so it stays out of
      the shell history. --insecure allows http:// to a loopback / private dev
      platform only.
  csm link status      Show the link (never the token)
  csm unlink           Forget the link (revoke the host on the platform too)
  csm fleet enroll <url> <code> | --key <rfk_...>   Same as csm link (FLEET.md wording)`

const agentUsage = `usage:
  csm agent                  Run the host agent in the foreground (systemd runs this)
  csm agent install          Install and start it as a systemd service (csm-agent.service)
  csm agent remove           Stop and remove the service
  csm agent status           Link + service state
  csm agent config [key [value]]
      Agent settings (fleet/agent.json). Keys:
        readyup_bundle          Ready Up bundle for host.update_plugins: a zip path or https URL
                                ({version} and {bundle} are replaced). Unset: the GitHub release
                                on the host's channel (csm plugins channel/version), SHA256-checked.
        readyup_installer       Ready Up install.sh: a path or https URL (default: the one in the bundle)
        readyup_accept_license  noncommercial | commercial, passed to install.sh. Unset: the
                                host's answer (csm plugins license, or AT_ACCEPT_LICENSE)
        readyup_repo            owner/name for the releases lookup
      "csm agent config <key> ''" clears a key.`

func linkCommand(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			fmt.Print(linkStatus())
			return
		case "-h", "--help", "help":
			fmt.Println(linkUsage)
			return
		}
	}
	out, err := runLink(args, os.Stdin, isatty.IsTerminal(os.Stdin.Fd()))
	csm.LogAction("cli", "link", hostagent.Redact(out), err)
	fmt.Print(out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "link: %s\n", hostagent.Redact(err.Error()))
		os.Exit(1)
	}
}

func runLink(args []string, stdin io.Reader, stdinTTY bool) (string, error) {
	var pos []string
	insecure := false
	caFile := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--insecure":
			insecure = true
		case a == "--ca-file" && i+1 < len(args):
			caFile = args[i+1]
			i++
		case strings.HasPrefix(a, "--ca-file="):
			caFile = strings.TrimPrefix(a, "--ca-file=")
		case a == "--key" && i+1 < len(args):
			pos = append(pos, args[i+1])
			i++
		case strings.HasPrefix(a, "--"):
			return "", fmt.Errorf("unknown flag %s\n%s", a, linkUsage)
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 {
		return "", errors.New(linkUsage)
	}
	if !csm.CanManageServers() {
		return "", fmt.Errorf("run csm link as the CS2 user or as root: the host agent runs as the same user and needs the credentials")
	}
	secret := pos[1]
	if secret == "-" {
		if stdinTTY {
			fmt.Fprint(os.Stderr, "Paste the code or fleet key and press Enter: ")
		}
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("reading the code from stdin: %w", err)
		}
		secret = strings.TrimSpace(line)
	}
	machineID, err := hostagent.MachineID(nil)
	if err != nil {
		return "", err
	}
	hostname, _ := os.Hostname()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	creds, err := hostagent.Enroll(ctx, hostagent.EnrollOptions{
		PlatformURL: pos[0], CodeOrKey: secret, MachineID: machineID, Hostname: hostname,
		OS: csm.HostOSDescription(), CSMVersion: tui.Version(), InsecureDev: insecure, CAFile: caFile,
	})
	if err != nil {
		return "", err
	}
	paths := csm.HostAgentPaths()
	if err := hostagent.SaveCredentials(paths, creds); err != nil {
		return "", fmt.Errorf("saving the credentials: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Linked to %s as host %s.\n", creds.PlatformURL, creds.HostID)
	fmt.Fprintf(&b, "Credentials: %s (mode 0600).\n", paths.Credentials())
	if creds.FleetKey != "" {
		b.WriteString("Enrolled with a fleet key: servers created from the platform get it in cfg/ReadyUp/fleet.cfg and enroll themselves.\n")
	}
	if csm.RestartHostAgentServiceIfInstalled(context.Background()) {
		fmt.Fprintf(&b, "Restarted %s; it connects now.\n", csm.HostAgentUnit)
	} else {
		b.WriteString("Start the host agent: csm agent install (systemd service), or csm agent (foreground).\n")
	}
	return b.String(), nil
}

func linkStatus() string {
	paths := csm.HostAgentPaths()
	var b strings.Builder
	c, err := hostagent.LoadCredentials(paths)
	if err != nil {
		fmt.Fprintf(&b, "Not linked: %s\n", err)
	} else {
		fmt.Fprintf(&b, "Platform:    %s\n", c.PlatformURL)
		fmt.Fprintf(&b, "Host id:     %s\n", c.HostID)
		fmt.Fprintf(&b, "WebSocket:   %s\n", c.WSURL)
		fmt.Fprintf(&b, "Enrolled:    %s\n", c.EnrolledAt)
		if c.RotatedAt != "" {
			fmt.Fprintf(&b, "Token rotated: %s\n", c.RotatedAt)
		}
		if c.FleetKey != "" {
			b.WriteString("Fleet key:   stored (rfk_…); handed to servers created with enroll\n")
		} else {
			b.WriteString("Fleet key:   none (linked with a one-time code)\n")
		}
		if c.InsecureDev {
			b.WriteString("Transport:   insecure dev mode (plain ws/http to a private host)\n")
		}
		fmt.Fprintf(&b, "Credentials: %s\n", paths.Credentials())
	}
	fmt.Fprintf(&b, "Agent:       %s\n", csm.HostAgentServiceState(context.Background()))
	return b.String()
}

func unlinkCommand(args []string) {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "usage: csm unlink")
		os.Exit(1)
	}
	paths := csm.HostAgentPaths()
	c, _ := hostagent.LoadCredentials(paths)
	if err := hostagent.RemoveCredentials(paths); err != nil {
		fmt.Fprintf(os.Stderr, "unlink: %v\n", err)
		os.Exit(1)
	}
	csm.LogAction("cli", "unlink", "", nil)
	if c != nil {
		fmt.Printf("Forgot host %s on %s. Revoke it on the platform (Settings -> Hosts) as well.\n", c.HostID, c.PlatformURL)
	} else {
		fmt.Println("This machine was not linked.")
	}
	fmt.Println("A running agent goes idle; remove the service with: csm agent remove")
}

// fleetCommand is the spec's spelling: csm fleet enroll <url> <code> | --key <rfk_…>.
func fleetCommand(args []string) {
	if len(args) == 0 || args[0] != "enroll" {
		fmt.Fprintln(os.Stderr, linkUsage)
		os.Exit(1)
	}
	linkCommand(args[1:])
}

func agentCommand(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	ctx := context.Background()
	switch sub {
	case "":
		if err := runAgent(); err != nil {
			fmt.Fprintf(os.Stderr, "agent: %v\n", err)
			os.Exit(1)
		}
	case "install":
		if err := csm.InstallHostAgentService(ctx, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "agent install: %v\n", err)
			os.Exit(1)
		}
		if _, err := hostagent.LoadCredentials(csm.HostAgentPaths()); err != nil {
			fmt.Println("Note: this machine is not linked yet; the agent waits until `csm link <url> <code|key>`.")
		}
	case "remove", "uninstall":
		if err := csm.RemoveHostAgentService(ctx, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "agent remove: %v\n", err)
			os.Exit(1)
		}
	case "status":
		fmt.Print(linkStatus())
	case "config":
		out, err := agentConfig(args[1:])
		fmt.Print(out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent config: %v\n", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		fmt.Println(agentUsage)
	default:
		fmt.Fprintln(os.Stderr, agentUsage)
		os.Exit(1)
	}
}

func agentConfig(args []string) (string, error) {
	paths := csm.HostAgentPaths()
	if len(args) == 2 {
		if _, err := hostagent.SetConfig(paths, args[0], args[1]); err != nil {
			return "", err
		}
		csm.LogAction("cli", "agent config "+args[0], "", nil)
	} else if len(args) != 0 {
		return "", errors.New(agentUsage)
	}
	c, err := hostagent.LoadConfig(paths)
	if err != nil {
		return "", err
	}
	show := func(k, v, def string) string {
		if v == "" {
			return fmt.Sprintf("%-24s (unset) %s\n", k, def)
		}
		return fmt.Sprintf("%-24s %s\n", k, v)
	}
	return show("readyup_bundle", c.ReadyUpBundle, "-> GitHub release (csm plugins channel/version), SHA256-checked") +
		show("readyup_installer", c.ReadyUpInstaller, "-> install.sh from the bundle") +
		show("readyup_accept_license", c.ReadyUpAcceptLicense, "-> csm plugins license / AT_ACCEPT_LICENSE") +
		show("readyup_repo", c.ReadyUpRepo, "-> "+hostagent.DefaultReadyUpRepo), nil
}

// runAgent runs the host agent until SIGINT / SIGTERM.
func runAgent() error {
	if !csm.CanManageServers() {
		return fmt.Errorf("the host agent manages tmux sessions and game files: run it as the CS2 user or as root")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := log.New(os.Stdout, "", log.LstdFlags)
	// csm's helpers log every tmux lookup through the standard logger; the
	// agent looks every few seconds, so keep the journal to the agent's lines.
	log.SetOutput(io.Discard)
	hostname, _ := os.Hostname()
	a := hostagent.New(hostagent.Options{
		Paths:      csm.HostAgentPaths(),
		Backend:    csm.NewHostBackend(),
		CSMVersion: tui.Version(),
		OS:         csm.HostOSDescription(),
		Hostname:   hostname,
		Fetcher:    &hostagent.Fetcher{Defaults: csm.HostReadyUpDefaults},
		Logf: func(format string, args ...any) {
			line := fmt.Sprintf(format, args...)
			logger.Print(line)
			csm.AppendLog("csm.log", time.Now().Format(time.RFC3339)+" "+line+"\n")
		},
	})
	logger.Printf("csm %s host agent starting (state in %s)", tui.Version(), csm.HostAgentPaths().Dir)
	err := a.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
