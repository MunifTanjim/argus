package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/push"
)

func (d *Node) handlePushRegister(_ context.Context, params json.RawMessage) (any, error) {
	if d.pushStore == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "push notifications not enabled on this node"}
	}
	p, err := api.Decode[api.PushRegisterParams](params)
	if err != nil {
		return nil, err
	}
	if p.DeviceID == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "push: device_id required"}
	}
	if p.Endpoint == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "push.register: endpoint required"}
	}
	t := push.Target{Endpoint: p.Endpoint, P256dh: p.P256dh, Auth: p.Auth}
	if err := d.pushStore.Upsert(p.DeviceID, t); err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
	}
	return nil, nil
}

func (d *Node) handlePushUnregister(_ context.Context, params json.RawMessage) (any, error) {
	if d.pushStore == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "push notifications not enabled on this node"}
	}
	p, err := api.Decode[api.PushDeviceRef](params)
	if err != nil {
		return nil, err
	}
	if p.DeviceID == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "push: device_id required"}
	}
	if err := d.pushStore.Remove(p.DeviceID); err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
	}
	return nil, nil
}

func (d *Node) handlePushTest(ctx context.Context, params json.RawMessage) (any, error) {
	dvPtr := d.pushDeliverer.Load()
	if d.pushStore == nil || dvPtr == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "push notifications not enabled on this node"}
	}
	p, err := api.Decode[api.PushDeviceRef](params)
	if err != nil {
		return nil, err
	}
	if p.DeviceID == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "push: device_id required"}
	}
	disp := push.NewDispatcher(d.pushStore, push.NewRelaySender(*dvPtr), d.log)
	n := push.Notification{Title: "argus", Body: "Test notification — push is working.", Data: map[string]string{"test": "1"}}
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := disp.SendTo(sctx, p.DeviceID, n); err != nil {
		code := api.CodeInternalError
		if errors.Is(err, push.ErrGone) {
			code = api.CodePushGone
		}
		return nil, &api.RPCError{Code: code, Message: err.Error()}
	}
	return nil, nil
}

func (d *Node) handlePushVAPIDKey(_ context.Context, _ json.RawMessage) (any, error) {
	return api.PushVAPIDKey{}, nil // node holds no VAPID key; client fetches it from the gateway
}

// currentDeliverer loads the node's live push deliverer per call, so an uplink
// reconnect (which swaps the deliverer via SetPushDeliverer) is picked up without
// restarting Watch.
type currentDeliverer struct{ d *Node }

func (c currentDeliverer) Deliver(ctx context.Context, endpoint string, ciphertext []byte, ttl, urgency string) error {
	dv := c.d.pushDeliverer.Load()
	if dv == nil {
		return fmt.Errorf("push: no deliverer")
	}
	return (*dv).Deliver(ctx, endpoint, ciphertext, ttl, urgency)
}

// StartPush runs push.Watch over this node's own registry: desktop alerts render
// node-local, and (when a push store is set) mobile alerts are encrypted locally
// and handed to the current deliverer (read per delivery) after `delay`. Blocks
// until ctx is done; run it in a goroutine.
func (d *Node) StartPush(ctx context.Context, delay time.Duration) {
	events, cancel := d.reg.Subscribe()
	defer cancel()
	sinks := push.Sinks{Immediate: []push.Sink{d.DesktopSink()}, Delay: delay}
	if d.pushStore != nil {
		disp := push.NewDispatcher(d.pushStore, push.NewRelaySender(currentDeliverer{d}), d.log)
		sinks.Delayed = []push.Sink{disp}
	}
	push.Watch(ctx, events, sinks, d.log)
}

// uplinkDeliverer delivers encrypted mobile pushes over the node->gateway uplink via
// the push.deliver RPC.
type uplinkDeliverer struct{ peer *api.Peer }

func (u uplinkDeliverer) Deliver(ctx context.Context, endpoint string, ciphertext []byte, ttl, urgency string) error {
	params := api.PushDeliverParams{
		Endpoint:   endpoint,
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
		TTL:        ttl,
		Urgency:    urgency,
	}
	var res api.PushDeliverResult
	if err := u.peer.CallContext(ctx, api.MethodPushDeliver, params, &res); err != nil {
		return err
	}
	if res.Gone {
		return push.ErrGone
	}
	return nil
}
