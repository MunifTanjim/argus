package mcphttp

import (
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/api"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *api.RPCError   `json:"error,omitempty"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type callParams struct {
	Name      string                     `json:"name"`
	Arguments json.RawMessage            `json:"arguments"`
	Meta      map[string]json.RawMessage `json:"_meta"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResponse struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}
