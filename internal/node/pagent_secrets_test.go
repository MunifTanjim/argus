package node

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/policy"
	"github.com/MunifTanjim/argus/internal/session"
)

func useSecretKey(t *testing.T, d *Node) {
	t.Helper()
	d.secrets.path = filepath.Join(t.TempDir(), "pagent-secrets.key")
}

func setSecret(t *testing.T, d *Node, agentID, name, value string) {
	t.Helper()
	if _, err := callPAgent[struct{}](t, d.handlePAgentSecretSet, api.PAgentSecretSetParams{AgentID: agentID, Name: name, Description: "for " + name, Value: value}); err != nil {
		t.Fatal(err)
	}
}

func TestSecretRPCs(t *testing.T) {
	r := newMemRig(t, 0)
	useSecretKey(t, r.d)
	setSecret(t, r.d, r.a.ID, "GH_TOKEN", "ghp_secretvalue")
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentSecretSet, api.PAgentSecretSetParams{AgentID: r.a.ID, Name: "bad-name", Value: "x"}); err == nil {
		t.Error("bad name accepted")
	}
	res, err := callPAgent[api.PAgentSecretListResult](t, r.d.handlePAgentSecretList, api.PAgentRef{AgentID: r.a.ID})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(res)
	if len(res.Secrets) != 1 || res.Secrets[0].Name != "GH_TOKEN" || strings.Contains(string(raw), "ghp_") {
		t.Fatalf("list = %s", raw)
	}
	fi, err := os.Stat(r.d.secrets.path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key = %v %v", fi, err)
	}
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentSecretRm, api.PAgentSecretRef{AgentID: r.a.ID, Name: "GH_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentSecretRm, api.PAgentSecretRef{AgentID: r.a.ID, Name: "GH_TOKEN"}); err == nil {
		t.Error("second rm succeeded")
	}
}

