package csm

import "testing"

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
