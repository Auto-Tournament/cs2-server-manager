package csm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/sivert-io/cs2-server-manager/src/internal/gamefiles"
)

// Catalogs are the lists the platform reads (host.asset set "catalog"), built
// from the master install and kept on this host, never in the repository.
var Catalogs = map[string]func(csgoDir, masterDir string, v *gamefiles.VPK) ([]byte, error){
	MapsManifestFile:  buildMapsCatalog,
	SkinsManifestFile: buildSkinsCatalog,
}

var catalogMu sync.Mutex

// Catalog returns maps.json or skins.json for the master install, rebuilt
// only when pak01 changed (a CS2 update) and otherwise read from the cache.
func Catalog(name string) ([]byte, error) {
	build, ok := Catalogs[name]
	if !ok {
		return nil, fmt.Errorf("no catalog %q", name)
	}
	masterDir := filepath.Join("/home", ConfiguredCS2User(), "master-install")
	csgoDir := filepath.Join(masterDir, "game", "csgo")
	pak := filepath.Join(csgoDir, "pak01_dir.vpk")
	fi, err := os.Stat(pak)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%d-%d", fi.Size(), fi.ModTime().UnixNano())
	dir := filepath.Join(HostAgentPaths().Dir, "catalog")
	cached := filepath.Join(dir, name)

	catalogMu.Lock()
	defer catalogMu.Unlock()
	if k, err := os.ReadFile(cached + ".key"); err == nil && string(k) == key {
		if data, err := os.ReadFile(cached); err == nil {
			return data, nil
		}
	}
	v, err := gamefiles.OpenVPK(pak)
	if err != nil {
		return nil, err
	}
	data, err := build(csgoDir, masterDir, v)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err == nil {
		_ = os.WriteFile(cached, data, 0o644)
		_ = os.WriteFile(cached+".key", []byte(key), 0o644)
	}
	return data, nil
}

func jsonIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(v)
	return buf.Bytes(), err
}

// buildMapsCatalog is maps.json, with the screenshots and icons pak01 has.
func buildMapsCatalog(csgoDir, masterDir string, v *gamefiles.VPK) ([]byte, error) {
	in := mapManifestInput{Now: time.Now()}
	if entries, err := os.ReadDir(filepath.Join(csgoDir, "maps")); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				in.MapVPKs = append(in.MapVPKs, e.Name())
			}
		}
	}
	for _, p := range v.List(thumbnailVPKFolder, "_png.vtex_c") {
		in.ImageFiles = append(in.ImageFiles, strings.TrimSuffix(filepath.Base(p), "_png.vtex_c")+".webp")
	}
	for _, p := range v.List(mapIconVPKPrefix, ".vsvg_c") {
		id := strings.TrimSuffix(strings.TrimPrefix(p, mapIconVPKPrefix), ".vsvg_c")
		in.ImageFiles = append(in.ImageFiles, id+MapIconSuffix)
	}
	if gm, err := v.Read("gamemodes.txt"); err == nil {
		if ad, err := ParseActiveDutyMaps(string(gm)); err == nil {
			in.ActiveDuty = ad
		}
	}
	if b, err := os.ReadFile(filepath.Join(csgoDir, "steam.inf")); err == nil {
		in.PatchVer = parsePatchVersion(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(masterDir, "steamapps", "appmanifest_730.acf")); err == nil {
		in.BuildID = parseBuildID(string(b))
	}
	m := buildMapsManifest(in)
	if len(m.Maps) == 0 {
		return nil, fmt.Errorf("no maps in %s", csgoDir)
	}
	return jsonIndent(m)
}

// CatalogSkin is one entry of skins.json: a weapon finish and its preview pictures.
type CatalogSkin struct {
	Weapon       string  `json:"weapon"`
	WeaponName   *string `json:"weaponName"`
	PaintKit     int     `json:"paintKit"`
	PaintKitName string  `json:"paintKitName"`
	Name         *string `json:"name"`
	Rarity       *string `json:"rarity"`
	Image        string  `json:"image"`
	ImageMedium  string  `json:"imageMedium,omitempty"`
	ImageHeavy   string  `json:"imageHeavy,omitempty"`
}

const skinPreviews = "panorama/images/econ/default_generated/"

var englishLine = regexp.MustCompile(`(?m)^\s*"([^"]+)"\s+"((?:[^"\\]|\\.)*)"`)

// gameText decodes a game text file: UTF-8 (BOM optional) or UTF-16LE with a BOM.
func gameText(b []byte) string {
	if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
		u := make([]uint16, (len(b)-2)/2)
		for i := range u {
			u[i] = uint16(b[2+2*i]) | uint16(b[3+2*i])<<8
		}
		return string(utf16.Decode(u))
	}
	return strings.TrimPrefix(string(b), "\ufeff")
}

