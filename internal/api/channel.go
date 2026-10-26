package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/fxamacker/cbor/v2"
)

// RelayFrame is a JSON-RPC frame on a relayed (gateway) link. A blind gateway
// routes it by Route.ChanID alone and forwards Raw verbatim to the paired peer,
// never reading the cleartext Method/ID or touching the sealed Body. Raw is the
// envelope without framing; Body is the JSON body token (JSON) or the sealed
// bytes (CBOR).
type RelayFrame struct {
	Enc    Encoding
	Method string
	ID     *json.RawMessage
	Route  RouteHeader
	Body   []byte
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

// seal returns the Body for enc: a quoted base64 string for JSON, the sealed
// bytes for CBOR.
func (c *Channel) seal(enc Encoding, inner []byte) (json.RawMessage, error) {
	sealed, err := c.cipher.Seal(inner)
	if err != nil {
		return nil, fmt.Errorf("api: channel seal: %w", err)
	}
	if enc == EncodingCBOR {
		return json.RawMessage(sealed), nil
	}
	q, err := json.Marshal(base64.StdEncoding.EncodeToString(sealed))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(q), nil
}

func (c *Channel) open(f RelayFrame) ([]byte, error) {
	sealed := f.Body
	if f.Enc != EncodingCBOR {
		var enc string
		if err := json.Unmarshal(f.Body, &enc); err != nil {
			return nil, fmt.Errorf("api: channel body not a string: %w", err)
		}
		var err error
		sealed, err = base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, fmt.Errorf("api: channel body base64: %w", err)
		}
	}
	inner, err := c.cipher.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("api: channel open: %w", err)
	}
	return inner, nil
}

// sealedResponse is the inner (sealed) shape of a response: exactly one of Result
// or Error. Kept inside Body so node error detail never appears in cleartext.
type sealedResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type cborSealedResponse struct {
	Result cbor.RawMessage `cbor:"result,omitempty"`
	Error  *RPCError       `cbor:"error,omitempty"`
}

// OpenParams returns the inner params bytes, encoded in f.Enc.
func (c *Channel) OpenParams(f RelayFrame) ([]byte, error) {
	return c.open(f)
}

// OpenResponse returns the inner result bytes, encoded in f.Enc.
func (c *Channel) OpenResponse(f RelayFrame) ([]byte, *RPCError, error) {
	inner, err := c.open(f)
	if err != nil {
		return nil, nil, err
	}
	if f.Enc == EncodingCBOR {
		var r cborSealedResponse
		if err := CBORUnmarshal(inner, &r); err != nil {
			return nil, nil, fmt.Errorf("api: response body: %w", err)
		}
		return r.Result, r.Error, nil
	}
	var r sealedResponse
	if err := json.Unmarshal(inner, &r); err != nil {
		return nil, nil, fmt.Errorf("api: response body: %w", err)
	}
	return r.Result, r.Error, nil
}

// SealRequestFrame output is ready for Peer.SendRawFrame(enc, frame). params is
// already encoded in enc.
func (c *Channel) SealRequestFrame(enc Encoding, id *json.RawMessage, method, nodeID string, params []byte) ([]byte, error) {
	body, err := c.seal(enc, params)
	if err != nil {
		return nil, err
	}
	return message{
		ID:     id,
		Method: method,
		Route:  &RouteHeader{ChanID: c.id, NodeID: nodeID},
		Body:   body,
		enc:    enc,
	}.marshal()
}

// SealResponseFrame output is ready for Peer.SendRawFrame(enc, frame). result is
// already encoded in enc.
func (c *Channel) SealResponseFrame(enc Encoding, id *json.RawMessage, result []byte, rpcErr *RPCError) ([]byte, error) {
	var inner []byte
	var err error
	if enc == EncodingCBOR {
		inner, err = CBORMarshal(cborSealedResponse{Result: cbor.RawMessage(result), Error: rpcErr})
	} else {
		inner, err = json.Marshal(sealedResponse{Result: result, Error: rpcErr})
	}
	if err != nil {
		return nil, err
	}
	body, err := c.seal(enc, inner)
	if err != nil {
		return nil, err
	}
	return message{
		ID:    id,
		Route: &RouteHeader{ChanID: c.id},
		Body:  body,
		enc:   enc,
	}.marshal()
}

// SealNotificationFrame overwrites route.ChanID with this channel; the caller sets
// only SubID/TermID. params is already encoded in enc.
func (c *Channel) SealNotificationFrame(enc Encoding, method string, route RouteHeader, params []byte) ([]byte, error) {
	body, err := c.seal(enc, params)
	if err != nil {
		return nil, err
	}
	route.ChanID = c.id
	return message{
		Method: method,
		Route:  &route,
		Body:   body,
		enc:    enc,
	}.marshal()
}

// ParseRelayFrame parses a frame payload (without framing) as a relay frame.
func ParseRelayFrame(enc Encoding, raw []byte) (RelayFrame, error) {
	m, err := unmarshalMessage(enc, raw)
	if err != nil {
		return RelayFrame{}, fmt.Errorf("api: parse relay frame: %w", err)
	}
	if !m.isRelay() {
		return RelayFrame{}, fmt.Errorf("api: not a relay frame")
	}
	return m.relayFrame(append([]byte(nil), raw...)), nil
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
