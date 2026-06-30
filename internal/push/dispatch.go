package push

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Dispatcher delivers notifications to registered device targets via the sender,
// pruning targets that come back gone.
type Dispatcher struct {
	store  *Store
	sender Sender
	log    *slog.Logger
}

// NewDispatcher returns a dispatcher backed by store and sender. log may be nil.
func NewDispatcher(store *Store, sender Sender, log *slog.Logger) *Dispatcher {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Dispatcher{store: store, sender: sender, log: log}
}

// Send delivers n to every registered device. Best-effort: per-device failures
// are logged and skipped, gone targets pruned, so one bad device never blocks the rest.
func (d *Dispatcher) Send(ctx context.Context, n Notification) {
	recs, err := d.store.List()
	if err != nil {
		d.log.Warn("push: list targets", "err", err)
		return
	}
	d.log.Info("push: delivering", "devices", len(recs))
	for _, rec := range recs {
		sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := d.sender.Send(sctx, rec.Target, n)
		cancel()
		switch {
		case err == nil:
		case errors.Is(err, ErrGone):
			d.log.Info("push: pruned gone target")
			_ = d.store.Remove(rec.DeviceID)
		default:
			d.log.Warn("push: send failed", "err", err)
		}
	}
}

// Notify makes *Dispatcher a Sink, delivering to every registered device.
func (d *Dispatcher) Notify(ctx context.Context, n Notification) { d.Send(ctx, n) }

// SendTo delivers n to a single device (e.g. the test endpoint), pruning the
// device when its target is gone.
func (d *Dispatcher) SendTo(ctx context.Context, deviceID string, n Notification) error {
	t, ok, err := d.store.Get(deviceID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("push: no registration for device")
	}
	err = d.sender.Send(ctx, t, n)
	if errors.Is(err, ErrGone) {
		_ = d.store.Remove(deviceID)
	}
	return err
}
