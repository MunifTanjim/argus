package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/api"
)

// History RPC handlers serve past sessions discovered on disk (read-only). They
// ignore any node_id (the gateway uses it to route here); each scans this machine.
// History is not tied to a live session, so it routes to the default adapter;
// cross-adapter history aggregation is deferred until a second tool is added.

func (d *Node) handleHistoryProjects(context.Context, json.RawMessage) (any, error) {
	return d.adapterFor("").ListHistoryProjects()
}

func (d *Node) handleHistorySessions(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.HistorySessionsParams](params)
	if err != nil {
		return nil, err
	}
	if p.ProjectDir == "" {
		return nil, fmt.Errorf("historySessions: project_dir is required")
	}
	return d.adapterFor("").ListHistorySessions(p.ProjectDir, p.Limit, p.Offset)
}

func (d *Node) handleHistoryTranscript(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.HistoryTranscriptParams](params)
	if err != nil {
		return nil, err
	}
	if p.TranscriptPath == "" {
		return nil, fmt.Errorf("historyTranscript: transcript_path is required")
	}
	if p.AgentID != "" {
		v, found, err := d.adapterFor("").ReadHistorySubagentView(p.TranscriptPath, p.AgentID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown subagent: " + p.AgentID}
		}
		return v, nil
	}
	return d.adapterFor("").ReadHistoryTranscript(p.TranscriptPath)
}

func (d *Node) handleHistoryToolDetail(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.HistoryToolDetailParams](params)
	if err != nil {
		return nil, err
	}
	if p.TranscriptPath == "" {
		return nil, fmt.Errorf("historyToolDetail: transcript_path is required")
	}
	td, found, err := d.adapterFor("").FindHistoryToolDetail(p.TranscriptPath, p.AgentID, p.ToolID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown tool: " + p.ToolID}
	}
	return toAPIToolDetail(td), nil
}
