package csm

import (
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// Issue #106: after `su <user>` the environment still names root's runtime
// dir, whose bus refuses the user. csm uses the user's own.
func TestUserBusEnvIgnoresAnotherUsersRuntimeDir(t *testing.T) {
	got := envMap(userBusEnv([]string{"PATH=/usr/bin", "XDG_RUNTIME_DIR=/run/user/0", "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/0/bus"}, 1000))
	if got["XDG_RUNTIME_DIR"] != "/run/user/1000" || got["DBUS_SESSION_BUS_ADDRESS"] != "unix:path=/run/user/1000/bus" {
		t.Fatalf("got %v", got)
	}
	if got["PATH"] != "/usr/bin" {
		t.Fatalf("other variables must stay: %v", got)
	}
}

func TestUserBusEnvFillsMissingAndKeepsOwn(t *testing.T) {
	got := envMap(userBusEnv([]string{"HOME=/home/cs2"}, 1001))
	if got["XDG_RUNTIME_DIR"] != "/run/user/1001" || got["DBUS_SESSION_BUS_ADDRESS"] != "unix:path=/run/user/1001/bus" {
		t.Fatalf("missing: got %v", got)
	}
	own := envMap(userBusEnv([]string{"XDG_RUNTIME_DIR=/run/user/1001", "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1001/bus,guid=abc"}, 1001))
	if own["DBUS_SESSION_BUS_ADDRESS"] != "unix:path=/run/user/1001/bus,guid=abc" {
		t.Fatalf("own bus must stay: got %v", own)
	}
	if n := strings.Count(strings.Join(userBusEnv([]string{"XDG_RUNTIME_DIR=/run/user/0"}, 5), "\n"), "XDG_RUNTIME_DIR="); n != 1 {
		t.Fatalf("XDG_RUNTIME_DIR set %d times", n)
	}
}
