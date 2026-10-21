package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

type launchMode string

const (
	launchModeIsolated  launchMode = "isolated"
	launchModeConnected launchMode = "connected"
	launchModeGateway   launchMode = "gateway"
)

type launchState struct {
	Mode       launchMode `json:"mode"`
	GatewayURL string     `json:"gateway_url,omitempty"`
	Token      string     `json:"token,omitempty"`
}

func (st launchState) needsGateway() bool { return st.Mode == launchModeGateway }

func launchStatePath(nodeSocket string) string { return nodeSocket + ".launch.json" }

func loadLaunchState(nodeSocket string) (*launchState, error) {
	data, err := os.ReadFile(launchStatePath(nodeSocket))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st launchState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", launchStatePath(nodeSocket), err)
	}
	return &st, nil
}

func saveLaunchState(nodeSocket string, st launchState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return atomicfile.Write(launchStatePath(nodeSocket), data)
}

func removeLaunchState(nodeSocket string) error {
	err := os.Remove(launchStatePath(nodeSocket))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
