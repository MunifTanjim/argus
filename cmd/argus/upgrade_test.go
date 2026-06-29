package main

import "testing"

func TestUpgradeAssetPattern(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "argus-*-linux-amd64"},
		{"darwin", "arm64", "argus-*-darwin-arm64"},
	}
	for _, tt := range tests {
		if got := upgradeAssetPattern(tt.goos, tt.goarch); got != tt.want {
			t.Errorf("upgradeAssetPattern(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}
