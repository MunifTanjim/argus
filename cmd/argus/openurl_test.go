package main

import (
	"reflect"
	"testing"
)

func TestBrowserCommand(t *testing.T) {
	const url = "https://example.com/register"
	tests := []struct {
		goos     string
		wantCmd  string
		wantArgs []string
	}{
		{"darwin", "open", []string{url}},
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", url}},
		{"linux", "xdg-open", []string{url}},
		{"freebsd", "xdg-open", []string{url}},
	}
	for _, tt := range tests {
		cmd, args := browserCommand(tt.goos, url)
		if cmd != tt.wantCmd || !reflect.DeepEqual(args, tt.wantArgs) {
			t.Errorf("browserCommand(%q, url) = %q %v, want %q %v",
				tt.goos, cmd, args, tt.wantCmd, tt.wantArgs)
		}
	}
}
