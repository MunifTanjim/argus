package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/MunifTanjim/argus/internal/api"
)

const hostPollEvery = 30 * time.Second

type hostEntry struct {
	info api.HostInfo
	err  error
}

func (m model) hostInfoCmd(nodeID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var info api.HostInfo
		err := client.Call(api.MethodHostInfo, api.HostInfoParams{NodeID: nodeID}, &info)
		return hostInfoMsg{nodeID: nodeID, info: info, err: err}
	}
}

func (m model) setWakelockCmd(nodeID, until string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var w api.HostWakelock
		err := client.Call(api.MethodHostSetWakelock, api.HostSetWakelockParams{NodeID: nodeID, Until: until}, &w)
		return hostWakelockMsg{nodeID: nodeID, wakelock: w, err: err}
	}
}

func hostTick(nodeID string, gen int) tea.Cmd {
	return tea.Tick(hostPollEvery, func(time.Time) tea.Msg { return hostTickMsg{nodeID: nodeID, gen: gen} })
}

func openNodeSummary(c *ctx, nodeID string) tea.Cmd {
	c.replaceBase(summaryComp{kind: rowNode, id: nodeID})
	return c.m.hostInfoCmd(nodeID)
}

// An unknown node has none.
func (m model) nodeHasWakelock(nodeID string) bool {
	n, ok := m.node(nodeID)
	return ok && n.Capabilities.HostWakelock
}

func (m model) showsNodeSummary(nodeID string) bool {
	s, ok := m.baseComp().(summaryComp)
	return ok && s.kind == rowNode && s.id == nodeID
}

// Each reply re-arms one tick under a new gen, so older ticks go stale and
// polls never stack.
func (m model) updateHost(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case hostInfoMsg:
		if m.hosts == nil {
			m.hosts = map[string]hostEntry{}
		}
		m.hosts[msg.nodeID] = hostEntry{info: msg.info, err: msg.err}
		if !m.showsNodeSummary(msg.nodeID) {
			return m, nil
		}
		m.hostGen++
		return m, hostTick(msg.nodeID, m.hostGen)
	case hostTickMsg:
		if msg.gen != m.hostGen || !m.showsNodeSummary(msg.nodeID) {
			return m, nil
		}
		return m, m.hostInfoCmd(msg.nodeID)
	case hostWakelockMsg:
		if msg.err != nil {
			m.flash = "wakelock failed: " + msg.err.Error()
		} else if e, ok := m.hosts[msg.nodeID]; ok && e.err == nil {
			e.info.Wakelock = msg.wakelock
			m.hosts[msg.nodeID] = e
		}
		return m, m.hostInfoCmd(msg.nodeID)
	}
	return m, nil
}

func hostLines(e hostEntry, wakelock bool, now time.Time, w int) string {
	if e.err != nil {
		return truncateLine(StyleErrorBold.Render("host info error: "+e.err.Error()), w) + "\n\n"
	}
	var b strings.Builder
	row := func(label, value string) {
		b.WriteString(truncateLine("  "+dimStyle.Render(fmt.Sprintf("%-11s", label))+value, w) + "\n")
	}
	if e.info.OS != "" {
		row("OS", e.info.OS)
	}
	row("Uptime", fmtUptime(e.info.UptimeSeconds))
	if bat := e.info.Battery; bat != nil {
		row("Battery", fmtBattery(*bat))
	}
	if wakelock {
		row("Wakelock", fmtWakelock(e.info.Wakelock.Until, now))
	}
	return b.String() + "\n"
}

func fmtUptime(secs int64) string {
	units := []struct {
		n    int64
		unit string
	}{{secs / 86400, "d"}, {secs % 86400 / 3600, "h"}, {secs % 3600 / 60, "m"}}
	var parts []string
	for _, u := range units {
		if u.n > 0 || len(parts) > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", u.n, u.unit))
		}
	}
	if len(parts) == 0 {
		return "<1m"
	}
	return strings.Join(parts, " ")
}

var batteryStates = map[string]string{
	"charging": "charging", "discharging": "discharging", "full": "full", "not_charging": "not charging",
}

