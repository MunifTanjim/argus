package node

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/agentgit"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/pagent"
)

const (
	evHealth = "health"

	healthCLIMissing    = "cli-missing"
	healthSpawnFailures = "spawn-failures"
	healthPausedPrefix  = "paused:"

	// spawnFailuresNotice is how many spawn or resume failures in a row raise
	// a health notice.
	spawnFailuresNotice = 3
)

// cliInstalled reports whether the agent CLI that ad launches is on PATH.
func (d *Node) cliInstalled(ad adapter.Adapter) (string, bool) {
	name, _ := ad.SpawnCommand(adapter.SpawnOptions{})
	if _, headless := ad.(adapter.Spawner); headless || name == "" {
		return name, true
	}
	if d.lookPath != nil {
		return name, d.lookPath(name)
	}
	_, err := exec.LookPath(name)
	return name, err == nil
}

// healthNotice records a health notice: a health event in the agent's latest
// chat thread and a push, both at most once per key until resolved, then
// every healthRepush.
func (d *Node) healthNotice(ctx context.Context, a pagent.Agent, key, message string) {
	k := a.ID + "/" + key
	now := d.pagentNow()
	d.notify.mu.Lock()
	last, seen := d.notify.health[k]
	d.notify.mu.Unlock()
	if seen && now.Sub(last) < healthRepush {
		return
	}
	d.agentNotice(ctx, a, evHealth, map[string]string{"key": key, "message": message})
	threadID := ""
	if th, ok, err := d.pagents.LatestThread(ctx, a.ID, threadChat); err == nil && ok {
		threadID = th.ID
	}
	d.pagentHealth(ctx, a, threadID, key, a.Name+": "+message, "")
}

// noteSpawn counts spawn and resume failures of an agent in a row; the
// third raises a health notice, a success resolves it.
func (d *Node) noteSpawn(ctx context.Context, a pagent.Agent, err error) {
	d.notify.mu.Lock()
	if d.notify.spawnFails == nil {
		d.notify.spawnFails = map[string]int{}
	}
	if err == nil {
		delete(d.notify.spawnFails, a.ID)
		d.notify.mu.Unlock()
		d.pagentHealthResolved(a, healthSpawnFailures)
		return
	}
	d.notify.spawnFails[a.ID]++
	n := d.notify.spawnFails[a.ID]
	d.notify.mu.Unlock()
	if n >= spawnFailuresNotice {
		d.healthNotice(ctx, a, healthSpawnFailures, fmt.Sprintf("sessions failed to start %d times in a row; last: %v", n, err))
	}
}

// pausedHealth pushes a health notice for a routine paused by fatigue, budget
// or absence; the timeline already has the routine.paused event.
func (d *Node) pausedHealth(ctx context.Context, a pagent.Agent, slug, reason string) {
	threadID := ""
	if th, ok, err := d.pagents.LatestThread(ctx, a.ID, threadChat); err == nil && ok {
		threadID = th.ID
	}
	d.pagentHealth(ctx, a, threadID, healthPausedPrefix+slug, fmt.Sprintf("%s: routine %s paused (%s)", a.Name, slug, reason), "")
}

