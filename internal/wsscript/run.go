package wsscript

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Env is the environment a script sees on top of the node's.
type Env struct {
	WorkspacePath, RootPath, WorkspaceName, TargetBranch string
}

func (e Env) vars() []string {
	return []string{
		"ARGUS_WORKSPACE_PATH=" + e.WorkspacePath,
		"ARGUS_ROOT_PATH=" + e.RootPath,
		"ARGUS_WORKSPACE_NAME=" + e.WorkspaceName,
		"ARGUS_TARGET_BRANCH=" + e.TargetBranch,
	}
}

type State string

const (
	Running State = "running"
	OK      State = "ok"
	Failed  State = "failed"
)

type Run struct {
	State    State
	Command  string
	ExitCode int
	Started  time.Time
	Ended    time.Time
}

var ErrRunning = errors.New("setup is already running")

var setupTimeout = 15 * time.Minute

const bufferBytes = 64 << 10

type setupRun struct {
	run    Run
	out    *ring
	cancel context.CancelFunc
	done   chan struct{}
}

// Runner runs workspace setups in the background and keeps each workspace's
// last run in memory.
type Runner struct {
	mu       sync.Mutex
	runs     map[string]*setupRun
	ctx      context.Context
	cancel   context.CancelFunc
	onChange func()
	wg       sync.WaitGroup
}

// NewRunner calls onChange (may be nil) after a setup starts or ends.
func NewRunner(onChange func()) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{runs: map[string]*setupRun{}, ctx: ctx, cancel: cancel, onChange: onChange}
}

func (r *Runner) changed() {
	if r.onChange != nil {
		r.onChange()
	}
}

// StartSetup runs prepare (may be nil) and then command (may be empty) for
// wsID in the background. prepare writes to the run's output and returns false
// to mark the run failed; command runs either way.
func (r *Runner) StartSetup(wsID, command string, env Env, prepare func(context.Context, io.Writer) bool) error {
	r.mu.Lock()
	if cur := r.runs[wsID]; cur != nil && cur.run.State == Running {
		r.mu.Unlock()
		return ErrRunning
	}
	ctx, cancel := context.WithTimeout(r.ctx, setupTimeout)
	sr := &setupRun{run: Run{State: Running, Command: command, Started: time.Now()}, out: newRing(bufferBytes), cancel: cancel, done: make(chan struct{})}
	r.runs[wsID] = sr
	r.wg.Add(1)
	r.mu.Unlock()
	r.changed()
	go func() {
		defer r.wg.Done()
		defer close(sr.done)
		defer cancel()
		prepared := prepare == nil || prepare(ctx, sr.out)
		var code int
		var err error
		if command != "" && ctx.Err() == nil {
			code, err = execScript(ctx, command, env, sr.out)
		}
		r.mu.Lock()
		sr.run.Ended = time.Now()
		sr.run.ExitCode = code
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			fmt.Fprintf(sr.out, "\ntimed out after %s\n", fmtDuration(setupTimeout))
			sr.run.State = Failed
		case errors.Is(ctx.Err(), context.Canceled):
			fmt.Fprint(sr.out, "\nstopped\n")
			sr.run.State = Failed
		case err != nil || code != 0:
			if err != nil && code == -1 {
				fmt.Fprintf(sr.out, "\n%s\n", err)
			}
			sr.run.State = Failed
		case !prepared:
			sr.run.State = Failed
		default:
			sr.run.State = OK
		}
		r.mu.Unlock()
		r.changed()
	}()
	return nil
}

// Fail records a failed run for wsID without running anything. It does nothing
// while wsID's setup runs.
func (r *Runner) Fail(wsID, command, output string) {
	out := newRing(bufferBytes)
	out.Write([]byte(output))
	now := time.Now()
	r.mu.Lock()
	if cur := r.runs[wsID]; cur != nil && cur.run.State == Running {
		r.mu.Unlock()
		return
	}
	r.runs[wsID] = &setupRun{run: Run{State: Failed, Command: command, ExitCode: -1, Started: now, Ended: now}, out: out}
	r.mu.Unlock()
	r.changed()
}

