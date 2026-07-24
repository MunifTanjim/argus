package push

import (
	"context"
	"net/http"
	"time"
)

// Deliverer hands an opaque, pre-encrypted Web Push body to whatever performs the
// actual HTTP egress (a co-located gateway in-process, or a remote gateway over the
// uplink). The node encrypts; the deliverer only signs + POSTs, so the gateway
// never sees cleartext. Returns ErrGone when the subscription is permanently dead.
type Deliverer interface {
	Deliver(ctx context.Context, endpoint string, ciphertext []byte, ttl, urgency string) error
}

// Encrypt composes the device JSON payload (with a per-delivery dedup id) and
// encrypts it for the target's subscription keys (RFC 8291). The result is an
// opaque aes128gcm body ready to POST.
func Encrypt(t Target, n Notification) ([]byte, error) {
	payload, err := encodePayload(n, messageID())
	if err != nil {
		return nil, err
	}
	return encryptWebPush(t.P256dh, t.Auth, payload)
}

// relaySender encrypts node-side then hands the opaque body to a Deliverer. It is
// a push.Sender, so it drops into the existing Dispatcher (which prunes ErrGone).
type relaySender struct{ deliver Deliverer }

// NewRelaySender returns a Sender that encrypts locally and delivers via d.
func NewRelaySender(d Deliverer) Sender { return relaySender{deliver: d} }

func (r relaySender) Send(ctx context.Context, t Target, n Notification) error {
	body, err := Encrypt(t, n)
	if err != nil {
		return err
	}
	return r.deliver.Deliver(ctx, t.Endpoint, body, unifiedPushTTL, unifiedPushUrgency)
}

// GatewayDeliverer POSTs pre-encrypted bodies via the gateway's VAPID key and HTTP
// client — the in-process (co-located gateway) Deliverer, and the engine behind the
// push.deliver RPC handler.
type GatewayDeliverer struct {
	client *http.Client
	vapid  *VAPID
}

// NewGatewayDeliverer returns a GatewayDeliverer signing with v (may be nil).
func NewGatewayDeliverer(v *VAPID) *GatewayDeliverer {
	return &GatewayDeliverer{client: &http.Client{Timeout: 10 * time.Second}, vapid: v}
}

func (g *GatewayDeliverer) Deliver(ctx context.Context, endpoint string, ciphertext []byte, ttl, urgency string) error {
	return PostEncrypted(ctx, g.client, g.vapid, endpoint, ciphertext, ttl, urgency)
}
