package node

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
)

// registerHandlers wires the RPC handlers onto srv. The handler implementations
// live in handlers_session.go / handlers_hook.go / handlers_history.go.
func (d *Node) registerHandlers(srv *api.Server) {
	// ping is a no-op latency probe; it does no work so the round trip measures the
	// connection, not the handler.
	srv.Handle(api.MethodPing, func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	srv.Handle(api.MethodSessionsList, d.handleSessionsList)
	srv.Handle(api.MethodNodeIdentify, d.handleNodeIdentify)
	srv.Handle(api.MethodSessionsRefresh, d.handleSessionsRefresh)
	srv.Handle(claudecode.HookMethod, d.handleHook)
	srv.Handle(api.MethodSessionTranscriptView, d.handleTranscriptView)
	srv.Handle(api.MethodSessionToolDetail, d.handleSessionToolDetail)
	srv.Handle(api.MethodSessionCapture, d.handleSessionCapture)
	srv.Handle(api.MethodSessionInput, d.handleSessionInput)
	srv.Handle(api.MethodSessionKey, d.handleSessionKey)
	srv.Handle(api.MethodSessionRespond, d.handleSessionRespond)
	srv.Handle(api.MethodSessionSpawn, d.handleSessionSpawn)
	srv.Handle(api.MethodSessionKill, d.handleSessionKill)
	srv.Handle(api.MethodSessionFocus, d.handleSessionFocus)
	srv.Handle(api.MethodPushDesktop, d.handlePushDesktop)
	srv.Handle(api.MethodSessionsHistoryProjects, d.handleHistoryProjects)
	srv.Handle(api.MethodSessionsHistorySessions, d.handleHistorySessions)
	srv.Handle(api.MethodSessionsHistoryTranscript, d.handleHistoryTranscript)
	srv.Handle(api.MethodSessionHistoryToolDetail, d.handleHistoryToolDetail)
	srv.Handle(api.MethodTranscriptSubscribe, d.handleTranscriptSubscribe)
	srv.Handle(api.MethodTranscriptUnsubscribe, d.handleTranscriptUnsubscribe)
}
