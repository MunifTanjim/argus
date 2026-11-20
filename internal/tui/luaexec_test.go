package tui

import (
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func shArgv(script string) []string { return []string{"sh", "-c", script} }

func TestExecCapturesOutput(t *testing.T) {
	r, err := runExec(shArgv("echo out; echo err >&2"), execOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if r.stdout != "out\n" || r.stderr != "err\n" || r.code != 0 || r.timedOut {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestExecCapsOutput(t *testing.T) {
	r, err := runExec(shArgv("head -c 3000000 /dev/zero; head -c 3000000 /dev/zero >&2"), execOpts{timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.stdout) != execOutputLimit || len(r.stderr) != execOutputLimit || r.code != 0 {
		t.Fatalf("stdout %d stderr %d code %d", len(r.stdout), len(r.stderr), r.code)
	}
}

func TestExecNonZeroIsAResult(t *testing.T) {
	r, err := runExec(shArgv("exit 3"), execOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if r.code != 3 || r.timedOut {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestExecTimeoutTerminates(t *testing.T) {
	start := time.Now()
	r, err := runExec(shArgv("sleep 10"), execOpts{timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) >= 2*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	if !r.timedOut || r.signal != "SIGTERM" || r.code != -1 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestExecChildHoldingOutputIsNotATimeout(t *testing.T) {
	r, err := runExec(shArgv("sleep 3 & exit 0"), execOpts{timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if r.timedOut || r.code != 0 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestExecKillsAfterDelay(t *testing.T) {
	start := time.Now()
	r, err := runExec(shArgv("trap '' TERM; sleep 10"), execOpts{timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) >= 6*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	if !r.timedOut || r.signal != "SIGKILL" || r.code != -1 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestExecTimeoutStopsTheProcessGroup(t *testing.T) {
	cases := []struct {
		name   string
		script string
		within time.Duration
	}{
		{"on SIGTERM", "sleep 10 & echo $!; wait", 2 * time.Second},
		{"on SIGKILL", "trap '' TERM; sleep 10 & echo $!; wait", 6 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start := time.Now()
			r, err := runExec(shArgv(c.script), execOpts{timeout: 200 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			if !r.timedOut {
				t.Fatalf("unexpected result: %+v", r)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(r.stdout))
			if err != nil {
				t.Fatalf("stdout = %q", r.stdout)
			}
			for syscall.Kill(pid, 0) == nil {
				if time.Since(start) > c.within {
					_ = syscall.Kill(pid, syscall.SIGKILL)
					t.Fatalf("grandchild %d still runs after %v", pid, time.Since(start))
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

func TestExecEnv(t *testing.T) {
	t.Setenv("ARGUS_X", "a")
	b := "b"
	r, err := runExec(shArgv(`echo "$ARGUS_X|$ARGUS_Y|"`), execOpts{
		env: map[string]*string{"ARGUS_Y": &b, "ARGUS_X": nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.stdout != "|b|\n" {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

func TestExecClearEnv(t *testing.T) {
	b := "b"
	r, err := runExec([]string{"/usr/bin/env"}, execOpts{
		clearEnv: true,
		env:      map[string]*string{"ARGUS_Y": &b},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.stdout != "ARGUS_Y=b\n" {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

func TestExecStdinAndCwd(t *testing.T) {
	dir := t.TempDir()
	in := "hi"
	r, err := runExec(shArgv("cat; pwd"), execOpts{cwd: dir, stdin: &in})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.stdout) < 2 || r.stdout[:2] != "hi" {
		t.Fatalf("stdout = %q", r.stdout)
	}
	got, err := filepath.EvalSymlinks(r.stdout[2 : len(r.stdout)-1])
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cwd = %q, want %q", got, want)
	}
}

func TestExecDiscard(t *testing.T) {
	for _, tc := range []struct {
		opts           execOpts
		stdout, stderr string
	}{
		{execOpts{discardStdout: true}, "", "err\n"},
		{execOpts{discardStderr: true}, "out\n", ""},
	} {
		r, err := runExec(shArgv("echo out; echo err >&2"), tc.opts)
		if err != nil {
			t.Fatal(err)
		}
		if r.stdout != tc.stdout || r.stderr != tc.stderr {
			t.Errorf("%+v: unexpected result: %+v", tc.opts, r)
		}
	}
}

func TestExecStartFailure(t *testing.T) {
	if _, err := runExec([]string{"argus-no-such-program"}, execOpts{}); err == nil {
		t.Fatal("expected error")
	}
}
