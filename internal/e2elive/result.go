package e2elive

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// Result is one CLI invocation: the arguments, the two output streams captured
// separately, and the process exit code. A non-zero exit is data, not an error —
// the error-path tests assert on it.
type Result struct {
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
}

// callerSupplied reports whether args already contains a value for the given flag
// name (e.g. "--token"). It matches both "--token=<val>" and the two-element form
// "--token" "<val>".
func callerSupplied(args []string, flag string) bool {
	prefix := flag + "="
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

// buildLockArgs returns the full argument slice for `argus lock`, appending the
// default socket/gateway/token values only when the caller has not already
// supplied them. This prevents harness defaults from shadowing intentionally
// wrong values in error-path tests.
func buildLockArgs(callerArgs []string, socket, gwURL, token string) []string {
	full := append([]string{"lock"}, callerArgs...)
	if !callerSupplied(callerArgs, "--socket") {
		full = append(full, "--socket="+socket)
	}
	if !callerSupplied(callerArgs, "--gateway") {
		full = append(full, "--gateway="+gwURL)
	}
	if !callerSupplied(callerArgs, "--token") {
		full = append(full, "--token="+token)
	}
	return full
}

func (n *Node) LockRun(args ...string) Result {
	return n.LockRunEnv(nil, args...)
}

// LockRunEnv runs `argus lock ...` against this node with extra environment
// entries appended to its isolated env. Harness defaults for --socket, --gateway,
// and --token are appended only when the caller has not already supplied them, so
// error-path tests can pass intentionally wrong values.
func (n *Node) LockRunEnv(extra []string, args ...string) Result {
	full := buildLockArgs(args, n.Socket, n.cluster.GWURL, n.cluster.Token)
	cmd := exec.CommandContext(n.cluster.ctx, argusBin, full...)
	cmd.Env = append(append([]string{}, n.env...), extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
			stderr.WriteString("\nharness: " + err.Error() + "\n")
		}
	}
	return Result{Args: full, Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}
}
