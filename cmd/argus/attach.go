package main

import "github.com/MunifTanjim/argus/internal/config"

type attachAction int

const (
	attachNone    attachAction = iota // no usable state: run the launch flow
	attachState                       // all recorded servers run: connect from the state
	attachPartial                     // some recorded servers run: refuse
)

// decideAttach returns attachNone for a stale state, in which none of the
// recorded servers runs.
func decideAttach(st *launchState, nodeRunning, gatewayRunning bool) attachAction {
	if st == nil {
		return attachNone
	}
	if !st.needsGateway() {
		gatewayRunning = nodeRunning
	}
	switch {
	case nodeRunning && gatewayRunning:
		return attachState
	case nodeRunning || gatewayRunning:
		return attachPartial
	}
	return attachNone
}

// attachTarget gives the gateway URL, token, and trust head that reach the
// servers st records, with the same head that their spawn path connected with.
func attachTarget(cfg *config.Config, st *launchState, head []byte) (string, string, []byte, error) {
	switch st.Mode {
	case launchModeIsolated:
		return "", "", nil, nil
	case launchModeGateway:
		h, err := resolveTrustHead(cfg)
		return st.GatewayURL, st.Token, h, err
	}
	return st.GatewayURL, st.Token, head, nil
}
