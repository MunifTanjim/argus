package main

type attachAction int

const (
	attachNone    attachAction = iota // no usable state: run the launch flow
	attachState                       // all recorded servers run: connect from the state
	attachPartial                     // some recorded servers run: refuse
)

// decideAttach returns attachNone with stale set when none of the recorded
// servers runs.
func decideAttach(st *launchState, nodeRunning, gatewayRunning bool) (action attachAction, stale bool) {
	if st == nil {
		return attachNone, false
	}
	if !st.needsGateway() {
		if nodeRunning {
			return attachState, false
		}
		return attachNone, true
	}
	switch {
	case nodeRunning && gatewayRunning:
		return attachState, false
	case nodeRunning || gatewayRunning:
		return attachPartial, false
	}
	return attachNone, true
}
