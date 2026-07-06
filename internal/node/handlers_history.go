package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// History RPC handlers serve past sessions discovered on disk (read-only). They
// ignore any node_id (the gateway uses it to route here); each scans this machine.

func (d *Node) handleHistoryProjects(context.Context, json.RawMessage) (any, error) {
	lists := make([][]session.HistoryProject, 0, len(d.adapterList))
	for _, a := range d.adapterList {
		ps, err := a.ListHistoryProjects()
		if err != nil {
			d.log.Warn("history projects", "agent", a.Agent(), "err", err)
			continue
		}
		lists = append(lists, ps)
	}
	return mergeProjects(lists), nil
}

func (d *Node) handleHistorySessions(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.HistorySessionsParams](params)
	if err != nil {
		return nil, err
	}
	// An empty ProjectDir is the "(unknown)" bucket (workspace-less sessions),
	// not a missing param.
	var all []session.HistorySession
	for _, a := range d.adapterList {
		page, err := a.ListHistorySessions(p.ProjectDir, 0, 0) // all; node paginates
		if err != nil {
			d.log.Warn("history sessions", "agent", a.Agent(), "err", err)
			continue
		}
		for i := range page.Items {
			page.Items[i].Agent = a.Agent()
		}
		all = append(all, page.Items...)
	}
	return mergeSessions(all, p.Offset, p.Limit), nil
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
		v, found, err := d.adapterFor(p.Agent).ReadHistorySubagentView(p.TranscriptPath, p.AgentID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown subagent: " + p.AgentID}
		}
		return v, nil
	}
	return d.adapterFor(p.Agent).ReadHistoryTranscript(p.TranscriptPath)
}

func (d *Node) handleHistoryToolDetail(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.HistoryToolDetailParams](params)
	if err != nil {
		return nil, err
	}
	if p.TranscriptPath == "" {
		return nil, fmt.Errorf("historyToolDetail: transcript_path is required")
	}
	td, found, err := d.adapterFor(p.Agent).FindHistoryToolDetail(p.TranscriptPath, p.AgentID, p.ToolID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown tool: " + p.ToolID}
	}
	return toAPIToolDetail(td), nil
}
