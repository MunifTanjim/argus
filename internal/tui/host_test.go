package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
)

type hostClient struct {
	recordingClient
	info api.HostInfo
	err  error
}

func (c *hostClient) Call(method string, params, out any) error {
	_ = c.recordingClient.Call(method, params, out)
	if method == api.MethodHostInfo {
		if c.err != nil {
			return c.err
		}
		if p, ok := out.(*api.HostInfo); ok {
			*p = c.info
		}
	}
	return nil
}

func TestFmtUptime(t *testing.T) {
	for secs, want := range map[int64]string{0: "<1m", 59: "<1m", 720: "12m", 15120: "4h 12m", 274320: "3d 4h 12m", 259500: "3d 0h 5m"} {
		if got := fmtUptime(secs); got != want {
			t.Errorf("fmtUptime(%d) = %q, want %q", secs, got, want)
		}
	}
}

func TestFmtBattery(t *testing.T) {
	cases := map[api.HostBattery]string{
		{Percent: 82, State: "charging"}:     "82% · charging",
		{Percent: 40, State: "not_charging"}: "40% · not charging",
		{Percent: 7, State: "unknown"}:       "7%",
	}
	for in, want := range cases {
		if got := fmtBattery(in); got != want {
			t.Errorf("fmtBattery(%+v) = %q, want %q", in, got, want)
		}
	}
}

func TestFmtWakelock(t *testing.T) {
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.Local)
	today := time.Date(2026, 10, 3, 14, 30, 0, 0, time.Local).Format(time.RFC3339)
	tomorrow := time.Date(2026, 10, 4, 14, 30, 0, 0, time.Local).Format(time.RFC3339)
	for in, want := range map[string]string{
		"":                         "off",
		api.HostWakelockIndefinite: "on until turned off",
		today:                      "on until 14:30",
		tomorrow:                   "on until Oct 4 14:30",
	} {
		if got := fmtWakelock(in, now); got != want {
			t.Errorf("fmtWakelock(%q) = %q, want %q", in, got, want)
		}
	}
}

func withWakelockCap(m model, on bool) model {
	m.nodeInfo = []api.NodeInfo{{ID: "n1", Label: "home", Capabilities: api.NodeCapabilities{HostWakelock: on}}}
	return m
}

func nodeSummaryModel(c Client) model {
	m := withWakelockCap(projectsTestModel(), true)
	m.client = c
	m = withFocus(selectRow(m, "n1"), mainPane)
	return m
}

func TestNodeSummaryHostLines(t *testing.T) {
	c := &hostClient{info: api.HostInfo{
		OS:            "macOS 26.0.1 (arm64)",
		UptimeSeconds: 274320,
		Battery:       &api.HostBattery{Percent: 82, State: "charging"},
		Wakelock:      api.HostWakelock{Until: api.HostWakelockIndefinite},
	}}
	m := nodeSummaryModel(c)
	m, _ = upd(m, hostInfoMsg{nodeID: "n1", info: c.info})
	assertGolden(t, "node-summary-host", m)
}

func TestNodeSummaryHostError(t *testing.T) {
	m := nodeSummaryModel(&hostClient{})
	m, _ = upd(m, hostInfoMsg{nodeID: "n1", err: errors.New("connection lost")})
	assertGolden(t, "node-summary-host-error", m)
}

func TestNodeSummaryFetchesOnOpen(t *testing.T) {
	c := &hostClient{}
	m := projectsTestModel()
	m.client = c
	m.left.tree.selectRow("n1")
	execCmd(m.showRow("n1"))
	if len(c.paramsOf(api.MethodHostInfo)) == 0 {
		t.Fatal("opening the node summary must call host.info")
	}
}

