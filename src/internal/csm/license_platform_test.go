package csm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const otherLicenseKey = "ATL1.eyJ2IjoxLCJpZCI6IkwtcGxhdCJ9.b3RoZXJzaWduYXR1cmU"

// fakeApplier records what would be written to every server.
type fakeApplier struct {
	calls  []string
	failed int
}

func (f *fakeApplier) apply(w io.Writer, key string) (int, int) {
	f.calls = append(f.calls, key)
	return 2, f.failed
}

func platformKey(key, rev string) *PlatformLicense {
	return &PlatformLicense{Key: &key, Revision: rev}
}

func platformCleared() *PlatformLicense {
	return &PlatformLicense{Key: nil, Revision: "none"}
}

func licenseTestRoot(t *testing.T) {
	t.Helper()
	t.Setenv("CSM_ROOT", t.TempDir())
}

func TestSyncPlatformLicenseAppliesOnceAndRecordsRevision(t *testing.T) {
	licenseTestRoot(t)
	f := &fakeApplier{}
	var out bytes.Buffer

	syncPlatformLicense(&out, platformKey(testLicenseKey, "sha256:aaaa"), f.apply, "nobody")
	if len(f.calls) != 1 || f.calls[0] != testLicenseKey {
		t.Fatalf("calls = %q", f.calls)
	}
	s, _ := LoadLicenseSettings()
	if s.Key != testLicenseKey || s.Source != LicenseSourcePlatform || s.PlatformRevision != "sha256:aaaa" {
		t.Fatalf("stored %+v", s)
	}
	if strings.Contains(out.String(), testLicenseKey) {
		t.Fatalf("the key was logged:\n%s", out.String())
	}

	// Same revision again: nothing to do.
	syncPlatformLicense(&out, platformKey(testLicenseKey, "sha256:aaaa"), f.apply, "nobody")
	if len(f.calls) != 1 {
		t.Fatalf("re-applied an unchanged key: %q", f.calls)
	}

	// A new key: applied.
	syncPlatformLicense(&out, platformKey(otherLicenseKey, "sha256:bbbb"), f.apply, "nobody")
	if len(f.calls) != 2 || f.calls[1] != otherLicenseKey {
		t.Fatalf("calls = %q", f.calls)
	}
	if !strings.Contains(out.String(), "license L-plat") {
		t.Fatalf("log should name the license id:\n%s", out.String())
	}
}

func TestSyncPlatformLicenseRetriesAfterAFailure(t *testing.T) {
	licenseTestRoot(t)
	f := &fakeApplier{failed: 1}
	var out bytes.Buffer

	syncPlatformLicense(&out, platformKey(testLicenseKey, "sha256:aaaa"), f.apply, "nobody")
	if !strings.Contains(out.String(), "warning") || !strings.Contains(out.String(), "next poll") {
		t.Fatalf("a failure must be a warning that retries:\n%s", out.String())
	}
	s, _ := LoadLicenseSettings()
	if s.Key != testLicenseKey || s.PlatformRevision != "" {
		t.Fatalf("stored %+v; the key is kept but the revision is not recorded", s)
	}

	f.failed = 0
	syncPlatformLicense(&out, platformKey(testLicenseKey, "sha256:aaaa"), f.apply, "nobody")
	if len(f.calls) != 2 {
		t.Fatalf("did not retry: %q", f.calls)
	}
	if s, _ := LoadLicenseSettings(); s.PlatformRevision != "sha256:aaaa" {
		t.Fatalf("stored %+v", s)
	}
}

func TestSyncPlatformLicenseWinsOverAManualKey(t *testing.T) {
	licenseTestRoot(t)
	if err := saveLicenseSettings(LicenseSettings{Key: otherLicenseKey}, "nobody"); err != nil {
		t.Fatal(err)
	}
	f := &fakeApplier{}
	var out bytes.Buffer
	syncPlatformLicense(&out, platformKey(testLicenseKey, "sha256:aaaa"), f.apply, "nobody")
	if len(f.calls) != 1 || f.calls[0] != testLicenseKey {
		t.Fatalf("calls = %q", f.calls)
	}
	if !strings.Contains(out.String(), "csm license set") {
		t.Fatalf("log should say it replaced a manual key:\n%s", out.String())
	}
}

