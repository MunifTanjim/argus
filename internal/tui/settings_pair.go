package tui

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/mdp/qrterminal/v3"

	"github.com/MunifTanjim/argus/internal/clienttoken"
)

// pairPopup shows a pairing offer while AwaitPairing waits for the app to
// connect. cancel ends that wait.
type pairPopup struct {
	seq      int
	offer    PairingOffer
	qr       string
	deadline time.Time
	cancel   context.CancelFunc
}

type pairTickMsg struct{ seq int }

type pairDoneMsg struct {
	seq       int
	connected bool
	err       error
}

func openPair(c *ctx, offer PairingOffer) tea.Cmd {
	seq := c.m.nextSettingsSeq()
	var buf bytes.Buffer
	qrterminal.GenerateWithConfig(offer.URI, qrterminal.Config{
		Level:          qrterminal.L,
		Writer:         &buf,
		HalfBlocks:     true,
		BlackChar:      qrterminal.BLACK_BLACK,
		WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar:      qrterminal.WHITE_WHITE,
		BlackWhiteChar: qrterminal.BLACK_WHITE,
		QuietZone:      1,
	})
	deadline := time.Now().Add(clienttoken.PendTTL)
	actx, cancel := context.WithDeadline(context.Background(), deadline)
	c.openPopup(pairPopup{seq: seq, offer: offer, qr: strings.TrimRight(buf.String(), "\n"), deadline: deadline, cancel: cancel})
	st, tok := c.m.settings, offer.Token
	return tea.Batch(func() tea.Msg {
		ok, err := st.AwaitPairing(actx, tok)
		return pairDoneMsg{seq: seq, connected: ok, err: err}
	}, pairTick(seq))
}

func pairTick(seq int) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return pairTickMsg{seq: seq} })
}

// dropPair takes the pair popups that match off the stack and cancels their
// waits. It edits the stack in place, as a component's close runs while the
// actions apply.
func dropPair(m *model, match func(pairPopup) bool) (pairPopup, bool) {
	var found pairPopup
	var ok bool
	m.popups = slices.DeleteFunc(slices.Clone(m.popups), func(p popup) bool {
		pp, is := p.(pairPopup)
		if !is || !match(pp) {
			return false
		}
		pp.cancel()
		found, ok = pp, true
		return true
	})
	return found, ok
}

// updatePairTick re-arms the countdown only while its popup is open.
func (m model) updatePairTick(msg pairTickMsg) (model, tea.Cmd) {
	for _, p := range m.popups {
		if pp, ok := p.(pairPopup); ok && pp.seq == msg.seq {
			return m, pairTick(msg.seq)
		}
	}
	return m, nil
}

func (p pairPopup) id() string                            { return "pair" }
func (p pairPopup) keySection() string                    { return "" }
func (p pairPopup) spins(*ctx) bool                       { return false }
func (p pairPopup) update(*ctx, tea.Msg) (popup, tea.Cmd) { return p, nil }

func (p pairPopup) handleKey(c *ctx, msg tea.KeyPressMsg) (popup, tea.Cmd) {
	switch msg.String() {
	case "esc":
		p.cancel()
		c.closePopup()
	case "y":
		return p, copyText(c, p.offer.URI, "copied the pairing uri")
	case "t":
		return p, copyText(c, p.offer.Token, "copied the token")
	case "u":
		return p, copyText(c, p.offer.URL, "copied the url")
	}
	return p, nil
}

func (p pairPopup) draw(c *ctx, scr uv.Screen, _ uv.Rectangle) {
	area := c.m.mainRect()
	c.hitRect(drawCenter(scr, area, p.render(c, area)))
}

// render draws the QR code above the URI when both fit area, the QR code
// alone in a tight frame when only it fits, and the URI alone otherwise. The
// seconds left come before the URI, so a cut takes the end of the URI instead.
func (p pairPopup) render(c *ctx, area uv.Rectangle) string {
	left := max(0, time.Until(p.deadline).Round(time.Second)/time.Second)
	f := popupFrame{title: "Pair a device", width: min(area.Dx(), maxCardWidth)}
	iw := f.innerWidth()
	text := append([]string{dimStyle.Render(fmt.Sprintf("expires in %ds", left))},
		wrapLine(p.offer.URI, iw)...)
	f.help = c.m.hints(iw, hint("y", "copy uri"), hint("t", "copy token"), hint("u", "copy url"), hint("esc", "cancel"))
	chrome, tightChrome := f.chrome()
	qrW, qrH := lipgloss.Size(p.qr)
	qr := lipgloss.PlaceHorizontal(iw, lipgloss.Center, p.qr)
	switch {
	case qrW <= iw && chrome+qrH+1+len(text) <= area.Dy():
		f.parts = append([]string{qr, ""}, text...)
	case qrW <= iw && tightChrome+qrH+1 <= area.Dy():
		f.tight = true
		f.parts = []string{qr, text[0]}
	default:
		f.parts = text
		f.height = min(area.Dy(), chrome+len(text))
	}
	return f.render()
}
