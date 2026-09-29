package csm

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// InstanceStatusReport is `csm instance status [N]`.
func InstanceStatusReport(ctx context.Context, only int) (string, error) {
	m, err := NewInstanceManager()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	cur, lerr := m.CurrentLayer()
	fmt.Fprintf(&b, "Backend: %s   instances root: %s   master: %s (CS2 build %d)\n", m.S.Backend, m.L.Root, m.L.Master, m.MasterBuild())
	if lerr != nil {
		fmt.Fprintf(&b, "Ready Up layer: none (%v)\n", lerr)
	} else {
		info := m.ReadLayerInfo(cur)
		fmt.Fprintf(&b, "Ready Up layer: %s (core %s, %s, built on CS2 build %d)\n", info.ID, info.Core, info.Bundle, info.MasterBuild)
	}
	list := m.List()
	if only > 0 {
		if !m.Exists(only) {
			return "", fmt.Errorf("instance %d does not exist", only)
		}
		list = []int{only}
	}
	if len(list) == 0 {
		b.WriteString("\nNo instances yet: csm instance create\n")
		return b.String(), nil
	}
	var targets []FleetTarget
	for _, n := range list {
		targets = append(targets, m.FleetTarget(n))
	}
	rows := ProbeFleet(ctx, targets)
	b.WriteString("\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tSTATE\tGAME\tGOTV\tSTATUS\tREADY UP\tLAYER\tUPDATE")
	var warnings []string
	for _, r := range rows {
		n := r.Target.Server
		p, _ := m.Ports(n)
		state := "stopped"
		layer := "-"
		upd := "-"
		if r.Target.Running {
			state = "running"
			layer = filepath.Base(m.LayerInUse(n))
			if pending, why := m.RestartPending(n); pending {
				upd = "restart pending: " + why
			} else {
				upd = "current"
			}
		}
		ru := string(r.State)
		if r.Status != nil {
			ru = PhaseLabel(r.Status.Summary)
		}
		fmt.Fprintf(tw, "%d\t%s\t%d\t%d\t%d\t%s\t%s\t%s\n", n, state, p.Game, p.TV, p.Status, ru, layer, upd)
		if sh, err := m.Shadows(n); err == nil && len(sh) > 0 {
			warnings = append(warnings, describeShadows(n, sh))
		}
	}
	_ = tw.Flush()
	for _, w := range warnings {
		b.WriteString("\n" + w)
	}
	return b.String(), nil
}

// InstanceLayerReport is `csm instance layer status`.
func InstanceLayerReport() (string, error) {
	m, err := NewInstanceManager()
	if err != nil {
		return "", err
	}
	cur, _ := m.CurrentLayer()
	used := m.layersInUse()
	var b strings.Builder
	layers := m.ListLayers()
	if len(layers) == 0 {
		return "No Ready Up layers yet: csm instance layer build\n", nil
	}
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "LAYER\tCORE\tBUNDLE\tCS2 BUILD\tBUILT\tREASON\t")
	for _, d := range layers {
		info := m.ReadLayerInfo(d)
		mark := ""
		if filepath.Clean(d) == filepath.Clean(cur) {
			mark = "current"
		}
		if used[filepath.Clean(d)] {
			mark = strings.TrimPrefix(mark+", in use", ", ")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n", info.ID, info.Core, info.Bundle, info.MasterBuild, info.BuiltAt, info.Reason, mark)
	}
	_ = tw.Flush()
	return b.String(), nil
}
