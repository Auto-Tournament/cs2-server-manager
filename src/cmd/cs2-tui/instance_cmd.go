package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

const instanceUsage = `usage: csm instance <command>

Instance mode runs many CS2 + Ready Up servers from ONE read-only install
(master-install) through overlayfs in an unprivileged user namespace: each
instance only stores what it writes. Instance N plays on base+10N (GOTV +1,
Ready Up status +7; base 27005 = 27015 for instance 1).

  csm instance create [N]          new instance (lowest free number by default)
  csm instance start|stop|restart N|all
  csm instance status [N]          state, ports, Ready Up phase, layer, pending restarts
  csm instance remove N [--force]  delete it and everything it wrote
  csm instance shell N             a shell in instance N's merged view (stopped instances)
  csm instance exec N -- CMD...    run CMD in stopped instance N's view (cwd = the view,
                                   $CSM_INSTANCE_DIR); CI uses it to run its own cs2.sh
  csm instance reset N             empty stopped instance N's upper layer (what it wrote)
  csm instance attach N            the instance's console (tmux; Ctrl-b d detaches)
  csm instance logs N [lines]      console log tail
  csm instance layer [status]      Ready Up layers (shared, read-only)
  csm instance layer build [--zip Z --installer I] [--bundle essentials|full]
                           [--accept-license noncommercial|commercial] [--for N]
                                   build a layer (default: the csm plugins release);
                                   --for N: instance N's private layer (CI): current does
                                   not move, no other instance ever uses it, N's previous
                                   private layer is removed
  csm instance layer unpin N       instance N back on the shared layer
  csm instance layer rebuild       same Ready Up, rebuilt on the current CS2 build
  csm instance layer use <id>      make an older layer current (rollback)
  csm instance update [--zip Z --installer I]
                                   Ready Up update: new layer, then restart idle instances
  csm instance update-game         CS2 update: a new game version (the running one is never
                                   written), layer rebuild, idle restarts
  csm instance game                CS2 game versions (current, in use, unused)
  csm instance gc                  remove layers and game versions nothing uses
  csm instance config [<key> <value>]
                                   backend servers|instances, base_port, map, max_players,
                                   private_shm on|off, nice 0..19,
                                   insecure on|off (-insecure: no VAC, test servers only)

Busy instances (a match on, players connected, or updates on hold) are never
restarted for an update; csm monitor restarts them once idle.`

// instanceBackendCommand runs a plain `csm start|stop|restart|logs|attach`
// against the instances when this host's backend is instances (instance N
// is server N). It reports false on a server-N host, where the caller runs
// the command as before.
func instanceBackendCommand(cmd string, args []string) bool {
	if !csm.InstanceBackendOn() {
		return false
	}
	out, err := runInstanceBackendCommand(cmd, args)
	csm.LogAction("cli", strings.TrimSpace(cmd+" "+strings.Join(args, " "))+" (instances)", out, err)
	if out != "" {
		fmt.Print(out)
	}
	if err != nil {
		var gate *csm.MatchInProgressError
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		if errors.As(err, &gate) {
			os.Exit(2)
		}
		os.Exit(1)
	}
	return true
}

