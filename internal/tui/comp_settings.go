package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
	"github.com/MunifTanjim/argus/internal/keyfmt"
)

type settingsSub int

const (
	settingsLock settingsSub = iota
	settingsPaired
)

var settingsSubLabels = []string{"Trust", "Paired devices"}

type settingsLockMsg struct {
	seq  int
	view LockView
	err  error
}

type settingsActionMsg struct {
	seq     int
	text    string
	err     error
	pin     bool
	rebuild bool
}

type actKind int

const (
	actLock actKind = iota
	// actPin shows its whole result and rebuilds the client when its result
	// says the client pin changed.
	actPin
	actPaired
)

type settingsAct struct {
	run  func(context.Context, Settings) (PinResult, error)
	kind actKind
}

func textResult(text string, err error) (PinResult, error) { return PinResult{Text: text}, err }

type settingsConfirmedMsg struct{ act settingsAct }

type settingsPinOfferMsg struct {
	seq   int
	offer PinOffer
	err   error
}

type settingsReconnectedMsg struct {
	client Client
	err    error
}

type settingsPairedMsg struct {
	seq  int
	list []api.ClientTokenInfo
	err  error
}

type settingsPairStartMsg struct {
	seq   int
	offer PairingOffer
	err   error
}

type settingsLogMsg struct {
	seq  int
	text string
	err  error
}

// settingsMsg is a reply the settings screen takes wherever it is in the main
// stack.
type settingsMsg interface{ settingsMsg() }

func (settingsLockMsg) settingsMsg()      {}
func (settingsActionMsg) settingsMsg()    {}
func (settingsLogMsg) settingsMsg()       {}
func (settingsConfirmedMsg) settingsMsg() {}
func (settingsPinOfferMsg) settingsMsg()  {}
func (settingsPairedMsg) settingsMsg()    {}
func (settingsPairStartMsg) settingsMsg() {}
func (pairDoneMsg) settingsMsg()          {}

type textView int

const (
	textNone textView = iota
	textLog
	textDetails
)

type settingsComp struct {
	sub        settingsSub
	cursor     int
	scroll     int
	lock       LockView
	lockErr    error
	lockLoaded bool
	lockSeq    int
	actionSeq  int
	actionErr  error
	actionOut  string
	offerSeq   int

	text       textView
	textScroll int
	logLoaded  bool
	logText    string
	logErr     error
	logSeq     int

	paired       []api.ClientTokenInfo
	pairedErr    error
	pairedLoaded bool
	pairedSeq    int
	pairedCursor int
	pairedScroll int
	pairedActSeq int
	pairedActErr error
	pairStartSeq int
}

func (s settingsComp) section() string           { return "settings" }
func (s settingsComp) raw(*ctx) bool             { return false }
func (s settingsComp) spins(*ctx) bool           { return false }
func (s settingsComp) fullScreen(*ctx) fullLevel { return fullFrame }
func (s settingsComp) offers(*ctx) []binding     { return sectionOffers[s.section()].keys }
func (s settingsComp) commands(*ctx) []binding   { return sectionLists[s.section()].own }
func (s settingsComp) layer() layer              { return baseLayer }
func (s settingsComp) pageStep(c *ctx) int       { return c.m.listPageStep() }

// close cancels a pairing wait, since its popup outlives the screen
// otherwise.
func (s settingsComp) close(c *ctx) tea.Cmd {
	dropPair(c.m, func(pairPopup) bool { return true })
	return nil
}

func (s settingsComp) footer(c *ctx) []binding {
	out := []binding{listKeys.TabNext}
	switch {
	case s.sub == settingsPaired:
		if !s.pairable() {
			break
		}
		if s.pairedCursor < len(s.paired) {
			out = append(out, settingsKeys.Remove)
		}
		out = append(out, settingsKeys.Pair)
	case s.text != textNone:
		out = append(out, logsKeys.Bottom)
	default:
		if d, ok := s.device(); ok && s.lock.Status.SignerTrusted {
			if d.Authorized {
				out = append(out, helpAs(settingsKeys.Remove, "revoke"))
			} else {
				out = append(out, settingsKeys.Sign)
			}
		}
		if s.canSignTUI() {
			out = append(out, settingsKeys.SignTUI)
		}
		if s.canLog() {
			out = append(out, settingsKeys.Log)
		}
		if s.canDetails() {
			out = append(out, settingsKeys.Details)
		}
	}
	return append(out, projectsKeys.Help)
}

