package csm

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/readyup"
)

func TestInstancePorts(t *testing.T) {
	p, err := instancePorts(DefaultInstanceBasePort, 1)
	if err != nil {
		t.Fatal(err)
	}
	if p != (InstancePorts{Game: 27015, TV: 27016, Client: 27017, Status: 27022}) {
		t.Fatalf("instance 1 ports = %+v", p)
	}
	p, _ = instancePorts(27100, 3)
	if p.Game != 27130 || p.TV != 27131 || p.Client != 27132 || p.Status != 27137 {
		t.Fatalf("instance 3 @27100 = %+v", p)
	}
	// Instances never overlap: every port of N is below N+1's game port.
	a, _ := instancePorts(27005, 4)
	b, _ := instancePorts(27005, 5)
	if a.Status >= b.Game {
		t.Fatalf("instance 4 (%+v) runs into 5 (%+v)", a, b)
	}
	for _, bad := range []struct{ base, n int }{{27005, 0}, {27005, -1}, {80, 1}, {65500, 9}, {27005, maxInstanceNumber + 1}} {
		if _, err := instancePorts(bad.base, bad.n); err == nil {
			t.Fatalf("instancePorts(%d, %d) accepted", bad.base, bad.n)
		}
	}
}

func TestOverlayMountOptions(t *testing.T) {
	got, err := overlayMountOptions([]string{"/i/layers/L1", "/home/cs2/master-install"}, "/i/instance-1/upper", "/i/instance-1/work")
	if err != nil {
		t.Fatal(err)
	}
	want := "lowerdir=/i/layers/L1:/home/cs2/master-install,upperdir=/i/instance-1/upper,workdir=/i/instance-1/work"
	if got != want {
		t.Fatalf("options\n got %s\nwant %s", got, want)
	}
	for _, bad := range [][3]string{
		{"relative/layer", "/u", "/w"},
		{"/a,b", "/u", "/w"},
		{"/a:b", "/u", "/w"},
		{"/l", "/u'x", "/w"},
		{"/l", "/u", ""},
	} {
		if _, err := overlayMountOptions([]string{bad[0]}, bad[1], bad[2]); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := overlayMountOptions(nil, "/u", "/w"); err == nil {
		t.Fatal("no lower accepted")
	}
}

func testLaunch() instanceLaunch {
	l := InstanceLayout{User: "cs2", Root: "/home/cs2/instances", Master: "/home/cs2/master-install"}
	p, _ := instancePorts(27100, 2)
	return instanceLaunch{N: 2, L: l, Ports: p, Map: "de_nuke", MaxPlayers: 12, PrivateShm: true, Nice: 5}
}

func TestInstanceExecScript(t *testing.T) {
	s := testLaunch()
	sh, err := renderInstanceExecScript(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`mount -t overlay overlay -o "lowerdir=$LAYER:$GAME,upperdir=/home/cs2/instances/instance-2/upper,workdir=/home/cs2/instances/instance-2/work" '/home/cs2/instances/instance-2/merged'`,
		`GAME="${2:-/home/cs2/master-install}"`,
		"mount -t tmpfs -o mode=1777,size=1g tmpfs /dev/shm",
		"cd '/home/cs2/instances/instance-2/merged/game'",
		"export HOME='/home/cs2/instances/instance-2/home'",
		"exec nice -n 5 './bin/linuxsteamrt64/cs2' '-dedicated'",
		"'-port' '27120' '+tv_port' '27121' '+clientport' '27122' '+maxplayers' '12'",
		"'+exec' 'instance.cfg' '+map' 'de_nuke'",
	} {
		if !strings.Contains(sh, want) {
			t.Fatalf("exec.sh lacks %q:\n%s", want, sh)
		}
	}
	if strings.Contains(sh, "sv_setsteamaccount") || strings.Contains(sh, "gslt") {
		t.Fatal("instances never get a GSLT")
	}
	s.PrivateShm = false
	sh, _ = renderInstanceExecScript(s)
	if strings.Contains(sh, "/dev/shm") {
		t.Fatal("private_shm off still mounts /dev/shm")
	}
	s.L.Root = "/bad,root"
	if _, err := renderInstanceExecScript(s); err == nil {
		t.Fatal("a comma in the instance root was accepted")
	}
}

func TestInstanceRunScript(t *testing.T) {
	sh := renderInstanceRunScript(testLaunch())
	for _, want := range []string{
		"CURRENT='/home/cs2/instances/layers/current'",
		`LAYER=$(readlink -f "$CURRENT")`,
		`unshare --user --map-root-user --mount --propagation private /bin/bash "$EXEC" "$LAYER" "$GAME"`,
		`GAME=$(cat "$LAYER.game" 2>/dev/null) || GAME=""`,
		`[ -n "$GAME" ] || GAME="$MASTER"`,
		`printf '%s\n%s\n' "$LAYER" "$GAME" > "$INUSE"`,
		`[ -e "$STOP" ] && { rm -f "$INUSE"; exit 0; }`,
		"if (( ${#exits[@]} >= 5 )); then",
		"sleep 10",
	} {
		if !strings.Contains(sh, want) {
			t.Fatalf("run.sh lacks %q:\n%s", want, sh)
		}
	}
}

func TestInstanceCfg(t *testing.T) {
	cfg := renderInstanceCfg(3, "LAN #3", "secret", true, true)
	for _, want := range []string{`hostname "LAN #3"`, `rcon_password "secret"`, "\nlog on\n", "exec readyup_license.cfg\n", "exec instance_custom.cfg\n"} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("instance.cfg lacks %q:\n%s", want, cfg)
		}
	}
	// The custom cfg is exec'd last so it can override csm's settings.
	if strings.LastIndex(cfg, "exec instance_custom.cfg") < strings.LastIndex(cfg, "log on") {
		t.Fatal("instance_custom.cfg is not last")
	}
	cfg = renderInstanceCfg(1, "x", "", false, false)
	if strings.Contains(cfg, "rcon_password") || strings.Contains(cfg, "exec ") {
		t.Fatalf("unexpected lines:\n%s", cfg)
	}
}

