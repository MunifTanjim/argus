package tui

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/keyfmt"
)

type fakeSettings struct {
	lock    LockView
	lockErr error
	log     string
	logErr  error
	signOut string
	signErr error
	revoke  string
	genesis PinOffer
	genErr  error
	pinOut  string
	pinErr  error
	unpin   string
	// pinKept makes Pin and Unpin report the client pin unchanged.
	pinKept  bool
	unpinErr error
	client   Client
	recErr   error
	paired   []api.ClientTokenInfo
	pairErr  error
	offer    PairingOffer
	offErr   error
	awaitOK  bool
	waitErr  error
	// awaitCtx, when set, makes AwaitPairing send its context and block
	// until the context ends.
	awaitCtx chan context.Context

	mu      sync.Mutex
	calls   []string
	removed []string
	awaited []string
	signed  [][]byte
	revoked [][]byte
	pinned  [][]byte
}

func (f *fakeSettings) LockStatus(context.Context) (LockView, error) {
	f.record("LockStatus")
	return f.lock, f.lockErr
}

func (f *fakeSettings) LockLog(context.Context) (string, error) {
	f.record("LockLog")
	return f.log, f.logErr
}

func (f *fakeSettings) SignDevice(_ context.Context, device []byte) (string, error) {
	f.record("SignDevice")
	f.signed = append(f.signed, device)
	return f.signOut, f.signErr
}

func (f *fakeSettings) RevokeDevice(_ context.Context, device []byte) (string, error) {
	f.record("RevokeDevice")
	f.revoked = append(f.revoked, device)
	return f.revoke, nil
}

func (f *fakeSettings) NetworkGenesis(context.Context) (PinOffer, error) {
	f.record("NetworkGenesis")
	return f.genesis, f.genErr
}

func (f *fakeSettings) Pin(_ context.Context, genesis []byte) (PinResult, error) {
	f.record("Pin")
	f.pinned = append(f.pinned, genesis)
	return PinResult{Text: f.pinOut, ClientPinChanged: !f.pinKept}, f.pinErr
}

func (f *fakeSettings) Unpin(context.Context) (PinResult, error) {
	f.record("Unpin")
	return PinResult{Text: f.unpin, ClientPinChanged: !f.pinKept}, f.unpinErr
}

func (f *fakeSettings) Reconnect() (Client, error) {
	f.record("Reconnect")
	return f.client, f.recErr
}

func (f *fakeSettings) PairedDevices(context.Context) ([]api.ClientTokenInfo, error) {
	f.record("PairedDevices")
	return f.paired, f.pairErr
}

func (f *fakeSettings) RemovePairedDevice(_ context.Context, token string) error {
	f.record("RemovePairedDevice")
	f.mu.Lock()
	f.removed = append(f.removed, token)
	f.mu.Unlock()
	return nil
}

func (f *fakeSettings) StartPairing(context.Context) (PairingOffer, error) {
	f.record("StartPairing")
	return f.offer, f.offErr
}

func (f *fakeSettings) AwaitPairing(ctx context.Context, token string) (bool, error) {
	f.record("AwaitPairing")
	f.mu.Lock()
	f.awaited = append(f.awaited, token)
	f.mu.Unlock()
	if f.awaitCtx != nil {
		f.awaitCtx <- ctx
		<-ctx.Done()
		return false, ctx.Err()
	}
	return f.awaitOK, f.waitErr
}

func (f *fakeSettings) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeSettings) called(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

func settingsModel(f *fakeSettings) model {
	m := projectsTestModel()
	m.width, m.height = 120, 40
	m.settings = f
	return m
}

// runChain runs cmd and the commands that its messages return, so an action's
// reload runs too.
func runChain(m model, cmd tea.Cmd) model {
	for range 5 {
		var next []tea.Cmd
		for _, msg := range execCmd(cmd) {
			var c tea.Cmd
			m, c = upd(m, msg)
			next = append(next, c)
		}
		cmd = tea.Batch(next...)
	}
	return m
}

func openedSettings(t *testing.T, f *fakeSettings) model {
	t.Helper()
	m, cmd := typeKeysCmd(settingsModel(f), "g,")
	m = runAll(m, cmd)
	if _, ok := m.main.top().(settingsComp); !ok {
		t.Fatalf("top = %T, want settingsComp", m.main.top())
	}
	return m
}

func TestSettingsOpensFromItsKeyAndCommand(t *testing.T) {
	f := &fakeSettings{}
	openedSettings(t, f)
	if !f.called("LockStatus") {
		t.Fatal("opening settings must load the lock status")
	}

	f = &fakeSettings{}
	m, cmd := upd(settingsModel(f), cmdMsg("settings"))
	m = runAll(m, cmd)
	if _, ok := m.main.top().(settingsComp); !ok {
		t.Fatalf("top = %T, want settingsComp", m.main.top())
	}
	if m.focused != mainPane {
		t.Fatalf("focus = %v, want the main pane", m.focused)
	}
	if !f.called("LockStatus") {
		t.Fatal("the settings command must load the lock status")
	}
}

func TestSettingsListsDevicesWithLabelsAndUnsigned(t *testing.T) {
	m := openedSettings(t, &fakeSettings{lock: LockView{
		NodeRunning: true,
		Devices: []LockDevice{
			{Key: []byte{1}, Label: "home", Authorized: true, ThisNode: true},
			{Key: []byte{2}, Label: "work"},
		},
	}})
	var home, work string
	for _, line := range strings.Split(viewText(m), "\n") {
		switch {
		case strings.Contains(line, "home"):
			home = line
		case strings.Contains(line, "work"):
			work = line
		}
	}
	if home == "" || work == "" {
		t.Fatalf("missing device rows:\n%s", viewText(m))
	}
	if strings.Contains(home, "not signed") {
		t.Errorf("home row marked unsigned: %q", home)
	}
	if !strings.Contains(work, "not signed") {
		t.Errorf("work row not marked unsigned: %q", work)
	}
}

func TestSettingsShowsTheRosterErrorAndKeysWithoutLabels(t *testing.T) {
	k := []byte{0xab, 0xcd}
	m := openedSettings(t, &fakeSettings{lock: LockView{
		NodeRunning: true,
		Devices:     []LockDevice{{Key: k, Authorized: true}},
		Summary:     LockSummary{Devices: DevicesLabelsFailed, DeviceCount: 1},
		RosterErr:   "nodes.list: dial: refused",
	}})
	out := viewText(m)
	for _, want := range []string{keyfmt.DeviceKey.Encode(k), "nodes.list: dial: refused"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
}

func TestSettingsMovesTheCursorAndSwitchesSections(t *testing.T) {
	m := openedSettings(t, &fakeSettings{lock: LockView{
		NodeRunning: true,
		Devices:     []LockDevice{{Key: []byte{1}, Label: "a"}, {Key: []byte{2}, Label: "b"}},
	}})
	m = typeKeys(m, "j")
	if s := m.main.top().(settingsComp); s.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", s.cursor)
	}
	m = typeKeys(m, "k")
	if s := m.main.top().(settingsComp); s.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", s.cursor)
	}
	m = typeKeys(m, "gt")
	if s := m.main.top().(settingsComp); s.sub != settingsPaired {
		t.Fatalf("sub = %v, want paired devices", s.sub)
	}
	m = typeKeys(m, "gT")
	if s := m.main.top().(settingsComp); s.sub != settingsLock {
		t.Fatalf("sub = %v, want lock", s.sub)
	}
}

func TestSettingsRefreshReloadsTheLockStatus(t *testing.T) {
	f := &fakeSettings{}
	m := openedSettings(t, f)
	f.calls = nil
	f.lock = LockView{NodeRunning: true, Summary: LockSummary{Lock: LockNodeError, NodeErr: "locked mode: reloaded"}}
	m, cmd := typeKeysCmd(m, "gr")
	m = runAll(m, cmd)
	if !f.called("LockStatus") {
		t.Fatal("refresh must reload the lock status")
	}
	if out := viewText(m); !strings.Contains(out, "locked mode: reloaded") {
		t.Fatalf("view lacks the reloaded status:\n%s", out)
	}
}

func TestSettingsDropsALateReplyAfterLeaving(t *testing.T) {
	m := openedSettings(t, &fakeSettings{})
	m, _ = upd(m, keyMsg("esc"))
	home, ok := m.main.top().(homeComp)
	if !ok {
		t.Fatalf("top = %T after esc, want homeComp", m.main.top())
	}
	before := viewText(m)
	m, cmd := upd(m, settingsLockMsg{seq: m.settingsSeq, view: LockView{StatusText: "late"}})
	if cmd != nil {
		t.Fatal("a late reply must not run a command")
	}
	if got, ok := m.main.top().(homeComp); !ok || !reflect.DeepEqual(got, home) {
		t.Fatalf("top = %#v after a late reply, want %#v", m.main.top(), home)
	}
	if after := viewText(m); after != before {
		t.Fatalf("a late reply changed the view:\n%s\nwant:\n%s", after, before)
	}
}

func TestSettingsUnavailableWithoutABackend(t *testing.T) {
	m := projectsTestModel()
	m = typeKeys(m, "g,")
	if m.flash != "settings unavailable" {
		t.Fatalf("flash = %q", m.flash)
	}
	if _, ok := m.main.top().(settingsComp); ok {
		t.Fatal("settings opened without a backend")
	}
}

