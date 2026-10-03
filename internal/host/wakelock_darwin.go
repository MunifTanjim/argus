//go:build darwin

package host

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

type cmdProcess struct{ cmd *exec.Cmd }

func (p cmdProcess) Kill() error { return p.cmd.Process.Kill() }
func (p cmdProcess) Wait() error { return p.cmd.Wait() }

// -w ends the assertion when the node exits, so a crash leaks no wakelock.
func defaultStarter() starter {
	if _, err := exec.LookPath("caffeinate"); err != nil {
		return nil
	}
	return func(secs int64) (process, error) {
		args := []string{"-i", "-w", strconv.Itoa(os.Getpid())}
		if secs > 0 {
			args = append(args, "-t", strconv.FormatInt(secs, 10))
		}
		cmd := exec.Command("caffeinate", args...)
		// Out of the node's process group, so a terminal SIGINT or SIGHUP that
		// stops the node does not end the child first and clear the saved state.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmdProcess{cmd}, nil
	}
}