func runInstanceBackendCommand(cmd string, args []string) (string, error) {
	force, rest := extractForce(args)
	var pos []string
	for _, a := range rest {
		switch a {
		case "--alternate", "-alternate", "--binary", "-binary":
			return "", fmt.Errorf("%s does not apply to instances (they always run the cs2 binary in their overlay)", a)
		}
		pos = append(pos, a)
	}
	set, err := csm.OpenServerSet()
	if err != nil {
		return "", err
	}
	if set.Count() == 0 {
		return "", errors.New(set.EmptyHint())
	}
	n := 0
	if len(pos) > 0 && pos[0] != "all" {
		if n, err = strconv.Atoi(pos[0]); err != nil || n < 1 {
			return "", fmt.Errorf("invalid instance number %q (must be a positive integer)", pos[0])
		}
		if err := set.Check(n); err != nil {
			return "", err
		}
	}
	target := "all instances"
	if n > 0 {
		target = fmt.Sprintf("instance %d", n)
	}
	ctx := context.Background()
	switch cmd {
	case "start":
		if err := set.Start(ctx, n); err != nil {
			return "", err
		}
		return fmt.Sprintf("Started %s. Status: csm status • console: csm attach <n>\n", target), nil
	case "stop", "restart":
		action := cmd
		if n > 0 {
			action = fmt.Sprintf("%s instance-%d", cmd, n)
		}
		if err := set.Gate(ctx, action, n, force, os.Stderr); err != nil {
			return "", err
		}
		if cmd == "stop" {
			err = set.Stop(n)
		} else {
			err = set.Restart(ctx, n)
		}
		if err != nil {
			return "", err
		}
		past := map[string]string{"stop": "Stopped", "restart": "Restarted"}[cmd]
		return fmt.Sprintf("%s %s.\n", past, target), nil
	case "logs":
		if n == 0 {
			return "", errors.New("usage: csm logs <instance> [lines]")
		}
		lines := 0
		if len(pos) > 1 {
			if lines, err = strconv.Atoi(pos[1]); err != nil {
				return "", fmt.Errorf("invalid line count %q", pos[1])
			}
		}
		return set.Logs(n, lines)
	case "attach":
		if n == 0 {
			return "", errors.New("usage: csm attach <instance>")
		}
		return runInstanceCommand([]string{"attach", strconv.Itoa(n)})
	}
	return "", fmt.Errorf("%s is not an instance command", cmd)
}

func instanceCommand(args []string) {
	out, err := runInstanceCommand(args)
	action := "instance"
	if len(args) > 0 {
		action += " " + strings.Join(args, " ")
	}
	csm.LogAction("cli", action, "", err)
	if out != "" {
		fmt.Print(out)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "instance: %v\n", err)
		os.Exit(1)
	}
}

// instanceNumbers parses N or "all".
func instanceNumbers(m *csm.InstanceManager, arg string) ([]int, error) {
	if arg == "all" {
		l := m.Serving()
		if len(l) == 0 {
			return nil, errors.New("no instances yet (csm instance create)")
		}
		return l, nil
	}
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 {
		return nil, fmt.Errorf("not an instance number: %q", arg)
	}
	return []int{n}, nil
}

// layerFlags parses --zip/--installer/--bundle/--accept-license.
func layerFlags(args []string) (*csm.LayerSource, error) {
	src, _, err := layerBuildFlags(args, false)
	return src, err
}

// layerBuildFlags is layerFlags plus --for N (allowFor).
func layerBuildFlags(args []string, allowFor bool) (*csm.LayerSource, int, error) {
	var src csm.LayerSource
	forN := 0
	license := ""
	for i := 0; i < len(args); i++ {
		val := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", args[i])
			}
			i++
			return args[i], nil
		}
		var err error
		switch args[i] {
		case "--zip":
			src.Zip, err = val()
		case "--installer":
			src.Installer, err = val()
		case "--bundle":
			src.Bundle, err = val()
		case "--accept-license":
			if license, err = val(); err == nil && license != "noncommercial" && license != "commercial" {
				err = fmt.Errorf("--accept-license is noncommercial or commercial, not %q", license)
			}
		case "--for":
			if !allowFor {
				return nil, 0, fmt.Errorf("unknown option %q", args[i])
			}
			var v string
			if v, err = val(); err == nil {
				if forN, err = strconv.Atoi(v); err != nil || forN < 1 {
					err = fmt.Errorf("--for needs an instance number, not %q", v)
				}
			}
		default:
			return nil, 0, fmt.Errorf("unknown option %q", args[i])
		}
		if err != nil {
			return nil, 0, err
		}
	}
	if src.Zip == "" && src.Installer == "" {
		if forN > 0 {
			return nil, 0, errors.New("--for needs --zip and --installer (the bundle to test)")
		}
		return nil, 0, nil
	}
	if src.Zip == "" || src.Installer == "" {
		return nil, 0, errors.New("--zip and --installer go together")
	}
	if license != "" {
		src.AcceptLicense = license
	} else if s, err := csm.LoadPluginSettings(); err == nil {
		src.AcceptLicense = s.Resolved().AcceptLicense
	}
	return &src, forN, nil
}

