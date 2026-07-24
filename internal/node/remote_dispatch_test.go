package node

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

// lockMethods is the set of lock.* methods that must be blocked on remote surfaces.
var lockMethods = []string{
	api.MethodLockInit,
	api.MethodLockStatus,
	api.MethodLockSign,
	api.MethodLockRevoke,
	api.MethodLockAddSigner,
	api.MethodLockRemoveSigner,
	api.MethodLockDisable,
	api.MethodLockLocalDisable,
}

// TestRemoteDispatchRejectsLockMethods verifies that remoteDispatch returns
// CodeMethodNotFound for every lock.* method, while the underlying server
// dispatch (d.server.DispatchFunc()) still serves them.
func TestRemoteDispatchRejectsLockMethods(t *testing.T) {
	d := newNode(nil)
	remote := d.remoteDispatch()
	local := d.server.DispatchFunc()

	for _, method := range lockMethods {
		t.Run(method, func(t *testing.T) {
			_, err := remote(context.Background(), method, json.RawMessage("{}"))
			if err == nil {
				t.Fatalf("remoteDispatch(%q): expected error, got nil", method)
			}
			rpcErr, ok := err.(*api.RPCError)
			if !ok {
				t.Fatalf("remoteDispatch(%q): want *api.RPCError, got %T: %v", method, err, err)
			}
			if rpcErr.Code != api.CodeMethodNotFound {
				t.Fatalf("remoteDispatch(%q): Code = %d, want %d", method, rpcErr.Code, api.CodeMethodNotFound)
			}
			if !strings.Contains(rpcErr.Message, method) {
				t.Fatalf("remoteDispatch(%q): Message = %q, want it to contain the method name", method, rpcErr.Message)
			}

			// The local dispatch must still serve this method (no error from routing,
			// though it may return an application error for missing params).
			_, localErr := local(context.Background(), method, json.RawMessage("{}"))
			if localErr != nil {
				if rpc, ok := localErr.(*api.RPCError); ok && rpc.Code == api.CodeMethodNotFound {
					t.Fatalf("local dispatch(%q): method unexpectedly not found (should be registered)", method)
				}
			}
			// Any other error (e.g. "not locked") is fine — the method was reached.
		})
	}
}

// TestRemoteDispatchPassesThroughNonLock verifies that remoteDispatch forwards
// non-lock.* methods to the underlying server dispatch.
func TestRemoteDispatchPassesThroughNonLock(t *testing.T) {
	d := newNode(nil)
	remote := d.remoteDispatch()

	nonLockMethods := []string{
		api.MethodPing,
		api.MethodSessionsList,
		api.MethodNodeIdentify,
	}
	for _, method := range nonLockMethods {
		t.Run(method, func(t *testing.T) {
			_, err := remote(context.Background(), method, nil)
			if err != nil {
				if rpc, ok := err.(*api.RPCError); ok && rpc.Code == api.CodeMethodNotFound {
					t.Fatalf("remoteDispatch(%q): unexpectedly blocked (method-not-found)", method)
				}
			}
			// Any non-method-not-found result (including nil error or an app error) means
			// the call was forwarded — that's what we're checking.
		})
	}
}
