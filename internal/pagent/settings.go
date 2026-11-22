package pagent

import (
	"time"

	"github.com/MunifTanjim/argus/internal/config"
)

// Settings are the effective settings of an agent: its definition over the
// configured defaults.
type Settings struct {
	Agent                 string
	Model                 string
	Timezone              string
	MemoryMode            string
	Notify                string
	Proactivity           string
	IdleClose             time.Duration
	MaxConcurrentSessions int

	QuietHours   string
	CoreBudget   int64
	CaptureAfter time.Duration
	TidyAt       string
	LogEntries   int
	Extractor    string
	Limits       config.AgentDefaults
}

// Resolve applies the values set in def over d.
func Resolve(def *Definition, d config.AgentDefaults) Settings {
	s := Settings{
		Agent:                 d.Agent,
		Model:                 d.Model,
		Timezone:              d.Timezone,
		MemoryMode:            d.Memory.Mode,
		Notify:                d.Notify,
		Proactivity:           d.Proactivity,
		IdleClose:             d.IdleClose,
		MaxConcurrentSessions: d.MaxConcurrentSessions,
		QuietHours:            d.QuietHours,
		CoreBudget:            d.Memory.CoreBudget,
		CaptureAfter:          d.Memory.CaptureAfter,
		TidyAt:                d.Memory.TidyAt,
		LogEntries:            d.Memory.LogEntries,
		Extractor:             d.Memory.Extractor,
		Limits:                d,
	}
	if def == nil {
		return s
	}
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&s.Agent, def.Agent)
	set(&s.Model, def.Model)
	set(&s.Timezone, def.Timezone)
	set(&s.MemoryMode, def.Memory.Mode)
	set(&s.Notify, def.Notify)
	set(&s.Proactivity, def.Proactivity)
	if def.IdleClose != 0 {
		s.IdleClose = def.IdleClose
	}
	if def.MaxConcurrentSessions != 0 {
		s.MaxConcurrentSessions = def.MaxConcurrentSessions
	}
	return s
}