func longLockView(devices int) LockView {
	v := LockView{
		NodeRunning: true,
		Summary: LockSummary{
			Lock:        LockNodeError,
			NodeErr:     "locked mode: enforcing" + strings.Repeat(" "+strings.Repeat("x", 80), 12),
			TUI:         TUIUnpinned,
			Devices:     DevicesListed,
			DeviceCount: devices,
		},
		RosterErr: "nodes.list: dial: refused",
	}
	for i := range devices {
		v.Devices = append(v.Devices, LockDevice{Key: []byte{byte(i)}, Label: fmt.Sprintf("device-%d", i), Authorized: true})
	}
	return v
}

func shortSettings(t *testing.T, v LockView) model {
	t.Helper()
	m := settingsModel(&fakeSettings{lock: v})
	m.width, m.height = 100, 24
	m, cmd := typeKeysCmd(m, "g,")
	return runAll(m, cmd)
}

func assertFitsPane(t *testing.T, m model) {
	t.Helper()
	w, h := m.mainSize()
	if n := strings.Count(m.main.top().view(&ctx{m: &m}, w, h), "\n") + 1; n > h-footerRows {
		t.Fatalf("pane renders %d lines, more than the %d rows it has", n, h-footerRows)
	}
}

func TestSettingsScrollsToTheCursorDevice(t *testing.T) {
	m := shortSettings(t, longLockView(5))
	if out := viewText(m); strings.Contains(out, "device-4") {
		t.Fatalf("the last device shows before scrolling; the test needs a taller body:\n%s", out)
	}
	if out := viewText(m); !strings.Contains(out, "locked mode: enforcing") {
		t.Fatalf("the first line is not in view before scrolling:\n%s", out)
	}
	assertFitsPane(t, m)
	for range 10 {
		m = typeKeys(m, "j")
	}
	out := viewText(m)
	if !strings.Contains(out, "▸ ✓ device-4") {
		t.Fatalf("the cursor device is not in view:\n%s", out)
	}
	assertFitsPane(t, m)
	m = typeKeys(m, "kkkk")
	if out := viewText(m); !strings.Contains(out, "▸ ✓ device-0") {
		t.Fatalf("the first device is not in view after moving back:\n%s", out)
	}
	assertFitsPane(t, m)
}

func TestSettingsScrollsBackAboveTheDevices(t *testing.T) {
	m := shortSettings(t, longLockView(5))
	m = typeKeys(m, "G")
	if strings.Contains(viewText(m), "locked mode: enforcing") {
		t.Fatal("the first line shows at the last device; the test needs a taller body")
	}
	m = typeKeys(m, "gg")
	if out := viewText(m); !strings.Contains(out, "locked mode: enforcing") {
		t.Fatalf("gg does not reach the first line:\n%s", out)
	}

	m = typeKeys(m, "G")
	for range 30 {
		m = typeKeys(m, "k")
	}
	if s := m.main.top().(settingsComp); s.scroll != 0 || s.cursor != 0 {
		t.Fatalf("scroll = %d, cursor = %d after k past the first device; want both 0", s.scroll, s.cursor)
	}
	m = typeKeys(m, "j")
	if s := m.main.top().(settingsComp); s.scroll != 1 || s.cursor != 0 {
		t.Fatalf("scroll = %d, cursor = %d after j with the first device out of view; want the text scrolled by 1", s.scroll, s.cursor)
	}
	for range 30 {
		m = typeKeys(m, "j")
	}
	if out := viewText(m); !strings.Contains(out, "▸ ✓ device-4") {
		t.Fatalf("j does not reach the last device:\n%s", out)
	}
	assertFitsPane(t, m)
}

func TestSettingsScrollsTheTextWithoutDevices(t *testing.T) {
	v := longLockView(0)
	v.Summary.NodeErr += strings.Repeat(" "+strings.Repeat("x", 80), 10)
	m := shortSettings(t, v)
	if strings.Contains(viewText(m), "not pinned") {
		t.Fatal("the last line shows before scrolling; the test needs a taller body")
	}
	m = typeKeys(m, "j")
	if s := m.main.top().(settingsComp); s.scroll != 1 {
		t.Fatalf("scroll = %d after j, want 1", s.scroll)
	}
	m = typeKeys(m, "k")
	if s := m.main.top().(settingsComp); s.scroll != 0 {
		t.Fatalf("scroll = %d after k, want 0", s.scroll)
	}
	for range 10 {
		m, _ = upd(m, keyMsg("ctrl+d"))
	}
	if out := viewText(m); !strings.Contains(out, "not pinned") {
		t.Fatalf("the last line is not reachable by scrolling:\n%s", out)
	}
	assertFitsPane(t, m)
	m, _ = upd(m, keyMsg("ctrl+u"))
	m, _ = upd(m, keyMsg("ctrl+u"))
	if s := m.main.top().(settingsComp); s.scroll >= 10 {
		t.Fatalf("scroll = %d after half-page up, want it lower", s.scroll)
	}
}

func TestSettingsAppliesOnlyTheLatestLockReply(t *testing.T) {
	m := openedSettings(t, &fakeSettings{})
	m, first := typeKeysCmd(m, "gr")
	m, second := typeKeysCmd(m, "gr")
	f := m.settings.(*fakeSettings)
	f.lock = LockView{NodeRunning: true, Summary: LockSummary{Lock: LockNodeError, NodeErr: "locked mode: newest"}}
	m = runAll(m, second)
	f.lock = LockView{NodeRunning: true, Summary: LockSummary{Lock: LockNodeError, NodeErr: "locked mode: stale"}}
	m = runAll(m, first)
	out := viewText(m)
	if !strings.Contains(out, "locked mode: newest") || strings.Contains(out, "stale") {
		t.Fatalf("an older reply replaced the newer one:\n%s", out)
	}
}

func TestSettingsReopenedIgnoresTheEarlierScreensReply(t *testing.T) {
	f := &fakeSettings{lock: LockView{NodeRunning: true, Summary: LockSummary{Lock: LockNodeError, NodeErr: "locked mode: old"}}}
	m, old := typeKeysCmd(settingsModel(f), "g,")
	m, _ = upd(m, keyMsg("esc"))
	m, cmd := typeKeysCmd(m, "g,")
	f.lock = LockView{NodeRunning: true, Summary: LockSummary{Lock: LockNodeError, NodeErr: "locked mode: new"}}
	m = runAll(m, cmd)
	f.lock = LockView{NodeRunning: true, Summary: LockSummary{Lock: LockNodeError, NodeErr: "locked mode: old"}}
	m = runAll(m, old)
	out := viewText(m)
	if !strings.Contains(out, "locked mode: new") || strings.Contains(out, "locked mode: old") {
		t.Fatalf("the earlier screen's reply applied:\n%s", out)
	}
}

func signerLock() LockView {
	return LockView{
		NodeRunning:    true,
		Status:         api.LockStatusResult{Enabled: true, SignerTrusted: true},
		Summary:        LockSummary{TUISign: TUIUnsigned, CanSignTUI: true},
		ClientIdentity: []byte{9},
		Devices: []LockDevice{
			{Key: []byte{1}, Label: "home", Authorized: true, ThisNode: true},
			{Key: []byte{2}, Label: "work"},
		},
	}
}

