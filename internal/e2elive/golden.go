package e2elive

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite e2elive golden files from observed CLI output")

func renderResult(r Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "$ argus %s\n", strings.Join(r.Args, " "))
	fmt.Fprintf(&b, "exit %d\n", r.ExitCode)
	b.WriteString("--- stdout ---\n")
	b.WriteString(r.Stdout)
	b.WriteString("--- stderr ---\n")
	b.WriteString(r.Stderr)
	return b.String()
}

func (c *Cluster) goldenPath(t *testing.T, name string) string {
	dir := c.goldenDir
	if dir == "" {
		dir = filepath.Join("testdata", strings.ReplaceAll(t.Name(), "/", "_"))
	}
	return filepath.Join(dir, fmt.Sprintf("%02d-%s.golden", c.steps, name))
}

// Step normalizes one CLI result and compares it against its golden file. A
// mismatch is reported with Errorf, never Fatalf, so a long journey reports every
// drifted step in a single run. With -update the golden is rewritten instead.
func (c *Cluster) Step(t *testing.T, name string, r Result) {
	t.Helper()
	path := c.goldenPath(t, name)
	c.steps++

	got, err := c.normalize(renderResult(r))
	if err != nil {
		t.Errorf("step %s: %v\nraw output:\n%s", name, err, renderResult(r))
		return
	}

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("step %s: mkdir: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("step %s: write golden: %v", name, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("step %s: %v — run: go test ./internal/e2elive -run %s -update", name, err, t.Name())
		return
	}
	if got != string(want) {
		t.Errorf("step %s: golden mismatch (%s)\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
	}
}
