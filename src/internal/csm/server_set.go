package csm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// ServerSet is what `csm status`, the plain `csm start|stop|restart|logs|
// attach` commands and the TUI act on: the server-N folders, or the overlay
// instances when instances.json says backend=instances. Instance N is
// "server N" to these commands, as it is to the platform.
type ServerSet struct {
	Tmux      *TmuxManager     // backend servers
	Instances *InstanceManager // backend instances
}

// OpenServerSet opens the host's configured backend.
func OpenServerSet() (*ServerSet, error) {
	if InstanceBackendOn() {
		m, err := NewInstanceManager()
		if err != nil {
			return nil, err
		}
		return &ServerSet{Instances: m}, nil
	}
	mgr, err := NewTmuxManager()
	if err != nil {
		return nil, err
	}
	return &ServerSet{Tmux: mgr}, nil
}

// IsInstances reports whether the set is overlay instances.
func (s *ServerSet) IsInstances() bool { return s.Instances != nil }

// Noun is "server" or "instance".
func (s *ServerSet) Noun() string {
	if s.IsInstances() {
		return "instance"
	}
	return "server"
}

// Numbers lists the servers (instances) there are.
func (s *ServerSet) Numbers() []int {
	if s.IsInstances() {
		return s.Instances.Serving()
	}
	out := make([]int, 0, s.Tmux.NumServers)
	for n := 1; n <= s.Tmux.NumServers; n++ {
		out = append(out, n)
	}
	return out
}

// Count is len(Numbers()).
func (s *ServerSet) Count() int { return len(s.Numbers()) }

// Check reports whether server (instance) n exists.
func (s *ServerSet) Check(n int) error {
	if s.IsInstances() {
		if n < 1 || !s.Instances.Exists(n) {
			return fmt.Errorf("instance %d does not exist (csm instance create %d)", n, n)
		}
		return nil
	}
	if n < 1 || n > s.Tmux.NumServers {
		return fmt.Errorf("server-%d does not exist (only %d server(s) installed)", n, s.Tmux.NumServers)
	}
	return nil
}

// EmptyHint is what to do when there is nothing to manage yet.
func (s *ServerSet) EmptyHint() string {
	if s.IsInstances() {
		return "No instances yet: csm instance create (backend is instances; `csm instance config backend servers` switches back)."
	}
	return "No servers found. Run the install wizard first (csm, as the CS2 user or root)."
}

// FleetTargets are the status probes' targets.
func (s *ServerSet) FleetTargets() []FleetTarget {
	if s.IsInstances() {
		return s.Instances.FleetTargets()
	}
	return s.Tmux.FleetTargets()
}

// Header is the status table's title line.
func (s *ServerSet) Header() string {
	if s.IsInstances() {
		return fmt.Sprintf("CS2 instances (%s, %d instance(s), overlay backend)", s.Instances.L.User, s.Count())
	}
	return fmt.Sprintf("CS2 servers (%s, %d server(s))", s.Tmux.CS2User, s.Tmux.NumServers)
}

// Notes are instance-mode lines for under the status table: the layer and
// game version new starts get, and which running instances wait for a
// restart onto them.
func (s *ServerSet) Notes() string {
	if !s.IsInstances() {
		return ""
	}
	m := s.Instances
	var b strings.Builder
	if cur, err := m.CurrentLayer(); err == nil {
		info := m.ReadLayerInfo(cur)
		fmt.Fprintf(&b, "Ready Up layer %s (core %s) on CS2 game version %s (build %d)\n", info.ID, info.Core, m.ReadGameInfo(m.layerGame(cur)).ID, gameBuild(m.layerGame(cur)))
	} else {
		fmt.Fprintf(&b, "No Ready Up layer yet: csm instance layer build\n")
	}
	for _, n := range m.List() {
		if !m.IsRunning(n) {
			continue
		}
		if pending, why := m.RestartPending(n); pending {
			fmt.Fprintf(&b, "Instance %d: restart pending (%s); csm monitor restarts it once it is idle.\n", n, why)
		}
	}
	return b.String()
}

// Start starts n (0 = all).
func (s *ServerSet) Start(ctx context.Context, n int) error {
	if s.IsInstances() {
		return s.Instances.StartMany(ctx, s.pick(n))
	}
	if n == 0 {
		return s.Tmux.StartAll()
	}
	return s.Tmux.Start(n)
}

// Stop stops n (0 = all).
func (s *ServerSet) Stop(n int) error {
	if s.IsInstances() {
		return s.Instances.StopMany(s.pick(n), 30*time.Second)
	}
	if n == 0 {
		return s.Tmux.StopAll()
	}
	return s.Tmux.Stop(n)
}

// Restart restarts n (0 = all).
func (s *ServerSet) Restart(ctx context.Context, n int) error {
	if s.IsInstances() {
		return s.Instances.RestartMany(ctx, s.pick(n))
	}
	if n == 0 {
		return s.Tmux.RestartAll()
	}
	return s.Tmux.Restart(n)
}

func (s *ServerSet) pick(n int) []int {
	if n == 0 {
		return s.Instances.Serving()
	}
	return []int{n}
}

// Gate refuses a disruptive action on n (0 = all) while a match is on,
// unless force.
func (s *ServerSet) Gate(ctx context.Context, action string, n int, force bool, w io.Writer) error {
	if !s.IsInstances() {
		var servers []int
		if n > 0 {
			servers = []int{n}
		}
		return s.Tmux.GateServers(ctx, action, servers, force, w)
	}
	var targets []FleetTarget
	for _, k := range s.pick(n) {
		if s.Instances.Exists(k) {
			targets = append(targets, s.Instances.FleetTarget(k))
		}
	}
	return GateDisruptive(ctx, action, targets, force, w)
}

// Logs is the tail of n's console.
func (s *ServerSet) Logs(n, lines int) (string, error) {
	if !s.IsInstances() {
		return s.Tmux.Logs(n, lines)
	}
	if err := s.Check(n); err != nil {
		return "", err
	}
	if lines <= 0 {
		lines = 100
	}
	return tailFile(s.Instances.L.ConsoleLog(n), lines)
}

// tailFile returns the last lines of path.
func tailFile(path string, lines int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n") + "\n", nil
}

// --- instances as a set -----------------------------------------------------

// StartMany starts the listed instances that are not running.
func (m *InstanceManager) StartMany(ctx context.Context, nums []int) error {
	var errs []error
	for _, n := range nums {
		if m.IsRunning(n) && len(nums) > 1 {
			continue
		}
		if err := m.Start(ctx, n); err != nil {
			errs = append(errs, fmt.Errorf("instance %d: %w", n, err))
		}
	}
	return errors.Join(errs...)
}

// StopMany stops the listed instances, in parallel.
func (m *InstanceManager) StopMany(nums []int, grace time.Duration) error {
	errc := make(chan error, len(nums))
	for _, n := range nums {
		go func(n int) {
			if err := m.Stop(n, grace); err != nil {
				errc <- fmt.Errorf("instance %d: %w", n, err)
				return
			}
			errc <- nil
		}(n)
	}
	var errs []error
	for range nums {
		if err := <-errc; err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RestartMany restarts the listed instances one after another.
func (m *InstanceManager) RestartMany(ctx context.Context, nums []int) error {
	var errs []error
	for _, n := range nums {
		if err := m.Restart(ctx, n); err != nil {
			errs = append(errs, fmt.Errorf("instance %d: %w", n, err))
		}
	}
	return errors.Join(errs...)
}