// Forget drops wsID's run, stopping it if it still runs.
func (r *Runner) Forget(wsID string) {
	r.mu.Lock()
	sr := r.runs[wsID]
	delete(r.runs, wsID)
	r.mu.Unlock()
	if sr != nil && sr.cancel != nil {
		sr.cancel()
	}
}

func (r *Runner) Status(wsID string) (Run, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sr := r.runs[wsID]
	if sr == nil {
		return Run{}, false
	}
	return sr.run, true
}

func (r *Runner) Output(wsID string) string {
	r.mu.Lock()
	sr := r.runs[wsID]
	r.mu.Unlock()
	if sr == nil {
		return ""
	}
	return sr.out.String()
}

// StopSetup stops a running setup of wsID and waits up to wait for it to end.
func (r *Runner) StopSetup(wsID string, wait time.Duration) {
	r.mu.Lock()
	sr := r.runs[wsID]
	r.mu.Unlock()
	if sr == nil || sr.cancel == nil {
		return
	}
	sr.cancel()
	select {
	case <-sr.done:
	case <-time.After(wait):
	}
}

// Close stops every setup, forgotten ones too, and waits up to 2 s for them
// to exit.
func (r *Runner) Close() {
	r.cancel()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

// RunTeardown runs command and waits for it, up to timeout. lastLine is the
// last non-empty output line.
func RunTeardown(ctx context.Context, command string, env Env, timeout time.Duration) (exitCode int, timedOut bool, lastLine string, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out := newRing(bufferBytes)
	code, err := execScript(ctx, command, env, out)
	lastLine = lastNonEmpty(CleanOutput(out.String()))
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return code, true, lastLine, nil
	}
	if err != nil && code == -1 {
		return code, false, lastLine, err
	}
	return code, false, lastLine, nil
}

// execScript runs command in its own process group so a cancel also ends its
// children. The exit code is -1 when the process did not start or was killed.
func execScript(ctx context.Context, command string, env Env, out *ring) (int, error) {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, sh, "-c", command)
	cmd.Dir = env.WorkspacePath
	cmd.Env = append(os.Environ(), env.vars()...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// A background child can keep the output pipe open after the shell exits.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr) && exitErr.ExitCode() >= 0:
		return exitErr.ExitCode(), nil
	case errors.Is(err, exec.ErrWaitDelay):
		return 0, nil
	}
	return -1, err
}

// ring keeps the last max bytes written to it.
type ring struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newRing(max int) *ring { return &ring{max: max} }

// Write lets the buffer grow to twice max before it drops the oldest bytes,
// so a chatty script does not copy max bytes on every write.
func (b *ring) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > 2*b.max {
		b.buf = append(b.buf[:0], b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *ring) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	buf := b.buf
	if over := len(buf) - b.max; over > 0 {
		buf = buf[over:]
	}
	return strings.ToValidUTF8(string(buf), "�")
}

// Tail returns the last lines of s within maxBytes, cut at a line boundary. A
// single line over maxBytes keeps its last maxBytes bytes, cut at a rune
// boundary.
func Tail(s string, lines, maxBytes int) string {
	all := strings.Split(strings.TrimRight(s, "\n"), "\n")
	all = all[max(0, len(all)-lines):]
	for len(all) > 1 && len(strings.Join(all, "\n")) > maxBytes {
		all = all[1:]
	}
	result := strings.Join(all, "\n")
	if len(result) > maxBytes {
		result = result[len(result)-maxBytes:]
		for len(result) > 0 && result[0]&0xC0 == 0x80 {
			result = result[1:]
		}
	}
	return result
}

func fmtDuration(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return d.String()
}

// CleanOutput makes script output safe to show: each line keeps only the text
// after its last carriage return, and escape sequences and control characters
// other than tab are removed.
func CleanOutput(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.TrimSuffix(l, "\r")
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		lines[i] = strings.Map(func(r rune) rune {
			if r != '\t' && unicode.IsControl(r) {
				return -1
			}
			return r
		}, ansi.Strip(l))
	}
	return strings.Join(lines, "\n")
}

func lastNonEmpty(s string) string {
	lines := bytes.Split([]byte(s), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(string(lines[i])); l != "" {
			return l
		}
	}
	return ""
}
