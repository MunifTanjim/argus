package tui

import (
	"fmt"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/keyfmt"
)

type Tone int

const (
	ToneDim Tone = iota
	ToneOK
	ToneWarn
	ToneBad
)

type LockState int

const (
	LockNotEnabled LockState = iota
	LockEnforcing
	LockDisabled
	LockOutUnpinned
	LockOutReplaced
	LockNoNode
	LockNodeError
)

type NodeState int

const (
	NodeNotApplicable NodeState = iota
	NodeSigner
	NodeSigned
	NodeUnsigned
)

type TUIPinState int

const (
	TUIUnpinned TUIPinState = iota
	TUIPinned
	TUIIdentityUnreadable
	TUIPinUnreadable
	TUIRootReplaced
	TUIRootReplacedUnchecked
	TUIRootDisabled
	TUILockedOut
	TUICheckFailed
)

type TUISignState int

const (
	TUISignUnknown TUISignState = iota
	TUISigned
	TUIUnsigned
)

type DevicesState int

const (
	DevicesHidden DevicesState = iota
	DevicesUnreported
	DevicesListed
	DevicesSomeUnsigned
	DevicesLabelsFailed
)

type pairingState int

const (
	pairingLoading pairingState = iota
	pairingAvailable
	pairingNeedsMaster
	pairingDisabled
	pairingError
)

func pairingStateOf(loaded bool, err error) pairingState {
	switch {
	case err == nil && !loaded:
		return pairingLoading
	case err == nil:
		return pairingAvailable
	case strings.Contains(err.Error(), "admin token required"):
		return pairingNeedsMaster
	case strings.Contains(err.Error(), "client token management not enabled"):
		return pairingDisabled
	}
	return pairingError
}

func (s pairingState) Tone() Tone {
	switch s {
	case pairingAvailable:
		return ToneOK
	case pairingNeedsMaster, pairingDisabled:
		return ToneWarn
	case pairingError:
		return ToneBad
	}
	return ToneDim
}

func (s pairingState) text(err error, key func(binding) string) string {
	switch s {
	case pairingAvailable:
		return "available · " + key(settingsKeys.Pair) + " to pair"
	case pairingNeedsMaster:
		return "needs the gateway's master token"
	case pairingDisabled:
		return "client token management not enabled on the gateway"
	case pairingError:
		return err.Error()
	}
	return "loading"
}

type LockSummary struct {
	Lock          LockState
	TrustRoot     string // fingerprint of the node's PinGenesis; set for LockEnforcing
	LockFix       string // LockOutReplaced: "argus lock pin <gen>"; "" when no new root is seen
	LocalDisabled bool
	NodeErr       string // LockNodeError
	Node          NodeState
	NodeFix       string // NodeUnsigned: the sign command for the node's identity
	TUI           TUIPinState
	TUIErr        string // the error text for the unreadable and check-failed states
	TUIRoot       string // TUIRootReplaced: the network root's fingerprint
	TUIFix        string // TUIRootReplaced: "argus lock pin <gen>"
	TUISign       TUISignState
	TUISignFix    string // TUIUnsigned: the sign command for the client identity
	CanSignTUI    bool   // TUIUnsigned and the node's signer is trusted
	Devices       DevicesState
	DeviceCount   int
	Unsigned      int // listed devices that are not signed
}

func (s LockState) Tone() Tone {
	switch s {
	case LockEnforcing:
		return ToneOK
	case LockDisabled, LockOutUnpinned, LockOutReplaced, LockNodeError:
		return ToneBad
	default:
		return ToneDim
	}
}

func (s NodeState) Tone() Tone {
	switch s {
	case NodeSigner, NodeSigned:
		return ToneOK
	case NodeUnsigned:
		return ToneWarn
	default:
		return ToneDim
	}
}

func (s TUIPinState) Tone() Tone {
	switch s {
	case TUIPinned:
		return ToneOK
	case TUIUnpinned:
		return ToneDim
	case TUICheckFailed:
		return ToneWarn
	default:
		return ToneBad
	}
}

