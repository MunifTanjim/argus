package api

import "errors"

// CallerInfo identifies the process that issued an agentctl call.
type CallerInfo struct {
	PID int               `json:"pid"`
	Env map[string]string `json:"env,omitempty"` // only the adapters' caller identity variables
}

var ErrNoCaller = errors.New("not running inside an argus agent session")
