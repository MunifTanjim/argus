package api

import (
	"context"
	"encoding/json"
	"time"
)

// StopDelay lets the reply reach the client before the shutdown closes the
// connection.
var StopDelay = 200 * time.Millisecond

// HandleStop serves server.stop on srv. Serve it only on local sockets.
func HandleStop(srv *Server, stop func()) {
	srv.Handle(MethodServerStop, func(context.Context, json.RawMessage) (any, error) {
		time.AfterFunc(StopDelay, stop)
		return nil, nil
	})
}
