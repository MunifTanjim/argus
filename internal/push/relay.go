package push

import (
	"context"
	"net/http"
	"net/url"
	"sync"
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

type relaySender struct{ deliver Deliverer }

func NewRelaySender(d Deliverer) Sender { return relaySender{deliver: d} }

func (r relaySender) Send(ctx context.Context, t Target, n Notification) error {
	body, err := Encrypt(t, n)
	if err != nil {
		return err
	}
	return r.deliver.Deliver(ctx, t.Endpoint, body, unifiedPushTTL, unifiedPushUrgency)
}

// PushPortAuth holds the credentials for delivering to a sealed PushPort endpoint.
type PushPortAuth struct {
	Host  string
	Token string
}

// GatewayDeliverer POSTs pre-encrypted bodies via the gateway's VAPID key and HTTP
// client — the in-process (co-located gateway) Deliverer, and the engine behind the
// push.deliver RPC handler.
type GatewayDeliverer struct {
	client *http.Client
	vapid  *VAPID

	mu      sync.RWMutex
	ppHost  string
	ppToken string
}

// NewGatewayDeliverer returns a GatewayDeliverer signing with v (may be nil). pp
// configures PushPort bearer-token delivery (may be nil).
func NewGatewayDeliverer(v *VAPID, pp *PushPortAuth) *GatewayDeliverer {
	g := &GatewayDeliverer{client: &http.Client{Timeout: 10 * time.Second}, vapid: v}
	if pp != nil {
		g.ppHost, g.ppToken = pp.Host, pp.Token
	}
	return g
}

func (g *GatewayDeliverer) Deliver(ctx context.Context, endpoint string, ciphertext []byte, ttl, urgency string) error {
	g.mu.RLock()
	host, token := g.ppHost, g.ppToken
	g.mu.RUnlock()
	if token != "" && sameHost(endpoint, host) {
		return PostToPushPort(ctx, g.client, token, endpoint, ciphertext, ttl, urgency)
	}
	return PostEncrypted(ctx, g.client, g.vapid, endpoint, ciphertext, ttl, urgency)
}

// HasPushPort reports whether a PushPort instance token is currently set.
func (g *GatewayDeliverer) HasPushPort() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.ppToken != ""
}

// SetPushPort updates the bearer credentials used for sealed PushPort endpoints.
// An empty token disables bearer delivery (VAPID is used instead).
func (g *GatewayDeliverer) SetPushPort(host, token string) {
	g.mu.Lock()
	g.ppHost, g.ppToken = host, token
	g.mu.Unlock()
}

func sameHost(endpoint, host string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && u.Host == host
}