func TestSettingsSignsAnUnsignedDevice(t *testing.T) {
	f := &fakeSettings{lock: signerLock(), signOut: "signed work\n"}
	m := openedSettings(t, f)
	m = typeKeys(m, "j")
	f.calls = nil
	m, cmd := typeKeysCmd(m, "s")
	m = runChain(m, cmd)
	if !reflect.DeepEqual(f.signed, [][]byte{{2}}) {
		t.Fatalf("signed = %v, want the work key", f.signed)
	}
	if !f.called("LockStatus") {
		t.Fatal("a sign must reload the lock status")
	}
	if m.flash != "signed work" {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestSettingsRevokeAsksFirst(t *testing.T) {
	f := &fakeSettings{lock: signerLock(), revoke: "revoked home"}
	m := openedSettings(t, f)
	m, cmd := typeKeysCmd(m, "dd")
	m = runChain(m, cmd)
	p, ok := m.popups.front().(confirmPopup)
	if !ok {
		t.Fatalf("popup = %T, want confirmPopup", m.popups.front())
	}
	if p.title != "Revoke home?" {
		t.Fatalf("title = %q", p.title)
	}
	if f.called("RevokeDevice") {
		t.Fatal("revoke ran before the confirmation")
	}
	m, _ = upd(m, keyMsg("k"))
	m, cmd = upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if !reflect.DeepEqual(f.revoked, [][]byte{{1}}) {
		t.Fatalf("revoked = %v, want the home key", f.revoked)
	}
	if m.popups.front() != nil {
		t.Fatalf("popup %T still open", m.popups.front())
	}
	if m.flash != "revoked home" {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestSettingsRevokeCancelDoesNothing(t *testing.T) {
	f := &fakeSettings{lock: signerLock()}
	m := openedSettings(t, f)
	m = typeKeys(m, "dd")
	m, _ = upd(m, keyMsg("j"))
	m, cmd := upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if f.called("RevokeDevice") {
		t.Fatal("cancel revoked the device")
	}
	if m.popups.front() != nil {
		t.Fatalf("popup %T still open", m.popups.front())
	}
}

func TestSettingsSignsThisTUI(t *testing.T) {
	f := &fakeSettings{lock: signerLock()}
	m := openedSettings(t, f)
	m, cmd := typeKeysCmd(m, "S")
	runChain(m, cmd)
	if !reflect.DeepEqual(f.signed, [][]byte{{9}}) {
		t.Fatalf("signed = %v, want the client identity", f.signed)
	}

	v := signerLock()
	v.Devices = append(v.Devices, LockDevice{Key: []byte{9}, Authorized: true})
	v.Summary.TUISign, v.Summary.CanSignTUI = TUISigned, false
	f = &fakeSettings{lock: v}
	m = openedSettings(t, f)
	m, cmd = typeKeysCmd(m, "S")
	runChain(m, cmd)
	if f.called("SignDevice") {
		t.Fatal("signed a tui that is already a device")
	}
}

func TestSettingsActionsNeedATrustedSigner(t *testing.T) {
	v := signerLock()
	v.Status.SignerTrusted = false
	for _, keys := range []string{"s", "S", "dd"} {
		f := &fakeSettings{lock: v}
		m := openedSettings(t, f)
		if keys == "s" {
			m = typeKeys(m, "j")
		}
		f.calls = nil
		m, cmd := typeKeysCmd(m, keys)
		m = runChain(m, cmd)
		if len(f.calls) > 0 {
			t.Errorf("%q called %v", keys, f.calls)
		}
		if m.popups.front() != nil {
			t.Errorf("%q opened %T", keys, m.popups.front())
		}
		if m.flash != "this node is not a trusted signer" {
			t.Errorf("%q: flash = %q", keys, m.flash)
		}
	}
}

func TestSettingsSignTUIIsQuietWhenNothingToSign(t *testing.T) {
	v := signerLock()
	v.Status.SignerTrusted = false
	v.Summary.TUISign, v.Summary.CanSignTUI = TUISigned, false
	m := openedSettings(t, &fakeSettings{lock: v})
	m, cmd := typeKeysCmd(m, "S")
	m = runChain(m, cmd)
	if m.flash != "" {
		t.Fatalf("flash = %q, want none for a signed tui", m.flash)
	}
}

func TestSettingsShowsAnActionError(t *testing.T) {
	f := &fakeSettings{lock: signerLock(), signErr: errors.New("lock.sign: not a signer")}
	m := openedSettings(t, f)
	m = typeKeys(m, "j")
	f.calls = nil
	m, cmd := typeKeysCmd(m, "s")
	m = runChain(m, cmd)
	if out := viewText(m); !strings.Contains(out, "not a signer") {
		t.Fatalf("view lacks the action error:\n%s", out)
	}
	if !f.called("LockStatus") {
		t.Fatal("a failed action must reload the lock status")
	}
	m, cmd = typeKeysCmd(m, "gr")
	m = runChain(m, cmd)
	if out := viewText(m); strings.Contains(out, "not a signer") {
		t.Fatalf("the action error outlived a reload:\n%s", out)
	}
}

func TestSettingsShowsTheLog(t *testing.T) {
	v := signerLock()
	v.Summary.Lock, v.Summary.NodeErr = LockNodeError, "locked mode: enforcing"
	f := &fakeSettings{lock: v, log: "0 genesis sigpub:aa\n"}
	m := openedSettings(t, f)
	m, cmd := typeKeysCmd(m, "L")
	m = runChain(m, cmd)
	out := viewText(m)
	if !strings.Contains(out, "0 genesis") {
		t.Fatalf("view lacks the log:\n%s", out)
	}
	if strings.Contains(out, "locked mode: enforcing") {
		t.Fatalf("the log shows with the lock body:\n%s", out)
	}
	m, _ = upd(m, keyMsg("esc"))
	if _, ok := m.main.top().(settingsComp); !ok {
		t.Fatalf("top = %T after esc, want settingsComp", m.main.top())
	}
	out = viewText(m)
	if strings.Contains(out, "0 genesis") || !strings.Contains(out, "locked mode: enforcing") {
		t.Fatalf("esc did not return to the lock body:\n%s", out)
	}
}

func TestSettingsScrollsTheLog(t *testing.T) {
	var log strings.Builder
	for i := range 60 {
		fmt.Fprintf(&log, "%d entry\n", i)
	}
	m := settingsModel(&fakeSettings{lock: signerLock(), log: log.String()})
	m.width, m.height = 100, 24
	m, cmd := typeKeysCmd(m, "g,")
	m = runChain(m, cmd)
	m, cmd = typeKeysCmd(m, "L")
	m = runChain(m, cmd)
	assertFitsPane(t, m)
	if out := viewText(m); strings.Contains(out, "59 entry") {
		t.Fatalf("the last entry shows before scrolling:\n%s", out)
	}
	m = typeKeys(m, "G")
	if out := viewText(m); !strings.Contains(out, "59 entry") {
		t.Fatalf("G does not reach the last entry:\n%s", out)
	}
	assertFitsPane(t, m)
	m = typeKeys(m, "gg")
	if out := viewText(m); !strings.Contains(out, "0 entry") {
		t.Fatalf("gg does not reach the first entry:\n%s", out)
	}
}

func TestSettingsLockBodyJumpsAndPages(t *testing.T) {
	m := shortSettings(t, longLockView(5))
	m = typeKeys(m, "G")
	if s := m.main.top().(settingsComp); s.cursor != 4 {
		t.Fatalf("cursor = %d after G, want 4", s.cursor)
	}
	m = typeKeys(m, "gg")
	if s := m.main.top().(settingsComp); s.cursor != 0 {
		t.Fatalf("cursor = %d after gg, want 0", s.cursor)
	}
	m, _ = upd(m, keyMsg("ctrl+f"))
	if s := m.main.top().(settingsComp); s.cursor != 4 {
		t.Fatalf("cursor = %d after a page down, want 4", s.cursor)
	}
	m, _ = upd(m, keyMsg("ctrl+b"))
	if s := m.main.top().(settingsComp); s.cursor != 0 {
		t.Fatalf("cursor = %d after a page up, want 0", s.cursor)
	}

	v := longLockView(0)
	v.Summary.NodeErr += strings.Repeat(" "+strings.Repeat("x", 80), 10)
	m = shortSettings(t, v)
	m = typeKeys(m, "G")
	if out := viewText(m); !strings.Contains(out, "not pinned") {
		t.Fatalf("G does not reach the end of the text:\n%s", out)
	}
	assertFitsPane(t, m)
	m = typeKeys(m, "gg")
	if s := m.main.top().(settingsComp); s.scroll != 0 {
		t.Fatalf("scroll = %d after gg, want 0", s.scroll)
	}
	m, _ = upd(m, keyMsg("ctrl+f"))
	if s := m.main.top().(settingsComp); s.scroll == 0 {
		t.Fatal("a page down did not scroll the text")
	}
}

func TestSettingsShowsAnActionErrorOnAShortPane(t *testing.T) {
	v := longLockView(5)
	v.Status.SignerTrusted = true
	v.Summary.NodeErr = "locked mode: enforcing" + strings.Repeat(" "+strings.Repeat("x", 80), 8)
	v.Devices[0].Authorized = false
	f := &fakeSettings{lock: v, signErr: errors.New("lock.sign: not a signer")}
	m := settingsModel(f)
	m.width, m.height = 100, 24
	m, cmd := typeKeysCmd(m, "g,")
	m = runChain(m, cmd)
	m = typeKeys(m, "Gkkkk")
	if s := m.main.top().(settingsComp); s.scroll == 0 || s.cursor != 0 {
		t.Fatalf("scroll = %d, cursor = %d; the test needs a scrolled body with the cursor on device-0", s.scroll, s.cursor)
	}
	m, cmd = typeKeysCmd(m, "s")
	m = runChain(m, cmd)
	out := viewText(m)
	if !strings.Contains(out, "not a signer") {
		t.Fatalf("the action error is out of view:\n%s", out)
	}
	if !strings.Contains(out, "▸ ! device-0") {
		t.Fatalf("the cursor device is out of view:\n%s", out)
	}
	assertFitsPane(t, m)
}

func TestSettingsAppliesOnlyTheLatestLogReply(t *testing.T) {
	f := &fakeSettings{lock: signerLock()}
	m := openedSettings(t, f)
	m, first := typeKeysCmd(m, "L")
	m, second := typeKeysCmd(m, "gr")
	f.log = "1 newest\n"
	m = runChain(m, second)
	f.log = "0 stale\n"
	m = runChain(m, first)
	out := viewText(m)
	if !strings.Contains(out, "1 newest") || strings.Contains(out, "0 stale") {
		t.Fatalf("an older log reply replaced the newer one:\n%s", out)
	}
}

func TestSettingsReopenedIgnoresTheEarlierScreensAction(t *testing.T) {
	f := &fakeSettings{lock: signerLock(), signOut: "signed work"}
	m := openedSettings(t, f)
	m = typeKeys(m, "j")
	m, sign := typeKeysCmd(m, "s")
	m, _ = upd(m, keyMsg("esc"))
	m = runChain(m, typeCmd(&m, "g,"))
	f.calls = nil
	m = runChain(m, sign)
	if m.flash == "signed work" || f.called("LockStatus") {
		t.Fatalf("the earlier screen's action reply applied: flash %q, calls %v", m.flash, f.calls)
	}
}

func typeCmd(m *model, keys string) tea.Cmd {
	var cmd tea.Cmd
	*m, cmd = typeKeysCmd(*m, keys)
	return cmd
}

func TestSettingsCancelledConfirmKeepsAPendingAction(t *testing.T) {
	f := &fakeSettings{lock: signerLock(), signOut: "signed work"}
	m := openedSettings(t, f)
	m = typeKeys(m, "j")
	m, sign := typeKeysCmd(m, "s")
	m = typeKeys(m, "k")
	m = typeKeys(m, "dd")
	if _, ok := m.popups.front().(confirmPopup); !ok {
		t.Fatalf("popup = %T, want confirmPopup", m.popups.front())
	}
	m, _ = upd(m, keyMsg("esc"))
	f.calls = nil
	m = runChain(m, sign)
	if m.flash != "signed work" {
		t.Fatalf("flash = %q, want the sign's flash", m.flash)
	}
	if !f.called("LockStatus") {
		t.Fatal("the sign did not reload the lock status")
	}
}

// swapSettingsModel stands in for Run, which the test models skip.
func swapSettingsModel(t *testing.T, f *fakeSettings) (model, *swapFakeClient) {
	t.Helper()
	old := newSwapFakeClient(false)
	m := openedSettings(t, f)
	sw := newSwapClient(old)
	m.client = sw
	t.Cleanup(func() { _ = sw.Close() })
	return m, old
}

func TestSettingsPinConfirmsTheFingerprintThenRebuilds(t *testing.T) {
	g := []byte{0xab}
	next := newSwapFakeClient(false)
	f := &fakeSettings{
		lock:    signerLock(),
		genesis: PinOffer{Genesis: g, Encoded: "gen:ab", Fingerprint: "apple banana"},
		pinOut:  "pinned",
		client:  next,
	}
	m, old := swapSettingsModel(t, f)
	m, cmd := typeKeysCmd(m, "p")
	m = runChain(m, cmd)
	p, ok := m.popups.front().(confirmPopup)
	if !ok {
		t.Fatalf("popup = %T, want confirmPopup", m.popups.front())
	}
	if !strings.Contains(p.title, "apple banana") || !reflect.DeepEqual(p.body, []string{"gen:ab", "apple banana"}) {
		t.Fatalf("title = %q, body = %q", p.title, p.body)
	}
	if f.called("Pin") {
		t.Fatal("pin ran before the confirmation")
	}
	m, _ = upd(m, keyMsg("k"))
	m, cmd = upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if !reflect.DeepEqual(f.pinned, [][]byte{g}) {
		t.Fatalf("pinned = %v, want %v", f.pinned, g)
	}
	if !f.called("Reconnect") {
		t.Fatal("a pin must rebuild the client")
	}
	if m.client.(*swapClient).current() != next {
		t.Fatal("the client was not swapped to the rebuilt one")
	}
	if !old.isClosed() {
		t.Fatal("the old client was not closed")
	}
	if err := m.client.Call("ping", nil, nil); err != nil || len(next.callLog()) != 1 {
		t.Fatalf("m.client does not reach the new client: %v, %v", err, next.callLog())
	}
}

func TestSettingsPinWithNoTrustLogFlashes(t *testing.T) {
	f := &fakeSettings{lock: signerLock()}
	m := openedSettings(t, f)
	m, cmd := typeKeysCmd(m, "p")
	m = runChain(m, cmd)
	if m.popups.front() != nil {
		t.Fatalf("popup %T opened", m.popups.front())
	}
	if m.flash != "no trust log on this network; nothing to pin" {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestSettingsPinShowsAGuardError(t *testing.T) {
	f := &fakeSettings{lock: signerLock(), genErr: errors.New("trust pin: pinned by config")}
	m := openedSettings(t, f)
	m, cmd := typeKeysCmd(m, "p")
	m = runChain(m, cmd)
	if m.popups.front() != nil {
		t.Fatalf("popup %T opened", m.popups.front())
	}
	if out := viewText(m); !strings.Contains(out, "pinned by config") {
		t.Fatalf("view lacks the guard error:\n%s", out)
	}
}

func TestSettingsUnpinAsksThenRebuilds(t *testing.T) {
	next := newSwapFakeClient(false)
	f := &fakeSettings{lock: signerLock(), unpin: "unpinned", client: next}
	m, _ := swapSettingsModel(t, f)
	m, cmd := typeKeysCmd(m, "P")
	m = runChain(m, cmd)
	p, ok := m.popups.front().(confirmPopup)
	if !ok || p.title != "Unpin this device?" {
		t.Fatalf("popup = %#v, want the unpin confirmation", m.popups.front())
	}
	if f.called("Unpin") {
		t.Fatal("unpin ran before the confirmation")
	}
	f.calls = nil
	m, _ = upd(m, keyMsg("k"))
	m, cmd = upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if len(f.calls) < 2 || f.calls[0] != "Unpin" || f.calls[1] != "Reconnect" {
		t.Fatalf("calls = %v, want Unpin then Reconnect", f.calls)
	}
	if m.client.(*swapClient).current() != next {
		t.Fatal("the client was not swapped to the rebuilt one")
	}
}

func TestSettingsReconnectFailureKeepsTheOldClient(t *testing.T) {
	f := &fakeSettings{lock: signerLock(), unpin: "unpinned", recErr: errors.New("dial: refused")}
	m, old := swapSettingsModel(t, f)
	m = typeKeys(m, "P")
	m, _ = upd(m, keyMsg("k"))
	m, cmd := upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if m.client.(*swapClient).current() != old || old.isClosed() {
		t.Fatal("a failed reconnect replaced the old client")
	}
	if err := m.client.Call("ping", nil, nil); err != nil || len(old.callLog()) != 1 {
		t.Fatalf("m.client does not reach the old client: %v, %v", err, old.callLog())
	}
	if out := viewText(m); !strings.Contains(out, "reconnect failed: dial: refused") {
		t.Fatalf("view lacks the reconnect error:\n%s", out)
	}
	f.calls = nil
	next := newSwapFakeClient(false)
	f.recErr, f.client = nil, next
	m, cmd = typeKeysCmd(m, "gr")
	m = runChain(m, cmd)
	if !f.called("Reconnect") {
		t.Fatal("refresh did not retry the reconnect")
	}
	if !f.called("LockStatus") {
		t.Fatal("refresh did not reload the lock status")
	}
	if m.client.(*swapClient).current() != next {
		t.Fatal("the retried reconnect did not swap the client")
	}
	if out := viewText(m); strings.Contains(out, "reconnect failed") {
		t.Fatalf("the reconnect error outlived a successful retry:\n%s", out)
	}
}

func TestSettingsReconnectWithoutASwappableClient(t *testing.T) {
	next := newSwapFakeClient(false)
	f := &fakeSettings{lock: signerLock(), unpin: "unpinned", client: next}
	m := openedSettings(t, f)
	m = typeKeys(m, "P")
	m, _ = upd(m, keyMsg("k"))
	m, cmd := upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if out := viewText(m); !strings.Contains(out, "reconnect failed: no swappable client") {
		t.Fatalf("view lacks the error:\n%s", out)
	}
	if !next.isClosed() {
		t.Fatal("the unused rebuilt client was not closed")
	}
}

func TestSettingsReconnectAfterLeavingStillSwaps(t *testing.T) {
	next := newSwapFakeClient(false)
	m, _ := swapSettingsModel(t, &fakeSettings{lock: signerLock()})
	m, _ = upd(m, keyMsg("esc"))
	before := viewText(m)
	m = runChain(m, func() tea.Msg { return settingsReconnectedMsg{client: next} })
	if m.client.(*swapClient).current() != next {
		t.Fatal("a reconnect reply after leaving did not swap the client")
	}
	if after := viewText(m); after != before {
		t.Fatalf("a late reconnect reply changed the view:\n%s\nwant:\n%s", after, before)
	}
}

func TestSettingsOpenedOverAFileShowsAndLoads(t *testing.T) {
	f := &fakeSettings{lock: LockView{NodeRunning: true, Summary: LockSummary{Lock: LockNodeError, NodeErr: "locked mode: enforcing"}}}
	m := withFile(settingsModel(f), fileComp{ws: "n1:w1", path: "go.mod", lines: []string{"x"}})
	if !m.hasOpenFile() {
		t.Fatal("the test needs an open file")
	}
	m, cmd := typeKeysCmd(m, "g,")
	m = runAll(m, cmd)
	if _, ok := m.main.top().(settingsComp); !ok {
		t.Fatalf("top = %T, want settingsComp", m.main.top())
	}
	if m.hasOpenFile() {
		t.Fatal("the file stays open over settings")
	}
	if out := viewText(m); !strings.Contains(out, "locked mode: enforcing") {
		t.Fatalf("the lock reply did not apply:\n%s", out)
	}
}

func settingsIn(t *testing.T, m model) settingsComp {
	t.Helper()
	i := m.settingsAt()
	if i < 0 {
		t.Fatal("no settingsComp in the main stack")
	}
	return m.main[i].(settingsComp)
}

func TestSettingsReplyReachesTheScreenUnderTheTop(t *testing.T) {
	f := &fakeSettings{}
	m, cmd := typeKeysCmd(settingsModel(f), "g,")
	m = withFile(m, fileComp{ws: "n1:w1", path: "go.mod", lines: []string{"x"}})
	if _, ok := m.main.top().(settingsComp); ok {
		t.Fatal("the test needs a component over settings")
	}
	f.lock = LockView{NodeRunning: true, Summary: LockSummary{Lock: LockNodeError, NodeErr: "locked mode: enforcing"}}
	m = runAll(m, cmd)
	if s := settingsIn(t, m); !s.lockLoaded || !strings.Contains(s.lock.Summary.NodeErr, "enforcing") {
		t.Fatalf("the reply did not reach the settings screen: %+v", s)
	}
	if _, ok := m.main.top().(fileComp); !ok {
		t.Fatalf("top = %T, want the file to stay on top", m.main.top())
	}
}

func TestSettingsUnpinShowsItsWholeResult(t *testing.T) {
	next := newSwapFakeClient(false)
	f := &fakeSettings{lock: signerLock(), client: next,
		unpin: "unpinned this device\n\nlock.genesis in this device's config still pins it to\n  gen:ab (apple banana)\nso it stays pinned and enforcing.\n"}
	m, _ := swapSettingsModel(t, f)
	m = typeKeys(m, "P")
	m, _ = upd(m, keyMsg("k"))
	m, cmd := upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if !f.called("Reconnect") || !f.called("LockStatus") {
		t.Fatalf("calls = %v, want the rebuild and the reload", f.calls)
	}
	out := viewText(m)
	for _, want := range []string{"still pins it to", "gen:ab (apple banana)", "stays pinned and enforcing"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	if !strings.HasPrefix(m.flash, "unpinned this device") || strings.Contains(m.flash, "\n") {
		t.Errorf("flash = %q, want the first line", m.flash)
	}

	f.signOut = "sign ok"
	m = typeKeys(m, "j")
	m, cmd = typeKeysCmd(m, "s")
	m = runChain(m, cmd)
	if out := viewText(m); strings.Contains(out, "stays pinned") {
		t.Fatalf("the unpin result outlived the next action:\n%s", out)
	}
}

func TestSettingsReconnectFailureAfterLeavingFlashesAndRetriesOnReopen(t *testing.T) {
	f := &fakeSettings{lock: signerLock()}
	m, old := swapSettingsModel(t, f)
	m, _ = upd(m, keyMsg("esc"))
	m = runChain(m, func() tea.Msg { return settingsReconnectedMsg{err: errors.New("dial: refused")} })
	if m.flash != "reconnect failed: dial: refused" {
		t.Fatalf("flash = %q, want the reconnect error", m.flash)
	}
	if m.client.(*swapClient).current() != old {
		t.Fatal("a failed reconnect replaced the old client")
	}
	m = runChain(m, typeCmd(&m, "g,"))
	if out := viewText(m); !strings.Contains(out, "reconnect failed: dial: refused") {
		t.Fatalf("the reopened screen lacks the reconnect error:\n%s", out)
	}
	next := newSwapFakeClient(false)
	f.client = next
	f.calls = nil
	m = runChain(m, typeCmd(&m, "gr"))
	if !f.called("Reconnect") {
		t.Fatal("refresh on the reopened screen did not retry the reconnect")
	}
	if m.client.(*swapClient).current() != next {
		t.Fatal("the retried reconnect did not swap the client")
	}
	if out := viewText(m); strings.Contains(out, "reconnect failed") {
		t.Fatalf("the reconnect error outlived a successful retry:\n%s", out)
	}
}

func TestSettingsPinWhoseRebuildFailsKeepsThePinAndTheOldClient(t *testing.T) {
	f := &fakeSettings{
		lock:    signerLock(),
		genesis: PinOffer{Genesis: []byte{0xab}, Encoded: "gen:ab", Fingerprint: "apple banana"},
		pinOut:  "pinned this device\n",
		recErr:  errors.New("dial: refused"),
	}
	m, old := swapSettingsModel(t, f)
	m = runChain(m, typeCmd(&m, "p"))
	m, _ = upd(m, keyMsg("k"))
	m, cmd := upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if len(f.pinned) == 0 {
		t.Fatal("the pin did not run")
	}
	if f.called("Unpin") {
		t.Fatal("a failed rebuild must not undo the pin")
	}
	if m.client.(*swapClient).current() != old || old.isClosed() {
		t.Fatal("a failed rebuild replaced the old client")
	}
	if out := viewText(m); !strings.Contains(out, "reconnect failed: dial: refused") {
		t.Fatalf("view lacks the reconnect error:\n%s", out)
	}
}

func TestSettingsShowsTheClientBlockWithANodeError(t *testing.T) {
	m := openedSettings(t, &fakeSettings{
		lock:    LockView{Summary: LockSummary{Lock: LockNoNode, TUI: TUIUnpinned}},
		lockErr: errors.New("node socket: connection refused"),
	})
	out := viewText(m)
	for _, want := range []string{"node socket: connection refused", "no local node", "This TUI"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
}

func TestSettingsLockFooterNamesRevoke(t *testing.T) {
	m := openedSettings(t, &fakeSettings{lock: signerLock()})
	foot := m.currentFooter()
	if !strings.Contains(ansi.Strip(foot), "dd revoke") || strings.Contains(foot, "remove") {
		t.Fatalf("lock footer = %q, want dd revoke", ansi.Strip(foot))
	}
}

func TestSettingsPinOfferAfterLeavingTheLockTabIsDropped(t *testing.T) {
	f := &fakeSettings{lock: signerLock(), genesis: PinOffer{Genesis: []byte{0xab}, Encoded: "gen:ab", Fingerprint: "apple banana"}}
	m := openedSettings(t, f)
	m, cmd := typeKeysCmd(m, "p")
	m = typeKeys(m, "gt")
	m = runChain(m, cmd)
	if m.popups.front() != nil {
		t.Fatalf("popup %T opened on the Paired devices tab", m.popups.front())
	}
}

func TestSettingsPinConfirmShowsTheWholeFingerprintWhenNarrow(t *testing.T) {
	fp := "apple banana cherry delta echo foxtrot golf hotel"
	enc := "gen:" + strings.Repeat("ab", 32)
	f := &fakeSettings{lock: signerLock(), genesis: PinOffer{Genesis: []byte{0xab}, Encoded: enc, Fingerprint: fp}}
	m := openedSettings(t, f)
	m.width = 44
	m = runChain(m, typeCmd(&m, "p"))
	if _, ok := m.popups.front().(confirmPopup); !ok {
		t.Fatalf("popup = %T, want confirmPopup", m.popups.front())
	}
	var rows []string
	for _, ln := range strings.Split(viewText(m), "\n") {
		if i, j := strings.Index(ln, "│"), strings.LastIndex(ln, "│"); i >= 0 && j > i {
			rows = append(rows, strings.TrimSpace(ln[i+len("│"):j]))
		}
	}
	if body := strings.Join(rows, " "); !strings.Contains(body, fp) {
		t.Errorf("the popup cuts the fingerprint:\n%s", viewText(m))
	}
	if body := strings.Join(rows, ""); !strings.Contains(body, enc) {
		t.Errorf("the popup cuts the encoded genesis:\n%s", viewText(m))
	}
}

func TestSettingsPinReconnectsWhenLeftBeforeTheActionRuns(t *testing.T) {
	g := []byte{0xab}
	next := newSwapFakeClient(false)
	f := &fakeSettings{
		lock:    signerLock(),
		genesis: PinOffer{Genesis: g, Encoded: "gen:ab", Fingerprint: "apple banana"},
		pinOut:  "pinned",
		client:  next,
	}
	m, _ := swapSettingsModel(t, f)
	m, cmd := typeKeysCmd(m, "p")
	m = runChain(m, cmd)
	m, _ = upd(m, keyMsg("k"))
	m, cmd = upd(m, keyMsg("enter"))
	var action tea.Cmd
	for _, msg := range execCmd(cmd) {
		m, action = upd(m, msg)
	}
	cmd = action
	m, _ = upd(m, keyMsg("esc"))
	if m.settingsAt() >= 0 {
		t.Fatal("esc did not leave the settings screen")
	}
	m = runChain(m, cmd)
	if !f.called("Reconnect") {
		t.Fatal("the pin did not rebuild the client after leaving")
	}
	if m.client.(*swapClient).current() != next {
		t.Fatal("the client was not swapped after leaving")
	}
	if err := m.client.Call("ping", nil, nil); err != nil || len(next.callLog()) != 1 {
		t.Fatalf("m.client does not reach the new client: %v, %v", err, next.callLog())
	}
}

func TestSettingsPinConfirmOpensOnCancel(t *testing.T) {
	f := &fakeSettings{lock: signerLock(), genesis: PinOffer{Genesis: []byte{0xab}, Encoded: "gen:ab", Fingerprint: "apple banana"}, client: newSwapFakeClient(false)}
	m, _ := swapSettingsModel(t, f)
	m = runChain(m, typeCmd(&m, "p"))
	m, cmd := upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if f.called("Pin") {
		t.Fatal("enter on a fresh pin confirmation pinned")
	}
	m = runChain(m, typeCmd(&m, "p"))
	m, _ = upd(m, keyMsg("k"))
	m, cmd = upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if !f.called("Pin") {
		t.Fatal("the pin choice did not pin")
	}
}

func TestSettingsRevokeConfirmOpensOnCancel(t *testing.T) {
	f := &fakeSettings{lock: signerLock()}
	m := openedSettings(t, f)
	m = typeKeys(m, "dd")
	m, cmd := upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if f.called("RevokeDevice") {
		t.Fatal("enter on a fresh revoke confirmation revoked")
	}
	m = typeKeys(m, "dd")
	m, _ = upd(m, keyMsg("k"))
	m, cmd = upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if !f.called("RevokeDevice") {
		t.Fatal("the revoke choice did not revoke")
	}
}

// confirmedPinAction confirms the pin and returns the action's reply without
// handing it to the model.
func confirmedPinAction(t *testing.T, m model) (model, tea.Msg) {
	t.Helper()
	m = runChain(m, typeCmd(&m, "p"))
	m, _ = upd(m, keyMsg("k"))
	m, cmd := upd(m, keyMsg("enter"))
	var action tea.Cmd
	for _, msg := range execCmd(cmd) {
		m, action = upd(m, msg)
	}
	msgs := execCmd(action)
	if len(msgs) != 1 {
		t.Fatalf("action replies = %v, want one", msgs)
	}
	return m, msgs[0]
}

func pinFake() *fakeSettings {
	return &fakeSettings{
		lock:    signerLock(),
		genesis: PinOffer{Genesis: []byte{0xab}, Encoded: "gen:ab", Fingerprint: "apple banana"},
		pinOut:  "pinned this device\n",
		client:  newSwapFakeClient(false),
	}
}

func TestSettingsPinThatKeepsTheClientPinDoesNotRebuild(t *testing.T) {
	f := pinFake()
	f.pinKept = true
	m, _ := swapSettingsModel(t, f)
	m = runChain(m, typeCmd(&m, "p"))
	m, _ = upd(m, keyMsg("k"))
	f.calls = nil
	m, cmd := upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if f.called("Reconnect") {
		t.Fatal("a pin that left the client pin alone rebuilt the client")
	}
	if !f.called("LockStatus") {
		t.Fatal("the pin did not reload the lock status")
	}
	if out := viewText(m); !strings.Contains(out, "pinned this device") {
		t.Fatalf("view lacks the pin result:\n%s", out)
	}
}

func TestSettingsRefusedPinDoesNotRebuild(t *testing.T) {
	f := pinFake()
	f.pinKept, f.pinErr = true, errors.New("the local node refused this genesis")
	m, _ := swapSettingsModel(t, f)
	m = runChain(m, typeCmd(&m, "p"))
	m, _ = upd(m, keyMsg("k"))
	m, cmd := upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if f.called("Reconnect") {
		t.Fatal("a refused pin rebuilt the client")
	}
	if out := viewText(m); !strings.Contains(out, "refused this genesis") {
		t.Fatalf("view lacks the pin error:\n%s", out)
	}
}

func TestSettingsFailedUnpinThatChangedTheClientPinRebuilds(t *testing.T) {
	next := newSwapFakeClient(false)
	f := &fakeSettings{lock: signerLock(), client: next, unpinErr: errors.New("chain: permission denied")}
	m, _ := swapSettingsModel(t, f)
	m = typeKeys(m, "P")
	m, _ = upd(m, keyMsg("k"))
	m, cmd := upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if !f.called("Reconnect") || m.client.(*swapClient).current() != next {
		t.Fatal("an unpin that cleared the client pin before failing did not rebuild the client")
	}
	if out := viewText(m); !strings.Contains(out, "chain: permission denied") {
		t.Fatalf("view lacks the unpin error after the rebuild:\n%s", out)
	}
}

func TestSettingsShowsReconnectingAndIgnoresARetryMeanwhile(t *testing.T) {
	f := pinFake()
	m, _ := swapSettingsModel(t, f)
	m, reply := confirmedPinAction(t, m)
	m, _ = upd(m, reply)
	if out := viewText(m); !strings.Contains(out, "reconnecting…") {
		t.Fatalf("view lacks the reconnecting line:\n%s", out)
	}
	m.reconnectErr = errors.New("reconnect failed: earlier")
	f.calls = nil
	m = runChain(m, typeCmd(&m, "gr"))
	if f.called("Reconnect") {
		t.Fatal("refresh started a second rebuild while one runs")
	}
}

func TestSettingsPinDuringARebuildRebuildsAfterIt(t *testing.T) {
	f := pinFake()
	m, _ := swapSettingsModel(t, f)
	m, first := confirmedPinAction(t, m)
	m, _ = upd(m, first)
	m, second := confirmedPinAction(t, m)
	f.calls = nil
	m, cmd := upd(m, second)
	execCmd(cmd)
	if f.called("Reconnect") {
		t.Fatal("a second rebuild started while the first runs")
	}
	m, cmd = upd(m, settingsReconnectedMsg{client: newSwapFakeClient(false)})
	execCmd(cmd)
	if !f.called("Reconnect") {
		t.Fatal("the waiting rebuild did not start after the first one")
	}
}

func TestSettingsPinOfferUnderAnotherComponentIsDropped(t *testing.T) {
	f := pinFake()
	m := openedSettings(t, f)
	m, cmd := typeKeysCmd(m, "p")
	m = withFile(m, fileComp{ws: "n1:w1", path: "go.mod", lines: []string{"x"}})
	if _, ok := m.main.top().(settingsComp); ok {
		t.Fatal("the test needs a component over settings")
	}
	m = runChain(m, cmd)
	if m.popups.front() != nil {
		t.Fatalf("popup %T opened over another component", m.popups.front())
	}
}

func TestSettingsPairingOfferUnderAnotherComponentIsDropped(t *testing.T) {
	f := &fakeSettings{offer: PairingOffer{URI: testPairURI, Token: "t"}}
	m := pairedSettings(t, f)
	m, cmd := typeKeysCmd(m, "a")
	m = withFile(m, fileComp{ws: "n1:w1", path: "go.mod", lines: []string{"x"}})
	for _, msg := range execCmd(cmd) {
		m, _ = upd(m, msg)
	}
	if m.popups.front() != nil {
		t.Fatalf("popup %T opened over another component", m.popups.front())
	}
}

func TestSettingsWrapsLongLockLinesAtANarrowWidth(t *testing.T) {
	key := "devpub:" + strings.Repeat("0123456789abcdef", 4)
	m := openedSettings(t, &fakeSettings{lock: LockView{NodeRunning: true,
		Summary: LockSummary{Node: NodeUnsigned, NodeFix: "argus lock sign " + key}}})
	m.width = 44
	var joined strings.Builder
	for _, ln := range strings.Split(viewText(m), "\n") {
		joined.WriteString(strings.TrimSpace(strings.Trim(strings.TrimSpace(ln), "│")))
	}
	if !strings.Contains(joined.String(), key) {
		t.Fatalf("the view cuts the devpub key:\n%s", viewText(m))
	}
}

var settingsNoOpFocusKeys = []binding{paneKeys.Left, paneKeys.Right, projectsKeys.ToggleSidebar, projectsKeys.ToggleFiles}

func TestSettingsFocusListLeavesOutThePaneMovesAndSidebarToggles(t *testing.T) {
	for _, b := range settingsNoOpFocusKeys {
		if screenHas("settings", b.name) {
			t.Errorf("settings lists %q, which does nothing there", b.name)
		}
	}
	for _, b := range []binding{projectsKeys.Palette, projectsKeys.Help, listKeys.Quit, projectsKeys.ToggleMouse, projectsKeys.ToggleVerbose,
		upgradeKeys.Node, upgradeKeys.Gateway, settingsKeys.Open} {
		if !screenHas("settings", b.name) {
			t.Errorf("settings lacks %q", b.name)
		}
	}
	m := openedSettings(t, &fakeSettings{lock: signerLock()})
	for _, b := range m.commandSet() {
		if slices.ContainsFunc(settingsNoOpFocusKeys, func(n binding) bool { return n.name == b.name }) {
			t.Errorf("the command line offers %q on settings", b.name)
		}
	}
}

func TestSettingsPaneMovesAndSidebarTogglesDoNothing(t *testing.T) {
	m := openedSettings(t, &fakeSettings{lock: signerLock()})
	m.width = 200
	before, focused, left, right := viewText(m), m.focused, m.sidebarVisible(), m.filesVisible()
	seqs := [][]tea.KeyPressMsg{cw('h'), cw('l'), cw('j'), cw('k'), cw('w'), cw('W'),
		{keyMsg("space"), keyMsg("o")}, {keyMsg("space"), keyMsg("e")}}
	for _, b := range settingsNoOpFocusKeys {
		seqs = append(seqs, []tea.KeyPressMsg{cmdMsg(b.name)})
	}
	for _, seq := range seqs {
		m = pressKeys(m, seq...)
		if m.focused != focused || m.sidebarVisible() != left || m.filesVisible() != right || viewText(m) != before {
			t.Fatalf("%v changed the settings screen:\n%s", seq, viewText(m))
		}
	}
}

func TestSettingsPinAfterAFailedSignDropsTheOldError(t *testing.T) {
	f := pinFake()
	f.signErr = errors.New("lock.sign: not a signer")
	m, _ := swapSettingsModel(t, f)
	m = typeKeys(m, "j")
	m = runChain(m, typeCmd(&m, "s"))
	if out := viewText(m); !strings.Contains(out, "not a signer") {
		t.Fatalf("view lacks the sign error:\n%s", out)
	}
	m = runChain(m, typeCmd(&m, "p"))
	m, _ = upd(m, keyMsg("k"))
	m, cmd := upd(m, keyMsg("enter"))
	m = runChain(m, cmd)
	if !f.called("Reconnect") {
		t.Fatal("the pin did not rebuild the client")
	}
	out := viewText(m)
	if strings.Contains(out, "not a signer") {
		t.Fatalf("the old sign error outlived the pin:\n%s", out)
	}
	if !strings.Contains(out, "pinned this device") {
		t.Fatalf("view lacks the pin result:\n%s", out)
	}
}

func TestSettingsWheelMovesTheCursorAndScrollsTheLog(t *testing.T) {
	m := openedSettings(t, &fakeSettings{
		lock: LockView{NodeRunning: true, Devices: []LockDevice{{Key: []byte{1}, Label: "a"}, {Key: []byte{2}, Label: "b"}}},
		log:  strings.Repeat("entry\n", 200),
	})
	m.mouse, m.hits = true, &hitMap{}
	r := m.mainRect()
	m, _ = wheelAt(m, r.Min.X+2, r.Min.Y+3, 1)
	if s := m.main.top().(settingsComp); s.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", s.cursor)
	}

	m, cmd := typeKeysCmd(m, "L")
	m = runChain(m, cmd)
	m, _ = wheelAt(m, r.Min.X+2, r.Min.Y+3, 3)
	if s := m.main.top().(settingsComp); s.textScroll != 3 || s.cursor != 1 {
		t.Fatalf("textScroll = %d, cursor = %d; want the log scrolled to 3 and the cursor kept", s.textScroll, s.cursor)
	}
}

func TestSettingsHidesSignTUIWithoutKnownMembership(t *testing.T) {
	v := signerLock()
	v.Summary.TUISign, v.Summary.CanSignTUI = TUISignUnknown, false
	f := &fakeSettings{lock: v}
	m := openedSettings(t, f)
	if slices.ContainsFunc(m.main.top().(settingsComp).footer(&ctx{m: &m}), func(b binding) bool { return b.name == settingsKeys.SignTUI.name }) {
		t.Error("the footer offers sign tui")
	}
	m, cmd := typeKeysCmd(m, "S")
	runChain(m, cmd)
	if f.called("SignDevice") {
		t.Error("signed the tui")
	}
}

func statusRowLine(t *testing.T, m model, label string) string {
	t.Helper()
	for _, line := range strings.Split(viewText(m), "\n") {
		if strings.Contains(line, "● "+label) {
			return line
		}
	}
	t.Fatalf("no %q row:\n%s", label, viewText(m))
	return ""
}

func TestSettingsLockRowsPerState(t *testing.T) {
	tests := []struct {
		name    string
		sum     LockSummary
		devices DevicesState
		want    []string
	}{
		{"enforcing", LockSummary{Lock: LockEnforcing, TrustRoot: "fp:1234"}, DevicesHidden, []string{"Locked", "enforcing", "fp:1234"}},
		{"not enabled", LockSummary{Lock: LockNotEnabled}, DevicesHidden, []string{"Locked", "not enabled"}},
		{"disabled", LockSummary{Lock: LockDisabled}, DevicesHidden, []string{"disabled network-wide", "break-glass"}},
		{"unpinned", LockSummary{Lock: LockOutUnpinned}, DevicesHidden, []string{"locked out", "not pinned to the network's root", "p to pin"}},
		{"replaced", LockSummary{Lock: LockOutReplaced, LockFix: "argus lock pin gen9"}, DevicesHidden, []string{"locked out", "pinned trust root was disabled", "run: argus lock pin gen9"}},
		{"replaced no root", LockSummary{Lock: LockOutReplaced}, DevicesHidden, []string{"no new root yet"}},
		{"local disable", LockSummary{Lock: LockEnforcing, TrustRoot: "fp", LocalDisabled: true}, DevicesHidden, []string{"local-disable active"}},
		{"no node", LockSummary{Lock: LockNoNode}, DevicesHidden, []string{"no local node"}},
		{"node error", LockSummary{Lock: LockNodeError, NodeErr: "socket: refused"}, DevicesHidden, []string{"socket: refused"}},
		{"node signer", LockSummary{Node: NodeSigner}, DevicesHidden, []string{"This node", "signed", "signer"}},
		{"node unsigned", LockSummary{Node: NodeUnsigned, NodeFix: "argus lock sign devpub:zz"}, DevicesHidden, []string{"not signed", "on a signer node run: argus lock sign devpub:zz"}},
		{"tui unpinned", LockSummary{TUI: TUIUnpinned}, DevicesHidden, []string{"This TUI", "not pinned"}},
		{"tui identity", LockSummary{TUI: TUIIdentityUnreadable, TUIErr: "no key"}, DevicesHidden, []string{"identity unreadable: no key"}},
		{"tui pin unreadable", LockSummary{TUI: TUIPinUnreadable, TUIErr: "bad pin"}, DevicesHidden, []string{"pin unreadable: bad pin", "refuses to start"}},
		{"tui replaced", LockSummary{TUI: TUIRootReplaced, TUIRoot: "fpnew", TUIFix: "argus lock pin g"}, DevicesHidden, []string{"replaced by fpnew", "run: argus lock pin g"}},
		{"tui replaced unchecked", LockSummary{TUI: TUIRootReplacedUnchecked}, DevicesHidden, []string{"gateway could not be reached"}},
		{"tui disabled", LockSummary{TUI: TUIRootDisabled}, DevicesHidden, []string{"pinned root disabled; no new root yet"}},
		{"tui locked out", LockSummary{TUI: TUILockedOut}, DevicesHidden, []string{"locked out", "p to pin"}},
		{"tui check failed", LockSummary{TUI: TUICheckFailed, TUIErr: "gw down"}, DevicesHidden, []string{"could not check the network: gw down"}},
		{"tui pinned signed", LockSummary{TUI: TUIPinned, TUISign: TUISigned}, DevicesHidden, []string{"pinned", "signed"}},
		{"tui can sign", LockSummary{TUI: TUIPinned, TUISign: TUIUnsigned, CanSignTUI: true}, DevicesHidden, []string{"not signed", "S to sign"}},
		{"tui cannot sign", LockSummary{TUI: TUIPinned, TUISign: TUIUnsigned, TUISignFix: "argus lock sign devpub:aa"}, DevicesHidden, []string{"on a signer node run: argus lock sign devpub:aa"}},
		{"devices unreported", LockSummary{Devices: DevicesUnreported, DeviceCount: 3}, DevicesUnreported, []string{"Devices", "3", "list not reported", "argus start"}},
		{"devices listed", LockSummary{Devices: DevicesListed, DeviceCount: 2}, DevicesListed, []string{"Devices", "2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := openedSettings(t, &fakeSettings{lock: LockView{NodeRunning: true, Summary: tt.sum}})
			out := strings.Join(strings.Fields(viewText(m)), " ")
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Errorf("view lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestSettingsStatusRowWrapsOnANarrowPane(t *testing.T) {
	m := settingsModel(&fakeSettings{lock: LockView{NodeRunning: true, Summary: LockSummary{
		TUI:    TUIPinUnreadable,
		TUIErr: strings.Repeat("abcd ", 23) + "lastword",
	}}})
	m.width, m.height = 60, 24
	m, cmd := typeKeysCmd(m, "g,")
	m = runAll(m, cmd)
	out := viewText(m)
	if !strings.Contains(out, "lastword") {
		t.Fatalf("the error's last word is cut:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if ansi.StringWidth(line) > 60 {
			t.Errorf("line wider than the pane: %q", line)
		}
	}
	assertFitsPane(t, m)
}

func TestSettingsNoLocalNodeShowsOnlyTheTUIRow(t *testing.T) {
	m := openedSettings(t, &fakeSettings{lock: LockView{Summary: LockSummary{
		Lock: LockNoNode, TUI: TUIUnpinned, Devices: DevicesListed, DeviceCount: 1,
	}}})
	out := viewText(m)
	if !strings.Contains(out, "This TUI") {
		t.Errorf("view lacks the TUI row:\n%s", out)
	}
	for _, bad := range []string{"This node", "Devices"} {
		if strings.Contains(out, bad) {
			t.Errorf("view shows %q with no local node:\n%s", bad, out)
		}
	}
}

func TestSettingsDeviceRowsMarkSigned(t *testing.T) {
	m := openedSettings(t, &fakeSettings{lock: LockView{
		NodeRunning:    true,
		ClientIdentity: []byte{9},
		Status:         api.LockStatusResult{SignerTrusted: true},
		Summary:        LockSummary{Devices: DevicesSomeUnsigned, DeviceCount: 3, Unsigned: 1},
		Devices: []LockDevice{
			{Key: []byte{1}, Label: "home", Authorized: true, ThisNode: true},
			{Key: []byte{2}, Label: "work"},
			{Key: []byte{9}, Label: "mine", Authorized: true},
			{Key: []byte{3}, Label: "other", Authorized: true},
		},
	}})
	lines := map[string]string{}
	for _, line := range strings.Split(viewText(m), "\n") {
		for _, n := range []string{"home", "work", "mine", "other"} {
			if strings.Contains(line, n) && !strings.Contains(line, "●") {
				lines[n] = line
			}
		}
	}
	checks := []struct {
		name, has, lacks string
	}{
		{"home", "✓", "!"},
		{"home", "this node", "this tui"},
		{"home", "signer", ""},
		{"work", "!", "✓"},
		{"work", "not signed", "signer"},
		{"mine", "this tui", "signer"},
		{"other", "✓", "signer"},
	}
	for _, c := range checks {
		line := lines[c.name]
		if !strings.Contains(line, c.has) || (c.lacks != "" && strings.Contains(line, c.lacks)) {
			t.Errorf("%s row %q: want %q, lacking %q", c.name, line, c.has, c.lacks)
		}
	}
	if statusRowLine(t, m, "Devices") == "" || !strings.Contains(statusRowLine(t, m, "Devices"), "1 not signed") {
		t.Errorf("devices row lacks the unsigned count: %q", statusRowLine(t, m, "Devices"))
	}
}

func TestSettingsRowTones(t *testing.T) {
	tests := []struct {
		name string
		got  Tone
		want Tone
	}{
		{"enforcing", LockSummary{Lock: LockEnforcing}.lockTone(), ToneOK},
		{"enforcing local-disabled", LockSummary{Lock: LockEnforcing, LocalDisabled: true}.lockTone(), ToneWarn},
		{"not enabled local-disabled", LockSummary{Lock: LockNotEnabled, LocalDisabled: true}.lockTone(), ToneWarn},
		{"locked out local-disabled", LockSummary{Lock: LockOutUnpinned, LocalDisabled: true}.lockTone(), ToneBad},
		{"tui pinned sign unknown", LockSummary{TUI: TUIPinned, TUISign: TUISignUnknown}.tuiTone(), ToneOK},
		{"tui pinned signed", LockSummary{TUI: TUIPinned, TUISign: TUISigned}.tuiTone(), ToneOK},
		{"tui pinned unsigned", LockSummary{TUI: TUIPinned, TUISign: TUIUnsigned}.tuiTone(), ToneWarn},
		{"tui unpinned", LockSummary{TUI: TUIUnpinned}.tuiTone(), ToneDim},
		{"tui locked out", LockSummary{TUI: TUILockedOut}.tuiTone(), ToneBad},
		{"devices all signed", DevicesListed.Tone(), ToneOK},
		{"devices one unsigned", DevicesSomeUnsigned.Tone(), ToneWarn},
		{"devices roster error", DevicesLabelsFailed.Tone(), ToneWarn},
		{"devices unreported", DevicesUnreported.Tone(), ToneWarn},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: tone = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func detailsLock(status string) LockView {
	v := signerLock()
	v.StatusText, v.TUIText = status, "tui line one\ntui line two"
	return v
}

func TestSettingsDetailsShowTheCLIText(t *testing.T) {
	v := detailsLock("status one\nstatus two")
	v.Summary.Lock, v.Summary.NodeErr = LockNodeError, "locked mode: enforcing"
	f := &fakeSettings{lock: v, log: "0 genesis sigpub:aa\n"}
	m := openedSettings(t, f)
	m = typeKeys(m, "i")
	out := viewText(m)
	for _, want := range []string{"status one", "status two", "tui line one", "tui line two"} {
		if !strings.Contains(out, want) {
			t.Fatalf("details lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "locked mode: enforcing") {
		t.Fatalf("the status rows show with the details:\n%s", out)
	}
	m, cmd := typeKeysCmd(m, "L")
	m = runChain(m, cmd)
	if out := viewText(m); !strings.Contains(out, "0 genesis") || strings.Contains(out, "status one") {
		t.Fatalf("L did not open the log:\n%s", out)
	}
}

func TestSettingsDetailsScrollAndClose(t *testing.T) {
	var st strings.Builder
	for i := range 60 {
		fmt.Fprintf(&st, "line %d\n", i)
	}
	m := shortSettings(t, detailsLock(st.String()))
	m = typeKeys(m, "i")
	m = typeKeys(m, "j")
	if s := m.main.top().(settingsComp); s.textScroll != 1 {
		t.Fatalf("textScroll = %d after j, want 1", s.textScroll)
	}
	m, _ = upd(m, keyMsg("esc"))
	if s := m.main.top().(settingsComp); s.text != textNone {
		t.Fatalf("text = %v after back, want none", s.text)
	}
	m = typeKeys(m, "i")
	if s := m.main.top().(settingsComp); s.textScroll != 0 {
		t.Fatalf("textScroll = %d on reopen, want 0", s.textScroll)
	}
	m = typeKeys(m, "gt")
	if s := m.main.top().(settingsComp); s.text != textNone {
		t.Fatalf("text = %v after a tab switch, want none", s.text)
	}
}

func TestSettingsDetailsFollowAReload(t *testing.T) {
	f := &fakeSettings{lock: detailsLock("old status")}
	m := openedSettings(t, f)
	m = typeKeys(m, "i")
	m, cmd := typeKeysCmd(m, "gr")
	m = runChain(m, cmd)
	f.lock = detailsLock("new status")
	m, cmd = typeKeysCmd(m, "gr")
	m = runChain(m, cmd)
	out := viewText(m)
	if !strings.Contains(out, "new status") || strings.Contains(out, "old status") {
		t.Fatalf("details did not follow the reload:\n%s", out)
	}
	if s := m.main.top().(settingsComp); s.text != textDetails {
		t.Fatalf("text = %v after the reload, want details", s.text)
	}
}

func TestSettingsDetailsOpenWithoutANode(t *testing.T) {
	devpub := "devpub:" + strings.Repeat("k", 100)
	f := &fakeSettings{lock: LockView{Summary: LockSummary{Lock: LockNoNode}, TUIText: "this tui\n  identity " + devpub}}
	m := openedSettings(t, f)
	if foot := ansi.Strip(m.currentFooter()); !strings.Contains(foot, "details") {
		t.Fatalf("footer lacks details: %q", foot)
	}
	m = typeKeys(m, "i")
	if s := m.main.top().(settingsComp); s.text != textDetails {
		t.Fatalf("text = %v after i, want details", s.text)
	}
	if out := viewText(m); !strings.Contains(out, "identity") {
		t.Fatalf("details lack the identity line:\n%s", out)
	}
}

func TestSettingsDetailsWrapLongLines(t *testing.T) {
	key := "devpub:" + strings.Repeat("k", 100)
	f := &fakeSettings{lock: LockView{Summary: LockSummary{Lock: LockNoNode}, TUIText: "run: argus lock sign " + key}}
	m := settingsModel(f)
	m.width, m.height = 60, 40
	m, cmd := typeKeysCmd(m, "g,")
	m = runAll(m, cmd)
	m = typeKeys(m, "i")
	out := strings.ReplaceAll(strings.ReplaceAll(viewText(m), "\n", ""), " ", "")
	if !strings.Contains(out, key) {
		t.Fatalf("details cut the key:\n%s", viewText(m))
	}
}

func TestSettingsLogWrapsLongLines(t *testing.T) {
	key := "sigpub:" + strings.Repeat("s", 100)
	f := &fakeSettings{lock: LockView{NodeRunning: true}, log: "0 genesis " + key + "\n"}
	m := settingsModel(f)
	m.width, m.height = 60, 40
	m, cmd := typeKeysCmd(m, "g,")
	m = runAll(m, cmd)
	m, cmd = typeKeysCmd(m, "L")
	m = runChain(m, cmd)
	out := strings.ReplaceAll(strings.ReplaceAll(viewText(m), "\n", ""), " ", "")
	if !strings.Contains(out, key) {
		t.Fatalf("the log cut the key:\n%s", viewText(m))
	}
}

func TestSettingsDetailsJoinWithOneBlankLine(t *testing.T) {
	f := &fakeSettings{lock: LockView{NodeRunning: true, StatusText: "status\n\n\n", TUIText: "\n\ntui"}}
	m := openedSettings(t, f)
	s := m.main.top().(settingsComp)
	s.text = textDetails
	got := strings.Join(s.textLines(&ctx{m: &m}), "|")
	if got != "status||tui" {
		t.Fatalf("details lines = %q, want status||tui", got)
	}
}

func TestDevicesTextDoesNotRepeatTheRosterErrorPrefix(t *testing.T) {
	s := LockSummary{Devices: DevicesLabelsFailed, DeviceCount: 2, Unsigned: 1}
	got := s.devicesText("node labels unavailable: refused")
	if got != "2 · 1 not signed · node labels unavailable: refused" {
		t.Fatalf("devicesText = %q", got)
	}
}

func TestLockTextOmitsAnEmptyTrustRoot(t *testing.T) {
	if got := (LockSummary{Lock: LockEnforcing}).lockText(nil); got != "enforcing" {
		t.Fatalf("lockText = %q, want enforcing", got)
	}
	if got := (LockSummary{Lock: LockEnforcing, TrustRoot: "apple banana"}).lockText(nil); got != "enforcing · trust root apple banana" {
		t.Fatalf("lockText = %q", got)
	}
}

func mouseSettings(t *testing.T, f *fakeSettings) model {
	t.Helper()
	m := settingsModel(f)
	m.gatewayURL = "wss://gw.example.com"
	m.mouse, m.hits = true, &hitMap{}
	m, cmd := typeKeysCmd(m, "g,")
	return runAll(m, cmd)
}

func TestSettingsClickSelectsADeviceRow(t *testing.T) {
	m := mouseSettings(t, &fakeSettings{lock: LockView{NodeRunning: true, Summary: LockSummary{Devices: DevicesListed},
		Devices: []LockDevice{{Key: []byte{1}, Label: "a", Authorized: true}, {Key: []byte{2}, Label: "b", Authorized: true}}}})
	x, y := itemCell(t, m, regMain, 1)
	m, _ = click(m, x, y)
	if s := m.main.top().(settingsComp); s.cursor != 1 {
		t.Fatalf("cursor = %d after a click on the second device, want 1", s.cursor)
	}
}

func TestSettingsClickSwitchesTabs(t *testing.T) {
	m := mouseSettings(t, &fakeSettings{lock: LockView{NodeRunning: true}, log: "0 genesis\n"})
	m, cmd := typeKeysCmd(m, "L")
	m = runChain(m, cmd)
	r := m.mainRect()
	x := r.Min.X + screenMargin + len(settingsSubLabels[0]) + 3
	m, _ = click(m, x, r.Min.Y)
	s := m.main.top().(settingsComp)
	if s.sub != settingsPaired || s.text != textNone {
		t.Fatalf("sub = %v, text = %v after a click on the Paired tab; want the Paired tab with no text view", s.sub, s.text)
	}
	m, _ = click(m, r.Min.X+screenMargin, r.Min.Y)
	if s := m.main.top().(settingsComp); s.sub != settingsLock {
		t.Fatalf("sub = %v after a click on the Trust tab, want Trust", s.sub)
	}
}

func TestSettingsClickSelectsAPairedRow(t *testing.T) {
	m := mouseSettings(t, &fakeSettings{paired: []api.ClientTokenInfo{{Token: "tok-one"}, {Token: "tok-two"}}})
	m = typeKeys(m, "gt")
	x, y := itemCell(t, m, regMain, 1)
	m, _ = click(m, x, y)
	if s := m.main.top().(settingsComp); s.pairedCursor != 1 {
		t.Fatalf("pairedCursor = %d after a click on the second paired device, want 1", s.pairedCursor)
	}
}
