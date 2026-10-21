package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/trustpin"
	"github.com/MunifTanjim/argus/internal/tui"
)

// settingsBackend's ctx outlives each call: it is the context a client built by
// Reconnect runs under.
type settingsBackend struct {
	ctx              context.Context
	cfg              *config.Config
	dial             func(ctx context.Context, head []byte) (tui.Client, error)
	reconnectTimeout time.Duration

	mu   sync.Mutex
	head []byte // the client pin the current client was built with
}

var _ tui.Settings = (*settingsBackend)(nil)

// newSettingsBackend talks to the gateway the TUI drives, which can differ from the
// configured one.
func newSettingsBackend(ctx context.Context, cfg *config.Config, gatewayURL, token string, head []byte) *settingsBackend {
	c := *cfg
	c.Gateway.URL, c.Token = gatewayURL, token
	b := &settingsBackend{ctx: ctx, cfg: &c, reconnectTimeout: reconnectTimeout, head: head}
	b.dial = func(ctx context.Context, head []byte) (tui.Client, error) {
		return dialClient(ctx, b.cfg, b.cfg.Gateway.URL, b.cfg.Token, b.cfg.Socket, head)
	}
	return b
}

const reconnectTimeout = 10 * time.Second

func (b *settingsBackend) LockStatus(ctx context.Context) (tui.LockView, error) {
	var v tui.LockView
	var cp clientPinInfo
	var wg sync.WaitGroup
	wg.Go(func() { cp = checkClientPin(ctx, b.cfg) })
	kp, idErr := e2e.LoadOrCreateIdentity(config.GetStatePath("client-identity.json"))
	if idErr == nil {
		v.ClientIdentity = kp.Public
	}
	st, err := lockStatusOnNode(ctx, b.cfg)
	if err != nil {
		wg.Wait()
		if errors.Is(err, os.ErrNotExist) {
			v.Summary.Lock = tui.LockNoNode
		} else {
			v.Summary.Lock, v.Summary.NodeErr = tui.LockNodeError, fmt.Sprintf("node socket: %v", err)
		}
		fillTUISummary(&v.Summary, cp, idErr)
		if idErr != nil {
			v.TUIText = cp.text()
		} else {
			v.TUIText = noLocalNodeText(kp.Public, cp)
		}
		return v, nil
	}
	v.NodeRunning = true
	v.Status = st
	out, _ := lockStatusLines(st)
	v.StatusText = out + authorizeHint(st)

	var roster []api.NodeDescriptor
	if b.cfg.Gateway.URL != "" && st.Enabled && devicesKnown(st) {
		wg.Go(func() {
			err := probeGateway(ctx, func(pctx context.Context) (err error) {
				roster, err = fetchRoster(pctx, b.cfg)
				return err
			})
			if err != nil {
				v.RosterErr = fmt.Sprintf("node labels unavailable: %v", err)
			}
		})
	}
	wg.Wait()
	v.TUIText = tuiRoleText(st, v.ClientIdentity, cp)
	v.Devices = joinRoster(st, roster)
	v.Summary = lockSummaryOf(st, v.ClientIdentity, idErr, cp, v.Devices, v.RosterErr)
	return v, nil
}

func lockSummaryOf(st api.LockStatusResult, identity []byte, idErr error, cp clientPinInfo, devices []tui.LockDevice, rosterErr string) tui.LockSummary {
	sum := tui.LockSummary{
		Lock:          lockStateOf(st),
		LocalDisabled: st.LocalDisabled,
		Node:          nodeStateOf(st),
		TUISign:       tuiSignStateOf(st, identity),
		DeviceCount:   st.DeviceCount,
	}
	switch sum.Lock {
	case tui.LockEnforcing:
		if len(st.PinGenesis) > 0 {
			sum.TrustRoot = fingerprintOf(st.PinGenesis)
		}
	case tui.LockOutReplaced:
		sum.LockFix = lockPinFix(st)
	}
	if sum.Node == tui.NodeUnsigned && len(st.IdentityPubKey) > 0 {
		sum.NodeFix = lockSignHint(st.IdentityPubKey)
	}
	if sum.TUISign == tui.TUIUnsigned {
		sum.TUISignFix = lockSignHint(identity)
		sum.CanSignTUI = st.SignerTrusted
	}
	for _, d := range devices {
		if !d.Authorized {
			sum.Unsigned++
		}
	}
	switch {
	case !st.Enabled:
		sum.Devices = tui.DevicesHidden
	case !devicesKnown(st):
		sum.Devices = tui.DevicesUnreported
	case rosterErr != "":
		sum.Devices = tui.DevicesLabelsFailed
	case sum.Unsigned > 0:
		sum.Devices = tui.DevicesSomeUnsigned
	default:
		sum.Devices = tui.DevicesListed
	}
	fillTUISummary(&sum, cp, idErr)
	return sum
}