func (s settingsComp) canLog() bool     { return s.lock.NodeRunning }
func (s settingsComp) canDetails() bool { return s.lockLoaded }

func openSettings(c *ctx) tea.Cmd {
	var s settingsComp
	s, lock := s.loadLock(c)
	s, paired := s.loadPaired(c)
	c.closeOverlays()
	c.replaceBase(s)
	c.focusOn(mainPane)
	return tea.Batch(lock, paired)
}

func (m *model) nextSettingsSeq() int {
	m.settingsSeq++
	return m.settingsSeq
}

// settingsCall numbers a backend call from the model, so that a reply to an
// earlier call, by this screen or by one opened before it, is ignored.
func settingsCall(c *ctx, call func(ctx context.Context, st Settings, seq int) tea.Msg) (int, tea.Cmd) {
	seq, st := c.m.nextSettingsSeq(), c.m.settings
	return seq, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), settingsCallTimeout)
		defer cancel()
		return call(ctx, st, seq)
	}
}

func (s settingsComp) loadPaired(c *ctx) (settingsComp, tea.Cmd) {
	if c.m.gatewayURL == "" {
		return s, nil
	}
	s.pairedActErr = nil
	var cmd tea.Cmd
	s.pairedSeq, cmd = settingsCall(c, func(ctx context.Context, st Settings, seq int) tea.Msg {
		list, err := st.PairedDevices(ctx)
		return settingsPairedMsg{seq: seq, list: list, err: err}
	})
	return s, cmd
}

func (s settingsComp) startPairing(c *ctx) (settingsComp, tea.Cmd) {
	var cmd tea.Cmd
	s.pairStartSeq, cmd = settingsCall(c, func(ctx context.Context, st Settings, seq int) tea.Msg {
		offer, err := st.StartPairing(ctx)
		return settingsPairStartMsg{seq: seq, offer: offer, err: err}
	})
	return s, cmd
}

func (s settingsComp) pairable() bool { return s.pairing() == pairingAvailable }

func (s settingsComp) pairing() pairingState { return pairingStateOf(s.pairedLoaded, s.pairedErr) }

func (s settingsComp) loadLock(c *ctx) (settingsComp, tea.Cmd) {
	var cmd tea.Cmd
	s.lockSeq, cmd = settingsCall(c, func(ctx context.Context, st Settings, seq int) tea.Msg {
		v, err := st.LockStatus(ctx)
		return settingsLockMsg{seq: seq, view: v, err: err}
	})
	return s, cmd
}

func (s settingsComp) loadLog(c *ctx) (settingsComp, tea.Cmd) {
	s.text, s.logLoaded, s.textScroll = textLog, false, 0
	var cmd tea.Cmd
	s.logSeq, cmd = settingsCall(c, func(ctx context.Context, st Settings, seq int) tea.Msg {
		text, err := st.LockLog(ctx)
		return settingsLogMsg{seq: seq, text: text, err: err}
	})
	return s, cmd
}

// runAction keeps only the latest action's reply on this screen in effect.
func (s settingsComp) runAction(c *ctx, act settingsAct) (settingsComp, tea.Cmd) {
	seq, cmd := settingsCall(c, func(ctx context.Context, st Settings, seq int) tea.Msg {
		r, err := act.run(ctx, st)
		return settingsActionMsg{seq: seq, text: r.Text, err: err, pin: act.kind == actPin, rebuild: r.ClientPinChanged}
	})
	if act.kind == actPaired {
		s.pairedActSeq = seq
	} else {
		s.actionSeq, s.actionOut, s.actionErr = seq, "", nil
	}
	return s, cmd
}

// startRebuild leaves a rebuild asked for during another to start once that
// one ends, as the running one may have read the pin before it changed.
func (m *model) startRebuild() tea.Cmd {
	if m.rebuilding {
		m.rebuildQueued = true
		return nil
	}
	m.rebuilding = true
	st := m.settings
	return func() tea.Msg {
		cl, err := st.Reconnect()
		return settingsReconnectedMsg{client: cl, err: err}
	}
}

