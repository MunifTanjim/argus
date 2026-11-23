package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func botNode(t *testing.T) (*Node, *bot.Store, string) {
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
	store := bot.NewStore(sqlDB, time.Now)
	root := t.TempDir()
	cfg := config.BotsConfig{DefaultDir: filepath.Join(root, "bots")}
	cfg.Defaults.Agent = "claude"
	cfg.Defaults.Git.Mode = "nested"
	d.SetBotStore(store, cfg)
	d.botPrimaryFile = filepath.Join(root, "bot-primary")
	return d, store, root
}

func callBot[T any](t *testing.T, h func(context.Context, json.RawMessage) (any, error), p any) (T, error) {
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

func TestBotCreateScaffoldsAndLists(t *testing.T) {
	d, _, root := botNode(t)
	info, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout", Model: "opus"})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(root, "bots", "scout")
	if info.Dir != wantDir || info.Name != "scout" || info.ID == "" || info.Status != "idle" || info.Agent != "claude" || info.Model != "opus" {
		t.Fatalf("info = %+v", info)
	}
	if _, err := os.Stat(filepath.Join(wantDir, "bot.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wantDir, ".git")); err != nil {
		t.Fatal(err)
	}
	list, err := callBot[api.BotListResult](t, d.handleBotList, api.BotListParams{})
	if err != nil || len(list.Bots) != 1 || list.Bots[0].ID != info.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout"}); rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("duplicate create err = %v", err)
	}

	if _, err := callBot[struct{}](t, d.handleBotSetPrimary, api.BotPrimaryParams{BotID: info.ID}); err != nil {
		t.Fatal(err)
	}
	list, _ = callBot[api.BotListResult](t, d.handleBotList, api.BotListParams{})
	if list.Primary != info.ID || !list.Bots[0].Primary || time.Since(list.PrimarySince) > time.Minute {
		t.Fatalf("primary = %+v", list)
	}
	if _, err := callBot[struct{}](t, d.handleBotSetPrimary, api.BotPrimaryParams{}); err != nil {
		t.Fatal(err)
	}
	list, _ = callBot[api.BotListResult](t, d.handleBotList, api.BotListParams{})
	if list.Primary != "" || !list.PrimarySince.IsZero() {
		t.Fatalf("primary after unset = %q", list.Primary)
	}

	renamed, err := callBot[api.BotInfo](t, d.handleBotRename, api.BotRenameParams{BotID: info.ID, Name: "ranger"})
	if err != nil || renamed.Name != "ranger" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	def, err := bot.LoadDefinition(wantDir)
	if err != nil || def.Name != "ranger" {
		t.Fatalf("definition = %+v, %v", def, err)
	}
}

func TestBotAddCollisionRefused(t *testing.T) {
	d, _, root := botNode(t)
	if _, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout"}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "clone")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "bot.yaml"), []byte("name: scout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := callBot[api.BotAddResult](t, d.handleBotAdd, api.BotAddParams{Dir: other}); rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("collision err = %v", err)
	}
	res, err := callBot[api.BotAddResult](t, d.handleBotAdd, api.BotAddParams{Dir: other, Name: "scout2"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Bot.Name != "scout2" || res.Bot.ID == "" {
		t.Fatalf("add = %+v", res)
	}
	def, err := bot.LoadDefinition(other)
	if err != nil || def.Name != "scout2" || def.ID != res.Bot.ID {
		t.Fatalf("definition = %+v, %v", def, err)
	}
}

func TestBotAddFoundInstructions(t *testing.T) {
	d, _, root := botNode(t)
	dir := filepath.Join(root, "hand")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("be terse\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := callBot[api.BotAddResult](t, d.handleBotAdd, api.BotAddParams{Dir: dir})
	if err != nil || !res.FoundInstructions || res.Bot.ID != "" {
		t.Fatalf("first = %+v, %v", res, err)
	}
	list, _ := callBot[api.BotListResult](t, d.handleBotList, api.BotListParams{})
	if len(list.Bots) != 0 {
		t.Fatal("registered before confirmation")
	}
	res, err = callBot[api.BotAddResult](t, d.handleBotAdd, api.BotAddParams{Dir: dir, ImportInstructions: true})
	if err != nil || res.Bot.ID == "" {
		t.Fatalf("second = %+v, %v", res, err)
	}
	def, err := bot.LoadDefinition(dir)
	if err != nil || !strings.Contains(def.Instructions, "be terse") {
		t.Fatalf("definition = %+v, %v", def, err)
	}
}

func TestBotAddRestoresArchived(t *testing.T) {
	d, _, _ := botNode(t)
	info, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := callBot[struct{}](t, d.handleBotForget, api.BotForgetParams{BotID: info.ID}); err != nil {
		t.Fatal(err)
	}
	list, _ := callBot[api.BotListResult](t, d.handleBotList, api.BotListParams{})
	if len(list.Bots) != 0 {
		t.Fatalf("archived bot listed: %+v", list)
	}
	list, _ = callBot[api.BotListResult](t, d.handleBotList, api.BotListParams{IncludeArchived: true})
	if len(list.Bots) != 1 || !list.Bots[0].Archived {
		t.Fatalf("include archived = %+v", list)
	}
	res, err := callBot[api.BotAddResult](t, d.handleBotAdd, api.BotAddParams{Dir: info.Dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Bot.ID != info.ID || res.Bot.Archived {
		t.Fatalf("restored = %+v", res.Bot)
	}
}

func TestBotAddAssignsMissingID(t *testing.T) {
	d, _, root := botNode(t)
	dir := filepath.Join(root, "noid")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bot.yaml"), []byte("name: noid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := callBot[api.BotAddResult](t, d.handleBotAdd, api.BotAddParams{Dir: dir})
	if err != nil || res.Bot.ID == "" {
		t.Fatalf("add = %+v, %v", res, err)
	}
	def, _ := bot.LoadDefinition(dir)
	if def.ID != res.Bot.ID {
		t.Fatalf("saved id = %q", def.ID)
	}
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain", "bot.yaml").Output()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("bot.yaml not committed: %q %v", out, err)
	}
}

func TestBotForgetPurgeRefusesLiveSession(t *testing.T) {
	d, store, _ := botNode(t)
	ctx := context.Background()
	info, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", AgentSessionID: "as1", Cwd: info.Dir, Status: session.StatusWorking,
	})
	if err := store.InsertThread(ctx, bot.Thread{ID: "th1", BotID: info.ID, Kind: "chat", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertThreadSession(ctx, bot.ThreadSession{ThreadID: "th1", SessionID: s.ID, CLI: "claude", StartedBy: "you"}); err != nil {
		t.Fatal(err)
	}
	got, err := callBot[api.BotInfo](t, d.handleBotShow, api.BotRef{BotID: info.ID})
	if err != nil || got.Status != "working" {
		t.Fatalf("show = %+v, %v", got, err)
	}
	if _, err := callBot[struct{}](t, d.handleBotForget, api.BotForgetParams{BotID: info.ID, Purge: true}); rpcCode(err) != api.CodeInvalidRequest {
		t.Fatalf("purge err = %v", err)
	}
	if a, ok, _ := store.BotByID(ctx, info.ID); !ok || !a.ArchivedAt.IsZero() {
		t.Fatalf("bot changed by refused purge: %+v %v", a, ok)
	}
}

func TestBotStatusPriority(t *testing.T) {
	d, store, _ := botNode(t)
	ctx := context.Background()
	info, _ := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout"})
	_ = store.InsertThread(ctx, bot.Thread{ID: "th1", BotID: info.ID, Kind: "chat", StartedBy: "you"})
	link := func(botSID string, u registry.HookUpdate) {
		u.Agent, u.AgentSessionID, u.Cwd = "claude", botSID, info.Dir
		s, _ := d.reg.ApplyHook(u)
		if err := store.InsertThreadSession(ctx, bot.ThreadSession{ThreadID: "th1", SessionID: s.ID, CLI: "claude", StartedBy: "you"}); err != nil {
			t.Fatal(err)
		}
	}
	status := func() string {
		got, err := callBot[api.BotInfo](t, d.handleBotShow, api.BotRef{BotID: info.ID})
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

func TestBotDisabledWithoutStore(t *testing.T) {
	d := newTestNode(t)
	_, err := callBot[api.BotListResult](t, d.handleBotList, api.BotListParams{})
	if rpcCode(err) != api.CodeInvalidRequest || !strings.Contains(err.Error(), "bots disabled") {
		t.Fatalf("err = %v", err)
	}
}

func TestBotCreateFromCopiesButNotMemory(t *testing.T) {
	d, _, _ := botNode(t)
	src, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout", Model: "opus"})
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

	cp, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "copy", From: "scout"})
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
	def, _ := bot.LoadDefinition(cp.Dir)
	if def.Name != "copy" || def.ID != cp.ID {
		t.Fatalf("definition = %+v", def)
	}
}

func TestBotInvalidNamesRefused(t *testing.T) {
	d, _, root := botNode(t)
	info, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../x", "a/b", ".hidden", "a..b", "has space", strings.Repeat("a", 65)} {
		_, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: name})
		if rpcCode(err) != api.CodeInvalidRequest || !strings.Contains(err.Error(), "invalid bot name") {
			t.Errorf("create %q err = %v", name, err)
		}
		if _, err := callBot[api.BotInfo](t, d.handleBotRename, api.BotRenameParams{BotID: info.ID, Name: name}); rpcCode(err) != api.CodeInvalidRequest {
			t.Errorf("rename %q err = %v", name, err)
		}
		dir := filepath.Join(root, "adddir")
		_ = os.MkdirAll(dir, 0o755)
		if _, err := callBot[api.BotAddResult](t, d.handleBotAdd, api.BotAddParams{Dir: dir, Name: name}); rpcCode(err) != api.CodeInvalidRequest {
			t.Errorf("add %q err = %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "x")); err == nil {
		t.Fatal("escaped default dir")
	}
}

func TestBotForgetClearsPrimary(t *testing.T) {
	d, _, _ := botNode(t)
	info, _ := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout"})
	if _, err := callBot[struct{}](t, d.handleBotSetPrimary, api.BotPrimaryParams{BotID: info.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := callBot[struct{}](t, d.handleBotForget, api.BotForgetParams{BotID: info.ID}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(d.botPrimaryFile); err != nil || len(b) != 0 {
		t.Fatalf("primary file = %q, %v", b, err)
	}
	// Restoring the bot must not make it primary again.
	res, err := callBot[api.BotAddResult](t, d.handleBotAdd, api.BotAddParams{Dir: info.Dir})
	if err != nil || res.Bot.Primary {
		t.Fatalf("restored = %+v, %v", res, err)
	}
}

func TestBotCreateAddNodeTarget(t *testing.T) {
	d, _, root := botNode(t)
	d.SetIdentity("home", "")
	d.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{
		ID: "b", Capabilities: api.NodeCapabilities{CrossNodeOrch: crossNodeOrchVersion},
	}})

	// Naming this node handles the call locally.
	info, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "local", NodeID: "home"})
	if err != nil || info.Name != "local" {
		t.Fatalf("create on self = %+v, %v", info, err)
	}
	// A roster node that is offline is forwarded to, and fails there.
	_, err = callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "far", NodeID: "b"})
	if !errors.Is(err, errPeerOffline) {
		t.Fatalf("create on b: err = %v, want offline", err)
	}
	_, err = callBot[api.BotAddResult](t, d.handleBotAdd, api.BotAddParams{Dir: filepath.Join(root, "x"), NodeID: "b"})
	if !errors.Is(err, errPeerOffline) {
		t.Fatalf("add on b: err = %v, want offline", err)
	}
	if _, err = callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "z", NodeID: "nope"}); err == nil {
		t.Fatal("unknown node accepted")
	}
}