func fillTUISummary(sum *tui.LockSummary, cp clientPinInfo, idErr error) {
	sum.TUI = cp.state()
	switch {
	case idErr != nil:
		sum.TUI = tui.TUIIdentityUnreadable
		sum.TUIErr = idErr.Error()
	case sum.TUI == tui.TUIPinUnreadable:
		sum.TUIErr = cp.perr.Error()
	case sum.TUI == tui.TUICheckFailed:
		sum.TUIErr = cp.neterr.Error()
	case sum.TUI == tui.TUIRootReplaced:
		sum.TUIRoot = fingerprintOf(cp.netGenesis)
		sum.TUIFix = cp.fix()
	}
}

// joinRoster lists the authorized devices with their roster labels, then the roster
// nodes whose keys are not authorized.
func joinRoster(st api.LockStatusResult, roster []api.NodeDescriptor) []tui.LockDevice {
	nodes, _ := gatherRosterDevices(roster)
	labelOf := func(key []byte) string {
		for _, n := range nodes {
			if bytes.Equal(n.pub, key) {
				return n.label
			}
		}
		return ""
	}
	thisNode := func(key []byte) bool {
		return len(st.IdentityPubKey) > 0 && bytes.Equal(key, st.IdentityPubKey)
	}

	out := make([]tui.LockDevice, 0, len(st.Devices)+len(nodes))
	for _, d := range st.Devices {
		out = append(out, tui.LockDevice{Key: d, Label: labelOf(d), Authorized: true, ThisNode: thisNode(d)})
	}
	for _, n := range nodes {
		listed := slices.ContainsFunc(out, func(d tui.LockDevice) bool { return bytes.Equal(d.Key, n.pub) })
		if !listed {
			out = append(out, tui.LockDevice{Key: n.pub, Label: n.label, ThisNode: thisNode(n.pub)})
		}
	}
	return out
}

func (b *settingsBackend) LockLog(ctx context.Context) (string, error) {
	res, err := lockLogOnNode(ctx, b.cfg)
	if err != nil {
		return "", err
	}
	return lockLogText(res), nil
}

func (b *settingsBackend) SignDevice(ctx context.Context, device []byte) (string, error) {
	return b.lockDevice(ctx, lockSignOp, device)
}

func (b *settingsBackend) RevokeDevice(ctx context.Context, device []byte) (string, error) {
	return b.lockDevice(ctx, lockRevokeOp, device)
}

func (b *settingsBackend) lockDevice(ctx context.Context, op lockDeviceOp, device []byte) (string, error) {
	res, err := lockDeviceOnNode(ctx, b.cfg, op.method, device)
	if err != nil {
		return "", err
	}
	return op.outcome(res.Changed), nil
}

func (b *settingsBackend) NetworkGenesis(ctx context.Context) (tui.PinOffer, error) {
	genesis, err := genesisFromNetwork(ctx, b.cfg)
	if err != nil {
		return tui.PinOffer{}, err
	}
	if genesis == nil {
		return tui.PinOffer{}, nil
	}
	if err := guardPin(b.cfg, genesis); err != nil {
		return tui.PinOffer{}, err
	}
	return tui.PinOffer{Genesis: genesis, Encoded: trustpin.Encode(genesis), Fingerprint: fingerprintOf(genesis)}, nil
}