// confirm asks before act and opens on Cancel, so a stray enter does nothing.
// The action is numbered when the user accepts, so a cancelled popup leaves a
// pending action's reply in effect.
func confirm(c *ctx, title, action, detail string, act settingsAct, body ...string) {
	c.openPopup(confirmPopup{popupID: "settings-confirm", title: title, action: action, detail: detail, body: body, cursor: 1,
		accept: func(*ctx) tea.Cmd { return func() tea.Msg { return settingsConfirmedMsg{act: act} } }})
}

func (s settingsComp) sign(c *ctx, key []byte) (settingsComp, tea.Cmd) {
	return s.runAction(c, settingsAct{run: func(ctx context.Context, st Settings) (PinResult, error) { return textResult(st.SignDevice(ctx, key)) }})
}

func (s settingsComp) loadPinOffer(c *ctx) (settingsComp, tea.Cmd) {
	var cmd tea.Cmd
	s.offerSeq, cmd = settingsCall(c, func(ctx context.Context, st Settings, seq int) tea.Msg {
		offer, err := st.NetworkGenesis(ctx)
		return settingsPinOfferMsg{seq: seq, offer: offer, err: err}
	})
	return s, cmd
}

func (s settingsComp) showActionErr(c *ctx, err error) settingsComp {
	s.actionErr, s.actionOut, s.scroll = err, "", 0
	return s.settle(c)
}

func (s settingsComp) device() (LockDevice, bool) {
	if s.cursor < len(s.lock.Devices) {
		return s.lock.Devices[s.cursor], true
	}
	return LockDevice{}, false
}

func (s settingsComp) canSignTUI() bool { return s.lock.Summary.CanSignTUI }

func (s settingsComp) update(c *ctx, msg tea.Msg) (component, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case settingsLockMsg:
		if msg.seq != s.lockSeq {
			break
		}
		s.lockLoaded = true
		s.lock, s.lockErr = msg.view, msg.err
		s.cursor = min(s.cursor, cursorBottom(len(s.lock.Devices)))
		s = s.settle(c)
		s.textScroll = s.clampText(c, s.textScroll)
	case settingsActionMsg:
		if msg.seq == s.pairedActSeq {
			if msg.err != nil {
				s = s.showPairedErr(c, msg.err)
				break
			}
			c.setFlash(msg.text)
			s, cmd = s.loadPaired(c)
			break
		}
		if msg.seq != s.actionSeq {
			break
		}
		if msg.err != nil {
			s = s.showActionErr(c, msg.err)
		} else {
			text := strings.TrimSpace(msg.text)
			c.setFlash(firstLine(text))
			if msg.pin {
				s.actionOut, s.scroll = text, 0
				s = s.settle(c)
			}
			s.actionErr = nil
		}
		if !msg.rebuild {
			s, cmd = s.loadLock(c)
		}
	case settingsConfirmedMsg:
		s, cmd = s.runAction(c, msg.act)
	case settingsPinOfferMsg:
		switch {
		case msg.seq != s.offerSeq, s.sub != settingsLock:
		case msg.err != nil:
			s = s.showActionErr(c, msg.err)
		case msg.offer.Genesis == nil:
			c.setFlash("no trust log on this network; nothing to pin")
		case !settingsOnTop(c):
		default:
			g := msg.offer.Genesis
			confirm(c, "Pin to "+msg.offer.Fingerprint+"?", "Pin", "", settingsAct{
				run:  func(ctx context.Context, st Settings) (PinResult, error) { return st.Pin(ctx, g) },
				kind: actPin,
			}, msg.offer.Encoded, msg.offer.Fingerprint)
		}
	case settingsReconnectedMsg:
		s, cmd = s.loadLock(c)
	case settingsPairedMsg:
		if msg.seq != s.pairedSeq {
			break
		}
		s.pairedLoaded = true
		s.paired, s.pairedErr = msg.list, msg.err
		s.pairedCursor = min(s.pairedCursor, cursorBottom(len(s.paired)))
		s = s.settlePaired(c)
	case settingsPairStartMsg:
		switch {
		case msg.seq != s.pairStartSeq:
		case msg.err != nil:
			s = s.showPairedErr(c, msg.err)
		case !settingsOnTop(c):
		default:
			cmd = openPair(c, msg.offer)
		}
	case pairDoneMsg:
		p, ok := dropPair(c.m, func(p pairPopup) bool { return p.seq == msg.seq })
		switch {
		case !ok:
		case msg.connected:
			c.setFlash("paired")
			s, cmd = s.loadPaired(c)
		case msg.err == nil, errors.Is(msg.err, context.DeadlineExceeded), !time.Now().Before(p.deadline):
			c.setFlash("no device connected")
		default:
			s = s.showPairedErr(c, msg.err)
		}
	case settingsLogMsg:
		if s.text == textLog && msg.seq == s.logSeq {
			s.logLoaded, s.logText, s.logErr = true, strings.TrimRight(msg.text, "\n"), msg.err
			s.textScroll = s.clampText(c, s.textScroll)
		}
	}
	return s, cmd
}

