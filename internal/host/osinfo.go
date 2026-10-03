package host

import (
	"runtime"
	"strings"
)

func osReleaseName(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(line), "PRETTY_NAME=")
		if ok {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

func withArch(name string) string { return name + " (" + runtime.GOARCH + ")" }