func TestHostTickOnlyForVisibleNode(t *testing.T) {
	m := nodeSummaryModel(&hostClient{})
	m, cmd := upd(m, hostInfoMsg{nodeID: "n1"})
	if cmd == nil {
		t.Fatal("a reply for the visible node must arm a tick")
	}
	gen := m.hostGen
	if _, cmd := upd(m, hostTickMsg{nodeID: "n1", gen: gen - 1}); cmd != nil {
		t.Fatal("a stale tick must not fetch")
	}
	if _, cmd := upd(m, hostTickMsg{nodeID: "other", gen: gen}); cmd != nil {
		t.Fatal("a tick for a hidden node must not fetch")
	}
	if _, cmd := upd(m, hostInfoMsg{nodeID: "other"}); cmd != nil {
		t.Fatal("a reply for a hidden node must not arm a tick")
	}
	if _, cmd := upd(m, hostTickMsg{nodeID: "n1", gen: gen}); cmd == nil {
		t.Fatal("a current tick for the visible node must fetch")
	}
}

func loadedNodeModel(t *testing.T, until string, capable bool) (model, *hostClient) {
	t.Helper()
	c := &hostClient{info: api.HostInfo{UptimeSeconds: 60, Wakelock: api.HostWakelock{Until: until}}}
	m := withWakelockCap(projectsTestModel(), capable)
	m.client = c
	m = selectRow(m, "n1")
	m, _ = upd(m, hostInfoMsg{nodeID: "n1", info: c.info})
	return m, c
}

func TestWakelockKeyOpensPicker(t *testing.T) {
	m, _ := loadedNodeModel(t, "", true)
	m, _ = upd(m, keyMsg("w"))
	p, ok := m.popups.front().(wakelockPicker)
	if !ok {
		t.Fatalf("popup %T, want wakelockPicker", m.popups.front())
	}
	if n := len(p.choices); n != 5 {
		t.Fatalf("%d choices, want 5 (no Off while off)", n)
	}
	assertGolden(t, "wakelock-picker", m)
}

func TestWakelockPickerOffWhenOn(t *testing.T) {
	m, _ := loadedNodeModel(t, api.HostWakelockIndefinite, true)
	m, _ = upd(m, keyMsg("w"))
	p := m.popups.front().(wakelockPicker)
	if last := p.choices[len(p.choices)-1]; last.label != "Off" {
		t.Fatalf("last choice %q, want Off", last.label)
	}
}

func TestWakelockPickerSetsUntil(t *testing.T) {
	m, c := loadedNodeModel(t, "", true)
	m, _ = upd(m, keyMsg("w"))
	before := time.Now().UTC()
	_, cmd := upd(m, enterKey)
	execCmd(cmd)
	calls := c.paramsOf(api.MethodHostSetWakelock)
	if len(calls) != 1 {
		t.Fatalf("host.setWakelock calls %d", len(calls))
	}
	p := calls[0].(api.HostSetWakelockParams)
	until, err := time.Parse(time.RFC3339, p.Until)
	if err != nil || p.NodeID != "n1" {
		t.Fatalf("params %+v", p)
	}
	if d := until.Sub(before); d < 29*time.Minute || d > 31*time.Minute {
		t.Fatalf("until %s is not 30 minutes ahead", p.Until)
	}
}