func TestSecretsInjectedIntoSpawnAndListedInInstructions(t *testing.T) {
	r := newThreadRig(t)
	useSecretKey(t, r.d)
	setSecret(t, r.d, r.agent.ID, "GH_TOKEN", "ghp_secretvalue")
	var env []string
	spawn := r.d.spawnFn
	r.d.spawnFn = func(ctx context.Context, p api.SpawnParams, presetID string) (api.SpawnResult, error) {
		env = secretEnvFrom(ctx)
		return spawn(ctx, p, presetID)
	}
	if _, err := r.send(t, api.PAgentSendParams{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env[0] != "GH_TOKEN=ghp_secretvalue" {
		t.Errorf("env = %v", env)
	}
	data, err := os.ReadFile(filepath.Join(r.agent.Dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "$GH_TOKEN: for GH_TOKEN") || strings.Contains(string(data), "ghp_secretvalue") {
		t.Errorf("instructions:\n%s", data)
	}
}

func TestWithSecretEnvWrapsCommand(t *testing.T) {
	ctx := context.WithValue(context.Background(), secretEnvKey{}, []string{"A_KEY=it's a secret"})
	command, args, cleanup, err := withSecretEnv(ctx, "sh", []string{"-c", `printf %s "$A_KEY"`})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, a := range args {
		if strings.Contains(a, "secret") && !strings.HasPrefix(a, os.TempDir()) {
			t.Fatalf("value on the command line: %q", args)
		}
	}
	out, err := runCmd(command, args...)
	if err != nil || out != "it's a secret" {
		t.Fatalf("out = %q %v", out, err)
	}
	if _, err := os.Stat(args[2]); !os.IsNotExist(err) {
		t.Errorf("assignment file left behind: %v", err)
	}
	c2, a2, _, _ := withSecretEnv(context.Background(), "claude", []string{"x"})
	if c2 != "claude" || len(a2) != 1 {
		t.Errorf("no secrets: %s %v", c2, a2)
	}
}

func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

func TestRequestSecretFlow(t *testing.T) {
	r := newMemRig(t, 0)
	useSecretKey(t, r.d)
	ctx := context.Background()
	hookSession(r.d, "%1", "c1")
	s, _ := r.d.reg.FindByAgentSession("claude", "c1")
	if err := r.s.InsertThreadSession(ctx, pagent.ThreadSession{ThreadID: r.th, SessionID: s.ID, CLI: "claude", AgentSessionID: "c1", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	caller := callerRef{Local: &session.Session{ID: s.ID, PAgentID: r.a.ID, PAgentThreadID: r.th}}
	if _, err := r.d.mcpRequestSecret(ctx, caller, requestSecretParams{Name: "API_KEY", Description: "weather API"}); err != nil {
		t.Fatal(err)
	}
	got, _ := r.d.reg.Get(s.ID)
	ix := got.Interaction
	if ix == nil || ix.Kind != session.InteractionSecret || ix.SecretName != "API_KEY" || ix.Message != "weather API" {
		t.Fatalf("interaction = %+v", ix)
	}
	var sent []string
	r.d.deliverFn = func(_ context.Context, _ session.Session, text string) error {
		sent = append(sent, text)
		return nil
	}
	if _, err := r.d.handleSessionRespond(ctx, mustJSON(api.RespondParams{SessionID: s.ID, RequestID: ix.RequestID, OptionValue: secretSave, SecretValue: "wx-12345"})); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || strings.Contains(sent[0], "wx-12345") || !strings.Contains(sent[0], "$API_KEY") {
		t.Fatalf("sent = %q", sent)
	}
	if got, _ = r.d.reg.Get(s.ID); got.Interaction != nil {
		t.Errorf("interaction left: %+v", got.Interaction)
	}
	vals := r.d.agentSecrets(ctx, r.a.ID)
	if len(vals) != 1 || vals[0].Value != "wx-12345" || vals[0].Description != "weather API" {
		t.Errorf("stored = %+v", vals)
	}
	if _, err := r.d.mcpRequestSecret(ctx, caller, requestSecretParams{Name: "lower"}); err == nil {
		t.Error("bad name accepted")
	}
}

func TestSecretRedaction(t *testing.T) {
	r := newApprovalRig(t, "routine")
	useSecretKey(t, r.d)
	setSecret(t, r.d, r.a.ID, "GH_TOKEN", "ghp_secretvalue")
	r.rules(t, policy.Rule{Tool: "Bash", Target: "curl", Decision: policy.OutcomeAllow, Note: "uses ghp_secretvalue"})
	cmd := `curl -X POST -H "Authorization: ghp_secretvalue" https://api.example/x`
	if got := r.decideNow(t, shellPermission(cmd)); got.Behavior != "deny" {
		t.Fatalf("answer = %+v", got)
	}
	ev := r.draftEvents(t)
	if len(ev) != 1 || strings.Contains(string(ev[0].Payload), "ghp_secretvalue") || !strings.Contains(string(ev[0].Payload), "«GH_TOKEN»") {
		t.Fatalf("draft = %s", ev[0].Payload)
	}
	// A sent shell draft runs with the real value and records redacted output.
	id := r.insertDraft(t, map[string]any{"tool": "Bash", "command": `printf '%s' "«GH_TOKEN»"`})
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentDraftSend, api.PAgentDraftParams{ThreadID: r.th, EventID: id}); err != nil {
		t.Fatal(err)
	}
	sent := r.waitEvent(t, evDraftSent)
	var p struct{ Output string }
	_ = json.Unmarshal(sent.Payload, &p)
	if p.Output != "«GH_TOKEN»" {
		t.Errorf("output = %q", p.Output)
	}
	for _, row := range r.activity(t) {
		if strings.Contains(row.Reason+row.Target+row.Rule, "ghp_secretvalue") {
			t.Errorf("activity not redacted: %+v", row)
		}
	}
}

func TestApprovalCardRedactsInput(t *testing.T) {
	r := newApprovalRig(t, startedByYou)
	useSecretKey(t, r.d)
	setSecret(t, r.d, r.a.ID, "GH_TOKEN", "ghp_secretvalue")
	c := parkRequest(t, r.d, r.sid, "r1", shellPermission("echo ghp_secretvalue"))
	if ix := head(r.d, r.sid); strings.Contains(ix.ToolInput, "ghp_secretvalue") || !strings.Contains(ix.ToolInput, "«GH_TOKEN»") {
		t.Errorf("card input = %q", ix.ToolInput)
	}
	c.cancel()
	waitOut(t, c)
}

func TestCaptureRedactsSecrets(t *testing.T) {
	r := newCaptureRig(t)
	useSecretKey(t, r.d)
	setSecret(t, r.d, r.agent.ID, "GH_TOKEN", "ghp_secretvalue")
	r.ad.set(uEntry("u1", "my token is ghp_secretvalue"), aEntry("a1", "ok"))
	r.setNow(3 * time.Minute)
	r.capture()
	if len(r.ex.prompts) != 1 || strings.Contains(r.ex.prompts[0], "ghp_secretvalue") || !strings.Contains(r.ex.prompts[0], "«GH_TOKEN»") {
		t.Fatalf("prompts = %v", r.ex.prompts)
	}
}

func TestSecretReservedNames(t *testing.T) {
	r := newMemRig(t, 0)
	useSecretKey(t, r.d)
	for _, name := range []string{"PATH", "HOME", "LD_PRELOAD", "ARGUS_SOCKET", "LC_ALL"} {
		if _, err := callPAgent[struct{}](t, r.d.handlePAgentSecretSet, api.PAgentSecretSetParams{AgentID: r.a.ID, Name: name, Value: "x"}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	setSecret(t, r.d, r.a.ID, "PATH_TOKEN", "value")
}
