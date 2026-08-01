package e2elive

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "argusbin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2elive: mktemp:", err)
		os.Exit(1)
	}
	bin := filepath.Join(dir, "argus")
	build := exec.Command("go", "build", "-o", bin, "github.com/MunifTanjim/argus/cmd/argus")
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2elive: build argus:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	argusBin = bin
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
