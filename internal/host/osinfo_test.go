package host

import (
	"runtime"
	"testing"
)

func TestOSReleaseName(t *testing.T) {
	for in, want := range map[string]string{
		"NAME=\"Ubuntu\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nID=ubuntu\n": "Ubuntu 24.04.1 LTS",
		"PRETTY_NAME='Fedora Linux 41'\n":                                  "Fedora Linux 41",
		"PRETTY_NAME=Alpine\n":                                             "Alpine",
		"NAME=\"Debian\"\n":                                                "",
		"":                                                                 "",
	} {
		if got := osReleaseName([]byte(in)); got != want {
			t.Errorf("osReleaseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithArch(t *testing.T) {
	if got, want := withArch("macOS 26.0.1"), "macOS 26.0.1 ("+runtime.GOARCH+")"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOSNotEmpty(t *testing.T) {
	if OS() == "" {
		t.Fatal("OS() must not be empty")
	}
}
