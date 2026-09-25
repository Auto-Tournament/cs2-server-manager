package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"

	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

// runStatusCommand implements `csm status [--watch] [--json] [--no-color]`.
func runStatusCommand(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	watch := fs.Bool("watch", false, "keep the table open and update it live from Ready Up's /stream")
	asJSON := fs.Bool("json", false, "print the rows as JSON")
	noColor := fs.Bool("no-color", false, "no ANSI colours")
	_ = fs.Parse(args)

	mgr, err := csm.NewTmuxManager()
	if err != nil {
		return err
	}
	color := !*noColor && os.Getenv("NO_COLOR") == "" && isatty.IsTerminal(os.Stdout.Fd())

	if *watch {
		return watchStatus(mgr, color)
	}

	rows := csm.ProbeFleet(context.Background(), mgr.FleetTargets())
	if *asJSON {
		return printStatusJSON(os.Stdout, rows)
	}
	out := statusHeader(mgr) + csm.RenderFleetTable(rows, csm.FleetTableOptions{Color: color})
	if mgr.NumServers > 0 {
		out += "\nConsole: csm attach <n> • live view: csm status --watch\n"
	}
	csm.LogAction("cli", "status", out, nil)
	fmt.Print(out)
	return nil
}

func statusHeader(mgr *csm.TmuxManager) string {
	return fmt.Sprintf("CS2 servers (%s, %d server(s)) — %s\n\n", mgr.CS2User, mgr.NumServers, time.Now().Format("15:04:05"))
}

// watchStatus redraws the table whenever a server's state changes, until
// Ctrl+C. Process state (tmux) is re-read every 10 s.
func watchStatus(mgr *csm.TmuxManager, color bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	w := csm.NewFleetWatcher(mgr.FleetTargets(), csm.WatchOptions{})
	w.Run(ctx)

	draw := func() {
		var b strings.Builder
		if color {
			b.WriteString("\x1b[H\x1b[2J")
		}
		b.WriteString(statusHeader(mgr))
		b.WriteString(csm.RenderFleetTable(w.Rows(), csm.FleetTableOptions{Color: color}))
		b.WriteString("\nLive from Ready Up /stream (polling /status where there is none). Ctrl+C to exit.\n")
		fmt.Print(b.String())
	}
	draw()

	procTick := time.NewTicker(10 * time.Second)
	defer procTick.Stop()
	// Redraw at most a few times a second when a match is busy.
	var pending bool
	throttle := time.NewTicker(250 * time.Millisecond)
	defer throttle.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.Changed():
			pending = true
		case <-throttle.C:
			if pending {
				pending = false
				draw()
			}
		case <-procTick.C:
			for _, t := range mgr.FleetTargets() {
				w.SetProcess(t)
			}
		}
	}
}

// statusJSONRow is the --json shape: stable field names for scripts.
type statusJSONRow struct {
	Server     int                `json:"server"`
	GamePort   int                `json:"game_port"`
	StatusPort int                `json:"status_port"`
	Running    bool               `json:"running"`
	Updating   bool               `json:"updating,omitempty"`
	ReadyUp    string             `json:"readyup"`
	Error      string             `json:"error,omitempty"`
	Phase      string             `json:"phase,omitempty"`
	UpdateSafe *bool              `json:"update_safe,omitempty"`
	Status     *csm.ReadyUpStatus `json:"status,omitempty"`
}

func printStatusJSON(w io.Writer, rows []csm.FleetRow) error {
	out := make([]statusJSONRow, 0, len(rows))
	for _, r := range rows {
		jr := statusJSONRow{
			Server:     r.Target.Server,
			GamePort:   r.Target.GamePort,
			StatusPort: r.StatusPort,
			Running:    r.Target.Running,
			Updating:   r.Target.Updating,
			ReadyUp:    string(r.State),
			Error:      r.Err,
			Status:     r.Status,
		}
		if r.Status != nil {
			jr.Phase = csm.PhaseLabel(r.Status.Summary)
			jr.UpdateSafe = r.Status.UpdateSafe
		}
		out = append(out, jr)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// extractForce removes --force / -force from args wherever it appears, so
// `csm restart 2 --force` works as well as `csm restart --force 2`.
func extractForce(args []string) (force bool, rest []string) {
	for _, a := range args {
		switch a {
		case "--force", "-force", "--force=true", "-force=true":
			force = true
		default:
			rest = append(rest, a)
		}
	}
	return force, rest
}

// gateOrExit refuses a disruptive command while Ready Up reports a live match
// on any of servers (all servers when empty), unless force is set.
func gateOrExit(mgr *csm.TmuxManager, action string, servers []int, force bool) {
	if err := mgr.GateServers(context.Background(), action, servers, force, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
