package shell

import "testing"

func TestDetectShell(t *testing.T) {
	tests := []struct {
		shell string
		want  string
	}{
		{"/bin/zsh", "zsh"},
		{"/bin/bash", "bash"},
		{"/usr/bin/fish", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Setenv("SHELL", tt.shell)
		if got := DetectShell(); got != tt.want {
			t.Errorf("DetectShell() with SHELL=%q = %q, want %q", tt.shell, got, tt.want)
		}
	}
}
