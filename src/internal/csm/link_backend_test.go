package csm

import (
	"context"
	"strings"
	"testing"

	"github.com/sivert-io/cs2-server-manager/src/internal/hostagent"
)

// fakeLinkHost is a host with servers 1..n and nothing behind them.
type fakeLinkHost struct {
	n       int
	started []int
	removed int
	hold    string
	updated []int
}

func (f *fakeLinkHost) Servers(context.Context) ([]hostagent.ServerState, error) {
	out := make([]hostagent.ServerState, f.n)
	for i := range out {
		out[i].Number = i + 1
	}
	return out, nil
}
func (f *fakeLinkHost) Host(context.Context) (hostagent.HostFacts, error) { return hostagent.HostFacts{}, nil }
func (f *fakeLinkHost) Start(_ context.Context, n int, _ string) error   { f.started = append(f.started, n); return nil }
func (f *fakeLinkHost) Stop(context.Context, int, int) error              { return nil }
func (f *fakeLinkHost) Restart(context.Context, int) error                { return nil }
func (f *fakeLinkHost) CreateServer(context.Context, func(string) error) (int, string, error) {
	f.n++
	return f.n, "", nil
}
func (f *fakeLinkHost) RemoveLastServer(context.Context) (string, error) { f.n--; f.removed++; return "", nil }
func (f *fakeLinkHost) UpdateGame(_ context.Context, s []int, _ func(string)) (string, error) {
	f.updated = s
	return "", nil
}
func (f *fakeLinkHost) InstallReadyUp(context.Context, int, hostagent.ReadyUpPlan) (string, error) { return "", nil }
func (f *fakeLinkHost) SetUpdatesHold(mode string) error                                     { f.hold = mode; return nil }
func (f *fakeLinkHost) LogFile(int, string) (string, error)                                  { return "", nil }
func (f *fakeLinkHost) ChownToCS2User(string) error                                          { return nil }
func (f *fakeLinkHost) AdoptLicenseAnswer(string) (bool, error)                              { return false, nil }
func (f *fakeLinkHost) FirstServersGamePort() int                                            { return 27015 }
func (f *fakeLinkHost) BootstrapFirstServers(context.Context, int, func(string) error, func(string, int)) ([]int, string, error) {
	return nil, "", nil
}
func (f *fakeLinkHost) FirstServerGamePort() (int, bool)                       { return 0, false }
func (f *fakeLinkHost) ReadyUpBundleFor(r string) string                       { return r }
func (f *fakeLinkHost) ReadyUpCurrent(int, hostagent.ReadyUpPlan) (bool, string) { return false, "" }

func TestLinkBackendOwnsWhatItCreates(t *testing.T) {
	t.Setenv("CSM_ROOT", t.TempDir())
	t.Setenv("CSM_LICENSE_CHECKIN_URL", "off")
	host := &fakeLinkHost{n: 2}
	a := &LinkBackend{inner: host, link: DefaultPlatformLink}
	b := &LinkBackend{inner: host, link: "club-b"}
	ctx := context.Background()

	// Servers made before the second link belong to the first.
	if s, _ := a.Servers(ctx); len(s) != 2 {
		t.Fatalf("default sees %d servers", len(s))
	}
	if s, _ := b.Servers(ctx); len(s) != 0 {
		t.Fatalf("club-b sees %d servers", len(s))
	}
	n, _, err := b.CreateServer(ctx, nil)
	if err != nil || n != 3 || ServerOwner(3) != "club-b" {
		t.Fatalf("create: n=%d owner=%s err=%v", n, ServerOwner(3), err)
	}
	if s, _ := b.Servers(ctx); len(s) != 1 || s[0].Number != 3 {
		t.Fatalf("club-b sees %+v", s)
	}
	// Neither touches the other's servers.
	if err := b.Start(ctx, 1, ""); err == nil || !strings.Contains(err.Error(), "another platform") {
		t.Fatalf("club-b started server-1: %v", err)
	}
	if err := a.Stop(ctx, 3, 5); err == nil {
		t.Fatal("default stopped club-b's server")
	}
	// Removing goes from the end, so only the owner of the last server can.
	if _, err := a.RemoveLastServer(ctx); err == nil {
		t.Fatal("default removed club-b's last server")
	}
	if _, err := b.RemoveLastServer(ctx); err != nil || host.removed != 1 || ServerOwner(3) != DefaultPlatformLink {
		t.Fatalf("club-b remove: %v", err)
	}
	// Updates only touch the platform's own servers.
	_, _ = a.UpdateGame(ctx, nil, nil)
	if len(host.updated) != 2 {
		t.Fatalf("default updated %v", host.updated)
	}
}

func TestLinkBackendCapAndHolds(t *testing.T) {
	t.Setenv("CSM_ROOT", t.TempDir())
	host := &fakeLinkHost{n: 0}
	b := &LinkBackend{inner: host, link: "club-b"}
	ctx := context.Background()
	if err := SetPlatformCap("club-b", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.CreateServer(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.CreateServer(ctx, nil); err == nil || !strings.Contains(err.Error(), "at most 1") {
		t.Fatalf("cap not applied: %v", err)
	}
	// The host holds updates while any platform asks it to.
	a := &LinkBackend{inner: host, link: DefaultPlatformLink}
	_ = a.SetUpdatesHold(HoldModeOn)
	_ = b.SetUpdatesHold(HoldModeOff)
	if host.hold != HoldModeOn {
		t.Fatalf("hold = %s", host.hold)
	}
}
