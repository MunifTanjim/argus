// Package push delivers out-of-band notifications to paired mobile devices so the
// gateway can reach a phone whose app is backgrounded (the live WebSocket only
// delivers while the app is open).
//
// Delivery is UnifiedPush / Web Push: an encrypted payload (RFC 8291) with VAPID
// auth (RFC 8292) is POSTed to a device-provided distributor endpoint. The gateway
// holds only a self-generated VAPID key.
package push

import (
	"context"
	"errors"
)

// Target is a registered device's Web Push subscription. Keys are empty for a
// plain (non-encrypted) endpoint.
type Target struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh,omitempty"` // Web Push subscription public key (base64url)
	Auth     string `json:"auth,omitempty"`   // Web Push subscription auth secret (base64url)
}

// valid reports whether the target is well-formed.
func (t Target) valid() bool { return t.Endpoint != "" }

// Notification is the user-facing payload plus structured data for deep-linking.
type Notification struct {
	Title string
	Body  string
	Data  map[string]string // e.g. session_id, node_id
}

// SessionID returns the session id carried in Data for deep-linking, or "".
// Single source for the "session_id" key (set in trigger.go).
func (n Notification) SessionID() string { return n.Data["session_id"] }

// ErrGone marks a permanently dead target (HTTP 404/410); senders wrap it so the
// dispatcher prunes the target.
var ErrGone = errors.New("push: target gone")

// Sender delivers one notification to one target.
type Sender interface {
	Send(ctx context.Context, t Target, n Notification) error
}

// Sink consumes a ready-to-send notification, so one transition detector (Watch)
// can feed several delivery channels (mobile dispatch, desktop rendering).
type Sink interface {
	Notify(ctx context.Context, n Notification)
}
