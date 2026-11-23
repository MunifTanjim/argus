package node

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/memory"
	"github.com/MunifTanjim/argus/internal/bot/policy"
	"github.com/MunifTanjim/argus/internal/botgit"
)

func (r *memRig) remember(t *testing.T, text string) error {
	t.Helper()
	_, err := r.d.botApplyMemory(context.Background(), r.a, r.th, "capture", []memory.Op{{Action: "add", File: "MEMORY.md", Text: text}}, "")
	return err
}

func (r *memRig) gitDir(t *testing.T) string {
	t.Helper()
	repo, err := botgit.Open(context.Background(), r.a.Dir, botgit.ModeNested)
	if err != nil {
		t.Fatal(err)
	}
	return repo.GitDir
}

func (r *memRig) trust(t *testing.T) []string {
	t.Helper()
	res, err := callBot[api.BotTrustResult](t, r.d.handleBotTrust, api.BotRef{BotID: r.a.ID})
	if err != nil {
		t.Fatal(err)
	}
	return res.Accepted
}

func (r *memRig) wantRefused(t *testing.T, what string) {
	t.Helper()
	before := len(r.log(t, 0))
	err := r.remember(t, "refused fact")
	if err == nil || !strings.Contains(err.Error(), what) {
		t.Fatalf("memory write err = %v, want a refusal naming %s", err, what)
	}
	if got := len(r.log(t, 0)); got != before {
		t.Fatalf("commits %d -> %d after a refusal", before, got)
	}
	if strings.Contains(r.read(t, "memory/MEMORY.md"), "refused fact") {
		t.Fatal("refused write left MEMORY.md changed")
	}
	notes := strings.Join(r.d.healthNotes(r.a), "\n")
	if !strings.Contains(notes, what) || !strings.Contains(notes, "argus bot trust scout") {
		t.Fatalf("health notes = %q, want one naming %s", notes, what)
	}
}

func TestTrustRefusesPythonHook(t *testing.T) {
	r := newMemRig(t, 0)
	if err := r.remember(t, "fact one"); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(t.TempDir(), "hook-ran")
	hook := filepath.Join(r.gitDir(t), "hooks", "post-commit")
	script := "#!/usr/bin/env python3\nimport pathlib\npathlib.Path(" + `"` + mark + `"` + ").touch()\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r.wantRefused(t, ".git/hooks/post-commit (added)")
	// Refusals keep happening, and git never ran the hook.
	r.wantRefused(t, ".git/hooks/post-commit (added)")
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("hook ran during a refused commit")
	}

	if got := r.trust(t); !slices.Equal(got, []string{".git/hooks/post-commit (added)"}) {
		t.Fatalf("accepted = %v", got)
	}
	if notes := r.d.healthNotes(r.a); len(notes) != 0 {
		t.Fatalf("health notes after trust = %v", notes)
	}
	if err := r.remember(t, "fact two"); err != nil {
		t.Fatalf("write after trust: %v", err)
	}
	if _, err := exec.LookPath("python3"); err == nil {
		if _, err := os.Stat(mark); err != nil {
			t.Fatal("trusted hook did not run")
		}
	}
}

func TestTrustRefusesConfigChange(t *testing.T) {
	r := newMemRig(t, 0)
	if err := r.remember(t, "fact one"); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", r.a.Dir, "config", "core.hooksPath", t.TempDir()).CombinedOutput(); err != nil {
		t.Fatalf("git config: %v %s", err, out)
	}
	r.wantRefused(t, ".git/config (changed)")
}

func TestTrustRefusesHandEditedApprovals(t *testing.T) {
	r := newMemRig(t, 0)
	if err := r.remember(t, "fact one"); err != nil {
		t.Fatal(err)
	}
	def, err := bot.LoadDefinition(r.a.Dir)
	if err != nil {
		t.Fatal(err)
	}
	// Edits to other keys are not protected.
	def.Description = "edited by hand"
	if err := def.Save(r.a.Dir); err != nil {
		t.Fatal(err)
	}
	if err := r.remember(t, "fact two"); err != nil {
		t.Fatalf("write after a description edit: %v", err)
	}
	def.Approvals = append(def.Approvals, policy.Rule{Tool: "Bash", Target: "*", Decision: policy.OutcomeAllow})
	if err := def.Save(r.a.Dir); err != nil {
		t.Fatal(err)
	}
	r.wantRefused(t, "bot.yaml approvals (changed)")
	if got := r.trust(t); !slices.Equal(got, []string{"bot.yaml approvals (changed)"}) {
		t.Fatalf("accepted = %v", got)
	}
	if err := r.remember(t, "fact three"); err != nil {
		t.Fatalf("write after trust: %v", err)
	}
}

