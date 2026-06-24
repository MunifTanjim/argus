// Package push delivers out-of-band notifications to paired mobile devices when a
// session needs the user's attention, so the self-hosted gateway can reach a
// phone whose app is backgrounded or killed (the live WebSocket only delivers
// while the app is open).
//
// Delivery is UnifiedPush / Web Push: the gateway HTTP-POSTs an encrypted payload
// (RFC 8291) with a VAPID authorization (RFC 8292) to a device-provided endpoint.
// The endpoint comes from a distributor on the phone — an external app (ntfy,
// Sunup) or the app's own embedded distributor. The gateway holds only a
// self-generated VAPID key.
//
// A Dispatcher fans a Notification out to every registered device Target and
// prunes targets that come back gone. A Watch loop turns session events into
// notifications on the awaiting-input transition.
package push

import (
	"context"
	"errors"
)

// Target is a registered device's Web Push subscription: the endpoint URL the
// gateway POSTs to, plus the subscription keys used to encrypt the payload
// (RFC 8291). The keys are empty for a plain (non-encrypted) endpoint.
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

// ErrGone marks a permanently dead target (HTTP 404/410) so the dispatcher prunes
// it. Senders wrap it for such responses.
var ErrGone = errors.New("push: target gone")

// Sender delivers one notification to one target.
type Sender interface {
	Send(ctx context.Context, t Target, n Notification) error
}