func (s settingsComp) switchTab(d int) settingsComp {
	n := settingsSub(len(settingsSubLabels))
	s.sub = (s.sub + n + settingsSub(d)) % n
	s.text = textNone
	return s
}

func (s settingsComp) showPairedErr(c *ctx, err error) settingsComp {
	s.pairedActErr, s.pairedScroll = err, 0
	return s.settlePaired(c)
}

func (s settingsComp) handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool) {
	m, k := c.m, listKeys
	c.setFlash("")
	var cmd tea.Cmd
	var handled bool
	switch {
	case s.sub == settingsPaired:
		s, cmd, handled = s.handlePairedKey(c, msg)
	case s.text != textNone:
		s, cmd, handled = s.handleTextKey(c, msg)
	default:
		s, cmd, handled = s.handlePinKey(c, msg)
		if !handled {
			s, cmd, handled = s.handleViewKey(c, msg)
		}
		if !handled && s.lock.NodeRunning {
			s, cmd, handled = s.handleLockKey(c, msg)
		}
	}
	if handled {
		return s, cmd, true
	}
	if d, ok := s.navDelta(c, msg); ok {
		return s.nav(c, d), nil, true
	}
	switch {
	case m.matches(msg, k.TabPrev):
		s = s.switchTab(-1)
	case m.matches(msg, k.TabNext):
		s = s.switchTab(1)
	case m.matches(msg, k.Refresh):
		switch {
		case s.sub == settingsPaired:
			s, cmd = s.loadPaired(c)
		case m.reconnectErr != nil && !m.rebuilding:
			s.actionErr, cmd = nil, m.startRebuild()
		default:
			s.actionErr = nil
			s, cmd = s.loadLock(c)
		}
	case m.matches(msg, k.Back):
		c.replaceBase(m.homePane())
	default:
		return s, nil, false
	}
	return s, cmd, true
}

func (s settingsComp) handleLockKey(c *ctx, msg tea.KeyPressMsg) (settingsComp, tea.Cmd, bool) {
	m, k := c.m, settingsKeys
	d, ok := s.device()
	var run func() (settingsComp, tea.Cmd)
	switch {
	case m.matches(msg, k.Sign):
		if ok && !d.Authorized {
			run = func() (settingsComp, tea.Cmd) { return s.sign(c, d.Key) }
		}
	case m.matches(msg, k.SignTUI):
		if s.lock.Summary.TUISign == TUIUnsigned {
			run = func() (settingsComp, tea.Cmd) { return s.sign(c, s.lock.ClientIdentity) }
		}
	case m.matches(msg, k.Remove):
		if ok && d.Authorized {
			run = func() (settingsComp, tea.Cmd) { return s, s.confirmRevoke(c, d) }
		}
	default:
		return s, nil, false
	}
	if run == nil {
		return s, nil, true
	}
	if !s.lock.Status.SignerTrusted {
		c.setFlash("this node is not a trusted signer")
		return s, nil, true
	}
	s, cmd := run()
	return s, cmd, true
}

func (s settingsComp) confirmRevoke(c *ctx, d LockDevice) tea.Cmd {
	name, detail := d.name(), ""
	if d.Label != "" {
		detail = keyfmt.DeviceKey.Encode(d.Key)
	}
	key := d.Key
	confirm(c, "Revoke "+name+"?", "Revoke", detail, settingsAct{
		run: func(ctx context.Context, st Settings) (PinResult, error) {
			return textResult(st.RevokeDevice(ctx, key))
		},
	})
	return nil
}

