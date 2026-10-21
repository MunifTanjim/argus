package tui

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/clienttoken"
)

const testPairURI = "argus://pair?url=wss%3A%2F%2Fh&token=t"

func pairedSettings(t *testing.T, f *fakeSettings) model {
	t.Helper()
	m := settingsModel(f)
	m.gatewayURL = "wss://gw.example.com"
	m, cmd := typeKeysCmd(m, "g,")
	m = runAll(m, cmd)
	return typeKeys(m, "gt")
}

// startPairing presses the pair key and opens the popup, returning the
// popup's commands without running them.
func startPairing(t *testing.T, m model) (model, tea.Cmd) {
	t.Helper()
	m, cmd := typeKeysCmd(m, "a")
	var next []tea.Cmd
	for _, msg := range execCmd(cmd) {
		var c tea.Cmd
		m, c = upd(m, msg)
		next = append(next, c)
	}
	if _, ok := m.popups.front().(pairPopup); !ok {
		t.Fatalf("popup = %T, want pairPopup", m.popups.front())
	}
	return m, tea.Batch(next...)
}

// goCmds runs each command of a batch on its own goroutine, so a blocking
// await or a tick does not hold the test.
func goCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				goCmds(c)
			}
		}
	}()
}

func pairOffer() PairingOffer { return PairingOffer{URI: testPairURI, URL: "wss://h", Token: "t"} }

func TestPairPopupCopiesTheURITokenAndURL(t *testing.T) {
	got := captureClipboard(t)
	m := pairedSettings(t, &fakeSettings{offer: PairingOffer{URI: testPairURI, URL: "wss://h", Token: "tok"}})
	m, _ = startPairing(t, m)
	if out := renderPair(m); !strings.Contains(out, "copy uri") || !strings.Contains(out, "copy token") || !strings.Contains(out, "copy url") {
		t.Fatalf("popup hints lack the copy keys:\n%s", out)
	}
	for _, tc := range []struct{ key, flash string }{
		{"y", "copied the pairing uri"},
		{"t", "copied the token"},
		{"u", "copied the url"},
	} {
		var cmd tea.Cmd
		m, cmd = typeKeysCmd(m, tc.key)
		m = runAll(m, cmd)
		if m.flash != tc.flash {
			t.Errorf("%s: flash = %q, want %q", tc.key, m.flash, tc.flash)
		}
		if _, ok := m.popups.front().(pairPopup); !ok {
			t.Fatalf("%s closed the popup", tc.key)
		}
	}
	if want := []string{testPairURI, "tok", "wss://h"}; !slices.Equal(*got, want) {
		t.Fatalf("clipboard = %q, want %q", *got, want)
	}
}

func renderPair(m model) string {
	p := m.popups.front().(pairPopup)
	return p.render(&ctx{m: &m}, m.mainRect())
}

func TestPairedDevicesListsTokens(t *testing.T) {
	tok := "abcdef0123456789abcdef"
	m := pairedSettings(t, &fakeSettings{paired: []api.ClientTokenInfo{{Token: tok, CreatedAt: "2026-10-20"}, {Token: "short"}}})
	out := viewText(m)
	for _, want := range []string{clienttoken.Short(tok), "2026-10-20"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, tok) {
		t.Errorf("view shows the full token:\n%s", out)
	}
}

func TestPairedDevicesRemoveAsksFirst(t *testing.T) {
	tok := "abcdef0123456789abcdef"
	f := &fakeSettings{paired: []api.ClientTokenInfo{{Token: "other-token-0000"}, {Token: tok}}}
	m := pairedSettings(t, f)
	m = typeKeys(m, "j")
	m, cmd := typeKeysCmd(m, "dd")
	m = runChain(m, cmd)
	p, ok := m.popups.front().(confirmPopup)
	if !ok {
		t.Fatalf("popup = %T, want confirmPopup", m.popups.front())
	}
	if p.title != "Remove this device?" {
		t.Fatalf("title = %q", p.title)
	}
	if f.called("RemovePairedDevice") {
		t.Fatal("remove ran before the confirmation")
	}
	f.calls = nil
	m, _ = upd(m, keyMsg("k"))
	m, cmd = upd(m, keyMsg("enter"))
	runChain(m, cmd)
	if !reflect.DeepEqual(f.removed, []string{tok}) {
		t.Fatalf("removed = %v, want the full token", f.removed)
	}
	if !f.called("PairedDevices") {
		t.Fatal("a remove must reload the paired devices")
	}
}

