package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

func TestExtractForce(t *testing.T) {
	for _, tt := range []struct {
		in    []string
		force bool
		rest  []string
	}{
		{nil, false, nil},
		{[]string{"2"}, false, []string{"2"}},
		{[]string{"2", "--force"}, true, []string{"2"}},
		{[]string{"--force", "--alternate", "2"}, true, []string{"--alternate", "2"}},
		{[]string{"-force"}, true, nil},
	} {
		force, rest := extractForce(tt.in)
		if force != tt.force || !reflect.DeepEqual(rest, tt.rest) {
			t.Errorf("extractForce(%q) = %v %q, want %v %q", tt.in, force, rest, tt.force, tt.rest)
		}
	}
}

func TestPrintStatusJSON(t *testing.T) {
	no := false
	rows := []csm.FleetRow{
		{Target: csm.FleetTarget{Server: 1, GamePort: 27015, Running: true}, StatusPort: 27022, State: csm.ReadyUpOK,
			Status: &csm.ReadyUpStatus{UpdateSafe: &no, Summary: csm.ReadyUpSummary{Mode: "match", Phase: "live", Round: 3}}},
		{Target: csm.FleetTarget{Server: 2, GamePort: 27025, Running: true}, StatusPort: 27032, State: csm.ReadyUpNone},
	}
	var buf bytes.Buffer
	if err := printStatusJSON(&buf, rows); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 2 || got[0]["phase"] != "live R3" || got[0]["update_safe"] != false || got[1]["readyup"] != "none" {
		t.Fatalf("unexpected JSON:\n%s", buf.String())
	}
	if _, ok := got[1]["update_safe"]; ok {
		t.Fatal("a server without Ready Up must not claim an update_safe value")
	}
}