func (s settingsComp) handlePairedKey(c *ctx, msg tea.KeyPressMsg) (settingsComp, tea.Cmd, bool) {
	m, k := c.m, settingsKeys
	if !m.matches(msg, k.Remove, k.Pair) {
		return s, nil, false
	}
	if !s.pairable() {
		return s, nil, true
	}
	if m.matches(msg, k.Pair) {
		s, cmd := s.startPairing(c)
		return s, cmd, true
	}
	if s.pairedCursor < len(s.paired) {
		tok := s.paired[s.pairedCursor].Token
		confirm(c, "Remove this device?", "Remove", clienttoken.Short(tok), settingsAct{
			kind: actPaired,
			run: func(ctx context.Context, st Settings) (PinResult, error) {
				return textResult("removed "+clienttoken.Short(tok), st.RemovePairedDevice(ctx, tok))
			},
		})
	}
	return s, nil, true
}

func (s settingsComp) handlePinKey(c *ctx, msg tea.KeyPressMsg) (settingsComp, tea.Cmd, bool) {
	m, k := c.m, settingsKeys
	switch {
	case m.matches(msg, k.Pin):
		s, cmd := s.loadPinOffer(c)
		return s, cmd, true
	case m.matches(msg, k.Unpin):
		confirm(c, "Unpin this device?", "Unpin", "", settingsAct{
			run:  func(ctx context.Context, st Settings) (PinResult, error) { return st.Unpin(ctx) },
			kind: actPin,
		})
		return s, nil, true
	}
	return s, nil, false
}

func (s settingsComp) handleViewKey(c *ctx, msg tea.KeyPressMsg) (settingsComp, tea.Cmd, bool) {
	m := c.m
	switch {
	case s.canLog() && m.matches(msg, settingsKeys.Log):
		s, cmd := s.loadLog(c)
		return s, cmd, true
	case s.canDetails() && m.matches(msg, settingsKeys.Details):
		s.text, s.textScroll = textDetails, 0
		return s, nil, true
	}
	return s, nil, false
}

func (s settingsComp) handleTextKey(c *ctx, msg tea.KeyPressMsg) (settingsComp, tea.Cmd, bool) {
	m, k := c.m, listKeys
	if s, cmd, ok := s.handleViewKey(c, msg); ok {
		return s, cmd, true
	}
	switch {
	case m.matches(msg, k.Refresh) && s.text == textLog:
		s, cmd := s.loadLog(c)
		return s, cmd, true
	case m.matches(msg, k.Back):
		s.text = textNone
		return s, nil, true
	}
	return s, nil, false
}

func (s settingsComp) click(c *ctx, t hitTarget, _ bool) (component, tea.Cmd) {
	switch {
	case t.kind == hitTab:
		s = s.switchTab(t.index - int(s.sub))
	case t.kind == hitRow && s.sub == settingsPaired:
		s = s.move(c, t.index-s.pairedCursor)
	case t.kind == hitRow && s.text == textNone:
		s = s.move(c, t.index-s.cursor)
	}
	return s, nil
}

func (s settingsComp) wheel(c *ctx, d int) (component, tea.Cmd) { return s.nav(c, d), nil }

func (s settingsComp) nav(c *ctx, d int) settingsComp {
	if s.text != textNone {
		s.textScroll = s.clampText(c, s.textScroll+d)
		return s
	}
	return s.move(c, d)
}

// navDelta takes up and down from the scroll keys while a text view is open and
// from the cursor keys otherwise.
func (s settingsComp) navDelta(c *ctx, msg tea.KeyPressMsg) (int, bool) {
	m, k := c.m, logsKeys
	up, down := listKeys.Up, listKeys.Down
	if s.text != textNone {
		up, down = k.Up, k.Down
	}
	switch {
	case m.matches(msg, up):
		return -1, true
	case m.matches(msg, down):
		return 1, true
	case m.matches(msg, k.HalfUp):
		return -halfBody(c), true
	case m.matches(msg, k.HalfDown):
		return halfBody(c), true
	case m.matches(msg, k.PageUp):
		return -bodyAvail(c), true
	case m.matches(msg, k.PageDown):
		return bodyAvail(c), true
	case m.matches(msg, k.Top):
		return -lockEnd, true
	case m.matches(msg, k.Bottom):
		return lockEnd, true
	}
	return 0, false
}

