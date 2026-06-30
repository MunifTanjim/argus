package push

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// unifiedPushTTL is the Web Push message lifetime (seconds); push services require
// a TTL header and reject the request without one.
const unifiedPushTTL = "86400"

// messageID returns a random per-delivery id stamped into every payload so the
// client can dedup replays: the UnifiedPush Android plugin buffers recent events
// and re-emits them (same id) to a freshly attached engine on Activity relaunch.
func messageID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Uniqueness, not secrecy, is what matters; a timestamp suffices.
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// encodePayload marshals the device JSON body, stamping id for replay dedup (see messageID).
func encodePayload(n Notification, id string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"id":    id,
		"title": n.Title,
		"body":  n.Body,
		"data":  n.Data,
	})
}

// UnifiedPushSender POSTs to a device-provided distributor endpoint. With
// subscription keys it sends an encrypted Web Push request (RFC 8291) with TTL and
// VAPID auth (RFC 8292); without keys (legacy plain endpoint) a plain JSON POST.
// A 404/410 means the subscription is gone.
type UnifiedPushSender struct {
	client *http.Client
	vapid  *VAPID // signs the VAPID header for Web Push; nil disables it
}

// NewUnifiedPushSender returns a sender with a bounded HTTP timeout. nil vapid
// omits the VAPID header (Web Push services may then reject restricted subscriptions).
func NewUnifiedPushSender(v *VAPID) *UnifiedPushSender {
	return &UnifiedPushSender{client: &http.Client{Timeout: 10 * time.Second}, vapid: v}
}

func (u *UnifiedPushSender) Send(ctx context.Context, t Target, n Notification) error {
	payload, err := encodePayload(n, messageID())
	if err != nil {
		return err
	}

	req, err := u.buildRequest(ctx, t, payload)
	if err != nil {
		return err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return fmt.Errorf("%w: %s %s", ErrGone, resp.Status, t.Endpoint)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("push: unifiedpush POST %s: %s: %s", t.Endpoint, resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}

// buildRequest builds the POST: an encrypted Web Push request when the target has
// subscription keys, else a plain JSON POST.
func (u *UnifiedPushSender) buildRequest(ctx context.Context, t Target, payload []byte) (*http.Request, error) {
	if t.P256dh == "" || t.Auth == "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("TTL", unifiedPushTTL)
		return req, nil
	}

	body, err := encryptWebPush(t.P256dh, t.Auth, payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", unifiedPushTTL)
	if u.vapid != nil {
		if auth, verr := u.vapid.authHeader(t.Endpoint, time.Now()); verr == nil {
			req.Header.Set("Authorization", auth)
		}
	}
	return req, nil
}
