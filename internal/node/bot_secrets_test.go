package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/policy"
	"github.com/MunifTanjim/argus/internal/procwalk"
	"github.com/MunifTanjim/argus/internal/session"
)

func useSecretKey(t *testing.T, d *Node) {
	t.Helper()
	d.secrets.path = filepath.Join(t.TempDir(), "bot-secrets.key")
}

func setSecret(t *testing.T, d *Node, botID, name, value string) {
	t.Helper()
	if _, err := callBot[struct{}](t, d.handleBotSecretSet, api.BotSecretSetParams{BotID: botID, Name: name, Description: "for " + name, Value: value}); err != nil {
		t.Fatal(err)
	}
}

func TestSecretRPCs(t *testing.T) {
	r := newMemRig(t, 0)
	useSecretKey(t, r.d)
	setSecret(t, r.d, r.a.ID, "GH_TOKEN", "ghp_secretvalue")
	if _, err := callBot[struct{}](t, r.d.handleBotSecretSet, api.BotSecretSetParams{BotID: r.a.ID, Name: "bad-name", Value: "x"}); err == nil {
		t.Error("bad name accepted")
	}
	res, err := callBot[api.BotSecretListResult](t, r.d.handleBotSecretList, api.BotRef{BotID: r.a.ID})
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
	if _, err := callBot[struct{}](t, r.d.handleBotSecretRm, api.BotSecretRef{BotID: r.a.ID, Name: "GH_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	if _, err := callBot[struct{}](t, r.d.handleBotSecretRm, api.BotSecretRef{BotID: r.a.ID, Name: "GH_TOKEN"}); err == nil {
		t.Error("second rm succeeded")
	}
}

func TestSecretsListedInInstructionsNotInjected(t *testing.T) {
	r := newThreadRig(t)
	useSecretKey(t, r.d)
	setSecret(t, r.d, r.bot.ID, "GH_TOKEN", "ghp_secretvalue")
	if _, err := r.send(t, api.BotSendParams{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(r.bot.Dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- GH_TOKEN: for GH_TOKEN") || !strings.Contains(string(data), "argus secret get") || strings.Contains(string(data), "ghp_secretvalue") {
		t.Errorf("instructions:\n%s", data)
	}
	for _, sp := range r.spawns {
		if strings.Contains(fmt.Sprint(sp), "ghp_secretvalue") {
			t.Errorf("secret in spawn: %+v", sp)
		}
	}
}

// secretCaller makes the claude session agentID a session of the rig's bot
// thread, when linked, and returns the caller info of a command it runs.
func secretCaller(t *testing.T, r *memRig, pane, agentID string, linked bool) api.CallerInfo {
	t.Helper()
	hookSession(r.d, pane, agentID)
	s, _ := r.d.reg.FindByAgentSession("claude", agentID)
	if linked {
		if err := r.s.InsertThreadSession(context.Background(), bot.ThreadSession{ThreadID: r.th, SessionID: s.ID, CLI: "claude", AgentSessionID: agentID, StartedBy: "you"}); err != nil {
			t.Fatal(err)
		}
	}
	r.d.adapterList = adapters.All()
	r.d.procTable = func(context.Context) (procwalk.Table, error) {
		return procwalk.Table{
			1:   {PID: 1, PPID: 0, Args: "/sbin/init"},
			100: {PID: 100, PPID: 1, Args: "/usr/local/bin/claude"},
			120: {PID: 120, PPID: 100, Args: "argus secret get"},
		}, nil
	}
	return api.CallerInfo{PID: 120, Env: map[string]string{"CLAUDE_PID": "100", "CLAUDE_CODE_SESSION_ID": agentID}}
}

func TestSecretGet(t *testing.T) {
	r := newMemRig(t, 0)
	useSecretKey(t, r.d)
	setSecret(t, r.d, r.a.ID, "GH_TOKEN", "ghp_secretvalue")
	caller := secretCaller(t, r, "%1", "c1", true)
	get := func(c api.CallerInfo, name string) (string, error) {
		res, err := r.d.handleBotSecretGet(context.Background(), mustJSON(api.BotSecretGetParams{Caller: c, Name: name}))
		if err != nil {
			return "", err
		}
		return res.(api.BotSecretGetResult).Value, nil
	}
	if v, err := get(caller, "GH_TOKEN"); err != nil || v != "ghp_secretvalue" {
		t.Fatalf("get = %q %v", v, err)
	}
	if _, err := get(caller, "NOPE"); err == nil || err.Error() != "no secret NOPE for this bot" {
		t.Errorf("missing: %v", err)
	}
	rows, err := r.s.ListActivity(context.Background(), r.a.ID, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, row := range rows {
		if strings.Contains(row.Target+row.Reason+row.Rule, "ghp_secretvalue") {
			t.Errorf("value in activity: %+v", row)
		}
		if row.Tool == secretGetTool {
			got[row.Target] = row.Decision
		}
	}
	if got["GH_TOKEN"] != policy.OutcomeAllow || got["NOPE"] != policy.OutcomeDeny {
		t.Errorf("activity = %v", got)
	}

	other := secretCaller(t, r, "%2", "c2", false)
	if _, err := get(other, "GH_TOKEN"); err == nil || err.Error() != "not a bot session" {
		t.Errorf("non-bot session: %v", err)
	}
	if _, err := get(api.CallerInfo{PID: 1}, "GH_TOKEN"); err == nil {
		t.Error("caller outside a session got a secret")
	}
}

func TestSecretGetIsLocalOnly(t *testing.T) {
	r := newMemRig(t, 0)
	for name, dispatch := range map[string]api.DispatchFunc{"remote": r.d.remoteDispatch(), "peer": r.d.peerDispatch()} {
		_, err := dispatch(context.Background(), api.MethodBotSecretGet, nil)
		if rpcErr, ok := err.(*api.RPCError); !ok || rpcErr.Code != api.CodeMethodNotFound {
			t.Errorf("%s: err = %v, want method not found", name, err)
		}
	}
}

func TestRequestSecretFlow(t *testing.T) {
	r := newMemRig(t, 0)
	useSecretKey(t, r.d)
	ctx := context.Background()
	hookSession(r.d, "%1", "c1")
	s, _ := r.d.reg.FindByAgentSession("claude", "c1")
	if err := r.s.InsertThreadSession(ctx, bot.ThreadSession{ThreadID: r.th, SessionID: s.ID, CLI: "claude", AgentSessionID: "c1", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	caller := callerRef{Local: &session.Session{ID: s.ID, BotID: r.a.ID, BotThreadID: r.th}}
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
	if len(sent) != 1 || strings.Contains(sent[0], "wx-12345") || !strings.Contains(sent[0], "argus secret get API_KEY") {
		t.Fatalf("sent = %q", sent)
	}
	if got, _ = r.d.reg.Get(s.ID); got.Interaction != nil {
		t.Errorf("interaction left: %+v", got.Interaction)
	}
	vals := r.d.botSecrets(ctx, r.a.ID)
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
	if _, err := callBot[struct{}](t, r.d.handleBotDraftSend, api.BotDraftParams{ThreadID: r.th, EventID: id}); err != nil {
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
	setSecret(t, r.d, r.bot.ID, "GH_TOKEN", "ghp_secretvalue")
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
		if _, err := callBot[struct{}](t, r.d.handleBotSecretSet, api.BotSecretSetParams{BotID: r.a.ID, Name: name, Value: "x"}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	setSecret(t, r.d, r.a.ID, "PATH_TOKEN", "value")
}
