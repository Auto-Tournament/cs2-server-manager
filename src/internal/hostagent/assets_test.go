package hostagent

import (
	"context"
	"os"
	"testing"
)

func TestAssetCommandChecks(t *testing.T) {
	for _, c := range []AssetCmd{{Set: "skins", File: "../x.webp"}, {Set: "other", File: "de_dust2.webp"}, {Set: "skins", File: "weapon_ak47"}} {
		if err := validateCommand(TypeAsset, &c); err == nil {
			t.Errorf("%+v passed", c)
		}
	}
	if err := validateCommand(TypeAsset, &AssetCmd{Set: "maps-480", File: "de_dust2_1.webp"}); err != nil {
		t.Error(err)
	}
	a := New(Options{})
	if r := a.handleAsset(context.Background(), NewULID(), &AssetCmd{Set: "skins", File: "x.webp"}); r.Error == nil || r.Error.Code != CodeUnsupported {
		t.Errorf("no install: %+v", r)
	}
}

// CS2_DIR points at an install's game/csgo: a real round trip through the agent.
func TestAssetRoundTrip(t *testing.T) {
	dir := os.Getenv("CS2_DIR")
	if dir == "" {
		t.Skip("CS2_DIR not set")
	}
	ta := startAgent(t, 1, "", func(o *Options) { o.CS2Dir = func() string { return dir } })
	ta.p.wait("hello", isType(TypeHello))
	for _, c := range [][3]string{
		{"skins-256", "weapon_ak47_cu_ak-47_phantom_disruptor.webp", "image/png"},
		{"maps-1280", "de_dust2.webp", "image/jpeg"},
		{"map-icons", "de_nuke_icon.svg", "image/svg+xml"},
	} {
		id := ta.p.send(TypeAsset, map[string]any{"set": c[0], "file": c[1]})
		if r := ta.p.result(id); r.Status != "ok" {
			t.Fatalf("%v: %+v", c, r)
		}
		ta.p.mu.Lock()
		h := ta.p.assets[id]
		ta.p.mu.Unlock()
		if h == nil || h.Get("Content-Type") != c[2] || h.Get("Authorization") != "Bearer "+testToken || h.Get("X-Len") == "0" {
			t.Fatalf("%v: upload %v", c, h)
		}
	}
	id := ta.p.send(TypeAsset, map[string]any{"set": "skins", "file": "weapon_ak47_nope.webp"})
	if r := ta.p.result(id); r.Error == nil || r.Error.Code != CodeNotFound {
		t.Fatalf("missing: %+v", r)
	}
}

func TestCatalogRoundTrip(t *testing.T) {
	ta := startAgent(t, 1, "", func(o *Options) {
		tmp := t.TempDir()
		o.CS2Dir = func() string { return tmp }
		o.Catalog = func(name string) ([]byte, error) { return []byte(`{"skins":[]}`), nil }
	})
	ta.p.wait("hello", isType(TypeHello))
	id := ta.p.send(TypeAsset, map[string]any{"set": "catalog", "file": "skins.json"})
	if r := ta.p.result(id); r.Status != "ok" {
		t.Fatalf("%+v", r)
	}
	ta.p.mu.Lock()
	h := ta.p.assets[id]
	ta.p.mu.Unlock()
	if h == nil || h.Get("Content-Type") != "application/json" || h.Get("X-Len") != "12" {
		t.Fatalf("upload %v", h)
	}
	if err := validateCommand(TypeAsset, &AssetCmd{Set: "catalog", File: "other.json"}); err == nil {
		t.Fatal("other.json passed")
	}
}
