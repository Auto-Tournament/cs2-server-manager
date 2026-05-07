package csm

import (
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