func TestWakelockUnsupportedFlash(t *testing.T) {
	m, _ := loadedNodeModel(t, "", false)
	m, _ = upd(m, keyMsg("w"))
	if m.popups.front() != nil {
		t.Fatal("no popup when unsupported")
	}
	if m.flash != "wakelock is not supported on this node" {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestWakelockBeforeLoadFetches(t *testing.T) {
	c := &hostClient{}
	m := withWakelockCap(projectsTestModel(), true)
	m.client = c
	m = selectRow(m, "n1")
	m.hosts = map[string]hostEntry{}
	m, cmd := upd(m, keyMsg("w"))
	if m.popups.front() != nil {
		t.Fatal("no popup before host info loads")
	}
	if m.flash != "host info is not loaded yet" || cmd == nil {
		t.Fatalf("flash %q cmd %v", m.flash, cmd)
	}
}

func TestWakelockKeyNeedsNodeRow(t *testing.T) {
	m, _ := loadedNodeModel(t, "", true)
	m = selectRow(m, "n1:p1")
	m, _ = upd(m, keyMsg("w"))
	if m.popups.front() != nil {
		t.Fatal("w on a project row must not open the picker")
	}
}

func TestNodeSummaryHostMinimal(t *testing.T) {
	c := &hostClient{info: api.HostInfo{UptimeSeconds: 720}}
	m := withWakelockCap(nodeSummaryModel(c), false)
	m, _ = upd(m, hostInfoMsg{nodeID: "n1", info: c.info})
	assertGolden(t, "node-summary-host-minimal", m)
}

func TestWakelockReplyUpdatesStateAndFetches(t *testing.T) {
	m, c := loadedNodeModel(t, "", true)
	m, cmd := upd(m, hostWakelockMsg{nodeID: "n1", wakelock: api.HostWakelock{Until: api.HostWakelockIndefinite}})
	if got := m.hosts["n1"].info.Wakelock.Until; got != api.HostWakelockIndefinite {
		t.Fatalf("wakelock %q, want the reply's state before the refetch", got)
	}
	before := len(c.paramsOf(api.MethodHostInfo))
	execCmd(cmd)
	if len(c.paramsOf(api.MethodHostInfo)) != before+1 {
		t.Fatal("a wakelock reply must fetch host.info")
	}
}

func TestWakelockErrorKeepsState(t *testing.T) {
	m, _ := loadedNodeModel(t, api.HostWakelockIndefinite, true)
	m, _ = upd(m, hostWakelockMsg{nodeID: "n1", err: errors.New("boom")})
	if got := m.hosts["n1"].info.Wakelock.Until; got != api.HostWakelockIndefinite {
		t.Fatalf("wakelock %q after a failed call", got)
	}
	if m.flash != "wakelock failed: boom" {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestNodeTerminalsProjectsTabFetchesHost(t *testing.T) {
	for name, open := range map[string]func(terminalsComp, *ctx) tea.Cmd{
		"stepTab":  func(tc terminalsComp, c *ctx) tea.Cmd { return tc.stepTab(c, 1) },
		"clickTab": func(tc terminalsComp, c *ctx) tea.Cmd { return tc.clickTab(c, int(nodeTabProjects)) },
	} {
		c := &hostClient{}
		m := projectsTestModel()
		m.client = c
		execCmd(open(terminalsComp{nodeID: "n1"}, &ctx{m: &m}))
		calls := c.paramsOf(api.MethodHostInfo)
		if len(calls) != 1 || calls[0].(api.HostInfoParams).NodeID != "n1" {
			t.Errorf("%s: host.info calls %v", name, calls)
		}
	}
}

func TestWakelockCommandOnlyOnNodeRow(t *testing.T) {
	m, _ := loadedNodeModel(t, "", true)
	if !m.applies(nodeKeys.Wakelock) {
		t.Fatal("node wakelock must apply on a node row")
	}
	m = selectRow(m, "n1:p1")
	if m.applies(nodeKeys.Wakelock) {
		t.Fatal("node wakelock must not apply on a project row")
	}
	for _, b := range m.commandSet() {
		if b.name == nodeKeys.Wakelock.name {
			t.Fatal("the command set on a project row has node wakelock")
		}
	}
}

func TestWakelockPickerLabelForNodeWithoutProjects(t *testing.T) {
	m := projectsTestModel()
	m.nodeInfo = []api.NodeInfo{{ID: "n9", Capabilities: api.NodeCapabilities{HostWakelock: true}}}
	m.hosts = map[string]hostEntry{"n9": {}}
	c := &ctx{m: &m}
	summaryComp{kind: rowNode, id: "n9"}.handleKey(c, keyMsg("w"))
	for _, a := range c.actions {
		if p, ok := a.pop.(wakelockPicker); ok {
			if p.label != "n9" {
				t.Fatalf("label %q, want the node id", p.label)
			}
			return
		}
	}
	t.Fatal("no wakelock picker opened")
}

func TestWakelockFooterHint(t *testing.T) {
	for _, capable := range []bool{true, false} {
		m, _ := loadedNodeModel(t, "", capable)
		for name, mm := range map[string]model{"tree": m, "summary": withFocus(m, mainPane)} {
			got := strings.Contains(ansi.Strip(mm.View().Content), "w wakelock")
			if got != capable {
				t.Errorf("%s footer, capable=%v: shows w wakelock = %v", name, capable, got)
			}
		}
	}
}
