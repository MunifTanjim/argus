package node

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestHandleLockPinBadGenesisIsInvalidRequest(t *testing.T) {
	d, _ := newLockNode(t)
	raw, _ := json.Marshal(api.LockPinParams{Genesis: []byte{1, 2, 3}})

	_, err := d.handleLockPin(context.Background(), raw)

	var rpcErr *api.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want *api.RPCError, got %v", err)
	}
	if rpcErr.Code != api.CodeInvalidRequest {
		t.Fatalf("bad genesis: want CodeInvalidRequest, got %d", rpcErr.Code)
	}
}

func TestHandleLockPinInternalErrorMapsToInternal(t *testing.T) {
	d := New() // no trust chain path configured -> AdoptPin hits an internal error
	_, genesis, _ := buildGenesisChain(t)
	raw, _ := json.Marshal(api.LockPinParams{Genesis: genesis})

	_, err := d.handleLockPin(context.Background(), raw)

	var rpcErr *api.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want *api.RPCError, got %v", err)
	}
	if rpcErr.Code != api.CodeInternalError {
		t.Fatalf("path unset: want CodeInternalError, got %d", rpcErr.Code)
	}
}
