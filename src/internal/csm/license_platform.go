package csm

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/license"
)

// The license key from the Auto Tournament platform
//
// When an admin saves or clears the license key in the platform (Settings →
// License), csm picks it up from the update-hold poll it already makes
// (platform_hold.go) and applies it exactly as `csm license set <key>` /
// `csm license clear` would, on every server it manages. Nothing is pushed:
// the platform has no route to a game host.
//
// The answer's `license` object:
//
//	{"key": "ATL1...." | null, "revision": "sha256:<16 hex>" | "none"}
//
// `revision` changes exactly when the key is saved, replaced or cleared. csm
// records the revision it last applied to every server in license.json
// (`platform_revision`); a partial failure leaves it unrecorded, so the next
// poll tries again.
//
// Precedence:
//
//   - This host has its own genuine paid key (`csm license set`): it stays.
//     A hosting provider's servers run under the provider's license, and the
//     platform leaves them out of its own count (host inventory `license`).
//   - Otherwise the platform has a key: it is applied, replacing a key that
//     doesn't verify.
//   - The platform has no key: a key csm got from the platform is cleared;
//     a key an operator set by hand is left alone.
//   - The platform gave no answer (unreachable, older than the hand-off, or
//     it could not read its key): nothing changes.
//
// Nothing here is ever an error for the monitor. A failed hand-off is a
// warning in auto_update_monitor.log and is retried at the next poll. The
// key itself is never logged, only its license id or its last characters.

// LicenseSourcePlatform marks a key that came from the platform.
const LicenseSourcePlatform = "platform"

// licenseApplier writes a key into every server's config ("" removes it) and
// returns how many servers took it and how many failed. Swapped in tests.
type licenseApplier func(w io.Writer, key string) (done, failed int)

// licenseFingerprint names a key for a log line without printing it: the
// license id when it can be read, otherwise its last six characters.
func licenseFingerprint(key string) string {
	key = strings.TrimSpace(key)
	if p, ok := license.Peek(key); ok && strings.TrimSpace(p.ID) != "" {
		return "license " + p.ID
	}
	if len(key) > 6 {
		return "key …" + key[len(key)-6:]
	}
	return "key"
}

// SyncPlatformLicense applies the platform's license hand-off (see above).
// It never returns an error: everything it has to say goes to w.
func SyncPlatformLicense(w io.Writer, lic *PlatformLicense) {
	syncPlatformLicense(w, lic, applyLicenseToAllServers, licenseCS2User())
}

