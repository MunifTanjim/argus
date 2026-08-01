package e2elive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

var argusBin string

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

	procs    []*exec.Cmd
	logs     []string
	logFiles []*os.File
	nodes    map[string]*Node
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
		for _, f := range c.logFiles {
			f.Close()
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
	c.logFiles = append(c.logFiles, logf)
	return cmd
}

func (c *Cluster) dialClient() (*api.Client, error) {
	conn, err := api.DialWSConn(c.ctx, c.GWURL+"/client", c.Token, nil)
	if err != nil {
		return nil, err
	}
	return api.NewClient(conn), nil
}

func (c *Cluster) StartGateway() {
	c.t.Helper()
	dir := filepath.Join(c.Root, "gw")
	env, err := isolatedEnv(dir)
	if err != nil {
		c.t.Fatalf("gateway env: %v", err)
	}
	args := []string{
		"start",
		"--mode=gateway",
		"--token=" + c.Token,
		"--listen-addr=" + c.GWAddr,
	}
	c.spawn("gw", filepath.Join(dir, "argus.log"), env, args)

	waitFor(c.t, "gateway /client ready", func() bool {
		cl, derr := c.dialClient()
		if derr != nil {
			return false
		}
		defer cl.Close()
		var r api.NodesListResult
		return cl.Call(api.MethodNodesList, nil, &r) == nil
	})
}
