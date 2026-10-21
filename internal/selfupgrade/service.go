package selfupgrade

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

// Timeout bounds one server.upgrade. A client that waits for the reply must
// wait longer.
const Timeout = 5 * time.Minute

// Service's restart callback must stop the process so that it execs the
// binary on disk.
type Service struct {
	up           *Updater
	restart      func()
	restartDelay time.Duration
}

// NewService calls restart shortly after it replies to server.restart.
func NewService(up *Updater, restart func()) *Service {
	return &Service{up: up, restart: restart, restartDelay: 200 * time.Millisecond}
}

// Register on a nil Service registers nothing.
func (s *Service) Register(srv *api.Server) {
	if s == nil {
		return
	}
	srv.Handle(api.MethodServerCheckUpgrade, s.handleCheck)
	srv.Handle(api.MethodServerUpgrade, s.handleUpgrade)
	srv.Handle(api.MethodServerRestart, s.handleRestart)
}

func (s *Service) handleCheck(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.UpgradeParams](params)
	if err != nil {
		return nil, err
	}
	check := s.up.Check
	if p.Fresh {
		check = s.up.CheckFresh
	}
	c, err := check(ctx)
	if err != nil {
		return nil, err
	}
	return api.CheckUpgradeResult{Current: c.Current, Latest: c.Latest}, nil
}

// The download outlives the request: a client that disconnects must not
// leave the swap half done.
func (s *Service) handleUpgrade(ctx context.Context, _ json.RawMessage) (any, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), Timeout)
	defer cancel()
	from, to, err := s.up.Apply(ctx)
	if err != nil {
		return nil, err
	}
	return api.UpgradeResult{From: from, To: to}, nil
}

// The delay lets the reply reach the client before the shutdown closes the
// connection.
func (s *Service) handleRestart(context.Context, json.RawMessage) (any, error) {
	if !s.up.BeginRestart() {
		return nil, ErrBusy
	}
	time.AfterFunc(s.restartDelay, s.restart)
	return nil, nil
}