// textLines is the open text view wrapped to the pane, so no key or command is cut.
func (s settingsComp) textLines(c *ctx) []string {
	return strings.Split(xansi.Wrap(s.textBody(), lockWidth(c), ""), "\n")
}

func (s settingsComp) textBody() string {
	if s.text == textDetails {
		var parts []string
		for _, t := range []string{s.lock.StatusText, s.lock.TUIText} {
			if t = strings.TrimLeft(strings.TrimRight(t, " \t\r\n"), "\r\n"); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	switch {
	case !s.logLoaded:
		return dimStyle.Render("loading")
	case s.logErr != nil:
		return StyleErrorBold.Render(s.logErr.Error())
	case s.logText != "":
		return s.logText
	}
	return dimStyle.Render("no log entries")
}

func (s settingsComp) clampText(c *ctx, top int) int {
	return clampTop(top, len(s.textLines(c)), bodyAvail(c))
}

func clampTop(top, n, avail int) int { return max(0, min(top, n-avail)) }

// bodyAvail is the tab body's height: the pane less the tabs row, its blank
// row, and the footer.
func bodyAvail(c *ctx) int {
	_, h := c.m.mainSize()
	return max(1, h-2-footerRows)
}

func halfBody(c *ctx) int { return max(1, bodyAvail(c)/2) }

// lockEnd is a move far enough to reach either end of the Trust body.
const lockEnd = 1 << 20

func (s settingsComp) move(c *ctx, d int) settingsComp {
	if s.sub == settingsPaired {
		n := 0
		if s.pairable() {
			n = len(s.paired)
		}
		s.pairedCursor, s.pairedScroll = listMove(s.pairedLines(c), s.pairedCursor, s.pairedScroll, d, n, bodyAvail(c))
		return s
	}
	s.cursor, s.scroll = listMove(s.lockLines(c), s.cursor, s.scroll, d, len(s.lock.Devices), bodyAvail(c))
	return s
}

func (s settingsComp) settle(c *ctx) settingsComp {
	s.scroll = clampTop(s.scroll, len(s.lockLines(c).lines), bodyAvail(c))
	return s
}

func (s settingsComp) settlePaired(c *ctx) settingsComp {
	s.pairedScroll = clampTop(s.pairedScroll, len(s.pairedLines(c).lines), bodyAvail(c))
	return s
}

// listMove moves the cursor over the n items of l by d and keeps a moved
// cursor's row in view. A move up past the first item scrolls the lines above
// it, and a move down first scrolls to a cursor row below the window, so text
// above the list stays reachable.
func listMove(l itemLines, cursor, scroll, d, n, avail int) (int, int) {
	if n == 0 {
		return cursor, clampTop(scroll+d, len(l.lines), avail)
	}
	if sp := l.span(cursor); d > 0 && sp.bottom > scroll+avail {
		step := min(d, sp.bottom-avail-scroll)
		scroll, d = scroll+step, d-step
	}
	next := cursorBy(cursor, d, n)
	if next != cursor {
		sp := l.span(next)
		scroll = windowScroll(len(l.lines), sp.top, sp.bottom, avail, scroll)
	}
	if rest := d - (next - cursor); rest < 0 {
		scroll += rest
	}
	return next, clampTop(scroll, len(l.lines), avail)
}

func (s settingsComp) view(c *ctx, _, _ int) string {
	bodyW := lockWidth(c)
	var body []string
	var top int
	avail := bodyAvail(c)
	list := func(l itemLines, scroll int) {
		body, top = l.lines, clampTop(scroll, len(l.lines), avail)
		l.hitVisible(c.below(2), top, min(len(body), top+avail))
	}
	switch {
	case s.sub == settingsPaired:
		list(s.pairedLines(c), s.pairedScroll)
	case s.text != textNone:
		body = s.textLines(c)
		top = clampTop(s.textScroll, len(body), avail)
	default:
		list(s.lockLines(c), s.scroll)
	}
	c.hitTabs(screenMargin, 0, 3, settingsSubLabels...)
	gutter := strings.Repeat(" ", screenMargin)
	lines := append([]string{c.m.tabLine(settingsSubLabels, int(s.sub)), ""}, body[top:min(len(body), top+avail)]...)
	for i, ln := range lines {
		lines[i] = gutter + truncateLine(ln, bodyW)
	}
	return strings.Join(lines, "\n")
}

func (s settingsComp) lockLines(c *ctx) itemLines {
	var l itemLines
	if !s.lockLoaded {
		l.text(dimStyle.Render("loading"))
		return l
	}
	gap := func() {
		if len(l.lines) > 0 {
			l.text("")
		}
	}
	w := lockWidth(c)
	text := func(t string) { l.text(xansi.Wrap(t, w, "")) }
	s.errLines(c, text)
	sum := s.lock.Summary
	row := func(label string, t Tone, text string) { l.text(statusRow(label, t, text, w)) }
	key := func(b binding) string { return c.m.keyTextOn(s.section(), b) }
	row("Locked", sum.lockTone(), sum.lockText(key))
	if s.lock.NodeRunning {
		row("This node", sum.Node.Tone(), sum.nodeText(s.lock.Status.IdentityPubKey))
	}
	row("This TUI", sum.tuiTone(), sum.tuiText(key))
	if !s.lock.NodeRunning {
		return l
	}
	if sum.Devices != DevicesHidden {
		row("Devices", sum.Devices.Tone(), sum.devicesText(s.lock.RosterErr))
	}
	if len(s.lock.Devices) > 0 {
		gap()
		l.text(headerStyle.Render("Devices"))
	}
	for i, d := range s.lock.Devices {
		l.add(i, s.deviceRow(c, i, d))
	}
	return l
}

const statusLabelCols = 11

func toneColor(t Tone) color.Color {
	switch t {
	case ToneOK:
		return ColorContextOk
	case ToneWarn:
		return ColorContextWarn
	case ToneBad:
		return ColorError
	}
	return ColorTextDim
}

func statusRow(label string, t Tone, text string, w int) string {
	const dotCols = 2
	dot := StyledIcon{"●", toneColor(t)}.Render() + " "
	head := dot + lipgloss.NewStyle().Bold(true).Render(label) + strings.Repeat(" ", max(1, statusLabelCols-xansi.StringWidth(label)))
	style := dimStyle
	if t == ToneBad {
		style = StyleErrorBold
	}
	indent := strings.Repeat(" ", dotCols+statusLabelCols)
	var out []string
	for i, ln := range strings.Split(xansi.Wrap(text, max(1, w-dotCols-statusLabelCols), ""), "\n") {
		if i == 0 {
			ln = head + style.Render(ln)
		} else {
			ln = indent + style.Render(ln)
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

func (s settingsComp) pairedLines(c *ctx) itemLines {
	var l itemLines
	w := lockWidth(c)
	if s.pairedActErr != nil {
		l.text(xansi.Wrap(StyleErrorBold.Render(s.pairedActErr.Error()), w, ""))
		l.text("")
	}
	if c.m.gatewayURL == "" {
		l.text(statusRow("Gateway", ToneDim, "no gateway", w))
		return l
	}
	l.text(statusRow("Gateway", ToneOK, c.m.gatewayURL, w))
	state := s.pairing()
	key := func(b binding) string { return c.m.keyTextOn(s.section(), b) }
	l.text(statusRow("Pairing", state.Tone(), state.text(s.pairedErr, key), w))
	if state != pairingAvailable {
		return l
	}
	if len(s.paired) == 0 {
		l.text(statusRow("Devices", ToneDim, "none", w))
		return l
	}
	l.text(statusRow("Devices", ToneOK, fmt.Sprintf("%d paired", len(s.paired)), w))
	l.text("")
	l.text(headerStyle.Render("Paired devices"))
	for i, d := range s.paired {
		l.add(i, rowMarker(i == s.pairedCursor, c.m.mainFocused())+pairedRow(d))
	}
	return l
}

const pairedTokenCols = 13

func pairedRow(d api.ClientTokenInfo) string {
	tok := clienttoken.Short(d.Token)
	row := Icon.Phone.Render() + " " + tok + strings.Repeat(" ", max(0, pairedTokenCols-xansi.StringWidth(tok)))
	created := d.CreatedAt
	age := ""
	if t, err := time.Parse(time.RFC3339, d.CreatedAt); err == nil {
		created = t.Local().Format("2006-01-02")
		if age = relTime(d.CreatedAt); age == "now" {
			age = "just now"
		} else {
			age += " ago"
		}
	}
	row += dimStyle.Render("  " + created)
	if age != "" {
		row += dimStyle.Render("  " + age)
	}
	return row
}

func (s settingsComp) errLines(c *ctx, text func(string)) {
	reconnectErr := c.m.reconnectErr
	if c.m.rebuilding {
		text(dimStyle.Render("reconnecting…"))
		reconnectErr = nil
	}
	for _, err := range []error{reconnectErr, s.actionErr, s.lockErr} {
		if err != nil {
			text(StyleErrorBold.Render(err.Error()))
		}
	}
	if s.actionOut != "" {
		text(s.actionOut)
	}
}

func lockWidth(c *ctx) int {
	w, _ := c.m.mainSize()
	return max(1, w-screenMargin)
}

func (s settingsComp) deviceRow(c *ctx, i int, d LockDevice) string {
	mark := Icon.Signed.Render()
	if !d.Authorized {
		mark = Icon.Unsigned.Render()
	}
	var tags []string
	if d.ThisNode {
		tags = append(tags, "this node")
		if s.lock.Status.SignerTrusted {
			tags = append(tags, "signer")
		}
	}
	if bytes.Equal(d.Key, s.lock.ClientIdentity) && len(d.Key) > 0 {
		tags = append(tags, "this tui")
	}
	if !d.Authorized {
		tags = append(tags, "not signed")
	}
	row := rowMarker(i == s.cursor, c.m.mainFocused()) + mark + " " + d.name()
	if len(tags) > 0 {
		row += dimStyle.Render("  " + strings.Join(tags, " · "))
	}
	return row
}

// updateSettings gives msg to the settings screen wherever it is in the main
// stack, as a component opened over it must not drop its replies. A pin
// action's rebuild starts even when the screen is gone.
func (m model) updateSettings(msg tea.Msg) (model, tea.Cmd) {
	var reconnect tea.Cmd
	if a, ok := msg.(settingsActionMsg); ok && a.rebuild {
		reconnect = m.startRebuild()
	}
	i := m.settingsAt()
	if i < 0 {
		return m, reconnect
	}
	c := &ctx{m: &m}
	comp, cmd := m.main[i].(settingsComp).update(c, msg)
	m.main = m.main.replaceAt(i, comp)
	return m, tea.Batch(reconnect, cmd, m.apply(c))
}

// settingsOnTop gates the popups a reply opens: over another component they
// would take keys meant for it.
func settingsOnTop(c *ctx) bool {
	_, ok := c.m.main.top().(settingsComp)
	return ok
}

func (m model) settingsAt() int {
	for i := len(m.main) - 1; i >= 0; i-- {
		if _, ok := m.main[i].(settingsComp); ok {
			return i
		}
	}
	return -1
}

var errNoSwappableClient = errors.New("no swappable client")

// settingsReconnected swaps in the rebuilt client even when the settings
// screen is gone: the pin is already written, so the connection must follow
// it. The error stays on the model, so a reopened screen shows it and can
// retry.
func (m model) settingsReconnected(msg settingsReconnectedMsg) (model, tea.Cmd) {
	var swap tea.Cmd
	sw, swappable := m.client.(*swapClient)
	next := msg.client
	switch {
	case msg.err != nil:
	case !swappable:
		msg.err = errNoSwappableClient
		swap = func() tea.Msg { _ = next.Close(); return nil }
	default:
		// swap closes the old client on the calling goroutine, which can block.
		swap = func() tea.Msg { sw.swap(next); return nil }
	}
	m.rebuilding = false
	if m.rebuildQueued {
		m.rebuildQueued = false
		swap = then(swap, m.startRebuild())
	}
	m.reconnectErr = nil
	if msg.err != nil {
		m.reconnectErr = fmt.Errorf("reconnect failed: %w", msg.err)
		if m.settingsAt() < 0 {
			m.flash = m.reconnectErr.Error()
		}
	}
	m, cmd := m.updateSettings(msg)
	return m, tea.Batch(swap, cmd)
}

// then runs b after a ends, dropping a's message.
func then(a, b tea.Cmd) tea.Cmd {
	if a == nil || b == nil {
		return tea.Batch(a, b)
	}
	return func() tea.Msg {
		a()
		return b()
	}
}