func TestSyncPlatformLicenseClearOnlyTakesBackItsOwnKey(t *testing.T) {
	t.Run("a manual key survives a platform with no key", func(t *testing.T) {
		licenseTestRoot(t)
		if err := saveLicenseSettings(LicenseSettings{Key: testLicenseKey}, "nobody"); err != nil {
			t.Fatal(err)
		}
		f := &fakeApplier{}
		syncPlatformLicense(io.Discard, platformCleared(), f.apply, "nobody")
		if len(f.calls) != 0 {
			t.Fatalf("cleared a manual key: %q", f.calls)
		}
		if s, _ := LoadLicenseSettings(); s.Key != testLicenseKey {
			t.Fatalf("stored %+v", s)
		}
	})

	t.Run("a platform key is cleared when the platform clears it", func(t *testing.T) {
		licenseTestRoot(t)
		f := &fakeApplier{}
		syncPlatformLicense(io.Discard, platformKey(testLicenseKey, "sha256:aaaa"), f.apply, "nobody")
		var out bytes.Buffer
		syncPlatformLicense(&out, platformCleared(), f.apply, "nobody")
		if len(f.calls) != 2 || f.calls[1] != "" {
			t.Fatalf("calls = %q", f.calls)
		}
		s, _ := LoadLicenseSettings()
		if s.Key != "" || s.PlatformRevision != "none" {
			t.Fatalf("stored %+v", s)
		}
		// Cleared already: nothing more to do.
		syncPlatformLicense(&out, platformCleared(), f.apply, "nobody")
		if len(f.calls) != 2 {
			t.Fatalf("calls = %q", f.calls)
		}
	})

	t.Run("a failed clear is retried", func(t *testing.T) {
		licenseTestRoot(t)
		f := &fakeApplier{}
		syncPlatformLicense(io.Discard, platformKey(testLicenseKey, "sha256:aaaa"), f.apply, "nobody")
		f.failed = 1
		var out bytes.Buffer
		syncPlatformLicense(&out, platformCleared(), f.apply, "nobody")
		if !strings.Contains(out.String(), "warning") {
			t.Fatalf("log:\n%s", out.String())
		}
		f.failed = 0
		syncPlatformLicense(&out, platformCleared(), f.apply, "nobody")
		if len(f.calls) != 3 {
			t.Fatalf("did not retry the clear: %q", f.calls)
		}
	})
}

func TestSyncPlatformLicenseNoAnswerChangesNothing(t *testing.T) {
	licenseTestRoot(t)
	if err := saveLicenseSettings(LicenseSettings{Key: testLicenseKey, Source: LicenseSourcePlatform}, "nobody"); err != nil {
		t.Fatal(err)
	}
	f := &fakeApplier{}
	syncPlatformLicense(io.Discard, nil, f.apply, "nobody")
	syncPlatformLicense(io.Discard, &PlatformLicense{Key: nil, Revision: ""}, f.apply, "nobody")
	if len(f.calls) != 0 {
		t.Fatalf("calls = %q", f.calls)
	}
	var out bytes.Buffer
	syncPlatformLicense(&out, platformKey(`x"; rcon_password "pwned`, "sha256:cccc"), f.apply, "nobody")
	if len(f.calls) != 0 || !strings.Contains(out.String(), "not a license key") {
		t.Fatalf("calls = %q, log:\n%s", f.calls, out.String())
	}
	if s, _ := LoadLicenseSettings(); s.Key != testLicenseKey {
		t.Fatalf("stored %+v", s)
	}
}

func TestSyncPlatformLicenseSurvivesAPanic(t *testing.T) {
	licenseTestRoot(t)
	var out bytes.Buffer
	syncPlatformLicense(&out, platformKey(testLicenseKey, "sha256:aaaa"),
		func(io.Writer, string) (int, int) { panic("boom") }, "nobody")
	if !strings.Contains(out.String(), "warning") {
		t.Fatalf("log:\n%s", out.String())
	}
}