func TestPairedDevicesNeedsTheMasterToken(t *testing.T) {
	f := &fakeSettings{pairErr: errors.New("unauthorized: admin token required")}
	m := pairedSettings(t, f)
	if out := viewText(m); !strings.Contains(out, "needs the gateway's master token") {
		t.Fatalf("view lacks the master token note:\n%s", out)
	}
	f.calls = nil
	m, cmd := typeKeysCmd(m, "a")
	runChain(m, cmd)
	if len(f.calls) != 0 {
		t.Fatalf("calls = %v, want none", f.calls)
	}
}

func TestPairedDevicesShowsTokenManagementDisabled(t *testing.T) {
	m := pairedSettings(t, &fakeSettings{pairErr: errors.New("rpc: client token management not enabled")})
	if out := viewText(m); !strings.Contains(out, "client token management not enabled") {
		t.Fatalf("view lacks the disabled note:\n%s", out)
	}
}

func TestPairedDevicesNeedsAGateway(t *testing.T) {
	f := &fakeSettings{}
	m := openedSettings(t, f)
	m = typeKeys(m, "gt")
	if out := viewText(m); !strings.Contains(out, "no gateway") {
		t.Fatalf("view lacks the gateway note:\n%s", out)
	}
	m, cmd := typeKeysCmd(m, "a")
	runChain(m, cmd)
	if f.called("PairedDevices") || f.called("StartPairing") {
		t.Fatalf("calls = %v, want no pairing calls", f.calls)
	}
}

func TestPairedDevicesShowsAStartError(t *testing.T) {
	f := &fakeSettings{offErr: errors.New("gateway has no public url")}
	m := pairedSettings(t, f)
	m, cmd := typeKeysCmd(m, "a")
	m = runChain(m, cmd)
	if m.popups.front() != nil {
		t.Fatalf("popup %T opened", m.popups.front())
	}
	if out := viewText(m); !strings.Contains(out, "gateway has no public url") {
		t.Fatalf("view lacks the error:\n%s", out)
	}
}

func TestPairedDevicesScrollsToTheCursor(t *testing.T) {
	var list []api.ClientTokenInfo
	for i := range 30 {
		list = append(list, api.ClientTokenInfo{Token: fmt.Sprintf("token-%02d-0000000", i), CreatedAt: fmt.Sprintf("day-%02d", i)})
	}
	f := &fakeSettings{paired: list}
	m := settingsModel(f)
	m.width, m.height = 100, 24
	m.gatewayURL = "wss://gw.example.com"
	m, cmd := typeKeysCmd(m, "g,")
	m = runAll(m, cmd)
	m = typeKeys(m, "gt")
	assertFitsPane(t, m)
	for range 29 {
		m, _ = upd(m, keyMsg("j"))
	}
	if out := viewText(m); !strings.Contains(out, "day-29") || !strings.Contains(out, "▸ ") {
		t.Fatalf("the cursor row is not in view:\n%s", out)
	}
	assertFitsPane(t, m)
}

func TestPairPopupShowsTheQRAndURI(t *testing.T) {
	f := &fakeSettings{offer: pairOffer(), awaitOK: true}
	m := pairedSettings(t, f)
	m, cmd := startPairing(t, m)
	p := m.popups.front().(pairPopup)
	t.Cleanup(p.cancel)
	if p.id() != "pair" {
		t.Fatalf("id = %q", p.id())
	}
	if left := time.Until(p.deadline); left < 50*time.Second || left > 61*time.Second {
		t.Fatalf("deadline in %v, want the pairing window", left)
	}
	out := ansi.Strip(renderPair(m))
	if !strings.Contains(out, testPairURI) {
		t.Fatalf("popup lacks the URI:\n%s", out)
	}
	if !strings.ContainsAny(out, "▀▄") {
		t.Fatalf("popup lacks the QR code:\n%s", out)
	}
	if !strings.Contains(viewText(m), testPairURI) {
		t.Fatal("the screen does not show the popup")
	}
	goCmds(cmd)
	deadline := time.Now().Add(5 * time.Second)
	for !f.called("AwaitPairing") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !reflect.DeepEqual(f.awaited, []string{"t"}) {
		t.Fatalf("awaited = %v, want the offer's token", f.awaited)
	}
}

