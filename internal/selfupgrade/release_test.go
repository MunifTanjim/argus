package selfupgrade

import (
	"runtime"
	"testing"
)

func rel(tag string, prerelease bool, assets ...string) release {
	r := release{TagName: tag, Prerelease: prerelease}
	for _, a := range assets {
		r.Assets = append(r.Assets, struct {
			Name string `json:"name"`
		}{Name: a})
	}
	return r
}

func fullRel(tag string, prerelease bool) release {
	a := AssetName(tag, runtime.GOOS, runtime.GOARCH)
	return rel(tag, prerelease, a, a+".sha256")
}

func TestPickRelease(t *testing.T) {
	tests := []struct {
		name     string
		releases []release
		beta     bool
		want     string
	}{
		{"stable", []release{fullRel("0.1.3", false), fullRel("0.1.2", false)}, false, "0.1.3"},
		{"beta channel", []release{fullRel("0.1.3", false), fullRel("0.1.4-beta.1", true)}, true, "0.1.4-beta.1"},
		{"stable channel ignores beta", []release{fullRel("0.1.3", false), fullRel("0.1.4-beta.1", true)}, false, "0.1.3"},
		{"stable skips prerelease tag without flag", []release{fullRel("0.1.4-beta.1", false), fullRel("0.1.3", false)}, false, "0.1.3"},
		{"skips missing assets", []release{
			rel("0.1.4-beta.2", true),
			rel("0.1.4-beta.1", true, AssetName("0.1.4-beta.1", runtime.GOOS, runtime.GOARCH)),
			fullRel("0.1.3", false),
		}, true, "0.1.3"},
		{"skips drafts and unparsable", []release{
			{TagName: "0.1.5", Draft: true, Assets: fullRel("0.1.5", false).Assets},
			fullRel("nightly", false),
			fullRel("0.1.3", false),
		}, false, "0.1.3"},
		{"order independent", []release{fullRel("0.1.4-beta.1", true), fullRel("0.1.4", false), fullRel("0.1.4-beta.2", true)}, true, "0.1.4"},
		{"none qualifies", []release{rel("0.1.3", false)}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickRelease(tt.releases, tt.beta, runtime.GOOS, runtime.GOARCH); got != tt.want {
				t.Fatalf("pickRelease = %q, want %q", got, tt.want)
			}
		})
	}
}