// buildSkinsCatalog is skins.json: every finish with a preview, named from
// items_game.txt and csgo_english.txt.
func buildSkinsCatalog(_, _ string, v *gamefiles.VPK) ([]byte, error) {
	raw, err := v.Read("scripts/items/items_game.txt")
	if err != nil {
		return nil, err
	}
	nodes, err := parseKeyValues(gameText(raw))
	if err != nil {
		return nil, fmt.Errorf("items_game.txt: %w", err)
	}
	root := findKV(nodes, func(n *kvNode) bool { return strings.EqualFold(n.Key, "items_game") })
	if root == nil {
		return nil, fmt.Errorf("items_game.txt has no items_game block")
	}
	english := map[string]string{}
	if b, err := v.Read("resource/csgo_english.txt"); err == nil {
		for _, m := range englishLine.FindAllStringSubmatch(gameText(b), -1) {
			if _, ok := english[strings.ToLower(m[1])]; !ok {
				english[strings.ToLower(m[1])] = m[2]
			}
		}
	}
	tr := func(token string) *string {
		if token == "" {
			return nil
		}
		if s, ok := english[strings.ToLower(strings.TrimPrefix(token, "#"))]; ok {
			return &s
		}
		return &token
	}
	blocks := func(key string) []*kvNode {
		var out []*kvNode
		for _, c := range root.Children {
			if c.IsBlock && strings.EqualFold(c.Key, key) {
				out = append(out, c)
			}
		}
		return out
	}
	value := func(n *kvNode, key string) string {
		if c := n.child(key); c != nil && !c.IsBlock {
			return c.Value
		}
		return ""
	}

	type kit struct {
		id        int
		name, tag string
	}
	kits := map[string]kit{}
	for _, b := range blocks("paint_kits") {
		for _, pk := range b.Children {
			id, err := strconv.Atoi(pk.Key)
			if name := value(pk, "name"); pk.IsBlock && name != "" && err == nil {
				kits[strings.ToLower(name)] = kit{id, name, value(pk, "description_tag")}
			}
		}
	}
	rarity := map[string]string{}
	for _, b := range blocks("paint_kits_rarity") {
		for _, r := range b.Children {
			rarity[strings.ToLower(r.Key)] = r.Value
		}
	}
	prefabs := map[string]*kvNode{}
	for _, b := range blocks("prefabs") {
		for _, pf := range b.Children {
			if pf.IsBlock {
				prefabs[strings.ToLower(pf.Key)] = pf
			}
		}
	}
	var itemName func(n *kvNode, depth int) string
	itemName = func(n *kvNode, depth int) string {
		if own := value(n, "item_name"); own != "" || depth > 8 {
			return own
		}
		for _, parent := range strings.Fields(value(n, "prefab")) {
			if pf, ok := prefabs[strings.ToLower(parent)]; ok {
				if found := itemName(pf, depth+1); found != "" {
					return found
				}
			}
		}
		return ""
	}
	type weapon struct {
		name    string
		display *string
	}
	weapons := map[string]weapon{}
	for _, b := range blocks("items") {
		for _, it := range b.Children {
			if name := value(it, "name"); it.IsBlock && name != "" {
				weapons[strings.ToLower(name)] = weapon{name, tr(itemName(it, 0))}
			}
		}
	}
	classes := make([]string, 0, len(weapons))
	for _, w := range weapons {
		classes = append(classes, w.name)
	}
	sort.Slice(classes, func(i, j int) bool { return len(classes[i]) > len(classes[j]) })

	have := map[string]bool{}
	for _, p := range v.List(skinPreviews, "_png.vtex_c") {
		have[p] = true
	}
	var skins []CatalogSkin
	for _, p := range v.List(skinPreviews, "_light_png.vtex_c") {
		stem := strings.TrimSuffix(strings.TrimPrefix(p, skinPreviews), "_light_png.vtex_c")
		var cls string
		var k kit
		for _, c := range classes {
			if rest, ok := strings.CutPrefix(stem, c+"_"); ok {
				if found, ok := kits[strings.ToLower(rest)]; ok {
					cls, k = c, found
					break
				}
			}
		}
		if cls == "" {
			continue // pets and other previews with no paint kit
		}
		w := weapons[strings.ToLower(cls)]
		s := CatalogSkin{Weapon: w.name, WeaponName: w.display, PaintKit: k.id, PaintKitName: k.name, Name: tr(k.tag), Image: stem + ".webp"}
		if r, ok := rarity[strings.ToLower(k.name)]; ok {
			s.Rarity = &r
		}
		if have[skinPreviews+stem+"_medium_png.vtex_c"] {
			s.ImageMedium = stem + "_medium.webp"
		}
		if have[skinPreviews+stem+"_heavy_png.vtex_c"] {
			s.ImageHeavy = stem + "_heavy.webp"
		}
		skins = append(skins, s)
	}
	if len(skins) == 0 {
		return nil, fmt.Errorf("no skins matched a paint kit")
	}
	sort.Slice(skins, func(i, j int) bool {
		if skins[i].Weapon != skins[j].Weapon {
			return skins[i].Weapon < skins[j].Weapon
		}
		return skins[i].PaintKit < skins[j].PaintKit
	})
	return jsonIndent(map[string]any{"skins": skins})
}
