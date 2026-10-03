package node

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/host"
)

// SetWakelockPath persists the wakelock at path so it survives a restart.
// Call before Run.
func (d *Node) SetWakelockPath(path string) { d.wakelock = host.NewWakelock(path) }

func (d *Node) handleHostInfo(ctx context.Context, _ json.RawMessage) (any, error) {
	uptime, err := host.Uptime()
	if err != nil {
		return nil, err
	}
	battery, err := host.Battery(ctx)
	if err != nil {
		return nil, err
	}
	return api.HostInfo{OS: host.OS(), UptimeSeconds: uptime, Battery: battery, Wakelock: d.wakelock.State()}, nil
}

func (d *Node) handleHostSetWakelock(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.HostSetWakelockParams](params)
	if err != nil {
		return nil, err
	}
	w, err := d.wakelock.Set(p.Until)
	if err != nil {
		return nil, wakelockRPCError(err)
	}
	return w, nil
}

func wakelockRPCError(err error) error {
	code := api.CodeInternalError
	if errors.Is(err, host.ErrInvalidUntil) || errors.Is(err, host.ErrWakelockUnsupported) {
		code = api.CodeInvalidRequest
	}
	return &api.RPCError{Code: code, Message: err.Error()}
}
