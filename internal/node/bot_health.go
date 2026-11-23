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
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/botgit"
	"github.com/MunifTanjim/argus/internal/config"
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

// healthNotice records a health notice: a health event in the bot's latest
// chat thread and a push, both at most once per key until resolved, then
// every healthRepush.
func (d *Node) healthNotice(ctx context.Context, a bot.Bot, key, message string) {
	k := a.ID + "/" + key
	now := d.botNow()
	d.notify.mu.Lock()
	last, seen := d.notify.health[k]
	d.notify.mu.Unlock()
	if seen && now.Sub(last) < healthRepush {
		return
	}
	d.botNotice(ctx, a, evHealth, map[string]string{"key": key, "message": message})
	threadID := ""
	if th, ok, err := d.bots.LatestThread(ctx, a.ID, threadChat); err == nil && ok {
		threadID = th.ID
	}
	d.botHealth(ctx, a, threadID, key, a.Name+": "+message, "")
}

// noteSpawn counts spawn and resume failures of a bot in a row; the
// third raises a health notice, a success resolves it.
func (d *Node) noteSpawn(ctx context.Context, a bot.Bot, err error) {
	d.notify.mu.Lock()
	if d.notify.spawnFails == nil {
		d.notify.spawnFails = map[string]int{}
	}
	if err == nil {
		delete(d.notify.spawnFails, a.ID)
		d.notify.mu.Unlock()
		d.botHealthResolved(a, healthSpawnFailures)
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
func (d *Node) pausedHealth(ctx context.Context, a bot.Bot, slug, reason string) {
	threadID := ""
	if th, ok, err := d.bots.LatestThread(ctx, a.ID, threadChat); err == nil && ok {
		threadID = th.ID
	}
	d.botHealth(ctx, a, threadID, healthPausedPrefix+slug, fmt.Sprintf("%s: routine %s paused (%s)", a.Name, slug, reason), "")
}

func (d *Node) handleBotUsage(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotUsageParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	a, err := d.botFind(ctx, s, p.BotID)
	if err != nil {
		return nil, err
	}
	rows := map[string]*api.BotUsageRow{}
	row := func(by string) *api.BotUsageRow {
		if rows[by] == nil {
			rows[by] = &api.BotUsageRow{StartedBy: by}
		}
		return rows[by]
	}
	runs, err := s.BotRunsSince(ctx, a.ID, p.Since)
	if err != nil {
		return nil, botErr("%v", err)
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
		return nil, botErr("%v", err)
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
				r.Tokens += sess.Summary.TotalTokens
			}
		}
	}
	res := api.BotUsageResult{BotID: a.ID, Since: p.Since, Rows: []api.BotUsageRow{}, Total: api.BotUsageRow{StartedBy: "total"}}
	for _, by := range slices.Sorted(maps.Keys(rows)) {
		r := *rows[by]
		res.Rows = append(res.Rows, r)
		res.Total.Sessions += r.Sessions
		res.Total.Cost += r.Cost
		res.Total.Tokens += r.Tokens
	}
	return res, nil
}

// settingValues are the values each bot setting accepts; nil accepts any
// value its parser does.
var settingValues = map[string][]string{
	"notify":        {"all", "needs-you", "off"},
	"proactivity":   {"off", "low", "default", "high"},
	"memory.mode":   {"auto", "review", "off"},
	"quiet-hours":   nil,
	"brief.enabled": {"true", "false"},
	"brief.at":      nil,
}

func applySetting(def *bot.Definition, key, value string) error {
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
			def.Brief = &bot.BriefDef{}
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

func (d *Node) handleBotSet(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotSetParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	a, err := d.botFind(ctx, s, p.BotID)
	if err != nil {
		return nil, err
	}
	key, value := strings.TrimSpace(p.Key), strings.TrimSpace(p.Value)
	defer lockMemory(a.ID)()
	repo, err := botgit.Open(ctx, a.Dir, d.botGitMode())
	if err != nil {
		return nil, botErr("%v", err)
	}
	if err := d.commitHandEdits(ctx, repo, a, "", []string{bot.DefinitionFile}); err != nil {
		return nil, botErr("%v", err)
	}
	def, err := bot.LoadDefinition(a.Dir)
	if err != nil {
		return nil, botErr("%v", err)
	}
	if err := applySetting(def, key, value); err != nil {
		return nil, botErr("%v", err)
	}
	if err := def.Save(a.Dir); err != nil {
		return nil, botErr("%v", err)
	}
	subject := fmt.Sprintf("bot: set %s %s", key, value)
	if value == "" {
		subject = "bot: unset " + key
	}
	hash, err := repo.CommitPaths(ctx, subject, memoryTrailers(a, startedByYou, ""), []string{bot.DefinitionFile}, true)
	if err != nil {
		return nil, botErr("%v", err)
	}
	d.notifyConns(api.MethodBotChanged)
	return api.BotSetResult{Commit: hash}, nil
}

// botSettings are the settings clients show: each value as it applies and
// whether bot.yaml sets it.
func botSettings(def *bot.Definition, set bot.Settings) map[string]api.BotSetting {
	var notify, proactivity, mode string
	if def != nil {
		notify, proactivity, mode = def.Notify, def.Proactivity, def.Memory.Mode
	}
	return map[string]api.BotSetting{
		"notify":      {Value: set.Notify, Explicit: notify != ""},
		"proactivity": {Value: set.Proactivity, Explicit: proactivity != ""},
		"memory_mode": {Value: set.MemoryMode, Explicit: mode != ""},
	}
}
