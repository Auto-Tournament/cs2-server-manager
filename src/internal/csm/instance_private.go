package csm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Private layers: one instance on a Ready Up build of its own
//
// A layer built with `csm instance layer build --for N` is instance N's
// only. It never becomes layers/current, so no other instance (and no
// restart csm monitor does) ever mounts it; instance N follows it through
// instance-N/layer.pin instead of layers/current. That is what Ready Up's CI
// uses: every run builds the freshly built bundle into a private layer for
// the CI instance, and the previous run's layer is collected.
//
// A pinned instance is not one of the host's servers: the host agent, the
// fleet and `start all` leave it alone. `csm instance exec N -- cmd` runs a
// command (the CI's own cs2.sh launch, its live test) inside N's view.

const (
	instancePinName    = "layer.pin"
	instanceExecLock   = "exec.lock"
	instanceExecDirEnv = "CSM_INSTANCE_DIR"
)

// PinFile is instance n's private layer pin (the layer id).
func (l InstanceLayout) PinFile(n int) string { return filepath.Join(l.Dir(n), instancePinName) }

// ExecLockFile is held while `csm instance exec` runs in instance n.
func (l InstanceLayout) ExecLockFile(n int) string { return filepath.Join(l.Dir(n), instanceExecLock) }

// validLayerID: a layer directory name, never a path.
func validLayerID(id string) error {
	if id == "" || id == "current" || strings.ContainsAny(id, "/\\ \t\n,:") || strings.HasPrefix(id, ".") || strings.HasSuffix(id, ".src") {
		return fmt.Errorf("not a layer id: %q", id)
	}
	return nil
}

// PinnedLayer is the private layer instance n is pinned to ("" = none, it
// follows layers/current).
func (m *InstanceManager) PinnedLayer(n int) string {
	data, err := os.ReadFile(m.L.PinFile(n))
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if validLayerID(id) != nil {
		return ""
	}
	return filepath.Join(m.L.LayersDir(), id)
}

// Pinned reports whether instance n is a private (CI) instance: it has a
// pin file. An empty one reserves it before its first private layer (it
// mounts layers/current until then) and keeps it out of Serving.
func (m *InstanceManager) Pinned(n int) bool { return fileExists(m.L.PinFile(n)) }

// Reserve makes instance n a private instance (an empty pin) unless it is
// one already.
func (m *InstanceManager) Reserve(n int) error {
	if m.Pinned(n) {
		return nil
	}
	if err := writeFileAtomicMode(m.L.PinFile(n), nil, 0o644); err != nil {
		return err
	}
	m.own(m.L.PinFile(n))
	return nil
}

// EffectiveLayer is the layer instance n mounts on its next start: its
// private layer when pinned, else layers/current.
func (m *InstanceManager) EffectiveLayer(n int) (string, error) {
	if p := m.PinnedLayer(n); p != "" {
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			return "", fmt.Errorf("instance %d is pinned to layer %s, which is gone (csm instance layer build --for %d, or csm instance layer unpin %d)", n, filepath.Base(p), n, n)
		}
		return p, nil
	}
	return m.CurrentLayer()
}

// layerBuildFor is the CS2 build instance n gets on its next start.
func (m *InstanceManager) layerBuildFor(n int) int64 {
	if l, err := m.EffectiveLayer(n); err == nil {
		return gameBuild(m.layerGame(l))
	}
	return m.MasterBuild()
}

// Serving lists the instances that are the host's servers: every instance
// but the pinned (private layer, e.g. CI) ones.
func (m *InstanceManager) Serving() []int {
	var out []int
	for _, n := range m.List() {
		if !m.Pinned(n) {
			out = append(out, n)
		}
	}
	return out
}

// pinnedLayers are the private layers some instance is pinned to.
func (m *InstanceManager) pinnedLayers() map[string]int {
	out := map[string]int{}
	for _, n := range m.List() {
		if p := m.PinnedLayer(n); p != "" {
			out[filepath.Clean(p)] = n
		}
	}
	return out
}

// pin points instance n at layer id.
func (m *InstanceManager) pin(n int, id string) error {
	if err := validLayerID(id); err != nil {
		return err
	}
	if err := writeFileAtomicMode(m.L.PinFile(n), []byte(id+"\n"), 0o644); err != nil {
		return err
	}
	m.own(m.L.PinFile(n))
	return nil
}

// Unpin puts instance n back on layers/current; its private layer is
// collected.
func (m *InstanceManager) Unpin(w io.Writer, n int) error {
	if err := m.requireInstancePrivileges("instance layer unpin"); err != nil {
		return err
	}
	if !m.Exists(n) {
		return fmt.Errorf("instance %d does not exist", n)
	}
	if err := os.Remove(m.L.PinFile(n)); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintf(w, "[✓] instance %d follows the shared layer again (restart it to use it)\n", n)
	m.GC(w)
	return nil
}

// lockExec takes instance n's exec lock without blocking. The returned file
// holds the lock until it is closed (or the process, after exec, exits).
func (m *InstanceManager) lockExec(n int) (*os.File, error) {
	f, err := os.OpenFile(m.L.ExecLockFile(n), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("instance %d is busy: a `csm instance exec` runs in it", n)
	}
	return f, nil
}

