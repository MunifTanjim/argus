package node

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/db"
)

// useBotStore gives d a bot store in a temporary directory.
func useBotStore(t *testing.T, d *Node) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "argus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	root := t.TempDir()
	cfg := config.BotsConfig{DefaultDir: filepath.Join(root, "bots")}
	cfg.Defaults.Agent = "claude"
	cfg.Defaults.Git.Mode = "nested"
	d.SetBotStore(bot.NewStore(sqlDB, time.Now), cfg)
	d.botPrimaryFile = filepath.Join(root, "bot-primary")
}

// dispatch runs method on d through its handler registry, as a client would.
func dispatch[T any](t *testing.T, ctx context.Context, d *Node, method string, p any) (T, error) {
	t.Helper()
	var out T
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.server.DispatchFunc()(ctx, method, raw)
	if err != nil {
		return out, err
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// onNode adds the node field to a bot request.
func onNode(t *testing.T, node string, p any) map[string]any {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m[api.BotNodeField] = node
	return m
}

// botPeers is nodes a and b (labeled "far") with bot stores, online to each other.
func botPeers(t *testing.T) (a, b *Node) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	url := startPeerGateway(t)
	a, _ = startPeerNodeWith(t, url, "a", mustKeyPair(t), true, func(d *Node) { useBotStore(t, d) })
	b, _ = startPeerNodeWith(t, url, "b", mustKeyPair(t), true, func(d *Node) {
		d.SetIdentity("b", "far")
		useBotStore(t, d)
	})
	waitPeerOnline(t, a, "b", true)
	waitPeerOnline(t, b, "a", true)
	return a, b
}

func TestBotCallForwardedToNamedNode(t *testing.T) {
	a, b := botPeers(t)
	ctx := t.Context()
	if _, err := dispatch[api.BotInfo](t, ctx, a, api.MethodBotCreate, api.BotCreateParams{Name: "home"}); err != nil {
		t.Fatal(err)
	}
	remote, err := dispatch[api.BotInfo](t, ctx, b, api.MethodBotCreate, api.BotCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}

	// By label, a forwards to b, which handles it as its own.
	got, err := dispatch[api.BotInfo](t, ctx, a, api.MethodBotShow, onNode(t, "far", api.BotRef{BotID: remote.ID}))
	if err != nil || got.ID != remote.ID || got.NodeID != "b" {
		t.Fatalf("show on far = %+v, %v", got, err)
	}
	// A write lands on b.
	got, err = dispatch[api.BotInfo](t, ctx, a, api.MethodBotRename, onNode(t, "b", api.BotRenameParams{BotID: remote.ID, Name: "scout2"}))
	if err != nil || got.Name != "scout2" {
		t.Fatalf("rename on b = %+v, %v", got, err)
	}
	if bb, ok, _ := b.bots.BotByID(ctx, remote.ID); !ok || bb.Name != "scout2" {
		t.Fatalf("b's bot = %+v, %v", bb, ok)
	}
	// Naming this node is handled here; b's bot is not here.
	if _, err := dispatch[api.BotInfo](t, ctx, a, api.MethodBotShow, onNode(t, "a", api.BotRef{BotID: remote.ID})); err == nil {
		t.Fatal("b's bot found on a")
	}
	// Routine and memory calls go the same way.
	routines, err := dispatch[api.BotRoutineListResult](t, ctx, a, api.MethodBotRoutineList, onNode(t, "far", api.BotRef{BotID: remote.ID}))
	if err != nil || routines.Routines == nil {
		t.Fatalf("routine list on far = %+v, %v", routines, err)
	}
	if _, err := dispatch[api.BotMemoryShowResult](t, ctx, a, api.MethodBotMemoryShow, onNode(t, "far", api.BotRef{BotID: remote.ID})); err != nil {
		t.Fatalf("memory show on far: %v", err)
	}
	if _, err := dispatch[api.BotInfo](t, ctx, a, api.MethodBotShow, onNode(t, "nope", api.BotRef{BotID: remote.ID})); err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("unknown node: err = %v", err)
	}
}

