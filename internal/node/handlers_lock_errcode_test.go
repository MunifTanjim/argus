package node

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// corruptSignerPrivate keeps a valid public half but truncates the private half,
// modeling a signer whose invariant (both halves the right length) was broken.
func corruptSignerPrivate(sk trustlog.SignerKey) trustlog.SignerKey {
	return trustlog.SignerKey{Public: sk.Public, Private: sk.Private[:10]}
}

// assertInvalidRequestNoPanic requires call to return a CodeInvalidRequest
// RPCError rather than panicking inside ed25519.Sign on a short private key.
func assertInvalidRequestNoPanic(t *testing.T, call func() (any, error)) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler panicked on a short signer private key instead of returning a clean error: %v", r)
		}
	}()
	_, err := call()
	var rpcErr *api.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want *api.RPCError, got %v", err)
	}
	if rpcErr.Code != api.CodeInvalidRequest {
		t.Fatalf("want CodeInvalidRequest, got %d", rpcErr.Code)
	}
}

func TestHandleLockInitShortPrivateKeyIsInvalidRequest(t *testing.T) {
	d, sk := newSignerLockNode(t)
	d.SetSignerKey(corruptSignerPrivate(sk))
	raw, _ := json.Marshal(api.LockInitParams{Signers: [][]byte{sk.Public}})
	assertInvalidRequestNoPanic(t, func() (any, error) {
		return d.handleLockInit(context.Background(), raw)
	})
}

func TestLockDeviceShortPrivateKeyIsInvalidRequest(t *testing.T) {
	d, sk := newSignerLockNode(t)
	initLockNode(t, d, sk)
	d.SetSignerKey(corruptSignerPrivate(sk))
	dev, _ := trustlog.GenerateSigner()
	raw, _ := json.Marshal(api.LockDeviceParams{Device: dev.Public})
	assertInvalidRequestNoPanic(t, func() (any, error) {
		return d.handleLockSign(context.Background(), raw)
	})
}

func TestLockSignerShortPrivateKeyIsInvalidRequest(t *testing.T) {
	d, sk := newSignerLockNode(t)
	initLockNode(t, d, sk)
	d.SetSignerKey(corruptSignerPrivate(sk))
	newSigner, _ := trustlog.GenerateSigner()
	raw, _ := json.Marshal(api.LockSignerParams{Signer: newSigner.Public})
	assertInvalidRequestNoPanic(t, func() (any, error) {
		return d.handleLockAddSigner(context.Background(), raw)
	})
}

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
