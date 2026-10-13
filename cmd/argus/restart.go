package main

import (
	"context"
	"os"
	"sync/atomic"
	"syscall"
)

// restartPath is set only by a server.restart request.
var restartPath atomic.Pointer[string]

var execFn = syscall.Exec

func requestRestart(path string, stop context.CancelFunc) {
	restartPath.Store(&path)
	stop()
}

// startEnv is the environment the process started with. A managed server drops
// ARGUS_TOKEN from its live environment, and its restart needs the token back.
var startEnv = os.Environ()

// reexec keeps the PID, so a supervisor sees no exit.
func reexec(path string) error {
	return execFn(path, os.Args, startEnv)
}