func (s DevicesState) Tone() Tone {
	switch s {
	case DevicesListed:
		return ToneOK
	case DevicesUnreported, DevicesSomeUnsigned, DevicesLabelsFailed:
		return ToneWarn
	default:
		return ToneDim
	}
}

func (s LockSummary) lockTone() Tone {
	t := s.Lock.Tone()
	if t != ToneBad && s.LocalDisabled {
		return ToneWarn
	}
	return t
}

func (s LockSummary) lockText(key func(binding) string) string {
	var t string
	switch s.Lock {
	case LockNoNode:
		t = "no local node"
	case LockNodeError:
		t = s.NodeErr
	case LockEnforcing:
		t = "enforcing"
		if s.TrustRoot != "" {
			t += " · trust root " + s.TrustRoot
		}
	case LockDisabled:
		t = "disabled network-wide: break-glass used, nothing is enforced"
	case LockOutUnpinned:
		t = "locked out: not pinned to the network's root · " + key(settingsKeys.Pin) + " to pin"
	case LockOutReplaced:
		t = "locked out: the pinned trust root was disabled, and the network locked again under a new root"
		if s.LockFix != "" {
			t += " · run: " + s.LockFix
		} else {
			t += " · no new root yet"
		}
	default:
		t = "not enabled"
	}
	if s.LocalDisabled {
		t += " · local-disable active"
	}
	return t
}

func shortIdentity(identity []byte) string {
	return xansi.Truncate(keyfmt.DeviceKey.Encode(identity), 17, "…")
}

func (s LockSummary) nodeText(identity []byte) string {
	switch s.Node {
	case NodeSigner:
		return "signed · signer"
	case NodeSigned:
		return "signed"
	case NodeUnsigned:
		if s.NodeFix != "" {
			return "not signed · on a signer node run: " + s.NodeFix
		}
		return "not signed"
	}
	if len(identity) == 0 {
		return ""
	}
	return shortIdentity(identity)
}

func (s LockSummary) tuiTone() Tone {
	if t := s.TUI.Tone(); t != ToneOK {
		return t
	}
	if s.TUISign == TUIUnsigned {
		return ToneWarn
	}
	return ToneOK
}

func (s LockSummary) tuiText(key func(binding) string) string {
	switch s.TUI {
	case TUIIdentityUnreadable:
		return "identity unreadable: " + s.TUIErr
	case TUIPinUnreadable:
		return "pin unreadable: " + s.TUIErr + "; argus refuses to start until it is fixed"
	case TUIRootReplaced:
		return fmt.Sprintf("pinned root replaced by %s · run: %s", s.TUIRoot, s.TUIFix)
	case TUIRootReplacedUnchecked:
		return "pinned root replaced; the gateway could not be reached to find the new one"
	case TUIRootDisabled:
		return "pinned root disabled; no new root yet"
	case TUILockedOut:
		return "locked out: not pinned to the network's root · " + key(settingsKeys.Pin) + " to pin"
	case TUICheckFailed:
		return "could not check the network: " + s.TUIErr
	case TUIPinned:
		return "pinned" + s.tuiSignText(key)
	}
	return "not pinned"
}

func (s LockSummary) tuiSignText(key func(binding) string) string {
	switch s.TUISign {
	case TUISigned:
		return " · signed"
	case TUIUnsigned:
		switch {
		case s.CanSignTUI:
			return " · not signed · " + key(settingsKeys.SignTUI) + " to sign"
		case s.TUISignFix != "":
			return " · not signed · on a signer node run: " + s.TUISignFix
		}
		return " · not signed"
	}
	return ""
}

func (s LockSummary) devicesText(rosterErr string) string {
	if s.Devices == DevicesUnreported {
		return fmt.Sprintf("%d · list not reported by the running daemon; restart it: argus start", s.DeviceCount)
	}
	parts := []string{fmt.Sprint(s.DeviceCount)}
	if s.Unsigned > 0 {
		parts = append(parts, fmt.Sprintf("%d not signed", s.Unsigned))
	}
	if s.Devices == DevicesLabelsFailed {
		parts = append(parts, rosterErr)
	}
	return strings.Join(parts, " · ")
}
