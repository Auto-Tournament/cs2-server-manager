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
  csm instance attach N            the instance's console (tmux; Ctrl-b d detaches)
  csm instance logs N [lines]      console log tail
  csm instance layer [status]      Ready Up layers (shared, read-only)
  csm instance layer build [--zip Z --installer I] [--bundle essentials|full]
                                   build a layer (default: the csm plugins release)
  csm instance layer rebuild       same Ready Up, rebuilt on the current CS2 build
  csm instance layer use <id>      make an older layer current (rollback)
  csm instance update [--zip Z --installer I]
                                   Ready Up update: new layer, then restart idle instances
  csm instance update-game         CS2 update: master once, layer rebuild, idle restarts
  csm instance config [<key> <value>]
                                   backend servers|instances, base_port, map, max_players,
                                   private_shm on|off, nice 0..19

Busy instances (a match on, players connected, or updates on hold) are never
restarted for an update; csm monitor restarts them once idle.`

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
		l := m.List()
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

// layerFlags parses --zip/--installer/--bundle.
func layerFlags(args []string) (*csm.LayerSource, error) {
	var src csm.LayerSource
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
		default:
			return nil, fmt.Errorf("unknown option %q", args[i])
		}
		if err != nil {
			return nil, err
		}
	}
	if src.Zip == "" && src.Installer == "" {
		return nil, nil
	}
	if src.Zip == "" || src.Installer == "" {
		return nil, errors.New("--zip and --installer go together")
	}
	if s, err := csm.LoadPluginSettings(); err == nil {
		src.AcceptLicense = s.Resolved().AcceptLicense
	}
	return &src, nil
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
			src, err := layerFlags(args[2:])
			if err != nil {
				return "", err
			}
			if src != nil {
				_, err = m.BuildLayer(ctx, w, *src, "operator")
			} else {
				_, err = m.BuildLayerFromRelease(ctx, w, "operator")
			}
			return "", err
		case "rebuild":
			_, err := m.RebuildLayer(ctx, w, "operator rebuild")
			return "", err
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
	case "config":
		if len(args) == 1 {
			s, err := csm.LoadInstanceSettings()
			if err != nil {
				return "", err
			}
			r := s.Resolved()
			return fmt.Sprintf("backend %s\nbase_port %d\nmap %s\nmax_players %d (0 = shared config)\nprivate_shm %v\nnice %d\n",
				r.Backend, r.BasePort, r.Map, r.MaxPlayers, r.PrivateShmOn(), r.Nice), nil
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
