package csm

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/hostagent"
)

// LinkBackend is what one platform link's agent sees of the host
// (platform_links.go): only the servers that platform owns, its own cap and
// license, and its own update hold. With a single link it owns every server
// and behaves exactly like HostBackend.
type LinkBackend struct {
	inner linkInner
	link  string
}

// linkInner is the host backend LinkBackend wraps (HostBackend; a fake in tests).
type linkInner interface {
	hostagent.Backend
	AdoptLicenseAnswer(use string) (bool, error)
	FirstServersGamePort() int
	BootstrapFirstServers(ctx context.Context, count int, beforeStart func(serverDir string) error, progress func(step string, pct int)) ([]int, string, error)
	FirstServerGamePort() (int, bool)
	ReadyUpBundleFor(requested string) string
	ReadyUpCurrent(n int, plan hostagent.ReadyUpPlan) (bool, string)
}

// createMu serializes creates and removes across links: server numbers come
// from the host's layout, so two platforms must not take the same next one.
var createMu sync.Mutex

// NewLinkBackend is the backend for one platform link's agent.
func NewLinkBackend(link string) *LinkBackend {
	if link == "" {
		link = DefaultPlatformLink
	}
	return &LinkBackend{inner: NewHostBackend(), link: link}
}

func (b *LinkBackend) owns(n int) bool { return ServerOwner(n) == b.link }

func (b *LinkBackend) mine(n int) error {
	if !b.owns(n) {
		return fmt.Errorf("server-%d belongs to another platform on this host", n)
	}
	return nil
}

func (b *LinkBackend) Servers(ctx context.Context) ([]hostagent.ServerState, error) {
	all, err := b.inner.Servers(ctx)
	if err != nil {
		return nil, err
	}
	out := all[:0:0]
	for _, s := range all {
		if b.owns(s.Number) {
			out = append(out, s)
		}
	}
	return out, nil
}

// Host is the machine facts, with this link's cap and the host's own license.
func (b *LinkBackend) Host(ctx context.Context) (hostagent.HostFacts, error) {
	f, err := b.inner.Host(ctx)
	if err != nil {
		return f, err
	}
	f.License = inventoryLicenseFor(b.link)
	return f, nil
}

func (b *LinkBackend) Start(ctx context.Context, server int, launchMode string) error {
	if err := b.mine(server); err != nil {
		return err
	}
	if err := CheckStart(linkStanding(b.link)); err != nil {
		return err
	}
	return b.inner.Start(ctx, server, launchMode)
}

func (b *LinkBackend) Stop(ctx context.Context, server int, graceS int) error {
	if err := b.mine(server); err != nil {
		return err
	}
	return b.inner.Stop(ctx, server, graceS)
}

func (b *LinkBackend) Restart(ctx context.Context, server int) error {
	if err := b.mine(server); err != nil {
		return err
	}
	if err := CheckStart(linkStanding(b.link)); err != nil {
		return err
	}
	return b.inner.Restart(ctx, server)
}

func (b *LinkBackend) ownedNumbers(ctx context.Context) []int {
	all, err := b.inner.Servers(ctx)
	if err != nil {
		return nil
	}
	nums := make([]int, 0, len(all))
	for _, s := range all {
		nums = append(nums, s.Number)
	}
	return ServersOwnedBy(b.link, nums)
}

// CreateServer: the host's cap for this platform and this platform's license
// first; the new server belongs to this link.
func (b *LinkBackend) CreateServer(ctx context.Context, beforeStart func(serverDir string) error) (int, string, error) {
	createMu.Lock()
	defer createMu.Unlock()
	if err := CheckPlatformCap(b.link, len(b.ownedNumbers(ctx)), 1); err != nil {
		return 0, "", err
	}
	if hostOwnKey() != "" {
		// The host's own license covers every server here: one pool, asked online.
		if err := GateCreate(ctx, 1); err != nil {
			return 0, "", err
		}
	} else if err := CheckCreate(linkStanding(b.link), len(b.ownedNumbers(ctx)), 1); err != nil {
		// This platform's license, for the servers it owns (the platform reserved against it already).
		return 0, "", err
	}
	n, out, err := b.inner.CreateServer(ctx, beforeStart)
	if err != nil {
		return n, out, err
	}
	if serr := SetServerOwner(n, b.link); serr != nil {
		out += fmt.Sprintf("\nwarning: could not record that server-%d belongs to this platform: %v\n", n, serr)
	}
	if b.link != DefaultPlatformLink {
		applyStoredLicenseToServer(discardWriter{}, licenseCS2User(), n)
	}
	return n, out, nil
}

