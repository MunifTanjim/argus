package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/invite"
)

// ErrIssuerUnreachable means the invite's issuing node did not answer: it is
// offline, or it silently dropped the handshake (unknown invite, or another
// attempt is redeeming the invite at that moment).
var ErrIssuerUnreachable = errors.New("issuer node is offline or unreachable")

// ErrInviteMismatch means the issuer's handshake reply did not verify: the
// invite secret or issuer key in the blob is not the issuer's.
var ErrInviteMismatch = errors.New("the invite does not match the issuing node; it may be mistyped or corrupted")

// ErrRedeemResponseLost means the redeem request went out but no answer came
// back intact; the issuer may already have consumed the invite.
var ErrRedeemResponseLost = errors.New("redeem response lost")

// inviteHandshakeTimeout is how long RedeemInvite waits for the issuer's msg2.
var inviteHandshakeTimeout = 15 * time.Second

// InviteRedeemTimeout bounds a whole RedeemInvite: relay.open and the handshake
// share the handshake timeout, then the redeem request gets the call timeout.
func InviteRedeemTimeout() time.Duration {
	return inviteHandshakeTimeout + callTimeout
}

// RedeemInvite opens an invite channel to the blob's issuer over a gateway
// connection, proving the invite secret with static as the initiator key, and
// sends the one lock.inviteRedeem the channel serves. Each step has its own
// timeout, so the whole call takes at most InviteRedeemTimeout. It closes conn.
func RedeemInvite(ctx context.Context, conn net.Conn, static e2e.KeyPair, b invite.Blob, extraKeys [][]byte) (api.LockInviteRedeemResult, error) {
	var res api.LockInviteRedeemResult
	handshakeDeadline := time.Now().Add(inviteHandshakeTimeout)
	frames := make(chan api.RelayFrame, 8)
	peer := api.NewPeer(conn, api.PeerOptions{
		OnRelayFrame: func(_ *api.Peer, f api.RelayFrame) {
			select {
			case frames <- f:
			default:
			}
		},
	})
	defer peer.Close()

	var open api.RelayOpenResult
	openCtx, openCancel := context.WithDeadline(ctx, handshakeDeadline)
	err := peer.CallContext(openCtx, api.MethodRelayOpen, api.RelayOpenParams{NodeID: b.IssuerID}, &open)
	openCancel()
	if err != nil {
		var rpcErr *api.RPCError
		if errors.As(err, &rpcErr) && strings.HasPrefix(rpcErr.Message, api.ErrMsgUnknownNode) {
			return res, fmt.Errorf("%w (%s)", ErrIssuerUnreachable, rpcErr.Message)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return res, fmt.Errorf("%w (the gateway did not open a channel)", ErrIssuerUnreachable)
		}
		return res, fmt.Errorf("relay.open: %w", err)
	}

	init, msg1, err := e2e.NewInviteInitiator(static, b.IssuerKey, api.ChannelPrologue(b.IssuerID, open.ChanID), b.Secret, b.InviteID)
	if err != nil {
		return res, err
	}
	hf, err := api.MarshalInviteHandshakeFrame(open.ChanID, msg1)
	if err != nil {
		return res, err
	}
	if err := peer.SendRawFrame(api.EncodingJSON, hf); err != nil {
		return res, err
	}

	// next waits for the first frame on the channel that want accepts, or fails
	// with timeoutErr once timeout passes.
	next := func(timeout time.Duration, timeoutErr error, want func(api.RelayFrame) bool) (api.RelayFrame, error) {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case f := <-frames:
				if f.Route.ChanID == open.ChanID && want(f) {
					return f, nil
				}
			case <-peer.Done():
				return api.RelayFrame{}, errors.New("gateway connection closed")
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return api.RelayFrame{}, timeoutErr
				}
				return api.RelayFrame{}, ctx.Err()
			case <-timer.C:
				return api.RelayFrame{}, timeoutErr
			}
		}
	}

	f, err := next(time.Until(handshakeDeadline), ErrIssuerUnreachable, func(f api.RelayFrame) bool {
		return f.Method == api.MethodE2EInviteHandshake
	})
	if err != nil {
		return res, err
	}
	msg2, err := api.HandshakeFromFrame(f)
	if err != nil {
		return res, err
	}
	sess, err := init.Finish(msg2)
	if err != nil {
		return res, fmt.Errorf("%w: %w", ErrInviteMismatch, err)
	}
	ch := api.NewChannel(open.ChanID, sess)

	params, err := json.Marshal(api.LockInviteRedeemParams{ExtraKeys: extraKeys})
	if err != nil {
		return res, err
	}
	id := json.RawMessage("1")
	req, err := ch.SealRequestFrame(api.EncodingJSON, &id, api.MethodLockInviteRedeem, b.IssuerID, params)
	if err != nil {
		return res, err
	}
	if err := peer.SendRawFrame(api.EncodingJSON, req); err != nil {
		return res, err
	}
	lost := func(cause error) error { return fmt.Errorf("%w: %w", ErrRedeemResponseLost, cause) }
	f, err = next(callTimeout, errors.New("the issuer did not answer the redeem request"), func(f api.RelayFrame) bool {
		return f.ID != nil && f.Method == ""
	})
	if err != nil {
		return res, lost(err)
	}
	result, rpcErr, err := ch.OpenResponse(f)
	if err != nil {
		return res, lost(err)
	}
	if rpcErr != nil {
		return res, rpcErr
	}
	if err := json.Unmarshal(result, &res); err != nil {
		return res, lost(fmt.Errorf("decode redeem result: %w", err))
	}
	return res, nil
}

// WaitDeviceAuthorized dials the gateway as a client with static and syncs the
// trust log rooted at genesis until it authorizes pub, or timeout passes.
func WaitDeviceAuthorized(ctx context.Context, dial api.Dialer, static e2e.KeyPair, genesis []byte, chainPath string, pub []byte, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dial(ctx)
	if err != nil {
		return fmt.Errorf("connect to the gateway: %w", err)
	}
	c, err := NewE2EClientWithIdentity(conn, static, genesis, chainPath)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	// Connect has no context; closing the client unblocks it.
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	notSeen := fmt.Errorf("not seen in the network's trust log within %s", timeout)
	if err := c.Connect(); err != nil {
		if ctx.Err() != nil {
			return notSeen
		}
		return err
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if c.DeviceAuthorized(pub) {
			return nil
		}
		select {
		case <-ctx.Done():
			return notSeen
		case <-tick.C:
		}
	}
}
