//go:build darwin

package host

import (
	"syscall"
	"testing"
	"time"
)

func TestCaffeinateStarterTimesOut(t *testing.T) {
	start := defaultStarter()
	if start == nil {
		t.Skip("caffeinate not found")
	}
	p, err := start(1)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = p.Kill()
		t.Fatal("caffeinate -t 1 did not exit")
	}
}

func TestCaffeinateStarterOwnProcessGroup(t *testing.T) {
	start := defaultStarter()
	if start == nil {
		t.Skip("caffeinate not found")
	}
	p, err := start(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill(); _ = p.Wait() })
	pgid, err := syscall.Getpgid(p.(cmdProcess).cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if pgid == syscall.Getpgrp() {
		t.Fatal("caffeinate shares the node's process group; a terminal SIGINT would end it")
	}
}
