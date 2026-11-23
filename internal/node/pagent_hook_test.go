package node

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/policy"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// hookScript writes a hook command that saves its input and prints reply.
func hookScript(t *testing.T, reply string) (command, inputFile string) {
	t.Helper()
	dir := t.TempDir()
	inputFile = filepath.Join(dir, "in.json")
	script := filepath.Join(dir, "hook.sh")
	body := "#!/bin/sh\ncat > " + inputFile + "\nprintf '%s' '" + reply + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, inputFile
}

func (r *approvalRig) activity(t *testing.T) []pagent.Activity {
	t.Helper()
	rows, err := r.s.ListActivity(context.Background(), r.a.ID, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestApprovalHookEnforceModes(t *testing.T) {
	for _, mode := range []string{config.HookFailOpen, config.HookFailClosed} {
		r := newApprovalRig(t, "routine")
		cmd, in := hookScript(t, `{"decision":"allow","reason":"ok by hook"}`)
		r.d.pagentCfg.Approvals.Hook = config.ApprovalHook{Command: cmd, Mode: mode}
		if got := r.decideNow(t, shellPermission("ls")); got.Behavior != "allow" {
			t.Errorf("%s: answer = %+v", mode, got)
		}
		var req hookRequest
		data, _ := os.ReadFile(in)
		if err := json.Unmarshal(data, &req); err != nil {
			t.Fatalf("%s: hook input %q: %v", mode, data, err)
		}
		if req.Action.Tool != "Bash" || req.Action.Target != "ls" || req.Agent.ID != r.a.ID || req.Thread != r.th || req.StartedBy != "routine" || req.Session != r.sid {
			t.Errorf("%s: hook input = %+v", mode, req)
		}
		if rows := r.activity(t); len(rows) != 1 || rows[0].DecidedBy != policy.ByHook || rows[0].Reason != "ok by hook" {
			t.Errorf("%s: activity = %+v", mode, rows)
		}
	}
}

func TestApprovalHookShadowOnlyRecords(t *testing.T) {
	r := newApprovalRig(t, "routine")
	cmd, _ := hookScript(t, `{"decision":"allow","reason":"looks fine"}`)
	r.d.pagentCfg.Approvals.Hook = config.ApprovalHook{Command: cmd, Mode: config.HookShadow}
	if got := r.decideNow(t, shellPermission("ls")); got.Behavior != "deny" || got.Message != policy.ReasonUnattended {
		t.Errorf("answer = %+v", got)
	}
	rows := r.activity(t)
	if len(rows) != 1 || rows[0].DecidedBy != policy.ByDefault || rows[0].Hook != "allow looks fine" {
		t.Errorf("activity = %+v", rows)
	}
}

func TestApprovalHookFailures(t *testing.T) {
	r := newApprovalRig(t, startedByYou)
	r.rules(t, policy.Rule{Tool: "Bash", Target: "ls", Decision: policy.OutcomeAllow})
	slow := filepath.Join(t.TempDir(), "slow.sh")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.d.pagentCfg.Approvals.Hook = config.ApprovalHook{Command: slow, Mode: config.HookFailClosed, Timeout: 100 * time.Millisecond}
	start := time.Now()
	got := r.decideNow(t, shellPermission("ls"))
	if got.Behavior != "deny" || got.Message != "approval hook failed: timed out after 100ms" {
		t.Errorf("fail-closed timeout = %+v", got)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("timeout not enforced: %v", time.Since(start))
	}
	bad, _ := hookScript(t, `nonsense`)
	r.d.pagentCfg.Approvals.Hook = config.ApprovalHook{Command: bad, Mode: config.HookFailOpen}
	if got := r.decideNow(t, shellPermission("ls")); got.Behavior != "allow" {
		t.Errorf("fail-open bad reply = %+v (rules should decide)", got)
	}
}

func TestApprovalHookURL(t *testing.T) {
	var got hookRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(data, &got)
		_, _ = w.Write([]byte(`{"decision":"deny","reason":"policy says no"}`))
	}))
	defer srv.Close()
	r := newApprovalRig(t, startedByYou)
	r.d.pagentCfg.Approvals.Hook = config.ApprovalHook{URL: srv.URL, Mode: config.HookFailOpen}
	if ans := r.decideNow(t, shellPermission("ls")); ans.Behavior != "deny" || ans.Message != "policy says no" {
		t.Errorf("answer = %+v", ans)
	}
	if got.Action.Tool != "Bash" || got.Node != r.d.id {
		t.Errorf("request = %+v", got)
	}
}

