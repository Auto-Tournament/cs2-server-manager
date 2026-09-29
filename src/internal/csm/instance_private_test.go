package csm

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// privateTestLayers makes a shared current layer and two private layers of
// instance 9 (both newer than current, as CI layers are).
func privateTestLayers(t *testing.T, m *InstanceManager) {
	t.Helper()
	for _, l := range []struct {
		id  string
		for_ int
	}{{"20260103-000000", 0}, {"20260104-000000", 0}, {"20260105-000000", 9}, {"20260106-000000", 9}} {
		writeInstFile(t, filepath.Join(m.L.LayersDir(), l.id, "game/csgo/readyup/installed.json"), `{"components":{"core":"0.1.0"}}`)
		writeInstFile(t, filepath.Join(m.L.LayersDir(), l.id+".src", "install.sh"), "#!/bin/sh")
		if err := writeJSONAtomic(filepath.Join(m.L.LayersDir(), l.id+".json"), LayerInfo{ID: l.id, Bundle: "full", For: l.for_}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.setCurrentLayer("20260104-000000"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{5, 9} {
		if err := writeJSONAtomic(m.L.StateFile(n), instanceState{Number: n}); err != nil {
			t.Fatal(err)
		}
	}
}

func layerNames(dirs []string) string {
	var out []string
	for _, d := range dirs {
		out = append(out, filepath.Base(d))
	}
	return strings.Join(out, " ")
}

func TestPrivateLayerPinAndGC(t *testing.T) {
	m := testInstanceManager(t)
	privateTestLayers(t, m)
	if err := m.pin(9, "20260106-000000"); err != nil {
		t.Fatal(err)
	}

	if got, _ := m.EffectiveLayer(9); filepath.Base(got) != "20260106-000000" {
		t.Fatalf("instance 9 mounts %q, want its private layer", got)
	}
	if got, _ := m.EffectiveLayer(5); filepath.Base(got) != "20260104-000000" {
		t.Fatalf("instance 5 mounts %q, want layers/current", got)
	}
	if s := m.Serving(); len(s) != 1 || s[0] != 5 {
		t.Fatalf("Serving = %v, want [5] (a pinned instance is not a server)", s)
	}
	if ft := m.FleetTargets(); len(ft) != 1 || ft[0].Server != 5 {
		t.Fatalf("FleetTargets = %+v", ft)
	}
	if err := m.UseLayer("20260106-000000"); err == nil {
		t.Fatal("a private layer became current")
	}

	// GC: the unpinned private layer goes; the pinned one, current and the
	// previous shared one (rollback target) stay.
	m.gcLayers(io.Discard)
	if got := layerNames(m.ListLayers()); got != "20260103-000000 20260104-000000 20260106-000000" {
		t.Fatalf("after gc: %s", got)
	}
	if _, err := os.Stat(filepath.Join(m.L.LayersDir(), "20260105-000000.src")); !os.IsNotExist(err) {
		t.Fatal("gc left the sources of a removed private layer")
	}
	if cur, _ := m.CurrentLayer(); filepath.Base(cur) != "20260104-000000" {
		t.Fatalf("current moved to %s", cur)
	}

	// Unpinned, its layer is collected and instance 9 follows current again.
	if err := m.Unpin(io.Discard, 9); err != nil {
		t.Fatal(err)
	}
	if got := layerNames(m.ListLayers()); got != "20260103-000000 20260104-000000" {
		t.Fatalf("after unpin: %s", got)
	}
	if got, _ := m.EffectiveLayer(9); filepath.Base(got) != "20260104-000000" {
		t.Fatalf("unpinned instance 9 mounts %q", got)
	}
	if len(m.Serving()) != 2 {
		t.Fatalf("Serving after unpin = %v", m.Serving())
	}
}

func TestPrivateLayerGone(t *testing.T) {
	m := testInstanceManager(t)
	privateTestLayers(t, m)
	if err := m.pin(9, "20260199-000000"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.EffectiveLayer(9); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("missing pinned layer: %v", err)
	}
	for _, bad := range []string{"", "current", "../x", ".build-1", "a,b", "x.src"} {
		if err := m.pin(9, bad); err == nil {
			t.Fatalf("pin accepted %q", bad)
		}
	}
}

func TestReservedInstance(t *testing.T) {
	m := testInstanceManager(t)
	privateTestLayers(t, m)
	if err := m.Reserve(9); err != nil {
		t.Fatal(err)
	}
	if !m.Pinned(9) || m.PinnedLayer(9) != "" {
		t.Fatalf("reserved: pinned %v layer %q", m.Pinned(9), m.PinnedLayer(9))
	}
	if got, _ := m.EffectiveLayer(9); filepath.Base(got) != "20260104-000000" {
		t.Fatalf("reserved instance mounts %q, want current until its first private layer", got)
	}
	if s := m.Serving(); len(s) != 1 || s[0] != 5 {
		t.Fatalf("Serving = %v", s)
	}
	// No pin kept anything: both private layers are collected.
	m.gcLayers(io.Discard)
	if got := layerNames(m.ListLayers()); got != "20260103-000000 20260104-000000" {
		t.Fatalf("after gc: %s", got)
	}
	// Reserve keeps an existing pin.
	if err := m.pin(9, "20260104-000000"); err != nil {
		t.Fatal(err)
	}
	if err := m.Reserve(9); err != nil || filepath.Base(m.PinnedLayer(9)) != "20260104-000000" {
		t.Fatalf("Reserve replaced the pin: %q %v", m.PinnedLayer(9), err)
	}
}

func TestInstanceRunScriptPin(t *testing.T) {
	sh := renderInstanceRunScript(testLaunch())
	for _, want := range []string{
		"PIN='/home/cs2/instances/instance-2/layer.pin'",
		`if [ -s "$PIN" ]; then`,
		`LAYER="$(dirname "$CURRENT")/$(head -n1 "$PIN")"`,
		`LAYER=$(readlink -f "$CURRENT")`,
	} {
		if !strings.Contains(sh, want) {
			t.Fatalf("run.sh lacks %q:\n%s", want, sh)
		}
	}
}

func TestInstanceRunInner(t *testing.T) {
	s := testLaunch()
	sh, err := renderInstanceRunInner(s, "/home/cs2/instances/layers/L9", "/home/cs2/master-install")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"mount -t overlay overlay -o 'lowerdir=/home/cs2/instances/layers/L9:/home/cs2/master-install,upperdir=/home/cs2/instances/instance-2/upper,workdir=/home/cs2/instances/instance-2/work' '/home/cs2/instances/instance-2/merged'",
		"mount -t tmpfs -o mode=1777,size=1g tmpfs /dev/shm",
		"export HOME='/home/cs2/instances/instance-2/home' CSM_INSTANCE_DIR='/home/cs2/instances/instance-2/merged' CSM_INSTANCE=2",
		"cd '/home/cs2/instances/instance-2/merged'",
		`exec nice -n 5 "$@"`,
	} {
		if !strings.Contains(sh, want) {
			t.Fatalf("exec script lacks %q:\n%s", want, sh)
		}
	}
	if _, err := renderInstanceRunInner(s, "/bad,layer", "/g"); err == nil {
		t.Fatal("a comma in the layer path was accepted")
	}
}

func TestExecArgvRefusals(t *testing.T) {
	m := testInstanceManager(t)
	if _, err := m.ExecArgv(3, []string{"true"}); err == nil {
		t.Fatal("exec in a missing instance")
	}
	if _, err := m.ExecArgv(3, nil); err == nil {
		t.Fatal("exec without a command")
	}
}
