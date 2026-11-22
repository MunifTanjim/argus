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
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func pagentNode(t *testing.T) (*Node, *pagent.Store, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	d := newTestNode(t)
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "argus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	store := pagent.NewStore(sqlDB, time.Now)
	root := t.TempDir()
	cfg := config.AgentsConfig{DefaultDir: filepath.Join(root, "agents")}
	cfg.Defaults.Agent = "claude"
	cfg.Defaults.Git.Mode = "nested"
	d.SetAgentStore(store, cfg)
	d.pagentPrimaryFile = filepath.Join(root, "pagent-primary")
	return d, store, root
}

func callPAgent[T any](t *testing.T, h func(context.Context, json.RawMessage) (any, error), p any) (T, error) {
	t.Helper()
	var zero T
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := h(context.Background(), raw)
	if err != nil {
		return zero, err
	}
	return res.(T), nil
}

func TestPAgentCreateScaffoldsAndLists(t *testing.T) {
	d, _, root := pagentNode(t)
	info, err := callPAgent[api.PAgentInfo](t, d.handlePAgentCreate, api.PAgentCreateParams{Name: "scout", Model: "opus"})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(root, "agents", "scout")
	if info.Dir != wantDir || info.Name != "scout" || info.ID == "" || info.Status != "idle" || info.Agent != "claude" || info.Model != "opus" {
		t.Fatalf("info = %+v", info)
	}
	if _, err := os.Stat(filepath.Join(wantDir, "agent.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wantDir, ".git")); err != nil {
		t.Fatal(err)
	}
	list, err := callPAgent[api.PAgentListResult](t, d.handlePAgentList, api.PAgentListParams{})
	if err != nil || len(list.Agents) != 1 || list.Agents[0].ID != info.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := callPAgent[api.PAgentInfo](t, d.handlePAgentCreate, api.PAgentCreateParams{Name: "scout"}); rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("duplicate create err = %v", err)
	}

	if _, err := callPAgent[struct{}](t, d.handlePAgentSetPrimary, api.PAgentPrimaryParams{AgentID: info.ID}); err != nil {
		t.Fatal(err)
	}
	list, _ = callPAgent[api.PAgentListResult](t, d.handlePAgentList, api.PAgentListParams{})
	if list.Primary != info.ID || !list.Agents[0].Primary {
		t.Fatalf("primary = %+v", list)
	}
	if _, err := callPAgent[struct{}](t, d.handlePAgentSetPrimary, api.PAgentPrimaryParams{}); err != nil {
		t.Fatal(err)
	}
	list, _ = callPAgent[api.PAgentListResult](t, d.handlePAgentList, api.PAgentListParams{})
	if list.Primary != "" {
		t.Fatalf("primary after unset = %q", list.Primary)
	}

	renamed, err := callPAgent[api.PAgentInfo](t, d.handlePAgentRename, api.PAgentRenameParams{AgentID: info.ID, Name: "ranger"})
	if err != nil || renamed.Name != "ranger" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	def, err := pagent.LoadDefinition(wantDir)
	if err != nil || def.Name != "ranger" {
		t.Fatalf("definition = %+v, %v", def, err)
	}
}

