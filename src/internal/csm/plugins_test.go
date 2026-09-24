package csm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSelectMetamodRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		releases         []metamodRelease
		wantTag          string
		wantPrerelease   bool
		wantFoundRelease bool
	}{
		{
			name: "multiple prereleases chooses newest prerelease",
			releases: []metamodRelease{
				{TagName: "1.11.0-git7100", Prerelease: true, PublishedAt: "2025-01-02T00:00:00Z"},
				{TagName: "1.11.0-git7200", Prerelease: true, PublishedAt: "2025-02-03T00:00:00Z"},
			},
			wantTag:          "1.11.0-git7200",
			wantPrerelease:   true,
			wantFoundRelease: true,
		},
		{
			name: "ignores stable release when prerelease exists",
			releases: []metamodRelease{
				{TagName: "1.11.0", Prerelease: false, PublishedAt: "2025-03-04T00:00:00Z"},
				{TagName: "1.12.0-git7300", Prerelease: true, PublishedAt: "2025-02-03T00:00:00Z"},
			},
			wantTag:          "1.12.0-git7300",
			wantPrerelease:   true,
			wantFoundRelease: true,
		},
		{
			name: "falls back to latest stable when no prerelease exists",
			releases: []metamodRelease{
				{TagName: "1.10.0", Prerelease: false, PublishedAt: "2025-01-01T00:00:00Z"},
				{TagName: "1.11.0", Prerelease: false, PublishedAt: "2025-03-01T00:00:00Z"},
			},
			wantTag:          "1.11.0",
			wantPrerelease:   false,
			wantFoundRelease: true,
		},
		{
			name:             "returns not found when list is empty",
			releases:         nil,
			wantTag:          "",
			wantPrerelease:   false,
			wantFoundRelease: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, found := selectMetamodRelease(tt.releases)
			if found != tt.wantFoundRelease {
				t.Fatalf("selectMetamodRelease() found = %v, want %v", found, tt.wantFoundRelease)
			}
			if got.TagName != tt.wantTag || got.Prerelease != tt.wantPrerelease {
				t.Fatalf("selectMetamodRelease() = (%q, prerelease=%v), want (%q, prerelease=%v)",
					got.TagName, got.Prerelease, tt.wantTag, tt.wantPrerelease)
			}
		})
	}
}

func TestSelectMetamodLinuxAsset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		assets   []metamodReleaseAsset
		wantName string
		wantURL  string
	}{
		{
			name: "prefers x86_64 linux tarball",
			assets: []metamodReleaseAsset{
				{Name: "mmsource-2.0-linux-arm64.tar.gz", URL: "https://example.com/arm64"},
				{Name: "mmsource-2.0-linux-x86_64.tar.gz", URL: "https://example.com/x86_64"},
			},
			wantName: "mmsource-2.0-linux-x86_64.tar.gz",
			wantURL:  "https://example.com/x86_64",
		},
		{
			name: "falls back to first linux tarball",
			assets: []metamodReleaseAsset{
				{Name: "mmsource-2.0-linux.tar.gz", URL: "https://example.com/linux"},
				{Name: "mmsource-2.0-windows.zip", URL: "https://example.com/win"},
			},
			wantName: "mmsource-2.0-linux.tar.gz",
			wantURL:  "https://example.com/linux",
		},
		{
			name: "returns empty when no linux tarball exists",
			assets: []metamodReleaseAsset{
				{Name: "mmsource-2.0-windows.zip", URL: "https://example.com/win"},
			},
			wantName: "",
			wantURL:  "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotName, gotURL := selectMetamodLinuxAsset(tt.assets)
			if gotName != tt.wantName || gotURL != tt.wantURL {
				t.Fatalf("selectMetamodLinuxAsset() = (%q, %q), want (%q, %q)", gotName, gotURL, tt.wantName, tt.wantURL)
			}
		})
	}
}

func TestMetamodReleaseTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		release metamodRelease
		want    time.Time
	}{
		{
			name: "uses published_at when valid",
			release: metamodRelease{
				PublishedAt: "2025-05-01T10:11:12Z",
				CreatedAt:   "2025-04-01T10:11:12Z",
			},
			want: time.Date(2025, 5, 1, 10, 11, 12, 0, time.UTC),
		},
		{
			name: "falls back to created_at when published_at is invalid",
			release: metamodRelease{
				PublishedAt: "invalid-time",
				CreatedAt:   "2025-04-01T10:11:12Z",
			},
			want: time.Date(2025, 4, 1, 10, 11, 12, 0, time.UTC),
		},
		{
			name: "returns zero time when both timestamps are invalid or empty",
			release: metamodRelease{
				PublishedAt: "not-rfc3339",
				CreatedAt:   "",
			},
			want: time.Time{},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := metamodReleaseTime(tt.release)
			if !got.Equal(tt.want) {
				t.Fatalf("metamodReleaseTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMetamodTargetVersion(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "unset uses pinned build", env: "", want: MetamodPinnedVersion},
		{name: "whitespace uses pinned build", env: "  ", want: MetamodPinnedVersion},
		{name: "latest opts into newest release", env: "latest", want: metamodLatest},
		{name: "latest is case insensitive", env: " LATEST ", want: metamodLatest},
		{name: "explicit tag overrides pin", env: "2.0.0.1468", want: "2.0.0.1468"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(metamodVersionEnv, tt.env)
			if got := metamodTargetVersion(); got != tt.want {
				t.Fatalf("metamodTargetVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestATCS2TargetVersion(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "unset uses latest", env: "", want: ""},
		{name: "whitespace uses latest", env: "   ", want: ""},
		{name: "bare version gets v prefix", env: "2.0.0", want: "v2.0.0"},
		{name: "v-prefixed version is kept as-is", env: "v2.0.0", want: "v2.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(atcs2VersionEnv, tt.env)
			if got := atcs2TargetVersion(); got != tt.want {
				t.Fatalf("atcs2TargetVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestATCS2ReleaseURL(t *testing.T) {
	const base = "https://api.github.com/repos/Auto-Tournament/cs2-plugin/releases"

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{name: "unset fetches latest", target: "", want: base + "/latest"},
		{name: "bare version fetches tags/v2.0.0", target: "v2.0.0", want: base + "/tags/v2.0.0"},
		{name: "v-prefixed version fetches tags/v2.0.0", target: "v2.0.0", want: base + "/tags/v2.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := atcs2ReleaseURL(base, tt.target); got != tt.want {
				t.Fatalf("atcs2ReleaseURL(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}

// TestDownloadATCS2MissingTag verifies that pinning to a tag GitHub does not
// have produces a clear error and leaves the updater's state untouched
// (nothing is downloaded or extracted).
func TestDownloadATCS2MissingTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tags/v9.9.9") {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
			return
		}
		t.Fatalf("unexpected request: %s", r.URL.Path)
	}))
	defer srv.Close()

	origURL := atcs2ReleasesURL
	atcs2ReleasesURL = srv.URL
	defer func() { atcs2ReleasesURL = origURL }()

	t.Setenv(atcs2VersionEnv, "9.9.9")

	tempDir := t.TempDir()
	up := &PluginUpdater{
		RootDir:      tempDir,
		GameDir:      tempDir,
		OverridesDir: tempDir,
		TempDir:      tempDir,
	}

	var buf strings.Builder
	err := up.downloadATCS2(&buf)
	if err == nil {
		t.Fatal("downloadATCS2() with a missing pinned tag returned nil error, want an error")
	}
	if !strings.Contains(err.Error(), "v9.9.9") {
		t.Fatalf("downloadATCS2() error = %q, want it to mention the pinned tag v9.9.9", err.Error())
	}
}

func TestDownloadATCS2LatestWhenUnpinned(t *testing.T) {
	requested := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
	}))
	defer srv.Close()

	origURL := atcs2ReleasesURL
	atcs2ReleasesURL = srv.URL
	defer func() { atcs2ReleasesURL = origURL }()

	t.Setenv(atcs2VersionEnv, "")

	tempDir := t.TempDir()
	up := &PluginUpdater{
		RootDir:      tempDir,
		GameDir:      tempDir,
		OverridesDir: tempDir,
		TempDir:      tempDir,
	}

	var buf strings.Builder
	_ = up.downloadATCS2(&buf)

	if !strings.HasSuffix(requested, "/latest") {
		t.Fatalf("downloadATCS2() with no pin requested %q, want it to end with /latest", requested)
	}
}
