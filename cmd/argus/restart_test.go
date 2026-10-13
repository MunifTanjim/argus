package main

import (
	"context"
	"os"
	"slices"
	"testing"
)

func TestReexecPassesPathAndArgs(t *testing.T) {
	var gotPath string
	var gotArgs []string
	oldExec := execFn
	execFn = func(path string, args, _ []string) error {
		gotPath, gotArgs = path, args
		return nil
	}
	t.Cleanup(func() { execFn = oldExec })
	if err := reexec("/opt/argus"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/opt/argus" || !slices.Equal(gotArgs, os.Args) {
		t.Fatalf("exec(%q, %v)", gotPath, gotArgs)
	}
}

func TestRequestRestartRecordsPathAndStops(t *testing.T) {
	t.Cleanup(func() { restartPath.Store(nil) })
	ctx, stop := context.WithCancel(context.Background())
	requestRestart("/opt/argus", stop)
	if p := restartPath.Load(); p == nil || *p != "/opt/argus" {
		t.Fatalf("restartPath = %v", p)
	}
	if ctx.Err() == nil {
		t.Fatal("requestRestart must stop runStart")
	}
}

// A managed server drops ARGUS_TOKEN from its live environment; its restart must
// still get the token it started with.
func TestReexecUsesStartupEnv(t *testing.T) {
	var gotEnv []string
	oldExec, oldEnv := execFn, startEnv
	execFn = func(_ string, _, env []string) error { gotEnv = env; return nil }
	t.Cleanup(func() { execFn = oldExec; startEnv = oldEnv })
	startEnv = []string{"A=1", "ARGUS_TOKEN=tok"}
	t.Setenv("ARGUS_TOKEN", "")
	if err := reexec("/opt/argus"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(gotEnv, startEnv) {
		t.Fatalf("restart env = %v, want the startup env", gotEnv)
	}
}