// execBusy reports whether a `csm instance exec` holds instance n.
func (m *InstanceManager) execBusy(n int) bool {
	f, err := m.lockExec(n)
	if err != nil {
		return fileExists(m.L.ExecLockFile(n))
	}
	f.Close()
	return false
}

// renderInstanceRunInner is the script `csm instance exec` runs inside the
// fresh namespace: mount the view (and a private /dev/shm), then exec the
// command ("$@") in it with the instance's HOME.
func renderInstanceRunInner(s instanceLaunch, layer, game string) (string, error) {
	n := s.N
	opts, err := overlayMountOptions([]string{layer, game}, s.L.Upper(n), s.L.Work(n))
	if err != nil {
		return "", err
	}
	if err := overlayPathOK(s.L.Merged(n)); err != nil {
		return "", err
	}
	q := shellQuote
	var b strings.Builder
	b.WriteString("set -eu\n")
	fmt.Fprintf(&b, "mount -t overlay overlay -o %s %s\n", q(opts), q(s.L.Merged(n)))
	if s.PrivateShm {
		b.WriteString("mount -t tmpfs -o mode=1777,size=1g tmpfs /dev/shm || echo \"[csm] warning: no private /dev/shm (sharing the host's)\" >&2\n")
	}
	fmt.Fprintf(&b, "export HOME=%s %s=%s CSM_INSTANCE=%d\n", q(s.L.Home(n)), instanceExecDirEnv, q(s.L.Merged(n)), n)
	fmt.Fprintf(&b, "echo \"[csm] instance %d view (layer %s) mounted at %s\" >&2\n", n, filepath.Base(layer), s.L.Merged(n))
	fmt.Fprintf(&b, "cd %s\n", q(s.L.Merged(n)))
	fmt.Fprintf(&b, "exec nice -n %d \"$@\"\n", s.Nice)
	return b.String(), nil
}

// ExecArgv is the command line of `csm instance exec n -- cmd...`: a fresh
// user+mount namespace with instance n's view (its effective layer, the game
// version under it, its upper) mounted at its merged directory, cmd run
// there. The caller execs it (see ExecInInstance).
func (m *InstanceManager) ExecArgv(n int, cmd []string) ([]string, error) {
	if len(cmd) == 0 {
		return nil, errors.New("usage: csm instance exec N -- <command> [args...]")
	}
	if !m.Exists(n) {
		return nil, fmt.Errorf("instance %d does not exist (csm instance create %d)", n, n)
	}
	if m.IsRunning(n) {
		// Two overlay mounts sharing one upper/work is undefined behaviour.
		return nil, fmt.Errorf("instance %d is running (csm instance stop %d first)", n, n)
	}
	layer, err := m.EffectiveLayer(n)
	if err != nil {
		return nil, err
	}
	s, err := m.prepare(n)
	if err != nil {
		return nil, err
	}
	inner, err := renderInstanceRunInner(s, layer, m.layerGame(layer))
	if err != nil {
		return nil, err
	}
	argv := append(unshareArgv(), "bash", "-c", inner, "csm-instance-"+strconv.Itoa(n))
	return append(argv, cmd...), nil
}

// ExecInInstance replaces this process with cmd running in instance n's
// view (`csm instance exec`). Signals (a CI timeout) reach cmd directly. It
// holds the instance's exec lock until cmd exits, so neither a start nor
// another exec mounts the same upper meanwhile. Runs as the CS2 user only.
func (m *InstanceManager) ExecInInstance(n int, cmd []string) error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("csm instance exec runs as the CS2 user, not root: sudo -iu %s csm instance exec ...", m.L.User)
	}
	if err := m.requireInstancePrivileges("instance exec"); err != nil {
		return err
	}
	if !m.Exists(n) {
		return fmt.Errorf("instance %d does not exist (csm instance create %d)", n, n)
	}
	lock, err := m.lockExec(n)
	if err != nil {
		return err
	}
	argv, err := m.ExecArgv(n, cmd)
	if err != nil {
		lock.Close()
		return err
	}
	bin, err := exec.LookPath("unshare")
	if err != nil {
		lock.Close()
		return err
	}
	// Keep the lock across exec: clear close-on-exec on its descriptor.
	if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, lock.Fd(), syscall.F_SETFD, 0); e != 0 {
		lock.Close()
		return fmt.Errorf("exec lock: %v", e)
	}
	return syscall.Exec(bin, argv, os.Environ())
}

// Reset deletes everything stopped instance n wrote (its upper layer), so it
// starts from its layer alone. Its HOME (Steam client files) stays.
func (m *InstanceManager) Reset(w io.Writer, n int) error {
	if err := m.requireInstancePrivileges("instance reset"); err != nil {
		return err
	}
	if !m.Exists(n) {
		return fmt.Errorf("instance %d does not exist", n)
	}
	if m.IsRunning(n) {
		return fmt.Errorf("instance %d is running (csm instance stop %d first)", n, n)
	}
	lock, err := m.lockExec(n)
	if err != nil {
		return err
	}
	defer lock.Close()
	for _, d := range []string{m.L.Upper(n), m.L.Work(n)} {
		if err := removeTreeForce(d); err != nil {
			return err
		}
	}
	if err := m.mkdirs(m.L.Upper(n), m.L.Work(n), m.L.CfgDir(n)); err != nil {
		return err
	}
	fmt.Fprintf(w, "[✓] instance %d reset: its upper layer is empty\n", n)
	return nil
}
