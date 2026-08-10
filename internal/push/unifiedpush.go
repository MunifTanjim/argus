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
const unifiedPushTTL = "1800"

// unifiedPushUrgency maps to FCM priority; "high" wakes the device immediately
// instead of letting Doze batch the delivery.
const unifiedPushUrgency = "high"

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

// PostEncrypted POSTs a pre-encrypted aes128gcm Web Push body to endpoint, adding
// the VAPID Authorization header (nil vapid omits it). Returns ErrGone on 404/410.
// This is the blind-relay half: the caller supplies an opaque ciphertext it need
// not have produced, so a gateway can deliver a body a node encrypted.
func PostEncrypted(ctx context.Context, client *http.Client, vapid *VAPID, endpoint string, body []byte, ttl, urgency string) error {
	if ttl == "" {
		ttl = unifiedPushTTL
	}
	if urgency == "" {
		urgency = unifiedPushUrgency
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", ttl)
	req.Header.Set("Urgency", urgency)
	if vapid != nil {
		if auth, verr := vapid.authHeader(endpoint, time.Now()); verr == nil {
			req.Header.Set("Authorization", auth)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return fmt.Errorf("%w: %s %s", ErrGone, resp.Status, endpoint)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("push: POST %s: %s: %s", endpoint, resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}
