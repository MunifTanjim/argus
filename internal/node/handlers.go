package node

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
)

func (d *Node) registerHandlers(srv *api.Server) {
	// ping is a no-op latency probe: round trip measures the connection, not the handler.
	srv.Handle(api.MethodPing, func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	srv.Handle(api.MethodSessionsList, d.handleSessionsList)
	srv.Handle(api.MethodNodeIdentify, d.handleNodeIdentify)
	srv.Handle(api.MethodServerInfo, d.handleServerInfo)
	srv.Handle(api.MethodSessionsRefresh, d.handleSessionsRefresh)
	srv.Handle(adapter.HookMethod, d.handleHook)
	srv.Handle(api.MethodSessionTranscriptView, d.handleTranscriptView)
	srv.Handle(api.MethodSessionToolDetail, d.handleSessionToolDetail)
	srv.Handle(api.MethodSessionCapture, d.handleSessionCapture)
	srv.Handle(api.MethodSessionInput, d.handleSessionInput)
	srv.Handle(api.MethodSessionKey, d.handleSessionKey)
	srv.Handle(api.MethodSessionRespond, d.handleSessionRespond)
	srv.Handle(api.MethodSessionSpawn, d.handleSessionSpawn)
	srv.Handle(api.MethodSessionResume, d.handleSessionResume)
	srv.Handle(api.MethodAgentsList, d.handleAgentsList)
	srv.Handle(api.MethodSessionKill, d.handleSessionKill)
	srv.Handle(api.MethodSessionFocus, d.handleSessionFocus)
	srv.Handle(api.MethodPushDesktop, d.handlePushDesktop)
	srv.Handle(api.MethodSessionsHistoryProjects, d.handleHistoryProjects)
	srv.Handle(api.MethodSessionsHistorySessions, d.handleHistorySessions)
	srv.Handle(api.MethodSessionsHistoryTranscript, d.handleHistoryTranscript)
	srv.Handle(api.MethodSessionHistoryToolDetail, d.handleHistoryToolDetail)
	srv.Handle(api.MethodTranscriptSubscribe, d.handleTranscriptSubscribe)
	srv.Handle(api.MethodTranscriptUnsubscribe, d.handleTranscriptUnsubscribe)
	srv.Handle(api.MethodTerminalOpen, d.handleTerminalOpen)
	srv.Handle(api.MethodTerminalInput, d.handleTerminalInput)
	srv.Handle(api.MethodTerminalResize, d.handleTerminalResize)
	srv.Handle(api.MethodTerminalClose, d.handleTerminalClose)
	srv.Handle(api.MethodSessionExport, d.handleExportBundle)
	srv.Handle(api.MethodSessionChangedFiles, d.handleChangedFiles)
	srv.Handle(api.MethodSessionFileDiff, d.handleFileDiff)
	srv.Handle(api.MethodSessionCommits, d.handleCommits)
	srv.Handle(api.MethodSessionCommitFiles, d.handleCommitFiles)
	srv.Handle(api.MethodLockInit, d.handleLockInit)
	srv.Handle(api.MethodLockStatus, d.handleLockStatus)
	srv.Handle(api.MethodLockSign, d.handleLockSign)
	srv.Handle(api.MethodLockRevoke, d.handleLockRevoke)
}