// RemoveLastServer removes the host's highest-numbered server, only when this
// platform owns it (server numbers are contiguous on the host).
func (b *LinkBackend) RemoveLastServer(ctx context.Context) (string, error) {
	createMu.Lock()
	defer createMu.Unlock()
	all, err := b.inner.Servers(ctx)
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "", fmt.Errorf("no servers on this host")
	}
	nums := make([]int, 0, len(all))
	for _, s := range all {
		nums = append(nums, s.Number)
	}
	sort.Ints(nums)
	last := nums[len(nums)-1]
	if !b.owns(last) {
		return "", fmt.Errorf("the last server on this host (server-%d) belongs to another platform; servers are removed from the end", last)
	}
	out, err := b.inner.RemoveLastServer(ctx)
	if err == nil {
		_ = SetServerOwner(last, DefaultPlatformLink)
	}
	return out, err
}

// UpdateGame updates only this platform's servers (nil = all of them).
func (b *LinkBackend) UpdateGame(ctx context.Context, servers []int, progress func(step string)) (string, error) {
	if servers == nil {
		servers = b.ownedNumbers(ctx)
		if len(servers) == 0 {
			return "This platform has no servers on this host.", nil
		}
	}
	for _, n := range servers {
		if err := b.mine(n); err != nil {
			return "", err
		}
	}
	return b.inner.UpdateGame(ctx, servers, progress)
}

func (b *LinkBackend) InstallReadyUp(ctx context.Context, server int, plan hostagent.ReadyUpPlan) (string, error) {
	if err := b.mine(server); err != nil {
		return "", err
	}
	return b.inner.InstallReadyUp(ctx, server, plan)
}

// SetUpdatesHold records this platform's hold; the host holds while any does.
func (b *LinkBackend) SetUpdatesHold(mode string) error {
	return b.inner.SetUpdatesHold(CombinedHold(b.link, mode))
}

func (b *LinkBackend) LogFile(server int, source string) (string, error) {
	if server > 0 {
		if err := b.mine(server); err != nil {
			return "", err
		}
	}
	return b.inner.LogFile(server, source)
}

func (b *LinkBackend) ChownToCS2User(path string) error { return b.inner.ChownToCS2User(path) }

// ReceivePlatformLicense is host.license: this platform's license for its servers.
func (b *LinkBackend) ReceivePlatformLicense(lic hostagent.LicenseCmd) error {
	return receiveLinkLicense(b.link, lic, b.ownedNumbers(context.Background()))
}

// --- optional extensions, passed through -----------------------------------------

func (b *LinkBackend) AdoptLicenseAnswer(use string) (bool, error) {
	return b.inner.AdoptLicenseAnswer(use)
}

func (b *LinkBackend) FirstServersGamePort() int { return b.inner.FirstServersGamePort() }

// BootstrapFirstServers installs a host's first servers; they belong to this link.
func (b *LinkBackend) BootstrapFirstServers(ctx context.Context, count int, beforeStart func(serverDir string) error, progress func(step string, pct int)) ([]int, string, error) {
	createMu.Lock()
	defer createMu.Unlock()
	if err := CheckPlatformCap(b.link, 0, count); err != nil {
		return nil, "", err
	}
	nums, out, err := b.inner.BootstrapFirstServers(ctx, count, beforeStart, progress)
	for _, n := range nums {
		_ = SetServerOwner(n, b.link)
	}
	return nums, out, err
}

func (b *LinkBackend) FirstServerGamePort() (int, bool) { return b.inner.FirstServerGamePort() }

func (b *LinkBackend) ReadyUpBundleFor(requested string) string {
	return b.inner.ReadyUpBundleFor(requested)
}

func (b *LinkBackend) ReadyUpCurrent(n int, plan hostagent.ReadyUpPlan) (bool, string) {
	return b.inner.ReadyUpCurrent(n, plan)
}

// inventoryLicenseFor is host.inventory.license for one link: the host's own
// license, and the cap the host set for this platform.
func inventoryLicenseFor(link string) *hostagent.InvLicense {
	out := &hostagent.InvLicense{}
	if own := hostOwnKey(); own != "" {
		if p := verifyQuiet(own, nil, time.Now()); p != nil {
			out.Own, out.LicenseID = true, p.ID
		}
	}
	if n := PlatformCap(link); n >= 0 {
		out.Cap = &n
	}
	if !out.Own && out.Cap == nil {
		return nil
	}
	return out
}
