package tui

import (
	"context"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/keyfmt"
)

type Settings interface {
	LockStatus(ctx context.Context) (LockView, error)
	LockLog(ctx context.Context) (string, error)
	SignDevice(ctx context.Context, device []byte) (string, error)
	RevokeDevice(ctx context.Context, device []byte) (string, error)
	NetworkGenesis(ctx context.Context) (PinOffer, error)
	Pin(ctx context.Context, genesis []byte) (PinResult, error)
	Unpin(ctx context.Context) (PinResult, error)
	// Reconnect builds a new client for the rest of the session.
	Reconnect() (Client, error)
	PairedDevices(ctx context.Context) ([]api.ClientTokenInfo, error)
	RemovePairedDevice(ctx context.Context, token string) error
	StartPairing(ctx context.Context) (PairingOffer, error)
	AwaitPairing(ctx context.Context, token string) (bool, error)
}

// LockView is the Trust tab's content.
type LockView struct {
	NodeRunning    bool
	Status         api.LockStatusResult // zero when !NodeRunning
	StatusText     string               // lock status text, as the CLI prints it
	TUIText        string               // the "this tui" block, as the CLI prints it
	ClientIdentity []byte
	Devices        []LockDevice
	RosterErr      string // "" when the roster loaded or no gateway is set
	Summary        LockSummary
}

type LockDevice struct {
	Key        []byte
	Label      string // node label from the roster; "" when unknown
	Authorized bool
	ThisNode   bool
}

func (d LockDevice) name() string {
	if d.Label != "" {
		return d.Label
	}
	return keyfmt.DeviceKey.Encode(d.Key)
}

type PinOffer struct {
	Genesis     []byte // nil: the network has no trust log
	Encoded     string // trustpin.Encode(Genesis)
	Fingerprint string // fingerprintOf(Genesis)
}

// PinResult comes with a Pin or Unpin error too, as a failed step can follow a
// written one.
type PinResult struct {
	Text string
	// ClientPinChanged reports that the client pin on disk no longer matches the
	// one the current client was built with.
	ClientPinChanged bool
}

type PairingOffer struct{ URI, URL, Token string }

const settingsCallTimeout = 10 * time.Second
