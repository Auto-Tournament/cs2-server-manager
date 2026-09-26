package csm

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestParseCIArgs(t *testing.T) {
	cmd, err := ParseCIArgs([]string{"setup", "--token", "AAAATOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Action != "setup" || cmd.Token != "AAAATOKEN" || cmd.Repo != CIDefaultRepo || cmd.Port != CIDefaultPort || cmd.Dir != "" {
		t.Fatalf("defaults: %+v", cmd)
	}

	cmd, err = ParseCIArgs([]string{"setup", "--token=T0K", "--repo", "me/fork", "--dir", "/srv/ci", "--port", "28000"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Repo != "me/fork" || cmd.Dir != "/srv/ci" || cmd.Port != 28000 {
		t.Fatalf("flags: %+v", cmd)
	}

	cmd, err = ParseCIArgs([]string{"remove", "--purge", "--token", "RM"})
	if err != nil || !cmd.Purge || cmd.Token != "RM" {
		t.Fatalf("remove: %+v %v", cmd, err)
	}
	if cmd, err = ParseCIArgs([]string{"status"}); err != nil || cmd.Action != "status" {
		t.Fatalf("status: %+v %v", cmd, err)
	}
	if _, err = ParseCIArgs([]string{"help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help: %v", err)
	}

	bad := [][]string{
		nil,
		{"frobnicate"},
		{"setup"}, // no token
		{"setup", "--token", "x", "--repo", "nope"},  // not owner/name
		{"setup", "--token", "x", "--port", "80"},    // privileged port
		{"setup", "--token", "x", "--port", "70000"}, // out of range
		{"setup", "--token", "x", "extra"},
		{"status", "--token", "x"}, // status takes no token
		{"update", "--purge"},
	}
	for _, args := range bad {
		if _, err := ParseCIArgs(args); err == nil {
			t.Errorf("ParseCIArgs(%q) = nil error", args)
		}
	}
}

func TestParseCIArgsKeepsTokenOutOfErrors(t *testing.T) {
	_, err := ParseCIArgs([]string{"setup", "--token", "SECRETTOKEN123", "--port", "SECRETTOKEN123"})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "SECRETTOKEN123") {
		t.Fatalf("token leaked: %v", err)
	}
}

func TestRedactSecret(t *testing.T) {
	if got := RedactSecret("config.sh --token ABC123 failed ABC123", "ABC123"); got != "config.sh --token *** failed ***" {
		t.Fatalf("got %q", got)
	}
	if got := RedactSecret("nothing here", ""); got != "nothing here" {
		t.Fatalf("empty secret changed output: %q", got)
	}
}

func TestRedactWriterSplitWrites(t *testing.T) {
	var out strings.Builder
	rw := newRedactWriter(&out, "SECRETTOKEN")
	for _, chunk := range []string{"using SECR", "ETTOKEN now\nprogress\r", "tail SECRETTOKEN"} {
		if _, err := rw.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	rw.Flush()
	if strings.Contains(out.String(), "SECRETTOKEN") {
		t.Fatalf("token leaked: %q", out.String())
	}
	if out.String() != "using *** now\nprogress\rtail ***" {
		t.Fatalf("got %q", out.String())
	}
}

func TestResolveCIDir(t *testing.T) {
	home := "/home/cs2servermanager"
	state := "/opt/cs2-server-manager"
	ok := map[string]string{
		"":             home + "/ru-ci",
		"~/ru-ci":      home + "/ru-ci",
		"~/ci/cs2/":    home + "/ci/cs2",
		"/srv/ru-ci":   "/srv/ru-ci",
		"/data/ci-cs2": "/data/ci-cs2",
	}
	for raw, want := range ok {
		got, err := resolveCIDir(raw, home, state)
		if err != nil || got != want {
			t.Errorf("resolveCIDir(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		"relative/dir",
		"/",
		"~",
		home,
		"~/server-1",
		"~/server-2/game",
		"~/master-install",
		"~/actions-runner-readyup/cs2",
		state + "/ci",
		"/srv/ru ci",
	} {
		if got, err := resolveCIDir(raw, home, state); err == nil {
			t.Errorf("resolveCIDir(%q) = %q, want error", raw, got)
		}
	}
}

func TestCIRunnerName(t *testing.T) {
	cases := map[string]string{
		"box1":           "box1-readyup-live",
		"box 1.example":  "box-1.example-readyup-live",
		"":               "csm-readyup-live",
		"--weird--host.": "weird--host-readyup-live",
	}
	for in, want := range cases {
		if got := ciRunnerName(in); got != want {
			t.Errorf("ciRunnerName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderCIRunnerUnit(t *testing.T) {
	unit := RenderCIRunnerUnit("/home/cs2servermanager/actions-runner-readyup")
	for _, want := range []string{
		"WorkingDirectory=/home/cs2servermanager/actions-runner-readyup\n",
		"ExecStart=/home/cs2servermanager/actions-runner-readyup/run.sh\n",
		"KillMode=process\n",
		"Restart=always\n",
		"WantedBy=default.target\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "User=") {
		t.Error("a user unit must not set User=")
	}
}

func TestRenderCIRunnerEnv(t *testing.T) {
	existing := "LANG=C.UTF-8\nCS2_CI_DIR=/old\nJAVA_HOME=/usr/lib/jvm\r\nCS2_CI_PORT=1\n\n"
	got := RenderCIRunnerEnv(existing, "/home/u/ru-ci", 27095)
	want := "LANG=C.UTF-8\nJAVA_HOME=/usr/lib/jvm\nCS2_CI_DIR=/home/u/ru-ci\nCS2_CI_PORT=27095\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	got = RenderCIRunnerEnv("", "/d", 1234)
	if got != "CS2_CI_DIR=/d\nCS2_CI_PORT=1234\n" {
		t.Fatalf("empty: %q", got)
	}
	vars := parseEnvFile(got)
	if vars["CS2_CI_DIR"] != "/d" || vars["CS2_CI_PORT"] != "1234" {
		t.Fatalf("parseEnvFile: %v", vars)
	}
}

func TestUserBusEnv(t *testing.T) {
	got := userBusEnv([]string{"PATH=/bin"}, 998)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "XDG_RUNTIME_DIR=/run/user/998") ||
		!strings.Contains(joined, "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/998/bus") {
		t.Fatalf("got %v", got)
	}
	got = userBusEnv([]string{"XDG_RUNTIME_DIR=/run/user/5", "DBUS_SESSION_BUS_ADDRESS=x"}, 998)
	if len(got) != 2 {
		t.Fatalf("existing session env should be kept as is: %v", got)
	}
}

func TestPickRunnerAsset(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	rel := runnerRelease{
		TagName: "v2.330.0",
		Body:    "notes\n<!-- BEGIN SHA linux-x64 -->" + sha + "<!-- END SHA linux-x64 -->\n",
	}
	rel.Assets = append(rel.Assets,
		struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		}{Name: "actions-runner-linux-arm64-2.330.0.tar.gz", URL: "arm"},
		struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		}{Name: "actions-runner-linux-x64-2.330.0.tar.gz", URL: "x64", Digest: "sha256:" + strings.Repeat("cd", 32)},
	)
	name, url, got, err := pickRunnerAsset(rel)
	if err != nil || name != "actions-runner-linux-x64-2.330.0.tar.gz" || url != "x64" || got != sha {
		t.Fatalf("got %q %q %q %v", name, url, got, err)
	}
	rel.Body = "no hashes"
	if _, _, got, _ := pickRunnerAsset(rel); got != strings.Repeat("cd", 32) {
		t.Fatalf("digest fallback: %q", got)
	}
	rel.TagName = "v9.9.9"
	if _, _, _, err := pickRunnerAsset(rel); err == nil {
		t.Fatal("want error for a missing asset")
	}
}

func TestCIPrivilegeError(t *testing.T) {
	if err := ciPrivilegeError(0, "root", "cs2servermanager"); err == nil || !strings.Contains(err.Error(), "sudo -iu cs2servermanager") {
		t.Fatalf("root: %v", err)
	}
	if err := ciPrivilegeError(998, "cs2servermanager", "cs2servermanager"); err != nil {
		t.Fatalf("cs2 user: %v", err)
	}
	if err := ciPrivilegeError(1000, "alice", "cs2servermanager"); err == nil {
		t.Fatal("other user should be refused")
	}
}
