package csm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// holdServer answers the hold endpoint, recording the token it was given.
func holdServer(t *testing.T, status int, body any) (url string, token *string) {
	t.Helper()
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != platformHoldPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		seen = r.Header.Get("X-Auto-Tournament-Token")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if s, ok := body.(string); ok {
			_, _ = w.Write([]byte(s))
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &seen
}

func TestHoldModeMigrationAndParsing(t *testing.T) {
	// A file written by csm <= 1.8.0 has only the boolean.
	if got := (AutoUpdateSettings{Hold: true}).Mode(); got != HoldModeOn {
		t.Fatalf("legacy hold=true -> %q, want %q", got, HoldModeOn)
	}
	if got := (AutoUpdateSettings{Hold: false}).Mode(); got != HoldModeAuto {
		t.Fatalf("legacy hold=false -> %q, want %q", got, HoldModeAuto)
	}
	// An explicit mode wins over the legacy boolean, whatever it says.
	if got := (AutoUpdateSettings{Hold: true, HoldMode: "off"}).Mode(); got != HoldModeOff {
		t.Fatalf("hold_mode=off with hold=true -> %q", got)
	}
	// A hand-edited typo asks the platform rather than never holding.
	if got := (AutoUpdateSettings{HoldMode: "sometimes"}).Mode(); got != HoldModeAuto {
		t.Fatalf("unknown mode -> %q, want %q", got, HoldModeAuto)
	}

	for _, tt := range []struct{ in, want string }{
		{"on", HoldModeOn}, {"YES", HoldModeOn}, {"1", HoldModeOn},
		{"off", HoldModeOff}, {"no", HoldModeOff}, {"0", HoldModeOff},
		{"auto", HoldModeAuto}, {" Auto ", HoldModeAuto}, {"platform", HoldModeAuto},
	} {
		got, err := ParseHoldMode(tt.in)
		if err != nil || got != tt.want {
			t.Fatalf("ParseHoldMode(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	if _, err := ParseHoldMode("maybe"); err == nil {
		t.Fatal("ParseHoldMode must reject nonsense")
	}
}

func TestSetUpdateHoldModePersistsAndKeepsLegacyBoolean(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CSM_ROOT", root)

	if _, err := SetUpdateHoldMode(HoldModeOn); err != nil {
		t.Fatal(err)
	}
	s, err := LoadAutoUpdateSettings()
	if err != nil || s.Mode() != HoldModeOn || !s.Hold {
		t.Fatalf("hold on: %+v (%v); the legacy boolean must stay in step", s, err)
	}

	if _, err := SetUpdateHoldMode(HoldModeAuto); err != nil {
		t.Fatal(err)
	}
	if s, _ = LoadAutoUpdateSettings(); s.Mode() != HoldModeAuto || s.Hold {
		t.Fatalf("auto: %+v; an older csm must not see a manual hold", s)
	}
}

func TestSetPlatformValidatesAndProtectsTheToken(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CSM_ROOT", root)

	if _, err := SetPlatform("cs.example.io", "tok"); err == nil {
		t.Fatal("a URL with no scheme must be rejected")
	}
	if _, err := SetPlatform("https://cs.example.io", ""); err == nil {
		t.Fatal("a URL with no token must be rejected")
	}

	if _, err := SetPlatform("https://cs.example.io/", "s3cret"); err != nil {
		t.Fatal(err)
	}
	s, err := LoadAutoUpdateSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Platform.BaseURL != "https://cs.example.io" {
		t.Fatalf("trailing slash kept: %q", s.Platform.BaseURL)
	}
	if !s.Platform.Configured() {
		t.Fatal("platform should be configured")
	}

	// The file now holds a secret.
	info, err := os.Stat(filepath.Join(root, "auto-update.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("auto-update.json is %o, want 0600: it holds the platform token", mode)
	}

	if _, err := SetPlatform("", ""); err != nil {
		t.Fatal(err)
	}
	if s, _ = LoadAutoUpdateSettings(); s.Platform.Configured() {
		t.Fatalf("platform not cleared: %+v", s.Platform)
	}
}

func TestPlatformSettingsEnvironmentOverrides(t *testing.T) {
	stored := PlatformSettings{BaseURL: "https://stored.example", Token: "stored"}
	t.Setenv(EnvPlatformURL, "https://env.example")
	t.Setenv(EnvPlatformToken, "env-token")
	got := stored.Resolved()
	if got.BaseURL != "https://env.example" || got.Token != "env-token" {
		t.Fatalf("environment did not win: %+v", got)
	}

	var empty PlatformSettings
	if !empty.Configured() {
		t.Fatal("the environment alone should be enough to configure a platform")
	}
}

func TestPlainHTTPToRemoteHost(t *testing.T) {
	for _, tt := range []struct {
		url  string
		want bool
	}{
		{"https://cs.example.io", false},
		{"http://localhost:3069", false},
		{"http://127.0.0.1:3069", false},
		{"http://[::1]:3069", false},
		{"http://cs.example.io", true},
		{"http://10.0.0.5:3069", true},
		{"not a url", false},
	} {
		if got := PlainHTTPToRemoteHost(tt.url); got != tt.want {
			t.Errorf("PlainHTTPToRemoteHost(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestFetchPlatformHold(t *testing.T) {
	t.Run("a hold is read, and the token is sent", func(t *testing.T) {
		url, token := holdServer(t, http.StatusOK, map[string]any{
			"success": true, "hold": true, "reason": "tournament \"Cup\" is in progress",
			"tournamentStatus": "in_progress",
		})
		answer, err := FetchPlatformHold(context.Background(),
			PlatformSettings{BaseURL: url, Token: "s3cret"})
		if err != nil {
			t.Fatal(err)
		}
		if !answer.Hold || answer.TournamentStatus != "in_progress" {
			t.Fatalf("answer = %+v", answer)
		}
		if *token != "s3cret" {
			t.Fatalf("token sent = %q", *token)
		}
	})

	t.Run("no hold is read", func(t *testing.T) {
		url, _ := holdServer(t, http.StatusOK, map[string]any{
			"success": true, "hold": false, "reason": "no match is loaded or live",
		})
		answer, err := FetchPlatformHold(context.Background(),
			PlatformSettings{BaseURL: url, Token: "s3cret"})
		if err != nil || answer.Hold {
			t.Fatalf("answer = %+v, err = %v", answer, err)
		}
	})

	t.Run("a rejected token says so", func(t *testing.T) {
		url, _ := holdServer(t, http.StatusUnauthorized, map[string]any{"success": false})
		_, err := FetchPlatformHold(context.Background(),
			PlatformSettings{BaseURL: url, Token: "wrong"})
		if err == nil || !strings.Contains(err.Error(), "SERVER_TOKEN") {
			t.Fatalf("err = %v; it should say which credential to check", err)
		}
	})

	t.Run("a platform without the endpoint says so", func(t *testing.T) {
		url, _ := holdServer(t, http.StatusOK, map[string]any{"success": true})
		_, err := FetchPlatformHold(context.Background(),
			PlatformSettings{BaseURL: url + "/elsewhere", Token: "s3cret"})
		if err == nil || !strings.Contains(err.Error(), "404") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("a non-JSON answer is an error, not a false", func(t *testing.T) {
		url, _ := holdServer(t, http.StatusOK, "<html>login</html>")
		_, err := FetchPlatformHold(context.Background(),
			PlatformSettings{BaseURL: url, Token: "s3cret"})
		if err == nil {
			t.Fatal("a captive portal or proxy page must not read as hold=false")
		}
	})

	t.Run("success:false is an error", func(t *testing.T) {
		url, _ := holdServer(t, http.StatusOK, map[string]any{"success": false, "hold": false})
		_, err := FetchPlatformHold(context.Background(),
			PlatformSettings{BaseURL: url, Token: "s3cret"})
		if err == nil {
			t.Fatal("the platform saying it could not answer must not read as hold=false")
		}
	})

	t.Run("a malformed URL is an error", func(t *testing.T) {
		_, err := FetchPlatformHold(context.Background(),
			PlatformSettings{BaseURL: "cs.example.io", Token: "s3cret"})
		if err == nil || !strings.Contains(err.Error(), "http://") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestResolveUpdateHold(t *testing.T) {
	t.Setenv(EnvPlatformURL, "")
	t.Setenv(EnvPlatformToken, "")
	ctx := context.Background()

	t.Run("manual on wins over a platform that says no hold", func(t *testing.T) {
		url, _ := holdServer(t, http.StatusOK, map[string]any{"success": true, "hold": false})
		h := ResolveUpdateHold(ctx, AutoUpdateSettings{
			HoldMode: HoldModeOn,
			Platform: PlatformSettings{BaseURL: url, Token: "s3cret"},
		})
		if !h.On || h.Source != HoldSourceManual {
			t.Fatalf("hold = %+v", h)
		}
	})

	t.Run("manual off wins over a platform that holds", func(t *testing.T) {
		url, token := holdServer(t, http.StatusOK, map[string]any{"success": true, "hold": true})
		h := ResolveUpdateHold(ctx, AutoUpdateSettings{
			HoldMode: HoldModeOff,
			Platform: PlatformSettings{BaseURL: url, Token: "s3cret"},
		})
		if h.On || h.Source != HoldSourceManual {
			t.Fatalf("hold = %+v", h)
		}
		if *token != "" {
			t.Fatal("`hold off` must not consult the platform at all")
		}
	})

	t.Run("auto with no platform does not hold", func(t *testing.T) {
		h := ResolveUpdateHold(ctx, AutoUpdateSettings{HoldMode: HoldModeAuto})
		if h.On || h.Source != HoldSourceNone {
			t.Fatalf("hold = %+v; an unconfigured csm must behave as it did before", h)
		}
	})

	t.Run("auto follows the platform", func(t *testing.T) {
		url, _ := holdServer(t, http.StatusOK, map[string]any{
			"success": true, "hold": true, "reason": "2 match(es) in progress (r1m1 on cs1)",
		})
		h := ResolveUpdateHold(ctx, AutoUpdateSettings{
			Platform: PlatformSettings{BaseURL: url, Token: "s3cret"},
		})
		if !h.On || h.Source != HoldSourcePlatform || !strings.Contains(h.Reason, "r1m1") {
			t.Fatalf("hold = %+v", h)
		}
	})

	t.Run("an unreachable platform holds", func(t *testing.T) {
		// A port nothing listens on: the poll fails, and csm must not update
		// while it cannot tell whether a tournament is running.
		h := ResolveUpdateHold(ctx, AutoUpdateSettings{
			Platform: PlatformSettings{BaseURL: "http://127.0.0.1:1", Token: "s3cret"},
		})
		if !h.On || h.Source != HoldSourceUnreachable {
			t.Fatalf("hold = %+v; an unreachable platform must hold", h)
		}
		if !strings.Contains(h.Reason, "could not ask") {
			t.Fatalf("reason = %q; it should say the platform could not be asked", h.Reason)
		}
	})

	t.Run("a legacy hold=true file still holds", func(t *testing.T) {
		h := ResolveUpdateHold(ctx, AutoUpdateSettings{Hold: true})
		if !h.On || h.Source != HoldSourceManual {
			t.Fatalf("hold = %+v; an upgrade must not lift a hold set before it", h)
		}
	})
}

func TestUpdateHoldDescribe(t *testing.T) {
	on := UpdateHold{On: true, Source: HoldSourcePlatform, Reason: "a tournament is in progress"}
	if got := on.Describe(); !strings.HasPrefix(got, "ON (platform: ") {
		t.Fatalf("Describe() = %q", got)
	}
	off := UpdateHold{Source: HoldSourceNone}
	if got := off.Describe(); got != "off (none)" {
		t.Fatalf("Describe() = %q", got)
	}
}
