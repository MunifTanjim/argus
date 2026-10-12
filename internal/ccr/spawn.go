package ccr

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// SpawnerConfig controls how a session's headless worker is launched.
type SpawnerConfig struct {
	Claude string // path to the claude binary (required to auto-spawn)
	// BaseURL is the externally reachable ingress base, e.g.
	// https://api.anthropic.com:8443 (scheme://host[:port]).
	BaseURL string
	// APIKey, when set, is exported to the worker as ANTHROPIC_API_KEY.
	APIKey string
	// Env is appended to the worker environment.
	Env []string
	// Debug turns on worker --verbose.
	Debug bool
}

// Worker is one spawned headless claude process.
type Worker struct {
	Session *Session
	Cmd     *exec.Cmd
	Log     *bytes.Buffer

	mu     sync.Mutex
	exited error
	done   chan struct{}
}

// SpawnWorker launches a headless claude worker exactly like the official
// bridge does (see devdocs/claude-remote-control-protocol.md): stream-json
// over the remote SDK transport, resuming from the session URL.
func SpawnWorker(ctx context.Context, cfg SpawnerConfig, s *Session, cwd string) (*Worker, error) {
	if cfg.Claude == "" {
		return nil, fmt.Errorf("spawner: claude path not configured")
	}
	sessionURL := fmt.Sprintf("%s/v1/code/sessions/%s", strings.TrimRight(cfg.BaseURL, "/"), s.ID)

	args := []string{
		"--print",
		"--sdk-url", sessionURL,
		"--session-id", s.ID,
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--replay-user-messages",
		"--resume=" + sessionURL,
	}
	if cfg.Debug {
		args = append(args, "--verbose", "--debug-file", fmt.Sprintf("/tmp/ccr-worker-%s.log", s.ID))
	}

	cmd := exec.CommandContext(ctx, cfg.Claude, args...)
	cmd.Dir = cwd
	cmd.Env = workerEnv(cfg, s)

	w := &Worker{Session: s, Cmd: cmd, Log: &bytes.Buffer{}, done: make(chan struct{})}
	cmd.Stdout = w.Log
	cmd.Stderr = w.Log
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn worker: %w", err)
	}
	go func() {
		w.mu.Lock()
		w.exited = cmd.Wait()
		w.mu.Unlock()
		s.MarkDead()
		close(w.done)
	}()
	return w, nil
}

// workerEnv builds the child environment following the bridge recipe: the
// bearer token via CLAUDE_CODE_SESSION_ACCESS_TOKEN, the epoch, and the
// bridge environment kind. Inference credentials pass through from the
// parent environment (ANTHROPIC_API_KEY, Bedrock/Vertex vars, …), so no
// claude.ai OAuth is involved.
func workerEnv(cfg SpawnerConfig, s *Session) []string {
	managed := map[string]string{
		"CLAUDE_CODE_SESSION_ACCESS_TOKEN": s.Token,
		"CLAUDE_CODE_ENVIRONMENT_KIND":    "bridge",
		"CLAUDE_CODE_WORKER_EPOCH":        fmt.Sprint(s.Epoch()),
		"CLAUDE_CODE_OAUTH_TOKEN":         "",
		"CLAUDE_CODE_REMOTE":              "1",
	}
	if cfg.APIKey != "" {
		managed["ANTHROPIC_API_KEY"] = cfg.APIKey
	}

	// Inherit the parent environment (PATH, HOME, Vertex config, CA trust,
	// proxy vars…), dropping keys we manage explicitly so ours win.
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, ok := managed[key]; ok {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, cfg.Env...)
	for k, v := range managed {
		env = append(env, k+"="+v)
	}
	return env
}

// Wait blocks until the worker exits and returns its error.
func (w *Worker) Wait() error {
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.exited
}

// Exited reports whether the worker has exited.
func (w *Worker) Exited() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

// Err returns the exit error, if any.
func (w *Worker) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.exited
}

// TailLog returns the last n bytes of captured worker output.
func (w *Worker) TailLog(n int) []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.Log.Bytes()
	if len(b) > n {
		return b[len(b)-n:]
	}
	return b
}
