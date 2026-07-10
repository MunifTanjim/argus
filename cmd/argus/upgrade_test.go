package main

import "testing"

func TestUpgradeAssetName(t *testing.T) {
	tests := []struct {
		tag, goos, goarch, want string
	}{
		{"0.0.9", "linux", "amd64", "argus-0.0.9-linux-amd64"},
		{"0.0.9", "darwin", "arm64", "argus-0.0.9-darwin-arm64"},
	}
	for _, tt := range tests {
		if got := upgradeAssetName(tt.tag, tt.goos, tt.goarch); got != tt.want {
			t.Errorf("upgradeAssetName(%q, %q, %q) = %q, want %q", tt.tag, tt.goos, tt.goarch, got, tt.want)
		}
	}
}
