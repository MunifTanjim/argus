package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	path := launchStatePath(nodeSocket)
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func removeLaunchState(nodeSocket string) error {
	err := os.Remove(launchStatePath(nodeSocket))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
