package csm

import (
	"path/filepath"
	"testing"

	"github.com/sivert-io/cs2-server-manager/src/internal/hostagent"
)

func TestLayerHasPlan(t *testing.T) {
	m := testInstanceManager(t)
	layer := filepath.Join(m.L.Root, "layers", "20260929-120133")
	writeInstFile(t, layer+".json", `{"id":"20260929-120133","core":"0.1.0-e2e1","bundle":"essentials","zip":"ready-up-essentials.zip"}`)
	writeInstFile(t, filepath.Join(layer+".src", "ready-up-essentials.zip"), "zip bytes A")
	dir := t.TempDir()
	same := filepath.Join(dir, "same.zip")
	other := filepath.Join(dir, "other.zip")
	writeInstFile(t, same, "zip bytes A")
	writeInstFile(t, other, "zip bytes B")

	cases := []struct {
		name string
		plan hostagent.ReadyUpPlan
		want bool
	}{
		{"release version matches", hostagent.ReadyUpPlan{Component: "essentials", Version: "v0.1.0-e2e1"}, true},
		{"release version differs", hostagent.ReadyUpPlan{Component: "essentials", Version: "0.2.0"}, false},
		{"bundle differs", hostagent.ReadyUpPlan{Component: "full", Version: "0.1.0-e2e1"}, false},
		// agent config readyup_bundle: a zip and no version.
		{"same configured zip", hostagent.ReadyUpPlan{Component: "essentials", Zip: same}, true},
		{"other configured zip", hostagent.ReadyUpPlan{Component: "essentials", Zip: other}, false},
		{"nothing to compare", hostagent.ReadyUpPlan{Component: "essentials"}, false},
	}
	for _, tc := range cases {
		if got := layerHasPlan(m, layer, tc.plan); got != tc.want {
			t.Errorf("%s: layerHasPlan = %v, want %v", tc.name, got, tc.want)
		}
	}
}
