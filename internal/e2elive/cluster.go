package e2elive

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

type Node struct {
}

type Cluster struct {
	t      *testing.T
	Root   string
	Token  string
	GWAddr string
	GWURL  string

	ctx    context.Context
	cancel context.CancelFunc

	procs []*exec.Cmd
	logs  []string
	nodes map[string]*Node
}

func New(t *testing.T) *Cluster {
	t.Helper()
	if argusBin == "" {
		t.Skip("argus binary not built (running under -short?)")
	}
	root, err := os.MkdirTemp("", "axe")
	if err != nil {
		t.Fatalf("scoped root: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Cluster{
		t:      t,
		Root:   root,
		Token:  "devtoken",
		ctx:    ctx,
		cancel: cancel,
		nodes:  map[string]*Node{},
	}
	c.GWAddr = freePort(t)
	c.GWURL = "ws://" + c.GWAddr

	t.Cleanup(func() {
		c.cancel()
		for i := len(c.procs) - 1; i >= 0; i-- {
			_ = c.procs[i].Wait()
		}
		if t.Failed() {
			for _, lp := range c.logs {
				if b, err := os.ReadFile(lp); err == nil {
					t.Logf("---- %s ----\n%s", lp, b)
				}
			}
		}
		_ = os.RemoveAll(root)
	})
	return c
}

func (c *Cluster) spawn(name, logPath string, env, args []string) *exec.Cmd {
	c.t.Helper()
	logf, err := os.Create(logPath)
	if err != nil {
		c.t.Fatalf("%s log: %v", name, err)
	}
	cmd := exec.CommandContext(c.ctx, argusBin, args...)
	cmd.Env = env
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		c.t.Fatalf("start %s: %v", name, err)
	}
	c.procs = append(c.procs, cmd)
	c.logs = append(c.logs, logPath)
	return cmd
}