func (b *settingsBackend) Pin(ctx context.Context, genesis []byte) (tui.PinResult, error) {
	var out, notes strings.Builder
	err := applyPin(ctx, b.cfg, genesis, &out, &notes)
	return tui.PinResult{Text: notes.String() + out.String(), ClientPinChanged: b.clientPinChanged()}, err
}

func (b *settingsBackend) Unpin(ctx context.Context) (tui.PinResult, error) {
	var out, notes strings.Builder
	err := unpinDevice(ctx, b.cfg, &out, &notes)
	return tui.PinResult{Text: out.String() + notes.String(), ClientPinChanged: b.clientPinChanged()}, err
}

// clientPinChanged counts an unresolvable pin as changed, so the rebuild reports
// it. A local socket client does not use the pin.
func (b *settingsBackend) clientPinChanged() bool {
	if b.cfg.Gateway.URL == "" {
		return false
	}
	head, err := clientHead(b.cfg)
	if err != nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return !bytes.Equal(head, b.head)
}

func (b *settingsBackend) Reconnect() (tui.Client, error) {
	head, err := clientHead(b.cfg)
	if err != nil {
		return nil, fmt.Errorf("refusing to connect open: %w", err)
	}
	type dialed struct {
		c   tui.Client
		err error
	}
	ctx, cancel := context.WithCancel(b.ctx)
	done := make(chan dialed, 1)
	go func() {
		c, err := b.dial(ctx, head)
		done <- dialed{c, err}
	}()
	select {
	case d := <-done:
		if d.err != nil {
			cancel()
			return nil, d.err
		}
		b.mu.Lock()
		b.head = head
		b.mu.Unlock()
		return cancelOnClose{Client: d.c, cancel: cancel}, nil
	case <-time.After(b.reconnectTimeout):
		cancel()
		go func() {
			if d := <-done; d.c != nil {
				_ = d.c.Close()
			}
		}()
		return nil, fmt.Errorf("the connection did not come up within %s", b.reconnectTimeout)
	}
}

// cancelOnClose ends the context its client runs under once the client closes.
type cancelOnClose struct {
	tui.Client
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	defer c.cancel()
	return c.Client.Close()
}

func (b *settingsBackend) gatewayCall(ctx context.Context, method string, params, out any) error {
	if b.cfg.Gateway.URL == "" {
		return errors.New("no gateway is set")
	}
	c, err := dialGatewayClient(ctx, b.cfg.Gateway.URL, b.cfg.Token)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.CallContext(ctx, method, params, out)
}

func (b *settingsBackend) PairedDevices(ctx context.Context) ([]api.ClientTokenInfo, error) {
	var list []api.ClientTokenInfo
	if err := b.gatewayCall(ctx, api.MethodClientsList, nil, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (b *settingsBackend) RemovePairedDevice(ctx context.Context, token string) error {
	return b.gatewayCall(ctx, api.MethodClientsRemove, api.ClientRemoveParams{Token: token}, nil)
}

func (b *settingsBackend) StartPairing(ctx context.Context) (tui.PairingOffer, error) {
	var start api.PairStartResult
	if err := b.gatewayCall(ctx, api.MethodClientsPairStart, nil, &start); err != nil {
		return tui.PairingOffer{}, err
	}
	if start.URL == "" {
		return tui.PairingOffer{}, errors.New("the gateway has no public URL, so a phone cannot reach it")
	}
	wsURL, err := pairingURL(start.URL)
	if err != nil {
		return tui.PairingOffer{}, err
	}
	return tui.PairingOffer{URI: pairingURIFor(wsURL, start.Token), URL: wsURL, Token: start.Token}, nil
}

// AwaitPairing closes its connection when ctx ends, which also unblocks the
// gateway's pairAwait for this token.
func (b *settingsBackend) AwaitPairing(ctx context.Context, token string) (bool, error) {
	var out api.PairAwaitResult
	if err := b.gatewayCall(ctx, api.MethodClientsPairAwait, api.PairAwaitParams{Token: token}, &out); err != nil {
		return false, err
	}
	return out.Connected, nil
}