func TestTrustRefusesGlobalApprovalsChange(t *testing.T) {
	r := newMemRig(t, 0)
	if err := r.remember(t, "fact one"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.d.botCfg.ProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.d.botCfg.ProfileDir, globalApprovalsFile), []byte("approvals:\n  - {tool: '*', decision: allow}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.wantRefused(t, "approvals.yaml (added)")
	// The profile repository is checked against the same shared record.
	_, err := r.d.commitMemory(context.Background(), r.a, r.d.botSettings(r.a), r.th, "capture", []memory.Op{{Action: "add", File: "PROFILE.md", Text: "lives in Lisbon"}}, "")
	if err == nil || !strings.Contains(err.Error(), "approvals.yaml") {
		t.Fatalf("profile write err = %v", err)
	}
	r.trust(t)
	if err := r.remember(t, "fact two"); err != nil {
		t.Fatalf("write after trust: %v", err)
	}
	if _, err := r.d.commitMemory(context.Background(), r.a, r.d.botSettings(r.a), r.th, "capture", []memory.Op{{Action: "add", File: "PROFILE.md", Text: "lives in Lisbon"}}, ""); err != nil {
		t.Fatalf("profile write after trust: %v", err)
	}
}

func TestTrustAcceptsArgusApprovalRule(t *testing.T) {
	r := newMemRig(t, 0)
	if err := r.remember(t, "fact one"); err != nil {
		t.Fatal(err)
	}
	if err := r.d.saveAllowRule(context.Background(), r.a, r.th, policy.Rule{Tool: "Bash", Target: "ls", Decision: policy.OutcomeAllow}); err != nil {
		t.Fatalf("save rule: %v", err)
	}
	if err := r.remember(t, "fact two"); err != nil {
		t.Fatalf("write after Argus saved a rule: %v", err)
	}
	if notes := r.d.healthNotes(r.a); len(notes) != 0 {
		t.Fatalf("health notes = %v", notes)
	}
}

func TestTrustRecordedAtCreate(t *testing.T) {
	r := newMemRig(t, 0)
	// The first commit happens after the hook appeared, but the bot's files
	// were recorded when it was created.
	if err := os.WriteFile(filepath.Join(r.gitDir(t), "hooks", "pre-commit"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.wantRefused(t, ".git/hooks/pre-commit (added)")
}

func TestTrustRefusesUndo(t *testing.T) {
	r := newMemRig(t, 0)
	if err := r.remember(t, "fact one"); err != nil {
		t.Fatal(err)
	}
	head := r.log(t, 1)[0].Hash
	if err := os.WriteFile(filepath.Join(r.gitDir(t), "hooks", "post-commit"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := callBot[api.BotMemoryUndoResult](t, r.d.handleBotMemoryUndo, api.BotMemoryUndoParams{BotID: r.a.ID, Commit: head})
	if err == nil || !strings.Contains(err.Error(), "post-commit") {
		t.Fatalf("undo err = %v", err)
	}
	if got := r.log(t, 1)[0].Hash; got != head {
		t.Fatalf("HEAD moved to %s", got)
	}
}

func TestHandEditErrRetryable(t *testing.T) {
	var rpc *api.RPCError
	if err := handEditErr(errUntrusted); !errors.As(err, &rpc) || rpc.Code != api.CodeInternalError {
		t.Fatalf("untrusted: %v", err)
	}
	if err := handEditErr(errors.New("x")); !errors.As(err, &rpc) || rpc.Code == api.CodeInternalError {
		t.Fatalf("other: %v", err)
	}
}