func runInstanceCommand(args []string) (string, error) {
	if len(args) == 0 {
		return csm.InstanceStatusReport(context.Background(), 0)
	}
	switch args[0] {
	case "-h", "--help", "help":
		return instanceUsage + "\n", nil
	}
	ctx := context.Background()
	m, err := csm.NewInstanceManager()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	w := io.Writer(&b)
	if args[0] == "layer" || args[0] == "update" || args[0] == "update-game" {
		// Long-running: stream to the terminal.
		w = os.Stdout
	}
	need := func(n int) error {
		if len(args) < n {
			return fmt.Errorf("usage: csm instance %s (csm instance -h)", args[0])
		}
		return nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		return instanceUsage + "\n", nil
	case "status":
		n := 0
		if len(args) > 1 {
			if n, err = strconv.Atoi(args[1]); err != nil {
				return "", fmt.Errorf("not an instance number: %q", args[1])
			}
		}
		return csm.InstanceStatusReport(ctx, n)
	case "create":
		n := 0
		if len(args) > 1 {
			if n, err = strconv.Atoi(args[1]); err != nil || n < 1 {
				return "", fmt.Errorf("not an instance number: %q", args[1])
			}
		}
		n, err = m.Create(w, n)
		if err == nil {
			if _, lerr := m.CurrentLayer(); lerr != nil {
				fmt.Fprintf(w, "No Ready Up layer yet: `csm instance layer build` before `csm instance start %d`.\n", n)
			} else {
				fmt.Fprintf(w, "Start it with: csm instance start %d\n", n)
			}
		}
		return b.String(), err
	case "start", "stop", "restart":
		if err := need(2); err != nil {
			return "", err
		}
		nums, err := instanceNumbers(m, args[1])
		if err != nil {
			return "", err
		}
		for _, n := range nums {
			switch args[0] {
			case "start":
				err = m.Start(ctx, n)
			case "stop":
				err = m.Stop(n, 30*time.Second)
			case "restart":
				err = m.Restart(ctx, n)
			}
			if err != nil {
				return b.String(), fmt.Errorf("instance %d: %w", n, err)
			}
			p, _ := m.Ports(n)
			past := map[string]string{"start": "started", "stop": "stopped", "restart": "restarted"}[args[0]]
			fmt.Fprintf(w, "instance %d: %s (game port %d)\n", n, past, p.Game)
		}
		return b.String(), nil
	case "remove":
		if err := need(2); err != nil {
			return "", err
		}
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return "", fmt.Errorf("not an instance number: %q", args[1])
		}
		force := len(args) > 2 && args[2] == "--force"
		return b.String(), m.Remove(w, n, force)
	case "shell":
		if err := need(2); err != nil {
			return "", err
		}
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return "", fmt.Errorf("not an instance number: %q", args[1])
		}
		cmd, err := m.ShellCommand(n)
		if err != nil {
			return "", err
		}
		return "", cmd.Run()
	case "exec":
		// csm instance exec N [--] cmd args...
		if err := need(3); err != nil {
			return "", err
		}
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1 {
			return "", fmt.Errorf("not an instance number: %q", args[1])
		}
		cmd := args[2:]
		if cmd[0] == "--" {
			cmd = cmd[1:]
		}
		return "", m.ExecInInstance(n, cmd)
	case "reset":
		if err := need(2); err != nil {
			return "", err
		}
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1 {
			return "", fmt.Errorf("not an instance number: %q", args[1])
		}
		return b.String(), m.Reset(w, n)
	case "attach":
		if err := need(2); err != nil {
			return "", err
		}
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return "", fmt.Errorf("not an instance number: %q", args[1])
		}
		if !m.IsRunning(n) {
			return "", fmt.Errorf("instance %d is not running", n)
		}
		cmd := exec.Command("tmux", "attach", "-t", "="+csm.InstanceSession(n))
		if os.Geteuid() == 0 {
			cmd = exec.Command("su", "-", m.L.User, "-c", "TMUX= tmux attach -t =cs2-inst-"+strconv.Itoa(n))
		}
		cmd.Env = append(os.Environ(), "TMUX=")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return "", cmd.Run()
	case "logs":
		if err := need(2); err != nil {
			return "", err
		}
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return "", fmt.Errorf("not an instance number: %q", args[1])
		}
		lines := 100
		if len(args) > 2 {
			if lines, err = strconv.Atoi(args[2]); err != nil {
				return "", fmt.Errorf("not a line count: %q", args[2])
			}
		}
		data, err := os.ReadFile(m.L.ConsoleLog(n))
		if err != nil {
			return "", err
		}
		all := strings.Split(string(data), "\n")
		if len(all) > lines {
			all = all[len(all)-lines:]
		}
		return strings.Join(all, "\n") + "\n", nil
	case "layer":
		sub := "status"
		if len(args) > 1 {
			sub = args[1]
		}
		switch sub {
		case "status":
			return csm.InstanceLayerReport()
		case "build":
			src, forN, err := layerBuildFlags(args[2:], true)
			if err != nil {
				return "", err
			}
			if src != nil && forN > 0 {
				_, err = m.BuildLayerFor(ctx, w, *src, fmt.Sprintf("private for instance %d", forN), forN)
			} else if src != nil {
				_, err = m.BuildLayer(ctx, w, *src, "operator")
			} else {
				_, err = m.BuildLayerFromRelease(ctx, w, "operator")
			}
			return "", err
		case "rebuild":
			_, err := m.RebuildLayer(ctx, w, "operator rebuild")
			return "", err
		case "unpin":
			if len(args) < 3 {
				return "", errors.New("usage: csm instance layer unpin N")
			}
			n, err := strconv.Atoi(args[2])
			if err != nil || n < 1 {
				return "", fmt.Errorf("not an instance number: %q", args[2])
			}
			return "", m.Unpin(w, n)
		case "use":
			if len(args) < 3 {
				return "", errors.New("usage: csm instance layer use <id>")
			}
			if err := m.UseLayer(args[2]); err != nil {
				return "", err
			}
			return "Layer " + args[2] + " is current; restart instances to use it.\n", nil
		}
		return "", fmt.Errorf("unknown layer command %q (csm instance -h)", sub)
	case "update":
		src, err := layerFlags(args[1:])
		if err != nil {
			return "", err
		}
		return "", m.UpdateReadyUp(ctx, w, csm.InstanceHoldNow(ctx), src)
	case "update-game":
		return "", m.UpdateGame(ctx, w, csm.InstanceHoldNow(ctx))
	case "game", "games":
		return m.GameVersionsReport(), nil
	case "gc":
		if err := m.GCNow(w); err != nil {
			return b.String(), err
		}
		if b.Len() == 0 {
			return "Nothing to remove.\n", nil
		}
		return b.String(), nil
	case "config":
		if len(args) == 1 {
			s, err := csm.LoadInstanceSettings()
			if err != nil {
				return "", err
			}
			r := s.Resolved()
			return fmt.Sprintf("backend %s\nbase_port %d\nmap %s\nmax_players %d (0 = shared config)\nprivate_shm %v\nnice %d\ninsecure %v\n",
				r.Backend, r.BasePort, r.Map, r.MaxPlayers, r.PrivateShmOn(), r.Nice, r.Insecure), nil
		}
		if len(args) != 3 {
			return "", errors.New("usage: csm instance config <key> <value>")
		}
		if _, err := csm.SetInstanceSetting(args[1], args[2]); err != nil {
			return "", err
		}
		msg := fmt.Sprintf("Saved %s = %s.\n", args[1], args[2])
		if args[1] == "backend" {
			msg += "The host agent now reports and manages " + args[2] + " once the host agent restarts.\n"
		}
		return msg, nil
	}
	return "", fmt.Errorf("unknown command %q\n\n%s", args[0], instanceUsage)
}
