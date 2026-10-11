package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"sync"
)

// response is one stdout record with type "response": the answer to a command
// correlated by id. Records without a matching pending call are session events
// (agent_start, message_update, tool_execution_*, extension_ui_request, ...)
// delivered to onEvent.
type response struct {
	ID      string          `json:"id"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// rpcConn speaks pi's RPC protocol (strict JSONL over stdin/stdout) with one
// pi --mode rpc child process. The zero value is unusable; build with
// newRPCConn (tests) or spawnRPC (production), then start it.
//
// Framing: one JSON object per line terminated by LF. Reads split on the LF
// byte only — never on Unicode line separators, which are valid inside JSON
// strings. Stdout must be drained continuously or pi stalls on backpressure.
type rpcConn struct {
	stdin  io.WriteCloser
	stdout *bufio.Reader // byte-oriented; ReadBytes splits on '\n' only

	onEvent func(typ string, raw json.RawMessage)

	wmu sync.Mutex // serializes command writes

	mu      sync.Mutex
	pending map[string]chan response

	done      chan struct{}
	closeOnce sync.Once
}

func newRPCConn(stdin io.WriteCloser, stdout io.Reader) *rpcConn {
	return &rpcConn{
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		pending: map[string]chan response{},
		done:    make(chan struct{}),
	}
}

// start reads stdout, delivering session events to onEvent.
func (c *rpcConn) start(onEvent func(typ string, raw json.RawMessage)) {
	c.onEvent = onEvent
	go c.readLoop()
}

// send writes one command record without waiting for a response (used for
// extension_ui_response and fire-and-forget notifications).
func (c *rpcConn) send(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.stdin.Write(line)
	return err
}

// call sends a command and waits for its correlated response. id must be
// unique among outstanding calls; responses may arrive out of order.
func (c *rpcConn) call(ctx context.Context, id, cmd string, params map[string]any) (json.RawMessage, error) {
	rec := map[string]any{"id": id, "type": cmd}
	maps.Copy(rec, params)
	ch := make(chan response, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.send(rec); err != nil {
		return nil, fmt.Errorf("pi: send %s: %w", cmd, err)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("pi: connection closed during %s", cmd)
	case r := <-ch:
		if !r.Success {
			return nil, fmt.Errorf("pi: %s failed: %s", cmd, r.Error)
		}
		return r.Data, nil
	}
}

func (c *rpcConn) readLoop() {
	defer close(c.done)
	for {
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return
		}
		if streamingDelta(line) {
			continue
		}
		var probe struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		if probe.Type == "response" && probe.ID != "" {
			var r response
			if json.Unmarshal(line, &r) != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[r.ID]
			c.mu.Unlock()
			if ch != nil {
				ch <- r
				continue
			}
		}
		if c.onEvent != nil {
			c.onEvent(probe.Type, line)
		}
	}
}

// streamingDelta reports a per-token message_update or a tool_execution_update
// (which carries the whole result so far): no handler reads them, so they skip
// decoding. pi writes the type key first.
func streamingDelta(line []byte) bool {
	return bytes.HasPrefix(line, []byte(`{"type":"message_update"`)) ||
		bytes.HasPrefix(line, []byte(`{"type":"tool_execution_update"`))
}

// close shuts the connection down orderly: closing stdin asks pi to dispose
// the runtime and exit. It does not wait for the process (see supervisor).
func (c *rpcConn) close() error {
	var err error
	c.closeOnce.Do(func() { err = c.stdin.Close() })
	return err
}

// piBin is the pi executable name, a var so tests can stub it.
var piBin = "pi"

// spawnRPC starts `pi --mode rpc --session-id <id>` in cwd with the gate
// extension loaded and ARGUS_MANAGED set, returning the connected rpcConn.
// The caller owns the process (reap via cmd.Wait after conn.close).
func spawnRPC(ctx context.Context, cwd, sessionID, extensionPath string) (*rpcConn, *exec.Cmd, error) {
	args := []string{"--mode", "rpc", "--session-id", sessionID}
	if extensionPath != "" {
		args = append(args, "--extension", extensionPath)
	}
	cmd := exec.CommandContext(ctx, piBin, args...)
	cmd.Dir = cwd
	cmd.Env = append(cmd.Environ(), gateEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("pi: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("pi: stdout pipe: %w", err)
	}
	// stderr stays inherited: diagnostics and crash logs, never protocol.
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("pi: start: %w", err)
	}
	return newRPCConn(stdin, stdout), cmd, nil
}
