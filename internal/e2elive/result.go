package e2elive

import (
	"bytes"
	"errors"
	"os/exec"
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

func (n *Node) LockRun(args ...string) Result {
	return n.LockRunEnv(nil, args...)
}

// LockRunEnv runs `argus lock ...` against this node with extra environment
// entries appended to its isolated env.
func (n *Node) LockRunEnv(extra []string, args ...string) Result {
	full := append([]string{"lock"}, args...)
	full = append(full,
		"--socket="+n.Socket,
		"--gateway="+n.cluster.GWURL,
		"--token="+n.cluster.Token,
	)
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