func TestPairPopupClosesOnConnect(t *testing.T) {
	f := &fakeSettings{offer: pairOffer()}
	m := pairedSettings(t, f)
	m, _ = startPairing(t, m)
	p := m.popups.front().(pairPopup)
	f.calls = nil
	m, cmd := upd(m, pairDoneMsg{seq: p.seq, connected: true})
	m = runAll(m, cmd)
	if m.popups.front() != nil {
		t.Fatalf("popup %T still open", m.popups.front())
	}
	if m.flash != "paired" {
		t.Fatalf("flash = %q", m.flash)
	}
	if !f.called("PairedDevices") {
		t.Fatal("a pairing must reload the paired devices")
	}
}

func TestPairPopupTimesOut(t *testing.T) {
	for _, msg := range []pairDoneMsg{{}, {err: context.DeadlineExceeded}} {
		m := pairedSettings(t, &fakeSettings{offer: pairOffer()})
		m, _ = startPairing(t, m)
		msg.seq = m.popups.front().(pairPopup).seq
		m, _ = upd(m, msg)
		if m.popups.front() != nil {
			t.Fatalf("popup %T still open", m.popups.front())
		}
		if m.flash != "no device connected" {
			t.Fatalf("flash = %q", m.flash)
		}
	}
}

func awaitStarted(t *testing.T, f *fakeSettings) context.Context {
	t.Helper()
	select {
	case ctx := <-f.awaitCtx:
		return ctx
	case <-time.After(5 * time.Second):
		t.Fatal("the await did not start")
		return nil
	}
}

func assertCancelled(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("await context ended with %v, want cancelled", ctx.Err())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the await context was not cancelled")
	}
}

func TestPairPopupEscCancelsTheAwait(t *testing.T) {
	f := &fakeSettings{offer: pairOffer(), awaitCtx: make(chan context.Context, 1)}
	m := pairedSettings(t, f)
	m, cmd := startPairing(t, m)
	seq := m.popups.front().(pairPopup).seq
	goCmds(cmd)
	ctx := awaitStarted(t, f)
	m, _ = upd(m, keyMsg("esc"))
	if m.popups.front() != nil {
		t.Fatalf("popup %T still open", m.popups.front())
	}
	assertCancelled(t, ctx)
	if m.flash != "" {
		t.Fatalf("flash = %q after esc, want none", m.flash)
	}
	f.calls = nil
	m, cmd = upd(m, pairDoneMsg{seq: seq, connected: true})
	m = runAll(m, cmd)
	if m.flash != "" {
		t.Fatalf("flash = %q after a late reply, want none", m.flash)
	}
	if f.called("PairedDevices") {
		t.Fatal("a late reply reloaded the paired devices")
	}
}

func TestPairPopupTickStopsWhenClosed(t *testing.T) {
	m := pairedSettings(t, &fakeSettings{offer: pairOffer()})
	m, _ = startPairing(t, m)
	seq := m.popups.front().(pairPopup).seq
	if _, cmd := upd(m, pairTickMsg{seq: seq}); cmd == nil {
		t.Fatal("an open popup must keep ticking")
	}
	m, _ = upd(m, keyMsg("esc"))
	if _, cmd := upd(m, pairTickMsg{seq: seq}); cmd != nil {
		t.Fatal("a closed popup kept ticking")
	}
}