func TestPAgentAddCollisionRefused(t *testing.T) {
	d, _, root := pagentNode(t)
	if _, err := callPAgent[api.PAgentInfo](t, d.handlePAgentCreate, api.PAgentCreateParams{Name: "scout"}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "clone")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "agent.yaml"), []byte("name: scout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := callPAgent[api.PAgentAddResult](t, d.handlePAgentAdd, api.PAgentAddParams{Dir: other}); rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("collision err = %v", err)
	}
	res, err := callPAgent[api.PAgentAddResult](t, d.handlePAgentAdd, api.PAgentAddParams{Dir: other, Name: "scout2"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Agent.Name != "scout2" || res.Agent.ID == "" {
		t.Fatalf("add = %+v", res)
	}
	def, err := pagent.LoadDefinition(other)
	if err != nil || def.Name != "scout2" || def.ID != res.Agent.ID {
		t.Fatalf("definition = %+v, %v", def, err)
	}
}

func TestPAgentAddFoundInstructions(t *testing.T) {
	d, _, root := pagentNode(t)
	dir := filepath.Join(root, "hand")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("be terse\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := callPAgent[api.PAgentAddResult](t, d.handlePAgentAdd, api.PAgentAddParams{Dir: dir})
	if err != nil || !res.FoundInstructions || res.Agent.ID != "" {
		t.Fatalf("first = %+v, %v", res, err)
	}
	list, _ := callPAgent[api.PAgentListResult](t, d.handlePAgentList, api.PAgentListParams{})
	if len(list.Agents) != 0 {
		t.Fatal("registered before confirmation")
	}
	res, err = callPAgent[api.PAgentAddResult](t, d.handlePAgentAdd, api.PAgentAddParams{Dir: dir, ImportInstructions: true})
	if err != nil || res.Agent.ID == "" {
		t.Fatalf("second = %+v, %v", res, err)
	}
	def, err := pagent.LoadDefinition(dir)
	if err != nil || !strings.Contains(def.Instructions, "be terse") {
		t.Fatalf("definition = %+v, %v", def, err)
	}
}

func TestPAgentAddRestoresArchived(t *testing.T) {
	d, _, _ := pagentNode(t)
	info, err := callPAgent[api.PAgentInfo](t, d.handlePAgentCreate, api.PAgentCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := callPAgent[struct{}](t, d.handlePAgentForget, api.PAgentForgetParams{AgentID: info.ID}); err != nil {
		t.Fatal(err)
	}
	list, _ := callPAgent[api.PAgentListResult](t, d.handlePAgentList, api.PAgentListParams{})
	if len(list.Agents) != 0 {
		t.Fatalf("archived agent listed: %+v", list)
	}
	list, _ = callPAgent[api.PAgentListResult](t, d.handlePAgentList, api.PAgentListParams{IncludeArchived: true})
	if len(list.Agents) != 1 || !list.Agents[0].Archived {
		t.Fatalf("include archived = %+v", list)
	}
	res, err := callPAgent[api.PAgentAddResult](t, d.handlePAgentAdd, api.PAgentAddParams{Dir: info.Dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Agent.ID != info.ID || res.Agent.Archived {
		t.Fatalf("restored = %+v", res.Agent)
	}
}

func TestPAgentAddAssignsMissingID(t *testing.T) {
	d, _, root := pagentNode(t)
	dir := filepath.Join(root, "noid")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte("name: noid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := callPAgent[api.PAgentAddResult](t, d.handlePAgentAdd, api.PAgentAddParams{Dir: dir})
	if err != nil || res.Agent.ID == "" {
		t.Fatalf("add = %+v, %v", res, err)
	}
	def, _ := pagent.LoadDefinition(dir)
	if def.ID != res.Agent.ID {
		t.Fatalf("saved id = %q", def.ID)
	}
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain", "agent.yaml").Output()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("agent.yaml not committed: %q %v", out, err)
	}
}

func TestPAgentForgetPurgeRefusesLiveSession(t *testing.T) {
	d, store, _ := pagentNode(t)
	ctx := context.Background()
	info, err := callPAgent[api.PAgentInfo](t, d.handlePAgentCreate, api.PAgentCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", AgentSessionID: "as1", Cwd: info.Dir, Status: session.StatusWorking,
	})
	if err := store.InsertThread(ctx, pagent.Thread{ID: "th1", AgentID: info.ID, Kind: "chat", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertThreadSession(ctx, pagent.ThreadSession{ThreadID: "th1", SessionID: s.ID, CLI: "claude", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	got, err := callPAgent[api.PAgentInfo](t, d.handlePAgentShow, api.PAgentRef{AgentID: info.ID})
	if err != nil || got.Status != "working" {
		t.Fatalf("show = %+v, %v", got, err)
	}
	if _, err := callPAgent[struct{}](t, d.handlePAgentForget, api.PAgentForgetParams{AgentID: info.ID, Purge: true}); rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("purge err = %v", err)
	}
	if a, ok, _ := store.AgentByID(ctx, info.ID); !ok || !a.ArchivedAt.IsZero() {
		t.Fatalf("agent changed by refused purge: %+v %v", a, ok)
	}
}

func TestPAgentStatusPriority(t *testing.T) {
	d, store, _ := pagentNode(t)
	ctx := context.Background()
	info, _ := callPAgent[api.PAgentInfo](t, d.handlePAgentCreate, api.PAgentCreateParams{Name: "scout"})
	_ = store.InsertThread(ctx, pagent.Thread{ID: "th1", AgentID: info.ID, Kind: "chat", StartedBy: "you"})
	link := func(agentSID string, u registry.HookUpdate) {
		u.Agent, u.AgentSessionID, u.Cwd = "claude", agentSID, info.Dir
		s, _ := d.reg.ApplyHook(u)
		if err := store.InsertThreadSession(ctx, pagent.ThreadSession{ThreadID: "th1", SessionID: s.ID, CLI: "claude", StartedBy: "you"}); err != nil {
			t.Fatal(err)
		}
	}
	status := func() string {
		got, err := callPAgent[api.PAgentInfo](t, d.handlePAgentShow, api.PAgentRef{AgentID: info.ID})
		if err != nil {
			t.Fatal(err)
		}
		return got.Status
	}
	link("a", registry.HookUpdate{Status: session.StatusWorking})
	if status() != "working" {
		t.Fatalf("status = %s", status())
	}
	link("b", registry.HookUpdate{Status: session.StatusAwaitingInput})
	if status() != "waiting" {
		t.Fatalf("status = %s", status())
	}
	link("c", registry.HookUpdate{Status: session.StatusAwaitingInput, Interaction: &session.Interaction{Kind: session.InteractionPermission}})
	if status() != "needs_you" {
		t.Fatalf("status = %s", status())
	}
}

func TestPAgentDisabledWithoutStore(t *testing.T) {
	d := newTestNode(t)
	_, err := callPAgent[api.PAgentListResult](t, d.handlePAgentList, api.PAgentListParams{})
	if rpcCode(err) != api.CodeInvalidRequest || !strings.Contains(err.Error(), "agents disabled") {
		t.Fatalf("err = %v", err)
	}
}

func TestPAgentCreateFromCopiesButNotMemory(t *testing.T) {
	d, _, _ := pagentNode(t)
	src, err := callPAgent[api.PAgentInfo](t, d.handlePAgentCreate, api.PAgentCreateParams{Name: "scout", Model: "opus"})
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src.Dir, "skills", "x"), 0o755))
	must(os.WriteFile(filepath.Join(src.Dir, "skills", "x", "SKILL.md"), []byte("skill"), 0o644))
	must(os.WriteFile(filepath.Join(src.Dir, "routines", "daily.yaml"), []byte("name: daily\nenabled: true\n"), 0o644))
	must(os.WriteFile(filepath.Join(src.Dir, "memory", "MEMORY.md"), []byte("secret"), 0o644))

	cp, err := callPAgent[api.PAgentInfo](t, d.handlePAgentCreate, api.PAgentCreateParams{Name: "copy", From: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	if cp.ID == src.ID || cp.Model != "opus" {
		t.Fatalf("copy = %+v", cp)
	}
	if b, err := os.ReadFile(filepath.Join(cp.Dir, "skills", "x", "SKILL.md")); err != nil || string(b) != "skill" {
		t.Fatalf("skill: %q %v", b, err)
	}
	b, err := os.ReadFile(filepath.Join(cp.Dir, "routines", "daily.yaml"))
	if err != nil || !strings.Contains(string(b), "enabled: false") || strings.Contains(string(b), "enabled: true") {
		t.Fatalf("routine: %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(cp.Dir, "memory", "MEMORY.md")); len(b) != 0 {
		t.Fatalf("memory copied: %q", b)
	}
	def, _ := pagent.LoadDefinition(cp.Dir)
	if def.Name != "copy" || def.ID != cp.ID {
		t.Fatalf("definition = %+v", def)
	}
}