func TestBotListAllFansOut(t *testing.T) {
	a, b := botPeers(t)
	ctx := t.Context()
	if _, err := dispatch[api.BotInfo](t, ctx, a, api.MethodBotCreate, api.BotCreateParams{Name: "home"}); err != nil {
		t.Fatal(err)
	}
	remote, err := dispatch[api.BotInfo](t, ctx, b, api.MethodBotCreate, api.BotCreateParams{Name: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.writePrimary(remote.ID); err != nil {
		t.Fatal(err)
	}

	res, err := dispatch[api.BotListResult](t, ctx, a, api.MethodBotList, api.BotListParams{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Bots) != 2 || len(res.Unreachable) != 0 || res.Primary != "" {
		t.Fatalf("list all = %+v", res)
	}
	byName := map[string]api.BotInfo{}
	for _, x := range res.Bots {
		byName[x.Name] = x
	}
	if h := byName["home"]; h.NodeID != "a" || h.NodeLabel != "a" {
		t.Fatalf("home = %+v", h)
	}
	if s := byName["scout"]; s.NodeID != "b" || s.NodeLabel != "far" || !s.Primary {
		t.Fatalf("scout = %+v", s)
	}

	// Without all, and from a peer, only the node's own bots.
	res, err = dispatch[api.BotListResult](t, ctx, a, api.MethodBotList, api.BotListParams{})
	if err != nil || len(res.Bots) != 1 {
		t.Fatalf("list = %+v, %v", res, err)
	}
	res, err = dispatch[api.BotListResult](t, withPeerNode(ctx, "b"), a, api.MethodBotList, api.BotListParams{All: true})
	if err != nil || len(res.Bots) != 1 {
		t.Fatalf("list all from a peer = %+v, %v", res, err)
	}
	// A node may be listed alone.
	res, err = dispatch[api.BotListResult](t, ctx, a, api.MethodBotList, onNode(t, "far", api.BotListParams{}))
	if err != nil || len(res.Bots) != 1 || res.Bots[0].Name != "scout" || res.Primary != remote.ID {
		t.Fatalf("list on far = %+v, %v", res, err)
	}
}

func TestBotListAllMarksUnreachable(t *testing.T) {
	d, _, _ := botNode(t)
	d.SetIdentity("home", "")
	for _, n := range []api.NodeDescriptor{
		{ID: "off", Capabilities: api.NodeCapabilities{CrossNodeOrch: crossNodeOrchVersion}},
		{ID: "old", Online: true},
	} {
		d.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: n})
	}
	if _, err := callBot[api.BotInfo](t, d.handleBotCreate, api.BotCreateParams{Name: "scout"}); err != nil {
		t.Fatal(err)
	}
	res, err := dispatch[api.BotListResult](t, t.Context(), d, api.MethodBotList, api.BotListParams{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Bots) != 1 || strings.Join(res.Unreachable, ",") != "off,old" {
		t.Fatalf("list all = %+v", res)
	}
}

func TestBotCallFromPeerNotForwarded(t *testing.T) {
	d, _, _ := botNode(t)
	d.SetIdentity("home", "")
	d.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{
		ID: "b", Online: true, Capabilities: api.NodeCapabilities{CrossNodeOrch: crossNodeOrchVersion},
	}})
	_, err := dispatch[api.BotInfo](t, withPeerNode(context.Background(), "c"), d, api.MethodBotShow, onNode(t, "b", api.BotRef{BotID: "x"}))
	if err == nil || !strings.Contains(err.Error(), "was sent to node home") {
		t.Fatalf("err = %v", err)
	}
	// Offline, a client's call fails as offline rather than running here.
	d.roster.apply(api.NodeEvent{Type: api.NodeEventAdded, Node: api.NodeDescriptor{ID: "b"}})
	if _, err := dispatch[api.BotInfo](t, context.Background(), d, api.MethodBotShow, onNode(t, "b", api.BotRef{BotID: "x"})); !errors.Is(err, errPeerOffline) {
		t.Fatalf("offline: err = %v", err)
	}
}
