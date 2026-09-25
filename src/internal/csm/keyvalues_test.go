package csm

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseActiveDutyMapsFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/gamemodes.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseActiveDutyMaps(string(data))
	if err != nil {
		t.Fatalf("ParseActiveDutyMaps: %v", err)
	}
	want := []string{"de_ancient", "de_anubis", "de_dust2", "de_inferno", "de_mirage", "de_nuke", "de_overpass"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("active duty = %v, want %v", got, want)
	}
}

func TestParseActiveDutyMapsErrors(t *testing.T) {
	cases := map[string]string{
		"no group":        `"GameModes.txt" { "mapgroups" { "mg_hostage" { "maps" { "cs_office" "" } } } }`,
		"only a mention":  `"GameModes.txt" { "mapgroupsMP" { "mg_active" "" } }`,
		"unbalanced":      `"GameModes.txt" { "mapgroups" { "mg_active" { "maps" { "de_nuke" "" } }`,
		"unterminated":    `"GameModes.txt" { "mapgroups`,
		"stray close":     `"a" "b" }`,
		"key has no val":  `"GameModes.txt"`,
		"brace no key":    `{ "a" "b" }`,
		"bad conditional": `"a" "b" [$WIN32`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseActiveDutyMaps(src); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestParseKeyValuesEscapesAndBareWords(t *testing.T) {
	src := "\ufeff" + `root { key "a \"quoted\" \\ value" bare word // trailing comment
	"nested" { "x" "1" } }`
	nodes, err := parseKeyValues(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Key != "root" || !nodes[0].IsBlock {
		t.Fatalf("unexpected root: %+v", nodes)
	}
	root := nodes[0]
	if v := root.child("KEY").Value; v != `a "quoted" \ value` {
		t.Fatalf("key = %q", v)
	}
	if v := root.child("bare").Value; v != "word" {
		t.Fatalf("bare = %q", v)
	}
	if n := root.child("nested"); n == nil || n.child("x").Value != "1" {
		t.Fatalf("nested = %+v", n)
	}
}

func TestParseBuildIDAndPatchVersion(t *testing.T) {
	acf := `"AppState"
{
	"appid"		"730"
	"name"		"Counter-Strike 2"
	"buildid"		"20123456"
	"InstalledDepots"
	{
		"2347771"
		{
			"manifest"		"123"
		}
	}
}`
	if got := parseBuildID(acf); got != "20123456" {
		t.Fatalf("buildid = %q", got)
	}
	if got := parseBuildID("not { valid"); got != "" {
		t.Fatalf("buildid from junk = %q", got)
	}

	inf := strings.Join([]string{"ClientVersion=2000701", "ServerVersion=2000701", "PatchVersion=1.41.1.4", "ProductName=cs2"}, "\r\n")
	if got := parsePatchVersion(inf); got != "1.41.1.4" {
		t.Fatalf("patch = %q", got)
	}
	if got := parsePatchVersion("ProductName=cs2"); got != "" {
		t.Fatalf("patch = %q", got)
	}
}
