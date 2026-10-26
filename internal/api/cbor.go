package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"

	"github.com/fxamacker/cbor/v2"
)

var (
	// EncMode and DecMode are the CBOR modes for all frames. Decoding is
	// bounded since frames come from untrusted peers.
	EncMode cbor.EncMode
	DecMode cbor.DecMode
)

func init() {
	var err error
	EncMode, err = cbor.EncOptions{}.EncMode()
	if err != nil {
		panic(err)
	}
	DecMode, err = cbor.DecOptions{
		MaxNestedLevels:  16,
		MaxArrayElements: 65536,
		MaxMapPairs:      65536,
		IndefLength:      cbor.IndefLengthForbidden,
		DupMapKey:        cbor.DupMapKeyEnforcedAPF,
		DefaultMapType:   reflect.TypeOf(map[string]any(nil)),
	}.DecMode()
	if err != nil {
		panic(err)
	}
}

func CBORMarshal(v any) ([]byte, error) { return EncMode.Marshal(v) }

func CBORUnmarshal(b []byte, v any) error { return DecMode.Unmarshal(b, v) }

// CBORToJSON converts a CBOR value to JSON; byte strings become base64.
func CBORToJSON(b []byte) (json.RawMessage, error) {
	var v any
	if err := CBORUnmarshal(b, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// cborMessage is the CBOR form of message; Body carries raw bytes.
type cborMessage struct {
	JSONRPC string          `cbor:"jsonrpc"`
	ID      *uint64         `cbor:"id,omitempty"`
	Method  string          `cbor:"method,omitempty"`
	Route   *RouteHeader    `cbor:"route,omitempty"`
	Body    []byte          `cbor:"body,omitempty"`
	Params  cbor.RawMessage `cbor:"params,omitempty"`
	Result  cbor.RawMessage `cbor:"result,omitempty"`
	Error   *RPCError       `cbor:"error,omitempty"`
}

// Marshal encodes v in e.
func (e Encoding) Marshal(v any) ([]byte, error) {
	if e == EncodingCBOR {
		return CBORMarshal(v)
	}
	return json.Marshal(v)
}

// Unmarshal decodes b, encoded in e, into v.
func (e Encoding) Unmarshal(b []byte, v any) error {
	if e == EncodingCBOR {
		return CBORUnmarshal(b, v)
	}
	return json.Unmarshal(b, v)
}

// EncodeParams marshals params as CBOR for a CBOR method when cborOK, otherwise
// as JSON. A json.RawMessage is always sent as JSON; nil params encode to nil.
func EncodeParams(method string, params any, cborOK bool) (Encoding, []byte, error) {
	if params == nil {
		return EncodingJSON, nil, nil
	}
	enc := EncodingJSON
	if _, raw := params.(json.RawMessage); !raw && CBORMethods[method] && cborOK {
		enc = EncodingCBOR
	}
	b, err := enc.Marshal(params)
	return enc, b, err
}

// marshal encodes m as a frame payload in m.enc.
func (m message) marshal() ([]byte, error) {
	m.JSONRPC = jsonrpcVersion
	if m.enc != EncodingCBOR {
		return json.Marshal(m)
	}
	cm := cborMessage{
		JSONRPC: m.JSONRPC,
		Method:  m.Method,
		Route:   m.Route,
		Body:    m.Body,
		Params:  cbor.RawMessage(m.Params),
		Result:  cbor.RawMessage(m.Result),
		Error:   m.Error,
	}
	if m.ID != nil {
		id, err := strconv.ParseUint(string(*m.ID), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("api: cbor id %s: %w", *m.ID, err)
		}
		cm.ID = &id
	}
	return CBORMarshal(cm)
}

// unmarshalMessage decodes a frame payload; a CBOR id becomes decimal JSON.
func unmarshalMessage(enc Encoding, payload []byte) (message, error) {
	var m message
	if enc != EncodingCBOR {
		err := json.Unmarshal(payload, &m)
		return m, err
	}
	var cm cborMessage
	if err := CBORUnmarshal(payload, &cm); err != nil {
		return m, err
	}
	m = message{
		JSONRPC: cm.JSONRPC,
		Method:  cm.Method,
		Route:   cm.Route,
		Body:    cm.Body,
		Params:  json.RawMessage(cm.Params),
		Result:  json.RawMessage(cm.Result),
		Error:   cm.Error,
		enc:     EncodingCBOR,
	}
	if cm.ID != nil {
		id := json.RawMessage(strconv.FormatUint(*cm.ID, 10))
		m.ID = &id
	}
	return m, nil
}

// relayFrame builds the RelayFrame for a relay message decoded from raw.
func (m *message) relayFrame(raw []byte) RelayFrame {
	return RelayFrame{
		Enc:    m.enc,
		Method: m.Method,
		ID:     m.ID,
		Route:  *m.Route,
		Body:   m.Body,
		Raw:    raw,
	}
}
