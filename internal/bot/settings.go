package bot

import (
	"time"

	"github.com/MunifTanjim/argus/internal/config"
)

// Settings are the effective settings of a bot: its definition over the
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
	BriefEnabled bool
	BriefAt      string
	NotifyDelay  time.Duration
	CoreBudget   int64
	CaptureAfter time.Duration
	TidyAt       string
	LogEntries   int
	Extractor    string
	Limits       config.BotDefaults
}

// Resolve applies the values set in def over d.
func Resolve(def *Definition, d config.BotDefaults) Settings {
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
		BriefEnabled:          d.Brief.Enabled,
		BriefAt:               d.Brief.At,
		NotifyDelay:           d.NotifyDelay,
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
	set(&s.QuietHours, def.QuietHours)
	if def.Brief != nil {
		if def.Brief.Enabled != nil {
			s.BriefEnabled = *def.Brief.Enabled
		}
		set(&s.BriefAt, def.Brief.At)
	}
	set(&s.Extractor, def.Memory.Extractor)
	if def.Memory.CaptureAfter != 0 {
		s.CaptureAfter = def.Memory.CaptureAfter
	}
	if def.IdleClose != 0 {
		s.IdleClose = def.IdleClose
	}
	if def.MaxConcurrentSessions != 0 {
		s.MaxConcurrentSessions = def.MaxConcurrentSessions
	}
	return s
}
