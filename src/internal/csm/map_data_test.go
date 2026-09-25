package csm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMapDisplayName(t *testing.T) {
	cases := map[string]string{
		"de_dust2":         "Dust II",
		"de_ancient":       "Ancient",
		"de_ancient_night": "Ancient (Night)",
		"ar_shoots_night":  "Shoots (Night)",
		"ar_pool_day":      "Pool Day",
		"cs_office":        "Office",
		"de_poseidon":      "Poseidon",
		"de_some_new_map":  "Some New Map",
	}
	for id, want := range cases {
		if got := MapDisplayName(id); got != want {
			t.Errorf("MapDisplayName(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestMapIDFromNames(t *testing.T) {
	vpk := map[string]string{
		"de_dust2.vpk":          "de_dust2",
		"de_poseidon.vpk":       "de_poseidon",
		"cs_office.vpk":         "cs_office",
		"de_dust2_vanity.vpk":   "",
		"graphics_settings.vpk": "",
		"lobby_mapveto.vpk":     "",
		"workshop_preview.vpk":  "",
		"de_inferno_000.vpk":    "",
		"de_inferno_dir.vpk":    "de_inferno",
		"de_nuke.txt":           "",
		"dz_blacksite.vpk":      "",
	}
	for name, want := range vpk {
		if got := mapIDFromVPKName(name); got != want {
			t.Errorf("mapIDFromVPKName(%q) = %q, want %q", name, got, want)
		}
	}
	img := map[string]string{
		"de_dust2.webp":                 "de_dust2",
		"de_dust2_1_thumb.webp":         "de_dust2",
		"de_ancient_night_thumb.webp":   "de_ancient_night",
		"cs_italy_0.webp":               "cs_italy",
		"lobby_mapveto.png":             "",
		"random_thumb.webp":             "",
		"maps.json":                     "",
		"de_ancient_night_4_thumb.webp": "de_ancient_night",
	}
	for name, want := range img {
		if got := mapIDFromImageName(name); got != want {
			t.Errorf("mapIDFromImageName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestBuildMapsManifest(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	m := buildMapsManifest(mapManifestInput{
		MapVPKs: []string{"de_poseidon.vpk", "de_dust2.vpk", "de_dust2_vanity.vpk", "graphics_settings.vpk"},
		ImageFiles: []string{
			"de_dust2.png", "de_dust2.webp", "de_dust2_thumb.webp",
			"de_dust2_2_thumb.webp", "de_dust2_10_thumb.webp", "de_dust2_1_thumb.webp", "de_dust2_1.webp",
			"de_ancient.webp", "de_ancient_thumb.webp",
			"de_ancient_night.webp", "de_ancient_night_1_thumb.webp",
			"lobby_mapveto.webp", "random_thumb.webp", "maps.json",
		},
		ActiveDuty: []string{"de_dust2", "de_ancient", "de_nuke"},
		PatchVer:   "1.41.1.4",
		BuildID:    "20123456",
		Now:        now,
	})

	if m.GeneratedAt != "2026-09-25T12:00:00Z" || m.PatchVersion != "1.41.1.4" || m.BuildID != "20123456" {
		t.Fatalf("header = %+v", m)
	}
	var ids []string
	for _, e := range m.Maps {
		ids = append(ids, e.ID)
	}
	wantIDs := []string{"de_ancient", "de_ancient_night", "de_dust2", "de_nuke", "de_poseidon"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("ids = %v, want %v", ids, wantIDs)
	}
	byID := map[string]MapEntry{}
	for _, e := range m.Maps {
		byID[e.ID] = e
	}

	dust := byID["de_dust2"]
	if dust.Name != "Dust II" || dust.Mode != "defusal" {
		t.Fatalf("dust2 = %+v", dust)
	}
	if dust.Images == nil || dust.Images.Full != "de_dust2.webp" || dust.Images.Thumb != "de_dust2_thumb.webp" {
		t.Fatalf("dust2 images = %+v", dust.Images)
	}
	if want := []string{"de_dust2_1_thumb.webp", "de_dust2_2_thumb.webp", "de_dust2_10_thumb.webp"}; !reflect.DeepEqual(dust.Variants, want) {
		t.Fatalf("dust2 variants = %v, want %v", dust.Variants, want)
	}
	// de_ancient must not pick up de_ancient_night's variants.
	if v := byID["de_ancient"].Variants; len(v) != 0 {
		t.Fatalf("de_ancient variants = %v", v)
	}
	night := byID["de_ancient_night"]
	if night.Images == nil || night.Images.Full != "de_ancient_night.webp" || night.Images.Thumb != "" {
		t.Fatalf("night images = %+v", night.Images)
	}
	if byID["de_poseidon"].Images != nil || byID["de_nuke"].Images != nil {
		t.Fatal("maps without screenshots should have no images")
	}
	if !reflect.DeepEqual(m.ActiveDuty, []string{"de_dust2", "de_ancient", "de_nuke"}) {
		t.Fatalf("activeDuty = %v", m.ActiveDuty)
	}

	// JSON shape: images omitted when absent, variants always an array.
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Maps []map[string]any `json:"maps"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, e := range raw.Maps {
		if _, ok := e["variants"].([]any); !ok {
			t.Fatalf("variants not an array in %v", e)
		}
		if e["id"] == "de_poseidon" {
			if _, ok := e["images"]; ok {
				t.Fatalf("de_poseidon should have no images key: %v", e)
			}
		}
	}
}

func TestWriteMapsManifestSkipsTimestampOnlyChanges(t *testing.T) {
	dir := t.TempDir()
	m := MapsManifest{GeneratedAt: "2026-01-01T00:00:00Z", PatchVersion: "1", Maps: []MapEntry{}, ActiveDuty: []string{"de_nuke"}}
	if written, err := writeMapsManifest(dir, m); err != nil || !written {
		t.Fatalf("first write: written=%v err=%v", written, err)
	}
	m.GeneratedAt = "2026-02-01T00:00:00Z"
	if written, err := writeMapsManifest(dir, m); err != nil || written {
		t.Fatalf("timestamp-only write: written=%v err=%v", written, err)
	}
	m.ActiveDuty = []string{"de_nuke", "de_train"}
	if written, err := writeMapsManifest(dir, m); err != nil || !written {
		t.Fatalf("changed write: written=%v err=%v", written, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, MapsManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var got MapsManifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.GeneratedAt != "2026-02-01T00:00:00Z" || len(got.ActiveDuty) != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestReadMapsManifestInput(t *testing.T) {
	master := t.TempDir()
	csgo := filepath.Join(master, "game", "csgo")
	thumbs := t.TempDir()
	mustWrite := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(csgo, "maps", "de_poseidon.vpk"), "")
	mustWrite(filepath.Join(csgo, "steam.inf"), "PatchVersion=1.41.1.4\n")
	mustWrite(filepath.Join(master, "steamapps", "appmanifest_730.acf"), `"AppState" { "buildid" "42" }`)
	mustWrite(filepath.Join(thumbs, "de_dust2.webp"), "")
	fixture, err := os.ReadFile("testdata/gamemodes.txt")
	if err != nil {
		t.Fatal(err)
	}
	gm := filepath.Join(t.TempDir(), "gamemodes.txt")
	mustWrite(gm, string(fixture))

	in, warnings := readMapsManifestInput(master, csgo, gm, thumbs)
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if in.PatchVer != "1.41.1.4" || in.BuildID != "42" || len(in.ActiveDuty) != 7 {
		t.Fatalf("input = %+v", in)
	}
	if !reflect.DeepEqual(in.MapVPKs, []string{"de_poseidon.vpk"}) || !reflect.DeepEqual(in.ImageFiles, []string{"de_dust2.webp"}) {
		t.Fatalf("files = %v %v", in.MapVPKs, in.ImageFiles)
	}

	// Without gamemodes.txt the manifest still builds, with a warning.
	_, warnings = readMapsManifestInput(master, csgo, filepath.Join(t.TempDir(), "missing.txt"), thumbs)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v", warnings)
	}
}
