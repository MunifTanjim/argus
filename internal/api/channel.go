package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/e2e"
)

// RelayFrame is a JSON-RPC frame on a relayed (gateway) link. A blind gateway
// reads the cleartext Method/ID/Route to route it and forwards Raw verbatim to the
// paired peer, never touching the sealed Body. An endpoint (client or node) uses
// Body with its Channel to decrypt the payload.
type RelayFrame struct {
	Method string
	ID     *json.RawMessage
	Route  RouteHeader
	Body   json.RawMessage
	Raw    []byte // the verbatim frame line, for a relay to forward unchanged
}

// Channel seals JSON-RPC payloads for one client<->node E2E channel. The routing
// header travels in cleartext so a blind gateway can relay by chan_id; the inner
// params/result/error travel in the sealed, opaque Body.
type Channel struct {
	id      string
	session *e2e.Session
}

// NewChannel binds a channel id to an established e2e session.
func NewChannel(id string, session *e2e.Session) *Channel {
	return &Channel{id: id, session: session}
}

// ID returns the channel id (the gateway's routing key).
func (c *Channel) ID() string { return c.id }

// seal encrypts inner JSON into the Body field: e2e-sealed bytes, base64-encoded
// as a JSON string so the result is valid json.RawMessage.
func (c *Channel) seal(inner json.RawMessage) (json.RawMessage, error) {
	sealed, err := c.session.Seal(inner)
	if err != nil {
		return nil, fmt.Errorf("api: channel seal: %w", err)
	}
	q, err := json.Marshal(base64.StdEncoding.EncodeToString(sealed))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(q), nil
}

// open reverses seal: base64-decode the Body JSON string, then e2e-open it.
func (c *Channel) open(body json.RawMessage) (json.RawMessage, error) {
	var enc string
	if err := json.Unmarshal(body, &enc); err != nil {
		return nil, fmt.Errorf("api: channel body not a string: %w", err)
	}
	sealed, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("api: channel body base64: %w", err)
	}
	inner, err := c.session.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("api: channel open: %w", err)
	}
	return json.RawMessage(inner), nil
}

// sealRequest builds a relay request frame: cleartext method/id/route, sealed params.
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

// sealNotification builds a relay notification frame. route lets the caller set
// SubID/TermID (cleartext demux handles); ChanID is always set to this channel.
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

// sealResponse builds a relay response frame with the result-or-error sealed in Body.
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

// OpenParams decrypts a relay request/notification frame's Body into its params.
func (c *Channel) OpenParams(f RelayFrame) (json.RawMessage, error) {
	return c.open(f.Body)
}

// OpenResponse decrypts a relay response frame's Body into its result or rpc error.
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

// SealRequestFrame seals a request into wire frame bytes ready for Peer.SendRawFrame.
func (c *Channel) SealRequestFrame(id *json.RawMessage, method, nodeID string, params json.RawMessage) ([]byte, error) {
	m, err := c.sealRequest(id, method, nodeID, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// SealResponseFrame seals a response (result or error) into wire frame bytes.
func (c *Channel) SealResponseFrame(id *json.RawMessage, result json.RawMessage, rpcErr *RPCError) ([]byte, error) {
	m, err := c.sealResponse(id, result, rpcErr)
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// SealNotificationFrame seals a notification into wire frame bytes. route lets the
// caller set SubID/TermID; ChanID is set to this channel.
func (c *Channel) SealNotificationFrame(method string, route RouteHeader, params json.RawMessage) ([]byte, error) {
	m, err := c.sealNotification(method, route, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// MethodE2EHandshake carries a Noise handshake message (msg1/msg2) as a relay
// frame Body over a channel, before any sealed app traffic.
const MethodE2EHandshake = "e2e.handshake"

// ChannelPrologue is the Noise prologue binding a channel to its node id and chan
// id. Client and node MUST derive it identically.
func ChannelPrologue(nodeID, chanID string) []byte {
	return []byte("argus-e2e/v1|" + nodeID + "|" + chanID)
}

// MarshalHandshakeFrame builds a relay frame carrying a raw Noise handshake message
// (unsealed — there is no session yet) for the given channel.
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

// HandshakeFromFrame extracts the raw Noise handshake bytes from a handshake frame.
func HandshakeFromFrame(f RelayFrame) ([]byte, error) {
	var enc string
	if err := json.Unmarshal(f.Body, &enc); err != nil {
		return nil, fmt.Errorf("api: handshake body: %w", err)
	}
	return base64.StdEncoding.DecodeString(enc)
}
