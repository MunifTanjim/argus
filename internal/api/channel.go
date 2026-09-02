package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/e2e"
)

// RelayFrame is a JSON-RPC frame on a relayed (gateway) link. A blind gateway
// routes it by Route.ChanID alone and forwards Raw verbatim to the paired peer,
// never reading the cleartext Method/ID or touching the sealed Body.
type RelayFrame struct {
	Method string
	ID     *json.RawMessage
	Route  RouteHeader
	Body   json.RawMessage
	Raw    []byte
}

// Cipher seals and opens channel payloads. Noise for E2EE; identity for plaintext.
type Cipher interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

type noiseCipher struct{ s *e2e.Session }

func (c noiseCipher) Seal(p []byte) ([]byte, error) { return c.s.Seal(p) }
func (c noiseCipher) Open(b []byte) ([]byte, error) { return c.s.Open(b) }

type identityCipher struct{}

func (identityCipher) Seal(p []byte) ([]byte, error) { return p, nil }
func (identityCipher) Open(b []byte) ([]byte, error) { return b, nil }

// Channel seals JSON-RPC payloads for one client<->node channel. The routing
// header travels in cleartext so a blind gateway can relay by chan_id; the inner
// params/result/error travel in the sealed, opaque Body.
type Channel struct {
	id     string
	cipher Cipher
}

func NewChannel(id string, session *e2e.Session) *Channel {
	return &Channel{id: id, cipher: noiseCipher{s: session}}
}

func NewPlainChannel(id string) *Channel {
	return &Channel{id: id, cipher: identityCipher{}}
}

func (c *Channel) ID() string { return c.id }

func (c *Channel) seal(inner json.RawMessage) (json.RawMessage, error) {
	sealed, err := c.cipher.Seal(inner)
	if err != nil {
		return nil, fmt.Errorf("api: channel seal: %w", err)
	}
	q, err := json.Marshal(base64.StdEncoding.EncodeToString(sealed))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(q), nil
}

func (c *Channel) open(body json.RawMessage) (json.RawMessage, error) {
	var enc string
	if err := json.Unmarshal(body, &enc); err != nil {
		return nil, fmt.Errorf("api: channel body not a string: %w", err)
	}
	sealed, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("api: channel body base64: %w", err)
	}
	inner, err := c.cipher.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("api: channel open: %w", err)
	}
	return json.RawMessage(inner), nil
}

func (c *Channel) sealRequest(id *json.RawMessage, method, nodeID string, params json.RawMessage) (message, error) {
	body, err := c.seal(params)
	if err != nil {
		return message{}, err
	}
	return message{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Method:  method,
		Route:   &RouteHeader{ChanID: c.id, NodeID: nodeID},
		Body:    body,
	}, nil
}

// sealNotification always overwrites route.ChanID with this channel; the caller
// sets only SubID/TermID.
func (c *Channel) sealNotification(method string, route RouteHeader, params json.RawMessage) (message, error) {
	body, err := c.seal(params)
	if err != nil {
		return message{}, err
	}
	route.ChanID = c.id
	return message{
		JSONRPC: jsonrpcVersion,
		Method:  method,
		Route:   &route,
		Body:    body,
	}, nil
}

// sealedResponse is the inner (sealed) shape of a response: exactly one of Result
// or Error. Kept inside Body so node error detail never appears in cleartext.
type sealedResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

func (c *Channel) sealResponse(id *json.RawMessage, result json.RawMessage, rpcErr *RPCError) (message, error) {
	inner, err := json.Marshal(sealedResponse{Result: result, Error: rpcErr})
	if err != nil {
		return message{}, err
	}
	body, err := c.seal(inner)
	if err != nil {
		return message{}, err
	}
	return message{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Route:   &RouteHeader{ChanID: c.id},
		Body:    body,
	}, nil
}

func (c *Channel) OpenParams(f RelayFrame) (json.RawMessage, error) {
	return c.open(f.Body)
}

func (c *Channel) OpenResponse(f RelayFrame) (json.RawMessage, *RPCError, error) {
	inner, err := c.open(f.Body)
	if err != nil {
		return nil, nil, err
	}
	var r sealedResponse
	if err := json.Unmarshal(inner, &r); err != nil {
		return nil, nil, fmt.Errorf("api: response body: %w", err)
	}
	return r.Result, r.Error, nil
}

// SealRequestFrame output is ready for Peer.SendRawFrame.
func (c *Channel) SealRequestFrame(id *json.RawMessage, method, nodeID string, params json.RawMessage) ([]byte, error) {
	m, err := c.sealRequest(id, method, nodeID, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

func (c *Channel) SealResponseFrame(id *json.RawMessage, result json.RawMessage, rpcErr *RPCError) ([]byte, error) {
	m, err := c.sealResponse(id, result, rpcErr)
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// SealNotificationFrame overwrites route.ChanID with this channel; the caller sets
// only SubID/TermID.
func (c *Channel) SealNotificationFrame(method string, route RouteHeader, params json.RawMessage) ([]byte, error) {
	m, err := c.sealNotification(method, route, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// ParseRelayFrame mirrors the RelayFrame construction in Peer's read loop; keep
// them in sync.
func ParseRelayFrame(line []byte) (RelayFrame, error) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		return RelayFrame{}, fmt.Errorf("api: parse relay frame: %w", err)
	}
	if !m.isRelay() {
		return RelayFrame{}, fmt.Errorf("api: not a relay frame")
	}
	return RelayFrame{
		Method: m.Method,
		ID:     m.ID,
		Route:  *m.Route,
		Body:   m.Body,
		Raw:    append([]byte(nil), line...),
	}, nil
}

// MethodE2EHandshake carries a Noise handshake message (msg1/msg2) as a relay
// frame Body over a channel, before any sealed app traffic.
const MethodE2EHandshake = "e2e.handshake"

// ChannelPrologue is the Noise prologue binding a channel to its node id and chan
// id. Client and node MUST derive it identically.
func ChannelPrologue(nodeID, chanID string) []byte {
	return []byte("argus-e2e/v1|" + nodeID + "|" + chanID)
}

// MarshalHandshakeFrame carries a raw Noise handshake message unsealed — there is
// no session yet.
func MarshalHandshakeFrame(chanID string, handshake []byte) ([]byte, error) {
	body, err := json.Marshal(base64.StdEncoding.EncodeToString(handshake))
	if err != nil {
		return nil, err
	}
	return json.Marshal(message{
		JSONRPC: jsonrpcVersion,
		Method:  MethodE2EHandshake,
		Route:   &RouteHeader{ChanID: chanID},
		Body:    json.RawMessage(body),
	})
}

func HandshakeFromFrame(f RelayFrame) ([]byte, error) {
	var enc string
	if err := json.Unmarshal(f.Body, &enc); err != nil {
		return nil, fmt.Errorf("api: handshake body: %w", err)
	}
	return base64.StdEncoding.DecodeString(enc)
}