func TestPairPopupLeavingSettingsCancelsTheAwait(t *testing.T) {
	f := &fakeSettings{offer: pairOffer(), awaitCtx: make(chan context.Context, 1)}
	m := pairedSettings(t, f)
	m, cmd := startPairing(t, m)
	goCmds(cmd)
	awaitCtx := awaitStarted(t, f)
	c := &ctx{m: &m}
	c.replaceBase(m.homePane())
	m.apply(c)
	if _, ok := m.main.top().(homeComp); !ok {
		t.Fatalf("top = %T, want homeComp", m.main.top())
	}
	if m.popups.front() != nil {
		t.Fatalf("popup %T still open after leaving", m.popups.front())
	}
	assertCancelled(t, awaitCtx)
}

func TestPairPopupTooSmallShowsTheURIOnly(t *testing.T) {
	m := pairedSettings(t, &fakeSettings{offer: pairOffer()})
	m.width, m.height = 40, 12
	m, _ = startPairing(t, m)
	t.Cleanup(m.popups.front().(pairPopup).cancel)
	area := m.mainRect()
	view := renderPair(m)
	out := ansi.Strip(view)
	if strings.ContainsAny(out, "▀▄█") {
		t.Fatalf("popup shows the QR code in a small area:\n%s", out)
	}
	var joined strings.Builder
	for _, ln := range strings.Split(out, "\n") {
		joined.WriteString(strings.TrimSpace(strings.Trim(ln, "│")))
	}
	if !strings.Contains(joined.String(), testPairURI) {
		t.Fatalf("popup lacks the URI:\n%s", out)
	}
	if !regexp.MustCompile(`\d+s`).MatchString(out) {
		t.Fatalf("popup lacks the seconds left:\n%s", out)
	}
	w, h := lipgloss.Size(view)
	if w > area.Dx() || h > area.Dy() {
		t.Fatalf("popup is %dx%d, larger than the %dx%d area:\n%s", w, h, area.Dx(), area.Dy(), out)
	}
	for _, ln := range strings.Split(view, "\n") {
		if lipgloss.Width(ln) > w {
			t.Fatalf("line wider than the popup: %q", ansi.Strip(ln))
		}
	}
}

var realPairURI = "argus://pair?url=wss%3A%2F%2Fargus-gateway.example.com%3A8443&token=" + strings.Repeat("0123456789abcdef", 4)

func TestPairPopupShowsTheQRForARealURIAt120x32(t *testing.T) {
	m := pairedSettings(t, &fakeSettings{offer: PairingOffer{URI: realPairURI, Token: "t"}})
	m.width, m.height = 120, 32
	m, _ = startPairing(t, m)
	t.Cleanup(m.popups.front().(pairPopup).cancel)
	view := renderPair(m)
	out := ansi.Strip(view)
	if !strings.ContainsAny(out, "▀▄") {
		t.Fatalf("popup lacks the QR code at 120x32:\n%s", out)
	}
	if !regexp.MustCompile(`expires in \d+s`).MatchString(out) {
		t.Fatalf("popup lacks the seconds left:\n%s", out)
	}
	area := m.mainRect()
	if w, h := lipgloss.Size(view); w > area.Dx() || h > area.Dy() {
		t.Fatalf("popup is %dx%d, larger than the %dx%d area:\n%s", w, h, area.Dx(), area.Dy(), out)
	}
}

func TestPairPopupKeepsTheSecondsWithARealURIWhenSmall(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {50, 12}, {60, 11}} {
		m := pairedSettings(t, &fakeSettings{offer: PairingOffer{URI: realPairURI, Token: "t"}})
		m.width, m.height = size[0], size[1]
		m, _ = startPairing(t, m)
		p := m.popups.front().(pairPopup)
		out := ansi.Strip(renderPair(m))
		p.cancel()
		if !regexp.MustCompile(`expires in \d+s`).MatchString(out) {
			t.Errorf("%dx%d: popup lacks the seconds left:\n%s", size[0], size[1], out)
		}
	}
}

