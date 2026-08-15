package e2elive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderResultShape(t *testing.T) {
	got := renderResult(Result{
		Args:     []string{"lock", "status", "--token=x"},
		Stdout:   "locked mode: not enabled\n",
		Stderr:   "",
		ExitCode: 0,
	})
	want := "$ argus lock status --token=x\n" +
		"exit 0\n" +
		"--- stdout ---\n" +
		"locked mode: not enabled\n" +
		"--- stderr ---\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderResultIncludesStderrAndNonZeroExit(t *testing.T) {
	got := renderResult(Result{
		Args:     []string{"lock", "init"},
		Stdout:   "",
		Stderr:   "Error: needs a signer key\n",
		ExitCode: 1,
	})
	if !strings.Contains(got, "exit 1\n") {
		t.Fatalf("exit code missing:\n%s", got)
	}
	if !strings.HasSuffix(got, "--- stderr ---\nError: needs a signer key\n") {
		t.Fatalf("stderr misplaced:\n%s", got)
	}
}

func TestStepWritesAndComparesGolden(t *testing.T) {
	dir := t.TempDir()
	c := &Cluster{Root: "/var/folders/xy/abc/T/axe1", GWAddr: "127.0.0.1:1", goldenDir: dir}
	r := Result{Args: []string{"lock", "status"}, Stdout: "locked mode: not enabled\n", ExitCode: 0}

	*updateGolden = true
	c.Step(t, "status", r)
	*updateGolden = false

	path := filepath.Join(dir, "00-status.golden")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("golden not written: %v", err)
	}

	c.steps = 0
	c.Step(t, "status", r) // must compare clean
	if t.Failed() {
		t.Fatalf("re-running the same step against its own golden failed")
	}
}

func TestStepReportsMismatchWithoutFataling(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "00-status.golden"), []byte("nope\n"), 0o644); err != nil {
		t.Fatalf("seed golden: %v", err)
	}
	c := &Cluster{Root: "/var/folders/xy/abc/T/axe1", GWAddr: "127.0.0.1:1", goldenDir: dir}

	fake := &testing.T{}
	c.Step(fake, "status", Result{Args: []string{"lock", "status"}, Stdout: "different\n"})
	if !fake.Failed() {
		t.Fatalf("mismatch did not mark the test failed")
	}
}
