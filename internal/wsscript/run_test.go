package wsscript

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testEnv(t *testing.T) Env {
	dir := t.TempDir()
	return Env{WorkspacePath: dir, RootPath: "/root/main", WorkspaceName: filepath.Base(dir), TargetBranch: "main"}
}

func waitDone(t *testing.T, r *Runner, ws string) Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if run, ok := r.Status(ws); ok && run.State != Running {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("setup did not finish")
	return Run{}
}

func TestSetupRunsInWorkspaceWithEnv(t *testing.T) {
	var changes atomic.Int32
	r := NewRunner(func() { changes.Add(1) })
	defer r.Close()
	env := testEnv(t)
	cmd := `pwd; echo "$ARGUS_WORKSPACE_PATH|$ARGUS_ROOT_PATH|$ARGUS_WORKSPACE_NAME|$ARGUS_TARGET_BRANCH"`
	if err := r.StartSetup("w1", cmd, env, nil); err != nil {
		t.Fatal(err)
	}
	run := waitDone(t, r, "w1")
	out := r.Output("w1")
	real, _ := filepath.EvalSymlinks(env.WorkspacePath)
	want := env.WorkspacePath + "|/root/main|" + env.WorkspaceName + "|main"
	if run.State != OK || run.Command != cmd || !strings.Contains(out, real) || !strings.Contains(out, want) {
		t.Errorf("run=%+v output=%q", run, out)
	}
	if changes.Load() < 2 {
		t.Errorf("onChange should fire on start and end, fired %d", changes.Load())
	}
}

func TestSetupFailureRecordsExitCode(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	if err := r.StartSetup("w1", "echo boom; exit 3", testEnv(t), nil); err != nil {
		t.Fatal(err)
	}
	if run := waitDone(t, r, "w1"); run.State != Failed || run.ExitCode != 3 || !strings.Contains(r.Output("w1"), "boom") {
		t.Errorf("run=%+v output=%q", run, r.Output("w1"))
	}
}

