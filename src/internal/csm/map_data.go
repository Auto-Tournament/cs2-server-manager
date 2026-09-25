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
	"time"
	"unicode"
	"unicode/utf8"
)

// MapsManifestFile is written next to the images in map_thumbnails/ so the
// platform knows which maps exist and which are in the Active Duty pool.
const MapsManifestFile = "maps.json"

// MapsManifest is the content of map_thumbnails/maps.json.
type MapsManifest struct {
	GeneratedAt  string     `json:"generatedAt"`
	PatchVersion string     `json:"patchVersion"`
	BuildID      string     `json:"buildId"`
	Maps         []MapEntry `json:"maps"`
	ActiveDuty   []string   `json:"activeDuty"`
}

// MapEntry describes one playable map.
type MapEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Mode is defusal (de_), hostage (cs_), armsrace (ar_) or other.
	Mode string `json:"mode"`
	// Images is omitted when the game ships no screenshot for the map.
	Images   *MapImages `json:"images,omitempty"`
	Variants []string   `json:"variants"`
}

// MapImages names the image files for a map, relative to map_thumbnails/.
type MapImages struct {
	Full  string `json:"full,omitempty"`
	Thumb string `json:"thumb,omitempty"`
}

var mapIDPrefixes = []string{"de_", "cs_", "ar_"}

func hasMapPrefix(id string) bool {
	for _, p := range mapIDPrefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// mapModeFromID maps an id prefix to its game mode.
func mapModeFromID(id string) string {
	switch {
	case strings.HasPrefix(id, "de_"):
		return "defusal"
	case strings.HasPrefix(id, "cs_"):
		return "hostage"
	case strings.HasPrefix(id, "ar_"):
		return "armsrace"
	}
	return "other"
}

// mapNameSpecialCases matches mapIdToDisplayName in the Auto Tournament
// platform (api/src/integrations/cs2/maps/fetchCS2Maps.ts); keep them in sync.
var mapNameSpecialCases = map[string]string{
	"dust2":         "Dust II",
	"shortdust":     "Shortdust",
	"pool_day":      "Pool Day",
	"ancient_night": "Ancient (Night)",
	"shoots_night":  "Shoots (Night)",
}

// MapDisplayName turns a map id into a display name the same way the
// platform does: de_dust2 -> Dust II, de_ancient -> Ancient.
func MapDisplayName(id string) string {
	name := id
	for _, p := range mapIDPrefixes {
		if strings.HasPrefix(name, p) {
			name = strings.TrimPrefix(name, p)
			break
		}
	}
	if s, ok := mapNameSpecialCases[name]; ok {
		return s
	}
	words := strings.Split(name, "_")
	for i, w := range words {
		if w == "" {
			continue
		}
		r, size := utf8.DecodeRuneInString(w)
		words[i] = string(unicode.ToUpper(r)) + w[size:]
	}
	return strings.Join(words, " ")
}

var (
	imageExtRe      = regexp.MustCompile(`(?i)\.(png|jpg|jpeg|gif|webp)$`)
	numberSuffixRe  = regexp.MustCompile(`_[0-9]+$`)
	mapVPKArchiveRe = regexp.MustCompile(`_[0-9]{3}$`)
)

// mapIDFromImageName returns the map id an image file belongs to
// (de_dust2_1_thumb.webp -> de_dust2), or "" for non-map images such as
// lobby_mapveto.png and random.png. It matches extractMapId in the platform.
func mapIDFromImageName(name string) string {
	if !imageExtRe.MatchString(name) {
		return ""
	}
	base := imageExtRe.ReplaceAllString(name, "")
	if base == "lobby_mapveto" || base == "random" {
		return ""
	}
	base = strings.TrimSuffix(base, "_thumb")
	base = numberSuffixRe.ReplaceAllString(base, "")
	if !hasMapPrefix(base) {
		return ""
	}
	return base
}

// mapIDFromVPKName returns the map id for a file in game/csgo/maps, or "" for
// files that are not playable maps (vanity scenes, graphics_settings,
// lobby_mapveto, workshop_preview, VPK archive chunks).
func mapIDFromVPKName(name string) string {
	if !strings.EqualFold(filepath.Ext(name), ".vpk") {
		return ""
	}
	id := strings.TrimSuffix(name, filepath.Ext(name))
	id = strings.TrimSuffix(id, "_dir")
	if !hasMapPrefix(id) || mapVPKArchiveRe.MatchString(id) {
		return ""
	}
	for _, skip := range []string{"_vanity", "_preview", "graphics_settings", "lobby_mapveto", "workshop_preview"} {
		if strings.Contains(id, skip) {
			return ""
		}
	}
	return id
}

// mapManifestInput is everything buildMapsManifest reads from disk, so the
// manifest logic can be unit tested without a CS2 install.
type mapManifestInput struct {
	MapVPKs    []string // file names in game/csgo/maps
	ImageFiles []string // file names in map_thumbnails
	ActiveDuty []string
	PatchVer   string
	BuildID    string
	Now        time.Time
}

func buildMapsManifest(in mapManifestInput) MapsManifest {
	images := make(map[string]bool, len(in.ImageFiles))
	ids := map[string]bool{}
	for _, f := range in.ImageFiles {
		images[f] = true
		if id := mapIDFromImageName(f); id != "" {
			ids[id] = true
		}
	}
	for _, f := range in.MapVPKs {
		if id := mapIDFromVPKName(f); id != "" {
			ids[id] = true
		}
	}
	for _, id := range in.ActiveDuty {
		ids[id] = true
	}

	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)

	m := MapsManifest{
		GeneratedAt:  in.Now.UTC().Format(time.RFC3339),
		PatchVersion: in.PatchVer,
		BuildID:      in.BuildID,
		Maps:         make([]MapEntry, 0, len(sorted)),
		ActiveDuty:   append([]string{}, in.ActiveDuty...),
	}
	for _, id := range sorted {
		e := MapEntry{ID: id, Name: MapDisplayName(id), Mode: mapModeFromID(id), Variants: mapVariants(id, in.ImageFiles)}
		img := MapImages{}
		if images[id+".webp"] {
			img.Full = id + ".webp"
		}
		if images[id+"_thumb.webp"] {
			img.Thumb = id + "_thumb.webp"
		}
		if img.Full != "" || img.Thumb != "" {
			e.Images = &img
		}
		m.Maps = append(m.Maps, e)
	}
	return m
}