func TestLayerBuildCmdline(t *testing.T) {
	c, err := layerBuildCmdline("/m", "/l/.b/upper", "/l/.b/work", "/l/.b/mnt", readyup.InstallArgs{Installer: "/l/x.src/install.sh", Bundle: "essentials", Zip: "/l/x.src/b.zip", AcceptLicense: "noncommercial"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"unshare --user --map-root-user --mount", "lowerdir=/m,upperdir=/l/.b/upper,workdir=/l/.b/work", "--dir", "/l/.b/mnt", "--zip", "--accept-license="} {
		if !strings.Contains(c, want) {
			t.Fatalf("build cmdline lacks %q: %s", want, c)
		}
	}
}

func TestRestartPending(t *testing.T) {
	if p, _ := restartPending("/l/a", "/l/a", 100, 100); p {
		t.Fatal("up to date reported pending")
	}
	if p, why := restartPending("/l/a", "/l/b", 100, 100); !p || !strings.Contains(why, "a -> b") {
		t.Fatalf("layer change: %v %q", p, why)
	}
	if p, why := restartPending("/l/a", "/l/a", 100, 101); !p || !strings.Contains(why, "100 -> 101") {
		t.Fatalf("build change: %v %q", p, why)
	}
	if p, _ := restartPending("", "/l/b", 0, 101); p {
		t.Fatal("unknown in-use layer / build must not count as pending")
	}
}

func TestDecideInstanceRestart(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	base := instanceRestartCheck{Pending: true, Why: "x", SafeKnown: true, Safe: true, Now: now, Grace: 5 * time.Minute}
	cases := []struct {
		name    string
		mut     func(*instanceRestartCheck)
		restart bool
		idle    bool
	}{
		{"not pending", func(c *instanceRestartCheck) { c.Pending = false }, false, false},
		{"hold", func(c *instanceRestartCheck) {
			c.Hold = UpdateHold{On: true, Reason: "tournament"}
			c.IdleSince = now.Add(-time.Hour)
		}, false, false},
		{"no answer", func(c *instanceRestartCheck) { c.SafeKnown = false }, false, false},
		{"mid-match", func(c *instanceRestartCheck) { c.Safe = false; c.IdleSince = now.Add(-time.Hour) }, false, false},
		{"players", func(c *instanceRestartCheck) { c.Players = 2 }, false, false},
		{"idle, grace starts", func(c *instanceRestartCheck) {}, false, true},
		{"idle, in grace", func(c *instanceRestartCheck) { c.IdleSince = now.Add(-time.Minute) }, false, true},
		{"idle past grace", func(c *instanceRestartCheck) { c.IdleSince = now.Add(-10 * time.Minute) }, true, true},
		{"no grace (operator)", func(c *instanceRestartCheck) { c.Grace = 0 }, true, true},
	}
	for _, tc := range cases {
		c := base
		tc.mut(&c)
		d := decideInstanceRestart(c)
		if d.Restart != tc.restart || d.Idle != tc.idle {
			t.Fatalf("%s: %+v", tc.name, d)
		}
	}
}

func writeInstFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindShadows(t *testing.T) {
	layer, upper := t.TempDir(), t.TempDir()
	writeInstFile(t, filepath.Join(layer, "game/csgo/cfg/ReadyUp/live.cfg"), "new live")
	writeInstFile(t, filepath.Join(layer, "game/csgo/cfg/ReadyUp/warmup.cfg"), "warmup")
	writeInstFile(t, filepath.Join(layer, "game/csgo/cfg/ReadyUp/fleet.cfg"), "template")
	writeInstFile(t, filepath.Join(layer, "game/csgo/readyup/plugins/match.so"), "so")
	// The instance edited live.cfg (old content), copied warmup.cfg unchanged,
	// has its own fleet.cfg (csm-managed) and runtime files the layer lacks.
	writeInstFile(t, filepath.Join(upper, "game/csgo/cfg/ReadyUp/live.cfg"), "old live, edited")
	writeInstFile(t, filepath.Join(upper, "game/csgo/cfg/ReadyUp/warmup.cfg"), "warmup")
	writeInstFile(t, filepath.Join(upper, "game/csgo/cfg/ReadyUp/fleet.cfg"), "url = x")
	writeInstFile(t, filepath.Join(upper, "game/csgo/readyup/status.json"), "{}")
	writeInstFile(t, filepath.Join(upper, "game/csgo/cfg/instance.cfg"), "log on")

	sh, err := findShadows(layer, upper)
	if err != nil {
		t.Fatal(err)
	}
	if len(sh) != 1 || sh[0].Path != "game/csgo/cfg/ReadyUp/live.cfg" || sh[0].Deleted {
		t.Fatalf("shadows = %+v", sh)
	}
	msg := describeShadows(4, sh)
	if !strings.Contains(msg, "instance 4") || !strings.Contains(msg, "live.cfg") || !strings.Contains(msg, instanceCustomCfgName) {
		t.Fatalf("warning = %q", msg)
	}
	if describeShadows(1, nil) != "" {
		t.Fatal("no shadows should say nothing")
	}
}

func TestIsWhiteout(t *testing.T) {
	cdev := fs.ModeDevice | fs.ModeCharDevice
	if !isWhiteout(cdev, 0) {
		t.Fatal("0:0 char device is a whiteout")
	}
	if isWhiteout(cdev, 0x0103) || isWhiteout(0o644, 0) || isWhiteout(fs.ModeDevice, 0) {
		t.Fatal("not whiteouts")
	}
}

func TestCopySkeleton(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeInstFile(t, filepath.Join(src, "game/csgo/maps/de_dust2.vpk"), "vpk")
	writeInstFile(t, filepath.Join(src, "game/bin/linuxsteamrt64/cs2"), "bin")
	if err := os.MkdirAll(filepath.Join(src, "steamapps/downloading"), 0o755); err != nil {
		t.Fatal(err)
	}
	n, err := copySkeleton(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if n < 7 {
		t.Fatalf("copied %d directories", n)
	}
	for _, d := range []string{"game/csgo/maps", "game/bin/linuxsteamrt64", "steamapps/downloading"} {
		if fi, err := os.Stat(filepath.Join(dst, d)); err != nil || !fi.IsDir() {
			t.Fatalf("%s missing: %v", d, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "game/csgo/maps/de_dust2.vpk")); !os.IsNotExist(err) {
		t.Fatal("files must not be copied into the skeleton")
	}
}

func TestInstanceSettings(t *testing.T) {
	t.Setenv("CSM_ROOT", t.TempDir())
	t.Setenv(EnvServerBackend, "")
	t.Setenv(EnvInstanceBasePort, "")
	t.Setenv(EnvInstanceNice, "")
	s, err := LoadInstanceSettings()
	if err != nil {
		t.Fatal(err)
	}
	r := s.Resolved()
	if r.Backend != ServerBackendServers || r.BasePort != DefaultInstanceBasePort || r.Map != "de_dust2" || !r.PrivateShmOn() || r.Nice != 0 {
		t.Fatalf("defaults = %+v", r)
	}
	if InstanceBackendOn() {
		t.Fatal("instance backend on by default")
	}
	for _, kv := range [][2]string{{"backend", "instances"}, {"base_port", "27100"}, {"private_shm", "off"}, {"nice", "10"}, {"map", "de_inferno"}} {
		if _, err := SetInstanceSetting(kv[0], kv[1]); err != nil {
			t.Fatalf("set %s: %v", kv[0], err)
		}
	}
	s, _ = LoadInstanceSettings()
	r = s.Resolved()
	if r.Backend != ServerBackendInstances || r.BasePort != 27100 || r.PrivateShmOn() || r.Nice != 10 || r.Map != "de_inferno" {
		t.Fatalf("saved = %+v", r)
	}
	if !InstanceBackendOn() {
		t.Fatal("backend instances not seen")
	}
	t.Setenv(EnvServerBackend, "servers")
	if InstanceBackendOn() {
		t.Fatal("env override ignored")
	}
	for _, kv := range [][2]string{{"backend", "docker"}, {"base_port", "80"}, {"nice", "40"}, {"map", "de dust"}, {"bogus", "1"}} {
		if _, err := SetInstanceSetting(kv[0], kv[1]); err == nil {
			t.Fatalf("set %s=%s accepted", kv[0], kv[1])
		}
	}
}

func testInstanceManager(t *testing.T) *InstanceManager {
	t.Helper()
	root := t.TempDir()
	return &InstanceManager{
		L: InstanceLayout{User: currentUsername(), Root: filepath.Join(root, "instances"), Master: filepath.Join(root, "master")},
		S: InstanceSettings{}.Resolved(),
	}
}

func stubInstancePorts(t *testing.T, busy ...int) {
	t.Helper()
	old := instancePortInUse
	instancePortInUse = func(p int) bool {
		for _, b := range busy {
			if b == p {
				return true
			}
		}
		return false
	}
	t.Cleanup(func() { instancePortInUse = old })
}

func TestNextFreeSkipsClassicServersAndBusyPorts(t *testing.T) {
	m := testInstanceManager(t)
	// Classic server-1..3 next to the instances root (stopped: no port in use).
	for _, n := range []int{1, 2, 3} {
		if err := os.MkdirAll(filepath.Join(filepath.Dir(m.L.Root), fmt.Sprintf("server-%d", n)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Instance 5 exists; something else listens on instance 4's game port (27045).
	if err := writeJSONAtomic(m.L.StateFile(5), instanceState{Number: 5}); err != nil {
		t.Fatal(err)
	}
	stubInstancePorts(t, 27045)
	if got := m.nextFree(); got != 6 {
		t.Fatalf("nextFree = %d, want 6 (1-3 classic servers, 4 port busy, 5 exists)", got)
	}
}

func TestInstanceListAndNextFree(t *testing.T) {
	stubInstancePorts(t)
	m := testInstanceManager(t)
	if len(m.List()) != 0 || m.nextFree() != 1 {
		t.Fatal("empty root")
	}
	for _, n := range []int{1, 2, 4} {
		if err := writeJSONAtomic(m.L.StateFile(n), instanceState{Number: n}); err != nil {
			t.Fatal(err)
		}
	}
	// A directory without instance.json is not an instance.
	_ = os.MkdirAll(m.L.Dir(7), 0o755)
	_ = os.MkdirAll(filepath.Join(m.L.Root, "instance-x"), 0o755)
	got := m.List()
	if len(got) != 3 || got[0] != 1 || got[2] != 4 {
		t.Fatalf("List = %v", got)
	}
	if m.nextFree() != 3 {
		t.Fatalf("nextFree = %d", m.nextFree())
	}
	if ft := m.FleetTarget(2); ft.Dir != m.L.Upper(2) || ft.GamePort != 27025 {
		t.Fatalf("fleet target = %+v", ft)
	}
}

func TestLayersCurrentAndGC(t *testing.T) {
	m := testInstanceManager(t)
	if _, err := m.CurrentLayer(); err == nil {
		t.Fatal("no layer yet")
	}
	for _, id := range []string{"20260101-000000", "20260102-000000", "20260103-000000", "20260104-000000"} {
		writeInstFile(t, filepath.Join(m.L.LayersDir(), id, "game/csgo/readyup/installed.json"), `{"components":{"core":"0.1.`+id[7:8]+`"}}`)
		writeInstFile(t, filepath.Join(m.L.LayersDir(), id+".src", "install.sh"), "#!/bin/sh")
		if err := writeJSONAtomic(filepath.Join(m.L.LayersDir(), id+".json"), LayerInfo{ID: id, Bundle: "essentials"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.setCurrentLayer("20260104-000000"); err != nil {
		t.Fatal(err)
	}
	cur, err := m.CurrentLayer()
	if err != nil || filepath.Base(cur) != "20260104-000000" {
		t.Fatalf("current = %q %v", cur, err)
	}
	if got := m.ReadLayerInfo(cur); got.Core != "0.1.4" || got.Bundle != "essentials" {
		t.Fatalf("info = %+v", got)
	}
	if len(m.ListLayers()) != 4 {
		t.Fatalf("layers = %v", m.ListLayers())
	}
	m.gcLayers(io.Discard)
	left := m.ListLayers()
	if len(left) != 2 || filepath.Base(left[0]) != "20260103-000000" || filepath.Base(left[1]) != "20260104-000000" {
		t.Fatalf("after gc = %v (want the current + one previous)", left)
	}
	if _, err := os.Stat(filepath.Join(m.L.LayersDir(), "20260101-000000.src")); !os.IsNotExist(err) {
		t.Fatal("gc left the sources of a removed layer")
	}
	if err := m.UseLayer("../etc"); err == nil {
		t.Fatal("UseLayer accepted a path")
	}
	if id := m.newLayerID(time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)); id != "20260104-000000-2" {
		t.Fatalf("newLayerID collision = %q", id)
	}
}

func TestRemoveTreeForce(t *testing.T) {
	d := t.TempDir()
	work := filepath.Join(d, "inst", "work", "work")
	writeInstFile(t, filepath.Join(work, "x"), "x")
	if err := os.Chmod(work, 0); err != nil {
		t.Fatal(err)
	}
	if err := removeTreeForce(filepath.Join(d, "inst")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d, "inst")); !os.IsNotExist(err) {
		t.Fatal("not removed")
	}
}
