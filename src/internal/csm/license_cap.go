package csm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/hostagent"
)

// The cap per platform
//
// A host that runs servers for someone else's platform (a hosting provider)
// can cap how many of its servers that platform may create here: "you get 5
// of my 40". Creates the platform asks for above the cap are refused with
// platform_cap; servers the operator creates locally (`csm`, the TUI) are not
// capped. Stored in <csm root>/platform_caps.json, keyed by the platform link
// ("default" for the one link a host has today).

// DefaultPlatformLink names the host's platform link.
const DefaultPlatformLink = "default"

type platformCaps struct {
	Caps map[string]int `json:"caps"`
}

func platformCapsPath() string { return filepath.Join(ResolveRoot(), "platform_caps.json") }

func loadPlatformCaps() platformCaps {
	var c platformCaps
	if data, err := os.ReadFile(platformCapsPath()); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	if c.Caps == nil {
		c.Caps = map[string]int{}
	}
	return c
}

// PlatformCap is the cap for a platform link, or -1 for none.
func PlatformCap(link string) int {
	if n, ok := loadPlatformCaps().Caps[link]; ok && n >= 0 {
		return n
	}
	return -1
}

// SetPlatformCap sets (n >= 0) or removes (n < 0) the cap for a platform link.
func SetPlatformCap(link string, n int) error {
	c := loadPlatformCaps()
	if n < 0 {
		delete(c.Caps, link)
	} else {
		c.Caps[link] = n
	}
	if err := writeJSONAtomicMode(platformCapsPath(), c, 0o644); err != nil {
		return err
	}
	if canChown() {
		_ = ensureOwnedByUser(licenseCS2User(), platformCapsPath())
	}
	return nil
}

// CheckPlatformCap refuses a platform's create above its cap on this host.
// `current` is the servers that platform has here.
func CheckPlatformCap(link string, current, adding int) error {
	limit := PlatformCap(link)
	if limit < 0 || current+adding <= limit {
		return nil
	}
	return &LicenseLimitError{Code: "platform_cap", Message: fmt.Sprintf(
		"this host lets your platform run at most %d server(s) here and %d are set up; ask the host's operator to raise the cap", limit, current)}
}

// inventoryLicense is host.inventory.license: this host's own license (when
// it has one) and the platform's cap here. Nil when there is nothing to say.
func inventoryLicense() *hostagent.InvLicense {
	out := &hostagent.InvLicense{}
	if s, err := LoadLicenseSettings(); err == nil && s.Key != "" && s.Source != LicenseSourcePlatform {
		if p := verifyQuiet(s.Key, nil, time.Now()); p != nil && !p.Lease {
			out.Own, out.LicenseID = true, p.ID
		}
	}
	if n := PlatformCap(DefaultPlatformLink); n >= 0 {
		out.Cap = &n
	}
	if !out.Own && out.Cap == nil {
		return nil
	}
	return out
}