func TestApprovalActivityForCards(t *testing.T) {
	r := newApprovalRig(t, startedByYou)
	c := parkRequest(t, r.d, r.sid, "r1", shellPermission("git push"))
	r.answer(t, c, api.RespondParams{RequestID: "r1", OptionValue: optAllowThread})
	c = parkRequest(t, r.d, r.sid, "r2", shellPermission("rm -rf x"))
	r.answer(t, c, api.RespondParams{RequestID: "r2", OptionValue: optDeny, Reason: "no"})
	rows := r.activity(t)
	if len(rows) != 2 {
		t.Fatalf("activity = %+v", rows)
	}
	if rows[0].Decision != "deny" || rows[0].DecidedBy != decidedUser || rows[0].Reason != "no" || rows[0].Class != policy.ClassIrreversible {
		t.Errorf("deny row = %+v", rows[0])
	}
	if rows[1].Decision != "allow" || rows[1].DecidedBy != decidedUser || rows[1].Target != "git" || rows[1].Class != policy.ClassOutward || rows[1].Reason != "allowed for this thread" {
		t.Errorf("allow row = %+v", rows[1])
	}

	res, err := callPAgent[api.PAgentActivityResult](t, r.d.handlePAgentActivity, api.PAgentActivityParams{AgentID: r.a.ID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Activity) != 1 || res.Activity[0].Decision != "deny" || len(res.Activity[0].Class) != 1 {
		t.Errorf("rpc = %+v", res)
	}
}

func TestApprovalActivityExpiry(t *testing.T) {
	r := newApprovalRig(t, "routine")
	r.d.pagentCfg.Approvals.UnattendedExpiry = 30 * time.Millisecond
	r.rules(t, policy.Rule{Tool: "Bash", Target: "make", Decision: policy.OutcomeAsk})
	r.decideNow(t, shellPermission("make"))
	rows := r.activity(t)
	if len(rows) != 1 || rows[0].DecidedBy != decidedExpiry || rows[0].Reason != reasonNoAnswer {
		t.Errorf("activity = %+v", rows)
	}
	res, _ := callPAgent[api.PAgentActivityResult](t, r.d.handlePAgentActivity, api.PAgentActivityParams{AgentID: r.a.ID, Unattended: true})
	if len(res.Activity) != 1 {
		t.Errorf("unattended = %+v", res)
	}
}

func TestDraftSendGrantsOnce(t *testing.T) {
	r := newApprovalRig(t, startedByYou)
	r.d.deliverFn = func(context.Context, session.Session, string) error { return nil }
	r.d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", Server: session.TmuxServerArgus, PaneID: "%1", Status: session.StatusIdle})
	input := map[string]string{"text": "hi"}
	id := r.insertDraft(t, map[string]any{"tool": "mcp__slack__post_message", "target": "post_message", "classes": []string{policy.ClassOutward}, "input": input})
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentDraftSend, api.PAgentDraftParams{ThreadID: r.th, EventID: id}); err != nil {
		t.Fatal(err)
	}
	ev := toolPermission("mcp__slack__post_message", input)
	if got := r.decideNow(t, ev); got.Behavior != "allow" {
		t.Fatalf("approved draft = %+v", got)
	}
	c := parkRequest(t, r.d, r.sid, "r2", ev)
	if ix := head(r.d, r.sid); ix == nil || ix.RequestID != "r2" {
		t.Fatalf("second request not asked: %+v", ix)
	}
	c.cancel()
	waitOut(t, c)
}

func TestApprovalHeadlessCardClearsWhenAnsweredInCLI(t *testing.T) {
	r, _, s := headlessRig(t, startedByYou)
	s = headlessAsk(r, s, "git push")
	if shown := r.d.withCaps(s); len(shown.Interaction.Options) != 5 {
		t.Fatalf("card = %+v", shown.Interaction)
	}
	s, _ = r.d.reg.ApplyHook(registry.HookUpdate{Agent: "opencode", AgentSessionID: "oc1", Status: session.StatusWorking, ReplaceInteraction: true})
	r.d.approvalTrack(context.Background(), registry.Event{Type: registry.EventUpdated, Session: s})
	r.d.approvals.mu.Lock()
	n := len(r.d.approvals.cards)
	r.d.approvals.mu.Unlock()
	if n != 0 {
		t.Errorf("cards left = %d", n)
	}
}

func TestHookURLAllowed(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://hooks.example.com/x": true,
		"http://127.0.0.1:8080/x":     true,
		"http://localhost/x":          true,
		"http://[::1]:9/x":            true,
		"http://hooks.example.com/x":  false,
		"ftp://hooks.example.com/x":   false,
		"https:///x":                  false,
	} {
		if got := hookURLAllowed(raw); got != want {
			t.Errorf("%s = %v", raw, got)
		}
	}
}