// mapVariants lists <id>_<n>_thumb.webp files, ordered by n.
func mapVariants(id string, files []string) []string {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(id) + `_([0-9]+)_thumb\.webp$`)
	type variant struct {
		n    int
		name string
	}
	var vs []variant
	for _, f := range files {
		if sm := re.FindStringSubmatch(f); sm != nil {
			n, _ := strconv.Atoi(sm[1])
			vs = append(vs, variant{n, f})
		}
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].n < vs[j].n })
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.name)
	}
	return out
}

// readMapsManifestInput gathers the manifest inputs from a CS2 install.
// gamemodesPath is the gamemodes.txt extracted from pak01_dir.vpk; a loose
// game/csgo/gamemodes.txt is used when it is missing. Problems that only make
// the manifest less complete are returned as warnings.
func readMapsManifestInput(masterDir, csgoDir, gamemodesPath, thumbsDir string) (mapManifestInput, []string) {
	in := mapManifestInput{Now: time.Now()}
	var warnings []string

	if entries, err := os.ReadDir(filepath.Join(csgoDir, "maps")); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				in.MapVPKs = append(in.MapVPKs, e.Name())
			}
		}
	} else {
		warnings = append(warnings, fmt.Sprintf("cannot list map VPKs: %v", err))
	}

	if entries, err := os.ReadDir(thumbsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				in.ImageFiles = append(in.ImageFiles, e.Name())
			}
		}
	}

	var gm []byte
	var gmErr error
	for _, p := range []string{gamemodesPath, filepath.Join(csgoDir, "gamemodes.txt")} {
		if gm, gmErr = os.ReadFile(p); gmErr == nil {
			break
		}
	}
	if gmErr != nil {
		warnings = append(warnings, "gamemodes.txt not found in pak01_dir.vpk; activeDuty will be empty")
	} else if ad, err := ParseActiveDutyMaps(string(gm)); err != nil {
		warnings = append(warnings, fmt.Sprintf("%v; activeDuty will be empty", err))
	} else {
		in.ActiveDuty = ad
	}

	if b, err := os.ReadFile(filepath.Join(csgoDir, "steam.inf")); err == nil {
		in.PatchVer = parsePatchVersion(string(b))
	} else {
		warnings = append(warnings, fmt.Sprintf("cannot read steam.inf: %v", err))
	}
	if b, err := os.ReadFile(filepath.Join(masterDir, "steamapps", "appmanifest_730.acf")); err == nil {
		in.BuildID = parseBuildID(string(b))
	}
	return in, warnings
}

// writeMapsManifest writes m to dir/maps.json. When only generatedAt would
// change, the existing file is left alone so re-runs don't create noise in
// git. It reports whether the file was written.
func writeMapsManifest(dir string, m MapsManifest) (bool, error) {
	path := filepath.Join(dir, MapsManifestFile)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')

	if old, err := os.ReadFile(path); err == nil {
		var prev MapsManifest
		if json.Unmarshal(old, &prev) == nil {
			prev.GeneratedAt = m.GeneratedAt
			if cmp, err := json.MarshalIndent(prev, "", "  "); err == nil && bytes.Equal(append(cmp, '\n'), data) {
				return false, nil
			}
		}
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}
