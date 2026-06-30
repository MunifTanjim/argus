package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"syscall"
	"time"
)

// Supervisor runs a Provider's CLI, scans its output for the public URL, and keeps
// it alive: unexpected exit → restart with capped backoff; ctx cancel → SIGTERM
// (then SIGKILL after KillGrace) and return ctx.Err(). A failed start returns at once.
type Supervisor struct {
	Logger     *slog.Logger
	MinBackoff time.Duration // default 1s
	MaxBackoff time.Duration // default 30s
	KillGrace  time.Duration // default 5s
}

func (s Supervisor) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s Supervisor) minBackoff() time.Duration {
	if s.MinBackoff > 0 {
		return s.MinBackoff
	}
	return time.Second
}

func (s Supervisor) maxBackoff() time.Duration {
	if s.MaxBackoff > 0 {
		return s.MaxBackoff
	}
	return 30 * time.Second
}

func (s Supervisor) killGrace() time.Duration {
	if s.KillGrace > 0 {
		return s.KillGrace
	}
	return 5 * time.Second
}

// Run blocks until ctx is cancelled (returning ctx.Err()) or the process cannot
// be started (returning that error).
func (s Supervisor) Run(ctx context.Context, p Provider, origin string, report func(url string)) error {
	log := s.logger()
	backoff := s.minBackoff()

	// One-time setup (create/route a named tunnel). A provider that knows its URL
	// ahead of time reports it here instead of via output scanning.
	if lp, ok := p.(LifecycleProvider); ok {
		url, err := lp.Prepare(ctx)
		if err != nil {
			return fmt.Errorf("%s: prepare: %w", p.Name(), err)
		}
		if url != "" && report != nil {
			report(url)
		}
	}

	for {
		spec, err := p.Command(origin)
		if err != nil {
			return fmt.Errorf("%s command: %w", p.Name(), err)
		}

		cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
		// On ctx cancel, SIGTERM first; CommandContext escalates to SIGKILL after WaitDelay.
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = s.killGrace()

		pr, pw := io.Pipe()
		cmd.Stdout = pw
		cmd.Stderr = pw

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start %s: %w", p.Name(), err)
		}

		classify, _ := p.(LineClassifier)
		scanDone := make(chan struct{})
		reported := false // a fresh quick tunnel prints a new URL each run
		go func() {
			sc := bufio.NewScanner(pr)
			for sc.Scan() {
				line := sc.Text()
				// Log at the provider's own severity so steady-state chatter sits below
				// the handler threshold while warnings/errors surface. Default Info.
				level := slog.LevelInfo
				if classify != nil {
					level = classify.ClassifyLine(line)
				}
				log.Log(ctx, level, "tunnel output", "provider", p.Name(), "line", line)
				if !reported {
					if u, ok := p.ExtractURL(line); ok {
						reported = true
						report(u)
					}
				}
			}
			close(scanDone)
		}()

		waitErr := cmd.Wait()
		pw.Close() // unblock the scanner with EOF
		<-scanDone

		if ctx.Err() != nil {
			return ctx.Err()
		}

		log.Warn("tunnel exited; restarting", "provider", p.Name(), "err", waitErr, "backoff", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, s.maxBackoff())
	}
}