func TestSettingsPairedRows(t *testing.T) {
	m := openedSettings(t, &fakeSettings{})
	m = typeKeys(m, "gt")
	if got := statusRowLine(t, m, "Gateway"); !strings.Contains(got, "no gateway") {
		t.Fatalf("gateway row = %q", got)
	}
	out := viewText(m)
	for _, hidden := range []string{"● Pairing", "● Devices"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("no-gateway view shows %q:\n%s", hidden, out)
		}
	}

	errTests := []struct {
		err  error
		want string
	}{
		{errors.New("unauthorized: admin token required"), "needs the gateway's master token"},
		{errors.New("rpc: client token management not enabled"), "not enabled"},
		{errors.New("boom exploded"), "boom exploded"},
	}
	for _, tc := range errTests {
		m := pairedSettings(t, &fakeSettings{pairErr: tc.err})
		if got := statusRowLine(t, m, "Pairing"); !strings.Contains(got, tc.want) {
			t.Errorf("pairing row for %v = %q, want %q", tc.err, got, tc.want)
		}
		if strings.Contains(viewText(m), "● Devices") {
			t.Errorf("devices row shown for %v", tc.err)
		}
	}

	list := []api.ClientTokenInfo{{Token: "aaaaaaaaaaaa1"}, {Token: "bbbbbbbbbbbb2"}, {Token: "cccccccccccc3"}}
	m = pairedSettings(t, &fakeSettings{paired: list})
	if got := statusRowLine(t, m, "Gateway"); !strings.Contains(got, "wss://gw.example.com") {
		t.Errorf("gateway row = %q", got)
	}
	if got := statusRowLine(t, m, "Pairing"); !strings.Contains(got, "available") {
		t.Errorf("pairing row = %q", got)
	}
	if got := statusRowLine(t, m, "Devices"); !strings.Contains(got, "3 paired") {
		t.Errorf("devices row = %q", got)
	}
	m = pairedSettings(t, &fakeSettings{})
	if got := statusRowLine(t, m, "Devices"); !strings.Contains(got, "none") {
		t.Errorf("empty devices row = %q", got)
	}
}

func TestSettingsPairedListColumns(t *testing.T) {
	now := time.Now()
	list := []api.ClientTokenInfo{
		{Token: "aaaaaaaaaaaa1", CreatedAt: now.Add(-3 * 24 * time.Hour).Format(time.RFC3339)},
		{Token: "bbbbbbbbbbbbbbbbbbbbbbbb2", CreatedAt: now.Add(-2 * time.Hour).Format(time.RFC3339)},
	}
	m := pairedSettings(t, &fakeSettings{paired: list})
	dateRE := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	var cols []int
	for _, ln := range strings.Split(viewText(m), "\n") {
		if !strings.Contains(ln, clienttoken.Short(list[0].Token)) && !strings.Contains(ln, clienttoken.Short(list[1].Token)) {
			continue
		}
		loc := dateRE.FindStringIndex(ln)
		if loc == nil || !strings.Contains(ln, " ago") {
			t.Fatalf("row lacks the date or age: %q", ln)
		}
		cols = append(cols, ansi.StringWidth(ln[:loc[0]]))
	}
	if len(cols) != 2 || cols[0] != cols[1] {
		t.Fatalf("date columns = %v, want 2 equal", cols)
	}
}

func TestSettingsPairedUnparseableDate(t *testing.T) {
	m := pairedSettings(t, &fakeSettings{paired: []api.ClientTokenInfo{{Token: "aaaaaaaaaaaa1", CreatedAt: "yesterday"}}})
	out := viewText(m)
	if !strings.Contains(out, "yesterday") || strings.Contains(out, " ago") {
		t.Fatalf("unparseable date row wrong:\n%s", out)
	}
}

func TestSettingsPairedJustNow(t *testing.T) {
	at := time.Now().Add(-5 * time.Second).Format(time.RFC3339)
	m := pairedSettings(t, &fakeSettings{paired: []api.ClientTokenInfo{{Token: "aaaaaaaaaaaa1", CreatedAt: at}}})
	out := viewText(m)
	if !strings.Contains(out, "just now") || strings.Contains(out, "now ago") {
		t.Fatalf("recent pairing row wrong:\n%s", out)
	}
}