func (d *Node) handlePAgentUsage(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentUsageParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	a, err := d.pagentFind(ctx, s, p.AgentID)
	if err != nil {
		return nil, err
	}
	rows := map[string]*api.PAgentUsageRow{}
	row := func(by string) *api.PAgentUsageRow {
		if rows[by] == nil {
			rows[by] = &api.PAgentUsageRow{StartedBy: by}
		}
		return rows[by]
	}
	runs, err := s.AgentRunsSince(ctx, a.ID, p.Since)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	for _, run := range runs {
		if run.ThreadID == "" {
			continue // never got a session
		}
		r := row(run.StartedBy)
		r.Sessions++
		r.Cost += run.Cost
		r.Tokens += run.Tokens
	}
	// Chat sessions: those started since, with the measures of live ones.
	threads, err := s.ListThreads(ctx, a.ID)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	for _, th := range threads {
		if th.Kind != threadChat {
			continue
		}
		links, err := s.ThreadSessions(ctx, th.ID)
		if err != nil {
			continue
		}
		for _, l := range links {
			if !p.Since.IsZero() && l.StartedAt.Before(p.Since) {
				continue
			}
			r := row(th.StartedBy)
			r.Sessions++
			if sess, ok := d.linkSession(l); ok && sess.Summary != nil {
				if sess.Summary.HasCost {
					r.Cost += sess.Summary.CostUSD
				}
				r.Tokens += int64(sess.Summary.Tokens)
			}
		}
	}
	res := api.PAgentUsageResult{AgentID: a.ID, Since: p.Since, Rows: []api.PAgentUsageRow{}, Total: api.PAgentUsageRow{StartedBy: "total"}}
	for _, by := range slices.Sorted(maps.Keys(rows)) {
		r := *rows[by]
		res.Rows = append(res.Rows, r)
		res.Total.Sessions += r.Sessions
		res.Total.Cost += r.Cost
		res.Total.Tokens += r.Tokens
	}
	return res, nil
}

// settingValues are the values each agent setting accepts; nil accepts any
// value its parser does.
var settingValues = map[string][]string{
	"notify":        {"all", "needs-you", "off"},
	"proactivity":   {"off", "low", "default", "high"},
	"memory.mode":   {"auto", "review", "off"},
	"quiet-hours":   nil,
	"brief.enabled": {"true", "false"},
	"brief.at":      nil,
}

func applySetting(def *pagent.Definition, key, value string) error {
	if allowed, ok := settingValues[key]; !ok {
		keys := slices.Sorted(maps.Keys(settingValues))
		return fmt.Errorf("unknown setting %q; one of %s", key, strings.Join(keys, ", "))
	} else if value != "" && allowed != nil && !slices.Contains(allowed, value) {
		return fmt.Errorf("%s must be one of %s", key, strings.Join(allowed, ", "))
	}
	switch key {
	case "notify":
		def.Notify = value
	case "proactivity":
		def.Proactivity = value
	case "memory.mode":
		def.Memory.Mode = value
	case "quiet-hours":
		if value != "" {
			if _, _, err := config.ParseQuietHours(value); err != nil {
				return err
			}
		}
		def.QuietHours = value
	case "brief.enabled", "brief.at":
		if def.Brief == nil {
			def.Brief = &pagent.BriefDef{}
		}
		if key == "brief.at" {
			if value != "" {
				if _, err := time.Parse("15:04", value); err != nil {
					return fmt.Errorf("brief.at must be a time such as 08:00")
				}
			}
			def.Brief.At = value
		} else if value == "" {
			def.Brief.Enabled = nil
		} else {
			on, _ := strconv.ParseBool(value)
			def.Brief.Enabled = &on
		}
		if def.Brief.Enabled == nil && def.Brief.At == "" {
			def.Brief = nil
		}
	}
	return nil
}

func (d *Node) handlePAgentSet(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentSetParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	a, err := d.pagentFind(ctx, s, p.AgentID)
	if err != nil {
		return nil, err
	}
	key, value := strings.TrimSpace(p.Key), strings.TrimSpace(p.Value)
	defer lockMemory(a.ID)()
	repo, err := agentgit.Open(ctx, a.Dir, d.pagentGitMode())
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if err := d.commitHandEdits(ctx, repo, a, "", []string{pagent.DefinitionFile}); err != nil {
		return nil, pagentErr("%v", err)
	}
	def, err := pagent.LoadDefinition(a.Dir)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if err := applySetting(def, key, value); err != nil {
		return nil, pagentErr("%v", err)
	}
	if err := def.Save(a.Dir); err != nil {
		return nil, pagentErr("%v", err)
	}
	subject := fmt.Sprintf("agent: set %s %s", key, value)
	if value == "" {
		subject = "agent: unset " + key
	}
	hash, err := repo.CommitPaths(ctx, subject, memoryTrailers(a, startedByYou, ""), []string{pagent.DefinitionFile}, true)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	d.notifyConns(api.MethodPAgentChanged)
	return api.PAgentSetResult{Commit: hash}, nil
}
