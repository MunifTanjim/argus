package main

import (
	"context"
	"os"
	"slices"
	"testing"
)

func TestReexecPassesArgsAndEnv(t *testing.T) {
	var gotPath string
	var gotArgs, gotEnv []string
	oldExec := execFn
	execFn = func(path string, args, env []string) error {
		gotPath, gotArgs, gotEnv = path, args, env
		return nil
	}
	t.Cleanup(func() { execFn = oldExec })
	if err := reexec("/opt/argus"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/opt/argus" || !slices.Equal(gotArgs, os.Args) || !slices.Equal(gotEnv, os.Environ()) {
		t.Fatalf("exec(%q, %v, env of %d)", gotPath, gotArgs, len(gotEnv))
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
