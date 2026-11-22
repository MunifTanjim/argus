package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/pagent/policy"
)

const (
	defaultHookTimeout = 5 * time.Second
	hookReplyMax       = 64 << 10
)

// hookRequest is what the policy hook is sent about one action.
type hookRequest struct {
	Action    hookAction `json:"action"`
	Agent     hookAgent  `json:"agent"`
	Thread    string     `json:"thread"`
	StartedBy string     `json:"started_by"`
	Session   string     `json:"session"`
	Node      string     `json:"node"`
}

type hookAction struct {
	Tool   string          `json:"tool"`
	Target string          `json:"target"`
	Class  []string        `json:"class"`
	Input  json.RawMessage `json:"input,omitempty"`
}

type hookAgent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type hookReply struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// hookHTTP is the client of URL hooks; tests replace it.
var hookHTTP = &http.Client{}

// approvalHook asks the node's policy hook about a. It returns nil when no
// hook is configured, and when a fail-open hook fails. A fail-closed hook that
// fails denies.
func (d *Node) approvalHook(ctx context.Context, sc approvalScope, a policy.Action) *policy.HookResult {
	h := d.pagentCfg.Approvals.Hook
	if h.Command == "" && h.URL == "" {
		return nil
	}
	mode := h.Mode
	if mode == "" {
		mode = config.HookShadow
	}
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	req := hookRequest{
		Action:    hookAction{Tool: a.Tool, Target: a.Target, Class: a.Class},
		Agent:     hookAgent{ID: sc.agent.ID, Name: sc.agent.Name},
		Thread:    sc.thread.ID,
		StartedBy: sc.thread.StartedBy,
		Session:   sc.sessionID,
		Node:      d.id,
	}
	if req.Action.Class == nil {
		req.Action.Class = []string{}
	}
	if len(a.Input) > 0 && json.Valid(a.Input) {
		req.Action.Input = a.Input
	}
	body, err := json.Marshal(req)
	if err != nil {
		return hookFailed(mode, err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var raw []byte
	if h.Command != "" {
		raw, err = runHookCommand(ctx, h.Command, body)
	} else {
		raw, err = postHook(ctx, h.URL, body)
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("timed out after %s", timeout)
	}
	var rep hookReply
	if err == nil {
		if jerr := json.Unmarshal(bytes.TrimSpace(raw), &rep); jerr != nil {
			err = fmt.Errorf("bad reply: %v", jerr)
		} else {
			switch rep.Decision {
			case policy.OutcomeAllow, policy.OutcomeAsk, policy.OutcomeDeny:
			default:
				err = fmt.Errorf("bad decision %q", rep.Decision)
			}
		}
	}
	if err != nil {
		d.log.Warn("pagent: approval hook failed", "mode", mode, "err", err)
		return hookFailed(mode, err)
	}
	if mode == config.HookShadow {
		d.log.Info("pagent: approval hook (shadow)", "tool", a.Tool, "target", a.Target, "decision", rep.Decision, "reason", rep.Reason)
	}
	return &policy.HookResult{Decision: rep.Decision, Mode: mode, Reason: rep.Reason}
}

func hookFailed(mode string, err error) *policy.HookResult {
	if mode == config.HookFailClosed {
		return &policy.HookResult{Decision: policy.OutcomeDeny, Mode: mode, Reason: "approval hook failed: " + err.Error()}
	}
	return nil
}

func runHookCommand(ctx context.Context, command string, body []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = bytes.NewReader(body)
	var out, errOut bytes.Buffer
	cmd.Stdout = &limitWriter{w: &out, n: hookReplyMax}
	cmd.Stderr = &limitWriter{w: &errOut, n: 4 << 10}
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return nil, fmt.Errorf("%v: %s", err, msg)
		}
		return nil, err
	}
	return out.Bytes(), nil
}

func postHook(ctx context.Context, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hookHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, hookReplyMax))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	return data, nil
}

// limitWriter keeps the first n bytes written and drops the rest.
type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		if _, err := l.w.Write(p[:k]); err != nil {
			return 0, err
		}
		l.n -= k
	}
	return len(p), nil
}