func TestSecondSetupRefusedWhileRunning(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	env := testEnv(t)
	if err := r.StartSetup("w1", "sleep 5", env, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.StartSetup("w1", "true", env, nil); err != ErrRunning {
		t.Errorf("second start = %v, want ErrRunning", err)
	}
	r.StopSetup("w1", 2*time.Second)
	if run, _ := r.Status("w1"); run.State != Failed {
		t.Errorf("a stopped setup should be failed: %+v", run)
	}
}

func TestSetupTimeoutKillsTheGroup(t *testing.T) {
	old := setupTimeout
	setupTimeout = 300 * time.Millisecond
	defer func() { setupTimeout = old }()
	r := NewRunner(nil)
	defer r.Close()
	env := testEnv(t)
	marker := filepath.Join(env.WorkspacePath, "child-alive")
	// The child outlives the shell unless the whole group is killed.
	if err := r.StartSetup("w1", `(sleep 1; touch child-alive) & sleep 30`, env, nil); err != nil {
		t.Fatal(err)
	}
	run := waitDone(t, r, "w1")
	if run.State != Failed || !strings.Contains(r.Output("w1"), "timed out after 300ms") {
		t.Errorf("run=%+v output=%q", run, r.Output("w1"))
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the timeout should have killed the script's child process")
	}
}

func TestOutputKeepsTheLast64KB(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	if err := r.StartSetup("w1", `i=0; while [ $i -lt 3000 ]; do echo "line $i ................................"; i=$((i+1)); done`, testEnv(t), nil); err != nil {
		t.Fatal(err)
	}
	waitDone(t, r, "w1")
	out := r.Output("w1")
	if len(out) > 64<<10 || !strings.Contains(out, "line 2999") || strings.Contains(out, "line 0 ") {
		t.Errorf("buffer is %d bytes; want the last 64 KB only", len(out))
	}
}

func TestFailRecordsARunWithoutRunning(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	r.Fail("w1", "", ".argus/settings.toml: bad")
	if run, ok := r.Status("w1"); !ok || run.State != Failed || r.Output("w1") != ".argus/settings.toml: bad" {
		t.Errorf("run=%+v output=%q", run, r.Output("w1"))
	}
}

func TestBackgroundChildDoesNotHoldRun(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	start := time.Now()
	if err := r.StartSetup("w1", `sleep 5 & echo started`, testEnv(t), nil); err != nil {
		t.Fatal(err)
	}
	if run := waitDone(t, r, "w1"); run.State != OK || time.Since(start) > 4500*time.Millisecond {
		t.Errorf("a background child should not hold the run open: %+v after %v", run, time.Since(start))
	}
}

func TestScriptStdinIsEmpty(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	if err := r.StartSetup("w1", `read x; echo "got:$x"`, testEnv(t), nil); err != nil {
		t.Fatal(err)
	}
	if run := waitDone(t, r, "w1"); !strings.Contains(r.Output("w1"), "got:") {
		t.Errorf("a script reading stdin should not hang: %+v", run)
	}
}

func TestEnvAndDirWithSpaces(t *testing.T) {
	env := testEnv(t)
	env.WorkspacePath = filepath.Join(env.WorkspacePath, "my ws")
	if err := os.Mkdir(env.WorkspacePath, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, last, err := RunTeardown(context.Background(), `test "$(pwd -P)" = "$(cd "$ARGUS_WORKSPACE_PATH" && pwd -P)" && echo same`, env, 5*time.Second)
	if err != nil || code != 0 || last != "same" {
		t.Errorf("code=%d last=%q err=%v", code, last, err)
	}
}

func TestShellFallback(t *testing.T) {
	t.Setenv("SHELL", "")
	code, _, last, err := RunTeardown(context.Background(), "echo ok", testEnv(t), 5*time.Second)
	if err != nil || code != 0 || last != "ok" {
		t.Errorf("code=%d last=%q err=%v", code, last, err)
	}
}

func TestStartFailureIsReported(t *testing.T) {
	t.Setenv("SHELL", "/nonexistent/shell")
	if _, _, _, err := RunTeardown(context.Background(), "true", testEnv(t), 5*time.Second); err == nil {
		t.Error("a shell that cannot start should return an error")
	}
	r := NewRunner(nil)
	defer r.Close()
	if err := r.StartSetup("w1", "true", testEnv(t), nil); err != nil {
		t.Fatal(err)
	}
	if run := waitDone(t, r, "w1"); run.State != Failed || !strings.Contains(r.Output("w1"), "nonexistent") {
		t.Errorf("a setup that cannot start should fail with the start error: %+v %q", run, r.Output("w1"))
	}
}

func TestRunTeardown(t *testing.T) {
	env := testEnv(t)
	if code, timedOut, last, err := RunTeardown(context.Background(), "echo a; echo b; exit 2", env, 5*time.Second); err != nil || code != 2 || timedOut || last != "b" {
		t.Errorf("exit: code=%d timedOut=%v last=%q err=%v", code, timedOut, last, err)
	}
	if _, timedOut, last, err := RunTeardown(context.Background(), "echo waiting; sleep 30", env, 300*time.Millisecond); err != nil || !timedOut || last != "waiting" {
		t.Errorf("timeout: timedOut=%v last=%q err=%v", timedOut, last, err)
	}
}

func TestTail(t *testing.T) {
	if got := Tail("a\nb\nc\n", 2, 100); got != "b\nc" {
		t.Errorf("Tail lines = %q", got)
	}
	if got := Tail("aaaa\nbbbb\ncccc", 10, 9); got != "bbbb\ncccc" {
		t.Errorf("Tail bytes cuts at a line = %q", got)
	}
	if got := Tail("abcdefghij", 10, 5); got != "fghij" {
		t.Errorf("Tail single long line = %q", got)
	}
	// "abcéxyz" is 8 bytes; the last 4 start inside é (0xC3,0xA9).
	if got := Tail("abcéxyz", 10, 4); got != "xyz" {
		t.Errorf("Tail rune boundary = %q", got)
	}
}

func TestFmtDuration(t *testing.T) {
	if got := fmtDuration(15 * time.Minute); got != "15m" {
		t.Errorf("fmtDuration(15m) = %q", got)
	}
	if got := fmtDuration(300 * time.Millisecond); got != "300ms" {
		t.Errorf("fmtDuration(300ms) = %q", got)
	}
}

func TestCloseKillsRunningSetup(t *testing.T) {
	r := NewRunner(nil)
	env := testEnv(t)
	marker := filepath.Join(env.WorkspacePath, "child-alive")
	if err := r.StartSetup("w1", `(sleep 1; touch child-alive) & sleep 30`, env, nil); err != nil {
		t.Fatal(err)
	}
	r.Close()
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("Close should have killed the script's child process")
	}
}

func TestStoppedSetupSaysStopped(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	if err := r.StartSetup("w1", "echo begin; sleep 30", testEnv(t), nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	r.StopSetup("w1", 5*time.Second)
	out := r.Output("w1")
	if !strings.HasSuffix(out, "\nstopped\n") || strings.Contains(out, "signal") || strings.Contains(out, "context canceled") {
		t.Errorf("a stopped setup should end with \"stopped\": %q", out)
	}
}

func TestFailDoesNotReplaceARunningSetup(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	if err := r.StartSetup("w1", "sleep 30", testEnv(t), nil); err != nil {
		t.Fatal(err)
	}
	r.Fail("w1", "", "bad settings")
	if run, _ := r.Status("w1"); run.State != Running || run.Command != "sleep 30" {
		t.Errorf("Fail should not replace a running setup: %+v", run)
	}
}

func TestForgetDropsTheRun(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	r.Fail("w1", "", "bad settings")
	r.Forget("w1")
	if run, ok := r.Status("w1"); ok || r.Output("w1") != "" {
		t.Errorf("a forgotten workspace should have no run: %+v %q", run, r.Output("w1"))
	}
}

func TestTeardownLastLineIsPlain(t *testing.T) {
	_, _, last, err := RunTeardown(context.Background(), `printf '\033[31mboom\033[0m\r\n'; exit 1`, testEnv(t), 5*time.Second)
	if err != nil || last != "boom" {
		t.Errorf("the last line should carry no escape or control characters: %q %v", last, err)
	}
}

func TestCloseWaitsForAForgottenSetup(t *testing.T) {
	var changes atomic.Int32
	r := NewRunner(func() { changes.Add(1) })
	if err := r.StartSetup("w1", "sleep 30", testEnv(t), nil); err != nil {
		t.Fatal(err)
	}
	r.Forget("w1")
	r.Close()
	if got := changes.Load(); got != 2 {
		t.Errorf("Close should wait for a forgotten setup to end: onChange ran %d times, want 2 (start and end)", got)
	}
}

func TestRingKeepsTheLastMaxBytes(t *testing.T) {
	b := newRing(4)
	for i, w := range []string{"ab", "cde", "fghij", "k"} {
		b.Write([]byte(w))
		want := []string{"ab", "bcde", "ghij", "hijk"}[i]
		if got := b.String(); got != want {
			t.Errorf("after write %d: got %q, want %q", i, got, want)
		}
	}
}

func TestSetupRunsPrepareBeforeCommand(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	prepare := func(_ context.Context, out io.Writer) bool {
		fmt.Fprintln(out, "prepared")
		return true
	}
	if err := r.StartSetup("w1", "echo script", testEnv(t), prepare); err != nil {
		t.Fatal(err)
	}
	if run := waitDone(t, r, "w1"); run.State != OK || r.Output("w1") != "prepared\nscript\n" {
		t.Errorf("run=%+v output=%q", run, r.Output("w1"))
	}
}

func TestSetupFailedPrepareStillRunsCommand(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	prepare := func(context.Context, io.Writer) bool { return false }
	if err := r.StartSetup("w1", "echo script", testEnv(t), prepare); err != nil {
		t.Fatal(err)
	}
	if run := waitDone(t, r, "w1"); run.State != Failed || run.ExitCode != 0 || r.Output("w1") != "script\n" {
		t.Errorf("run=%+v output=%q", run, r.Output("w1"))
	}
}

func TestSetupWithoutCommandRunsPrepareOnly(t *testing.T) {
	r := NewRunner(nil)
	defer r.Close()
	for _, ok := range []bool{true, false} {
		prepare := func(_ context.Context, out io.Writer) bool {
			fmt.Fprint(out, "copied")
			return ok
		}
		if err := r.StartSetup("w1", "", testEnv(t), prepare); err != nil {
			t.Fatal(err)
		}
		want := map[bool]State{true: OK, false: Failed}[ok]
		if run := waitDone(t, r, "w1"); run.State != want || run.Command != "" || r.Output("w1") != "copied" {
			t.Errorf("ok=%v: run=%+v output=%q", ok, run, r.Output("w1"))
		}
	}
}
