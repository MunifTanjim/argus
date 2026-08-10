package e2elive

import (
	"flag"
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	sweepStale()
	if err := buildTestImage(); err != nil {
		fmt.Fprintln(os.Stderr, "e2elive:", err)
		os.Exit(1)
	}
	code := m.Run()
	sweepStale()
	os.Exit(code)
}
