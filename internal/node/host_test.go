package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/host"
)

func TestHandleHostInfo(t *testing.T) {
	d := newTestNode(t)
	out, err := d.DispatchFunc()(context.Background(), api.MethodHostInfo, nil)
	if err != nil {
		t.Fatal(err)
	}
	info, ok := out.(api.HostInfo)
	if !ok {
		t.Fatalf("result %T", out)
	}
	if info.UptimeSeconds < 0 {
		t.Fatalf("uptime %d", info.UptimeSeconds)
	}
	if info.OS == "" {
		t.Fatal("host.info must report the OS")
	}
}

func TestHandleHostSetWakelockRejectsPast(t *testing.T) {
	d := newTestNode(t)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	params, _ := json.Marshal(api.HostSetWakelockParams{Until: past})
	_, err := d.DispatchFunc()(context.Background(), api.MethodHostSetWakelock, params)
	var rpcErr *api.RPCError
	if err == nil {
		t.Fatal("past until must fail")
	}
	if !errors.As(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Fatalf("err %v, want RPCError CodeInvalidRequest", err)
	}
}

func TestCapabilitiesReportWakelock(t *testing.T) {
	d := newTestNode(t)
	_, lookErr := exec.LookPath("caffeinate")
	want := runtime.GOOS == "darwin" && lookErr == nil
	if got := d.Capabilities().HostWakelock; got != want {
		t.Fatalf("caps.HostWakelock = %v, want %v", got, want)
	}
}

func TestWakelockRPCErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{fmt.Errorf("%w: x", host.ErrInvalidUntil), api.CodeInvalidRequest},
		{host.ErrWakelockUnsupported, api.CodeInvalidRequest},
		{errors.New("exec: caffeinate failed"), api.CodeInternalError},
	} {
		var rpcErr *api.RPCError
		if !errors.As(wakelockRPCError(tc.err), &rpcErr) || rpcErr.Code != tc.code || rpcErr.Message != tc.err.Error() {
			t.Errorf("wakelockRPCError(%v) = %v, want code %d", tc.err, rpcErr, tc.code)
		}
	}
}
