package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

type upgradeClient struct {
	recordingClient
	check      api.CheckUpgradeResult
	checkErr   error
	res        api.UpgradeResult
	restartErr error
}

func (c *upgradeClient) Call(method string, params, out any) error {
	_ = c.recordingClient.Call(method, params, out)
	switch method {
	case api.MethodServerCheckUpgrade:
		if c.checkErr != nil {
			return c.checkErr
		}
		*out.(*api.CheckUpgradeResult) = c.check
	case api.MethodServerUpgrade:
		*out.(*api.UpgradeResult) = c.res
	case api.MethodServerRestart:
		return c.restartErr
	}
	return nil
}

func upgradeModel(c *upgradeClient) model {
	m := projectsTestModel()
	m.nodeInfo = []api.NodeInfo{{ID: "n1", Label: "home"}}
	m.client = c
	return selectRow(m, "n1")
}

func runUpgradeCmd(t *testing.T, m model, name string) model {
	t.Helper()
	m, cmd := upd(m, cmdMsg(name))
	for _, msg := range execCmd(cmd) {
		m, _ = upd(m, msg)
	}
	return m
}

func TestUpgradeNodeConfirmsThenUpgradesAndRestarts(t *testing.T) {
	c := &upgradeClient{check: api.CheckUpgradeResult{Current: "1.0.0", Latest: "1.1.0"}, res: api.UpgradeResult{From: "1.0.0", To: "1.1.0"}}
	m := runUpgradeCmd(t, upgradeModel(c), "upgrade node")
	if p := c.paramsOf(api.MethodServerCheckUpgrade); len(p) != 1 || p[0].(api.ServerTargetParams).NodeID != "n1" {
		t.Fatalf("checkUpgrade params %+v", p)
	}
	if _, ok := m.popups.front().(confirmPopup); !ok {
		t.Fatalf("popup %T, want confirmPopup", m.popups.front())
	}
	m, cmd := upd(m, enterKey)
	for _, msg := range execCmd(cmd) {
		m, _ = upd(m, msg)
	}
	for _, method := range []string{api.MethodServerUpgrade, api.MethodServerRestart} {
		if p := c.paramsOf(method); len(p) != 1 || p[0].(api.ServerTargetParams).NodeID != "n1" {
			t.Fatalf("%s params %+v", method, p)
		}
	}
	if !strings.Contains(m.flash, "upgraded to 1.1.0") {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestUpgradeCancelDoesNothing(t *testing.T) {
	c := &upgradeClient{check: api.CheckUpgradeResult{Current: "1.0.0", Latest: "1.1.0"}}
	m := runUpgradeCmd(t, upgradeModel(c), "upgrade node")
	m, _ = upd(m, keyMsg("esc"))
	if m.popups.front() != nil || len(c.paramsOf(api.MethodServerUpgrade)) != 0 {
		t.Fatal("esc must close the popup without an upgrade")
	}
}

func TestUpgradeAlreadyLatest(t *testing.T) {
	c := &upgradeClient{check: api.CheckUpgradeResult{Current: "1.1.0", Latest: "1.1.0"}}
	m := runUpgradeCmd(t, upgradeModel(c), "upgrade node")
	if m.popups.front() != nil || !strings.Contains(m.flash, "already on the latest version (1.1.0)") {
		t.Fatalf("popup %T, flash %q", m.popups.front(), m.flash)
	}
}

func TestUpgradeUnsupportedTarget(t *testing.T) {
	c := &upgradeClient{checkErr: &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found"}}
	m := runUpgradeCmd(t, upgradeModel(c), "upgrade node")
	if !strings.Contains(m.flash, "home does not support remote upgrade") {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestUpgradeGatewayHasNoNodeID(t *testing.T) {
	c := &upgradeClient{check: api.CheckUpgradeResult{Current: "1.0.0", Latest: "1.1.0"}}
	m := upgradeModel(c)
	m.gatewayURL = "wss://gw.example.com"
	runUpgradeCmd(t, m, "upgrade gateway")
	if p := c.paramsOf(api.MethodServerCheckUpgrade); len(p) != 1 || p[0].(api.ServerTargetParams).NodeID != "" {
		t.Fatalf("checkUpgrade params %+v", p)
	}
}

func TestUpgradeGatewayNeedsGatewayConnection(t *testing.T) {
	c := &upgradeClient{check: api.CheckUpgradeResult{Current: "1.0.0", Latest: "1.1.0"}}
	m := runUpgradeCmd(t, upgradeModel(c), "upgrade gateway")
	if len(c.paramsOf(api.MethodServerCheckUpgrade)) != 0 || m.flash != "not connected to a gateway" {
		t.Fatalf("flash %q, checkUpgrade calls %d", m.flash, len(c.paramsOf(api.MethodServerCheckUpgrade)))
	}
}

func TestIsQuickTunnelURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://abc-def.trycloudflare.com": true,
		"wss://abc.trycloudflare.com/ws":    true,
		"https://argus.example.com":         false,
		"ws://127.0.0.1:7777":               false,
		"":                                  false,
	} {
		if got := isQuickTunnelURL(raw); got != want {
			t.Errorf("isQuickTunnelURL(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestUpgradeCommandNames(t *testing.T) {
	if got := resolveCommand("upgrade"); got != "upgrade node" {
		t.Fatalf("resolveCommand(upgrade) = %q", got)
	}
	if msg := commandError("upgrade foo"); !strings.Contains(msg, "unknown argument") {
		t.Fatalf("commandError(upgrade foo) = %q", msg)
	}
}

func localSocketUpgradeModel(c *upgradeClient) model {
	m := projectsTestModel()
	m.left.tree.data = []api.ProjectNode{{
		ID: "p1", Name: "argus", Kind: "git", Dir: "/repo/.git",
		Workspaces: []api.WorkspaceNode{{ID: "w1", Dir: "/repo", IsMain: true, Branch: "main"}},
	}}
	m.left.tree.rebuild()
	m.client = c
	return m
}

func TestUpgradeNodeLocalSocketTargetsThisMachine(t *testing.T) {
	for _, row := range []string{"", "p1", "w1"} {
		c := &upgradeClient{check: api.CheckUpgradeResult{Current: "1.0.0", Latest: "1.1.0"}}
		m := selectRow(localSocketUpgradeModel(c), row)
		got, ok := m.upgradeNodeTarget()
		if !ok || got != (upgradeTarget{label: "this machine"}) {
			t.Fatalf("row %q: target %+v, %v", row, got, ok)
		}
		m = runUpgradeCmd(t, m, "upgrade node")
		if p := c.paramsOf(api.MethodServerCheckUpgrade); len(p) != 1 || p[0].(api.ServerTargetParams).NodeID != "" {
			t.Fatalf("row %q: checkUpgrade params %+v", row, p)
		}
		if _, ok := m.popups.front().(confirmPopup); !ok {
			t.Fatalf("row %q: popup %T, want confirmPopup", row, m.popups.front())
		}
	}
}

func TestUpgradeNodeProjectRowTargetsParentNode(t *testing.T) {
	c := &upgradeClient{check: api.CheckUpgradeResult{Current: "1.0.0", Latest: "1.1.0"}}
	m := selectRow(upgradeModel(c), "n1:p1")
	if got, ok := m.upgradeNodeTarget(); !ok || got != (upgradeTarget{nodeID: "n1", label: "home"}) {
		t.Fatalf("target %+v, %v", got, ok)
	}
}

func TestUpgradeNodeOnHomeAsksForANode(t *testing.T) {
	m := homeTestModel()
	c := &upgradeClient{}
	m.client = c
	mm, _ := m.runCmdLine("upgrade")
	m = mm.(model)
	if m.flash != "select a node to upgrade" {
		t.Fatalf("flash %q, want select a node to upgrade", m.flash)
	}
	if p := c.paramsOf(api.MethodServerCheckUpgrade); len(p) != 0 {
		t.Fatalf("checkUpgrade called with %+v", p)
	}
}

func TestUpgradeUnknownTargetIsUnknownCommand(t *testing.T) {
	c := &upgradeClient{}
	mm, _ := upgradeModel(c).runCmdLine("upgrade foo")
	m := mm.(model)
	if m.flash != "unknown command: upgrade foo" {
		t.Fatalf("flash %q, want unknown command: upgrade foo", m.flash)
	}
	if p := c.paramsOf(api.MethodServerCheckUpgrade); len(p) != 0 {
		t.Fatalf("checkUpgrade called with %+v", p)
	}
}

func TestUpgradeRestartFailureSaysBinaryReplaced(t *testing.T) {
	c := &upgradeClient{
		check:      api.CheckUpgradeResult{Current: "1.0.0", Latest: "1.1.0"},
		res:        api.UpgradeResult{From: "1.0.0", To: "1.1.0"},
		restartErr: &api.RPCError{Code: api.CodeInternalError, Message: "boom"},
	}
	m := runUpgradeCmd(t, upgradeModel(c), "upgrade node")
	m, cmd := upd(m, enterKey)
	for _, msg := range execCmd(cmd) {
		m, _ = upd(m, msg)
	}
	if want := "home upgraded to 1.1.0 · restart failed: boom"; m.flash != want {
		t.Fatalf("flash %q, want %q", m.flash, want)
	}
}

func TestUpgradeRestartLostReplyIsRestarting(t *testing.T) {
	c := &upgradeClient{
		check:      api.CheckUpgradeResult{Current: "1.0.0", Latest: "1.1.0"},
		res:        api.UpgradeResult{From: "1.0.0", To: "1.1.0"},
		restartErr: errors.New("client: connection closed"),
	}
	m := runUpgradeCmd(t, upgradeModel(c), "upgrade node")
	m, cmd := upd(m, enterKey)
	for _, msg := range execCmd(cmd) {
		m, _ = upd(m, msg)
	}
	if want := "home upgraded to 1.1.0 · restarting"; m.flash != want {
		t.Fatalf("flash %q, want %q", m.flash, want)
	}
}

func TestUpgradeGatewayQuickTunnelPopupContent(t *testing.T) {
	c := &upgradeClient{check: api.CheckUpgradeResult{Current: "1.0.0", Latest: "1.1.0"}}
	m := upgradeModel(c)
	m.gatewayURL = "wss://abc.trycloudflare.com"
	m = runUpgradeCmd(t, m, "upgrade gateway")
	p, ok := m.popups.front().(confirmPopup)
	if !ok {
		t.Fatalf("popup %T, want confirmPopup", m.popups.front())
	}
	if p.id() != "upgrade" {
		t.Fatalf("id %q", p.id())
	}
	if p.title != "Upgrade the gateway from 1.0.0 to 1.1.0?" || p.action != "Upgrade and restart" {
		t.Fatalf("title %q, action %q", p.title, p.action)
	}
	if !strings.Contains(p.detail, "quick tunnel") {
		t.Fatalf("detail %q lacks the quick tunnel note", p.detail)
	}
}