func fmtBattery(b api.HostBattery) string {
	s := fmt.Sprintf("%d%%", b.Percent)
	if st, ok := batteryStates[b.State]; ok {
		s += " · " + st
	}
	return s
}

func fmtWakelock(until string, now time.Time) string {
	switch until {
	case "":
		return "off"
	case api.HostWakelockIndefinite:
		return "on until turned off"
	}
	t, err := time.Parse(time.RFC3339, until)
	if err != nil {
		return "on"
	}
	t = t.In(now.Location())
	if y, mo, d := t.Date(); y == now.Year() && mo == now.Month() && d == now.Day() {
		return "on until " + t.Format("15:04")
	}
	return "on until " + t.Format("Jan 2 15:04")
}

type wakelockChoice struct {
	label string
	dur   time.Duration
	fixed string // the until value when dur is 0
}

func (ch wakelockChoice) until(now time.Time) string {
	if ch.dur == 0 {
		return ch.fixed
	}
	return now.UTC().Add(ch.dur).Format(time.RFC3339)
}

func wakelockChoices(on bool) []wakelockChoice {
	cs := []wakelockChoice{
		{label: "30 minutes", dur: 30 * time.Minute},
		{label: "1 hour", dur: time.Hour},
		{label: "4 hours", dur: 4 * time.Hour},
		{label: "8 hours", dur: 8 * time.Hour},
		{label: "Until I turn it off", fixed: api.HostWakelockIndefinite},
	}
	if on {
		cs = append(cs, wakelockChoice{label: "Off"})
	}
	return cs
}

func openWakelockPicker(c *ctx, nodeID, label string) tea.Cmd {
	if !c.m.nodeHasWakelock(nodeID) {
		c.setFlash("wakelock is not supported on this node")
		return nil
	}
	e, ok := c.m.hosts[nodeID]
	if !ok || e.err != nil {
		c.setFlash("host info is not loaded yet")
		return c.m.hostInfoCmd(nodeID)
	}
	c.openPopup(wakelockPicker{nodeID: nodeID, label: label, choices: wakelockChoices(e.info.Wakelock.Until != "")})
	return nil
}

type wakelockPicker struct {
	nodeID  string
	label   string
	choices []wakelockChoice
	cursor  int
}

func (p wakelockPicker) id() string                            { return "wakelock" }
func (p wakelockPicker) keySection() string                    { return "" }
func (p wakelockPicker) spins(*ctx) bool                       { return false }
func (p wakelockPicker) update(*ctx, tea.Msg) (popup, tea.Cmd) { return p, nil }

func (p wakelockPicker) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	switch msg.String() {
	case "esc":
		c.closePopup()
	case "up", "k":
		p.cursor = cursorUp(p.cursor)
	case "down", "j":
		p.cursor = cursorDown(p.cursor, len(p.choices))
	case "enter":
		c.closePopup()
		return p, c.m.setWakelockCmd(p.nodeID, p.choices[p.cursor].until(time.Now()))
	}
	return p, nil
}

func (p wakelockPicker) click(c *ctx, t hitTarget) (popup, tea.Cmd) {
	p.cursor = t.index
	return p.handleKey(c, enterKey)
}

func (p wakelockPicker) wheel(_ *ctx, d int) (popup, tea.Cmd) {
	p.cursor = cursorBy(p.cursor, d, len(p.choices))
	return p, nil
}

func (p wakelockPicker) draw(c *ctx, scr uv.Screen, _ uv.Rectangle) {
	area := c.m.mainRect()
	f := pickerFrame(area)
	f.title = "Keep " + p.label + " awake"
	f.help = c.m.hints(f.innerWidth(), hint("↑/↓", "move"), hint("enter", "select"), hint("esc", "cancel"))
	var l itemLines
	for i, ch := range p.choices {
		l.add(i, spawnChoiceRow(ch.label, "", i == p.cursor, f.innerWidth()))
	}
	f.parts = l.window(f.bodyCtx(c), p.cursor, f.bodyHeight())
	c.hitRect(drawCenter(scr, area, f.render()))
}