func TestLicenseFingerprintNeverPrintsTheKey(t *testing.T) {
	if got := licenseFingerprint(otherLicenseKey); got != "license L-plat" {
		t.Fatalf("got %q", got)
	}
	got := licenseFingerprint(testLicenseKey)
	if strings.Contains(got, "ATL1") || len(got) > 12 {
		t.Fatalf("got %q", got)
	}
}

func TestFetchPlatformHoldReadsTheLicense(t *testing.T) {
	t.Run("a key", func(t *testing.T) {
		url, _ := holdServer(t, http.StatusOK, map[string]any{
			"success": true, "hold": false, "reason": "idle",
			"license": map[string]any{"key": testLicenseKey, "revision": "sha256:aaaa"},
		})
		answer, err := FetchPlatformHold(context.Background(), PlatformSettings{BaseURL: url, Token: "s3cret"})
		if err != nil {
			t.Fatal(err)
		}
		if answer.License == nil || answer.License.Key == nil || *answer.License.Key != testLicenseKey ||
			answer.License.Revision != "sha256:aaaa" {
			t.Fatalf("license = %+v", answer.License)
		}
	})

	t.Run("cleared", func(t *testing.T) {
		url, _ := holdServer(t, http.StatusOK, map[string]any{
			"success": true, "hold": false, "reason": "idle",
			"license": map[string]any{"key": nil, "revision": "none"},
		})
		answer, err := FetchPlatformHold(context.Background(), PlatformSettings{BaseURL: url, Token: "s3cret"})
		if err != nil || answer.License == nil || answer.License.Key != nil || answer.License.Revision != "none" {
			t.Fatalf("license = %+v, err = %v", answer.License, err)
		}
	})

	t.Run("an older platform sends none", func(t *testing.T) {
		url, _ := holdServer(t, http.StatusOK, map[string]any{"success": true, "hold": false})
		answer, err := FetchPlatformHold(context.Background(), PlatformSettings{BaseURL: url, Token: "s3cret"})
		if err != nil || answer.License != nil {
			t.Fatalf("license = %+v, err = %v", answer.License, err)
		}
	})
}

func TestPlatformLicenseForCycle(t *testing.T) {
	t.Setenv(EnvPlatformURL, "")
	t.Setenv(EnvPlatformToken, "")
	ctx := context.Background()
	body := map[string]any{"success": true, "hold": true,
		"license": map[string]any{"key": testLicenseKey, "revision": "sha256:aaaa"}}

	t.Run("auto: taken from the hold answer", func(t *testing.T) {
		url, _ := holdServer(t, http.StatusOK, body)
		s := AutoUpdateSettings{Platform: PlatformSettings{BaseURL: url, Token: "s3cret"}}
		hold := ResolveUpdateHold(ctx, s)
		lic, err := platformLicenseForCycle(ctx, s, hold)
		if err != nil || lic == nil || lic.Revision != "sha256:aaaa" {
			t.Fatalf("lic = %+v, err = %v", lic, err)
		}
	})

	t.Run("manual hold: asked for the license only", func(t *testing.T) {
		url, token := holdServer(t, http.StatusOK, body)
		s := AutoUpdateSettings{HoldMode: HoldModeOff, Platform: PlatformSettings{BaseURL: url, Token: "s3cret"}}
		hold := ResolveUpdateHold(ctx, s)
		if hold.On || hold.Source != HoldSourceManual {
			t.Fatalf("hold = %+v", hold)
		}
		lic, err := platformLicenseForCycle(ctx, s, hold)
		if err != nil || lic == nil || *token != "s3cret" {
			t.Fatalf("lic = %+v, err = %v", lic, err)
		}
	})

	t.Run("unreachable or unconfigured: no answer", func(t *testing.T) {
		unreachable := UpdateHold{On: true, Source: HoldSourceUnreachable}
		if lic, err := platformLicenseForCycle(ctx, AutoUpdateSettings{}, unreachable); lic != nil || err != nil {
			t.Fatalf("lic = %+v, err = %v", lic, err)
		}
		none := UpdateHold{Source: HoldSourceNone}
		if lic, err := platformLicenseForCycle(ctx, AutoUpdateSettings{}, none); lic != nil || err != nil {
			t.Fatalf("lic = %+v, err = %v", lic, err)
		}
	})
}
