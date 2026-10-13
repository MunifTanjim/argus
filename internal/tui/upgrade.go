package tui

import (
	"errors"
	"net/url"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// upgradeTarget is a node. An empty nodeID addresses the gateway, or the
// daemon itself over a local socket.
type upgradeTarget struct {
	nodeID string
	label  string
}

func (t upgradeTarget) params() api.ServerTargetParams {
	return api.ServerTargetParams{NodeID: t.nodeID}
}

var gatewayTarget = upgradeTarget{label: "the gateway"}

// A cloudflare quick tunnel gets a new random hostname each time the gateway
// starts.
func isQuickTunnelURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.HasSuffix(u.Hostname(), ".trycloudflare.com")
}

type upgradeCheckMsg struct {
	target upgradeTarget
	res    api.CheckUpgradeResult
	err    error
}

type upgradeDoneMsg struct {
	target     upgradeTarget
	res        api.UpgradeResult
	err        error
	restartErr error
}

func (m model) upgradeCheckCmd(t upgradeTarget) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.CheckUpgradeResult
		err := client.Call(api.MethodServerCheckUpgrade, t.params(), &res)
		return upgradeCheckMsg{target: t, res: res, err: err}
	}
}

func (m model) upgradeRunCmd(t upgradeTarget) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		var res api.UpgradeResult
		if err := client.Call(api.MethodServerUpgrade, t.params(), &res); err != nil {
			return upgradeDoneMsg{target: t, err: err}
		}
		if res.To != res.From {
			// The restart closes the connection, which can lose the reply. Only an
			// error reply means that the restart failed.
			var re *api.RPCError
			if err := client.Call(api.MethodServerRestart, t.params(), nil); errors.As(err, &re) {
				return upgradeDoneMsg{target: t, res: res, restartErr: err}
			}
		}
		return upgradeDoneMsg{target: t, res: res}
	}
}

// upgradeNodeTarget is the node of the node pane, or of the tree row under
// the cursor. An empty node id is the daemon on a local socket.
func (m model) upgradeNodeTarget() (upgradeTarget, bool) {
	var id string
	var found bool
	switch m.screen() {
	case "node":
		var s summaryComp
		s, found = m.baseComp().(summaryComp)
		id = s.id
	case "project-tree":
		if r, ok := m.left.tree.cursorRow(); ok {
			switch r.kind {
			case rowNode:
				id, found = r.id, true
			case rowProject, rowWorkspace:
				id, _, _ = session.SplitCompositeID(r.id)
				found = true
			}
		}
	}
	if !found {
		return upgradeTarget{}, false
	}
	if id == "" {
		return upgradeTarget{label: "this machine"}, true
	}
	label := id
	if n, ok := m.node(id); ok {
		label = nodeName(n.Label, id)
	}
	return upgradeTarget{nodeID: id, label: label}, true
}

func (m model) updateUpgrade(msg tea.Msg) (model, tea.Cmd) {
	c := &ctx{m: &m}
	switch msg := msg.(type) {
	case upgradeCheckMsg:
		switch {
		case api.IsMethodNotFound(msg.err):
			c.setFlash(msg.target.label + " does not support remote upgrade")
		case msg.err != nil:
			c.setFlash("upgrade check failed: " + msg.err.Error())
		case msg.res.Latest == msg.res.Current:
			c.setFlash(msg.target.label + " is already on the latest version (" + msg.res.Current + ")")
		default:
			c.setFlash("")
			c.openPopup(upgradeConfirm{target: msg.target, check: msg.res})
		}
	case upgradeDoneMsg:
		switch {
		case msg.err != nil:
			c.setFlash("upgrade failed: " + msg.err.Error())
		case msg.restartErr != nil:
			c.setFlash(msg.target.label + " upgraded to " + msg.res.To + " · restart failed: " + msg.restartErr.Error())
		case msg.res.To == msg.res.From:
			c.setFlash(msg.target.label + " is already on the latest version (" + msg.res.From + ")")
		default:
			c.setFlash(msg.target.label + " upgraded to " + msg.res.To + " · restarting")
		}
	}
	return m, m.apply(c)
}

var upgradeChoices = []string{"Upgrade and restart", "Cancel"}

type upgradeConfirm struct {
	target upgradeTarget
	check  api.CheckUpgradeResult
	cursor int
}

func (p upgradeConfirm) id() string                            { return "upgrade" }
func (p upgradeConfirm) keySection() string                    { return "" }
func (p upgradeConfirm) spins(*ctx) bool                       { return false }
func (p upgradeConfirm) update(*ctx, tea.Msg) (popup, tea.Cmd) { return p, nil }

func (p upgradeConfirm) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	switch msg.String() {
	case "esc":
		c.closePopup()
	case "up", "k":
		p.cursor = cursorUp(p.cursor)
	case "down", "j":
		p.cursor = cursorDown(p.cursor, len(upgradeChoices))
	case "enter":
		c.closePopup()
		if p.cursor == 0 {
			c.setFlash("upgrading " + p.target.label)
			return p, c.m.upgradeRunCmd(p.target)
		}
	}
	return p, nil
}

func (p upgradeConfirm) click(c *ctx, t hitTarget) (popup, tea.Cmd) {
	p.cursor = t.index
	return p.handleKey(c, enterKey)
}

func (p upgradeConfirm) wheel(_ *ctx, d int) (popup, tea.Cmd) {
	p.cursor = cursorBy(p.cursor, d, len(upgradeChoices))
	return p, nil
}

func (p upgradeConfirm) draw(c *ctx, scr uv.Screen, _ uv.Rectangle) {
	area := c.m.mainRect()
	f := pickerFrame(area)
	f.title = "Upgrade " + p.target.label + " from " + p.check.Current + " to " + p.check.Latest + "?"
	f.help = c.m.hints(f.innerWidth(), hint("↑/↓", "move"), hint("enter", "select"), hint("esc", "cancel"))
	var note string
	if p.target == gatewayTarget && isQuickTunnelURL(c.m.gatewayURL) {
		note = "the quick tunnel gets a new URL"
	}
	l := itemLines{key: "upgrade-confirm"}
	for i, ch := range upgradeChoices {
		sub := ""
		if i == 0 {
			sub = note
		}
		l.add(i, spawnChoiceRow(ch, sub, i == p.cursor, f.innerWidth()))
	}
	f.parts = l.window(f.bodyCtx(c), p.cursor, f.bodyHeight())
	c.hitRect(drawCenter(scr, area, f.render()))
}
