package config_test

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/config"
)

func TestCoordinationDefaults(t *testing.T) {
	isolateConfigDir(t)
	co := load(t, "").Agents.Coordination
	if co != (config.Coordination{Enabled: false, MaxDepth: 3, MaxRequestsPerRoot: 12, AnswerTimeout: time.Hour}) {
		t.Fatalf("coordination = %+v", co)
	}
}

func TestCoordinationFromFile(t *testing.T) {
	isolateConfigDir(t)
	c := load(t, writeConfig(t, "agents:\n  coordination:\n    enabled: true\n    max-depth: 2\n    answer-timeout: 30m\n"))
	if co := c.Agents.Coordination; !co.Enabled || co.MaxDepth != 2 || co.MaxRequestsPerRoot != 12 || co.AnswerTimeout != 30*time.Minute {
		t.Fatalf("coordination = %+v", co)
	}
	bad := load(t, writeConfig(t, "agents:\n  coordination:\n    answer-timeout: soon\n"))
	if err := bad.Validate(); err == nil {
		t.Fatal("a bad answer-timeout validates")
	}
}
