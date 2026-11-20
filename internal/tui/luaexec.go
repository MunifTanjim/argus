package tui

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	defaultExecTimeout = 2 * time.Second
	execKillDelay      = 2 * time.Second
	execOutputLimit    = 1 << 20
)

type execOpts struct {
	cwd           string
	env           map[string]*string
	clearEnv      bool
	stdin         *string
	timeout       time.Duration
	discardStdout bool
	discardStderr bool
}

type execResult struct {
	code           int
	signal         string
	stdout, stderr string
	timedOut       bool
}

// runExec returns an error only when the command never started.
func runExec(argv []string, o execOpts) (execResult, error) {
	timeout := o.timeout
	if timeout == 0 {
		timeout = defaultExecTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var mu sync.Mutex
	var groupKill *time.Timer
	cmd.Cancel = func() error {
		pgid := cmd.Process.Pid
		mu.Lock()
		groupKill = time.AfterFunc(execKillDelay, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		mu.Unlock()
		return syscall.Kill(-pgid, syscall.SIGTERM)
	}
	cmd.WaitDelay = execKillDelay
	cmd.Dir = o.cwd
	cmd.Env = execEnviron(o)
	if o.stdin != nil {
		cmd.Stdin = strings.NewReader(*o.stdin)
	}

	var stdout, stderr cappedBuffer
	if !o.discardStdout {
		cmd.Stdout = &stdout
	}
	if !o.discardStderr {
		cmd.Stderr = &stderr
	}

	if err := cmd.Start(); err != nil {
		return execResult{}, err
	}
	werr := cmd.Wait()
	mu.Lock()
	// A timeout after the command exits, while a child holds its output open,
	// stops nothing and does not count.
	timedOut := groupKill != nil
	// Processes left in the group after the command exits still get the
	// SIGKILL. With none left, the timer stops, so a reused pgid is safe.
	if groupKill != nil && syscall.Kill(-cmd.Process.Pid, 0) != nil {
		groupKill.Stop()
	}
	mu.Unlock()

	res := execResult{
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		timedOut: timedOut,
		code:     -1,
	}
	if ps := cmd.ProcessState; ps != nil {
		res.code = ps.ExitCode()
		if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			res.signal = unix.SignalName(ws.Signal())
		}
	} else if werr != nil {
		return execResult{}, werr
	}
	return res, nil
}

// cappedBuffer keeps the first execOutputLimit bytes and discards the rest
// while it reports them written, so the command does not block on a full pipe.
// Not embedded: a promoted ReadFrom lets io.Copy skip Write and the cap.
type cappedBuffer struct{ buf bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := execOutputLimit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }

// execEnviron returns nil, which inherits the environment, when o changes
// nothing.
func execEnviron(o execOpts) []string {
	if len(o.env) == 0 && !o.clearEnv {
		return nil
	}
	vars := map[string]string{}
	if !o.clearEnv {
		for _, kv := range os.Environ() {
			if k, v, ok := strings.Cut(kv, "="); ok {
				vars[k] = v
			}
		}
	}
	for k, v := range o.env {
		if v == nil {
			delete(vars, k)
		} else {
			vars[k] = *v
		}
	}
	out := make([]string, 0, len(vars))
	for k, v := range vars {
		out = append(out, k+"="+v)
	}
	return out
}