func syncPlatformLicense(w io.Writer, lic *PlatformLicense, apply licenseApplier, user string) {
	defer func() {
		// A bug here must not take the update monitor down with it.
		if r := recover(); r != nil {
			fmt.Fprintf(w, "License: warning: could not apply the platform's license key (%v); will try again at the next poll.\n", r)
		}
	}()
	if lic == nil {
		return
	}
	revision := strings.TrimSpace(lic.Revision)
	if revision == "" {
		fmt.Fprintln(w, "License: warning: the platform sent a license with no revision; ignoring it.")
		return
	}

	stored, err := LoadLicenseSettings()
	if err != nil {
		fmt.Fprintf(w, "License: warning: could not read the stored key (%v); will try again at the next poll.\n", err)
		return
	}

	key := ""
	if lic.Key != nil {
		key = strings.TrimSpace(*lic.Key)
	}

	if key == "" {
		// Cleared on the platform: only take back what the platform gave.
		if stored.Key == "" || stored.Source != LicenseSourcePlatform {
			return
		}
		done, failed := apply(w, "")
		if failed > 0 {
			fmt.Fprintf(w, "License: warning: the platform cleared the license key, but %d server(s) still have it; will try again at the next poll.\n", failed)
			return
		}
		next := LicenseSettings{Source: LicenseSourcePlatform, PlatformRevision: revision,
			SetAt: time.Now().UTC().Format(time.RFC3339)}
		if err := saveLicenseSettings(next, user); err != nil {
			fmt.Fprintf(w, "License: warning: removed the key from %d server(s), but could not update %s (%v); will try again at the next poll.\n", done, licenseSettingsPath(), err)
			return
		}
		fmt.Fprintf(w, "License: the platform cleared the license key; removed it from %d server(s).\n", done)
		return
	}

	// A host's own paid key (`csm license set`) is never overwritten: a
	// hosting provider's servers count toward the provider's license, not the
	// platform's, and their Ready Up keeps the provider's key.
	if stored.Key != "" && stored.Source != LicenseSourcePlatform && hostOwnsLicense(stored.Key) {
		if stored.PlatformRevision != revision {
			stored.PlatformRevision = revision
			if err := saveLicenseSettings(stored, user); err == nil {
				fmt.Fprintf(w, "License: keeping this host's own %s; the platform's key isn't applied here, and these servers count toward this host's license.\n", licenseFingerprint(stored.Key))
			}
		}
		return
	}

	if stored.Key == key && stored.Source == LicenseSourcePlatform && stored.PlatformRevision == revision {
		return // already applied everywhere
	}
	if !license.LooksLikeKey(key) {
		fmt.Fprintf(w, "License: warning: the platform sent something that is not a license key (%s); ignoring it.\n", licenseFingerprint(key))
		return
	}

	// Store first, so `csm license status` and regenerated configs
	// (update-config, bootstrap) already use it; the revision is recorded
	// only once every server has it.
	next := LicenseSettings{Key: key, Source: LicenseSourcePlatform,
		SetAt: time.Now().UTC().Format(time.RFC3339)}
	if err := saveLicenseSettings(next, user); err != nil {
		fmt.Fprintf(w, "License: warning: could not store the platform's license key in %s (%v); will try again at the next poll.\n", licenseSettingsPath(), err)
		return
	}
	done, failed := apply(w, key)
	if failed > 0 {
		fmt.Fprintf(w, "License: warning: handed the platform's %s to %d server(s), %d failed; will try again at the next poll.\n", licenseFingerprint(key), done, failed)
		return
	}
	storePlatformTerms(w, key, lic)
	next.PlatformRevision = revision
	if err := saveLicenseSettings(next, user); err != nil {
		fmt.Fprintf(w, "License: warning: could not record the applied revision in %s (%v); the key will be written again at the next poll.\n", licenseSettingsPath(), err)
		return
	}
	replaced := ""
	if stored.Key != "" && stored.Key != key && stored.Source != LicenseSourcePlatform {
		replaced = " (it replaces the key set with `csm license set`)"
	}
	fmt.Fprintf(w, "License: applied the platform's %s to %d server(s)%s.\n", licenseFingerprint(key), done, replaced)
}

// platformLicenseForCycle is the license hand-off for one monitor cycle. When
// the platform answered the hold question it is already in hold. When a
// manual `csm updates hold on|off` meant the platform was not asked about the
// hold, it is asked once, for the license only. Any failure is "no answer".
func platformLicenseForCycle(ctx context.Context, settings AutoUpdateSettings, hold UpdateHold) (*PlatformLicense, error) {
	if hold.Source == HoldSourcePlatform {
		return hold.License, nil
	}
	if hold.Source != HoldSourceManual || !settings.Platform.Configured() {
		return nil, nil
	}
	answer, err := FetchPlatformHold(ctx, settings.Platform)
	if err != nil {
		return nil, err
	}
	return answer.License, nil
}

// hostOwnsLicense reports whether key is a genuine paid key (not a lease),
// i.e. this host runs under a license of its own.
func hostOwnsLicense(key string) bool {
	p := verifyQuiet(key, nil, time.Now())
	return p != nil && !p.Lease
}

// storePlatformTerms keeps the platform's lease and license state for the
// key it handed over (license_checkin.json), so csm's own limits follow the
// platform's. Best-effort.
func storePlatformTerms(w io.Writer, key string, lic *PlatformLicense) {
	p := verifyQuiet(key, nil, time.Now())
	if p == nil || lic == nil {
		return
	}
	c := loadCheckinFile()
	c.LicenseID = p.ID
	c.LastAt = time.Now().UTC().Format(time.RFC3339)
	c.ServersElsewhere = 0
	c.Lease = leaseFor(p, lic.Lease)
	c.State = nil
	if lic.State != nil && lic.State.Status != "" {
		st := &CheckinState{Status: lic.State.Status}
		if lic.State.StopsOn != nil {
			st.StopsOn = *lic.State.StopsOn
		}
		if lic.State.ValidUntil != nil {
			st.ValidUntil = *lic.State.ValidUntil
		}
		c.State = st
	}
	if err := saveCheckinFile(c); err != nil {
		fmt.Fprintf(w, "License: warning: could not store the platform's license terms (%v).\n", err)
	}
}
