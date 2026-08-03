package e2elive

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
)

var argusBin string

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

	gwCmd  *exec.Cmd
	gwEnv  []string
	gwArgs []string
	gwGen  int

	redactions []redaction
	steps      int
	goldenDir  string // test override; empty means testdata/<TestName>
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
	if c.gwEnv == nil {
		env, err := isolatedEnv(dir)
		if err != nil {
			c.t.Fatalf("gateway env: %v", err)
		}
		c.gwEnv = env
		c.gwArgs = []string{
			"start",
			"--mode=gateway",
			"--token=" + c.Token,
			"--listen-addr=" + c.GWAddr,
		}
	}
	logPath := filepath.Join(dir, "argus.log")
	if c.gwGen > 0 {
		logPath = filepath.Join(dir, fmt.Sprintf("argus.%d.log", c.gwGen))
	}
	c.gwGen++
	c.gwCmd = c.spawn("gw", logPath, c.gwEnv, c.gwArgs)

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

// StopGateway kills the gateway and waits until its port stops answering, so a
// caller observing the fleet afterwards is genuinely seeing a gateway-less network.
func (c *Cluster) StopGateway() {
	c.t.Helper()
	if c.gwCmd == nil {
		c.t.Fatal("StopGateway: gateway was never started")
	}
	if c.gwCmd.Process != nil {
		_ = c.gwCmd.Process.Signal(syscall.SIGTERM)
	}
	_ = c.gwCmd.Wait()
	waitFor(c.t, "gateway stops answering", func() bool {
		cl, err := c.dialClient()
		if err != nil {
			return true
		}
		cl.Close()
		return false
	})
}

// RestartGateway stands in for the gateway host rebooting. The replacement starts
// with an empty entry store — it retains trust-log entries in memory only — so the
// fleet has to refill it from the nodes.
func (c *Cluster) RestartGateway() {
	c.t.Helper()
	c.StopGateway()
	c.StartGateway()
}

func (c *Cluster) AddNode(id string) *Node {
	c.t.Helper()
	if _, exists := c.nodes[id]; exists {
		c.t.Fatalf("AddNode: node %q already exists", id)
	}
	dir := filepath.Join(c.Root, id)
	env, err := isolatedEnv(dir)
	if err != nil {
		c.t.Fatalf("node %s env: %v", id, err)
	}
	sock := filepath.Join(dir, "s")
	args := []string{
		"start",
		"--gateway=" + c.GWURL,
		"--token=" + c.Token,
		"--id=" + id,
		"--label=" + id,
		"--socket=" + sock,
	}
	logPath := filepath.Join(dir, "argus.log")
	cmd := c.spawn(id, logPath, env, args)
	n := &Node{ID: id, Dir: dir, Socket: sock, cluster: c, env: env, args: args, cmd: cmd, logPath: logPath}
	c.nodes[id] = n
	return n
}

// StopNode signals the node's process and waits for it to exit, leaving its
// directory on disk for a later StartNode to reload.
func (c *Cluster) StopNode(id string) {
	c.t.Helper()
	n := c.nodes[id]
	if n == nil {
		c.t.Fatalf("StopNode: unknown node %q", id)
	}
	if n.cmd.Process != nil {
		_ = n.cmd.Process.Signal(syscall.SIGTERM)
	}
	_ = n.cmd.Wait()
}

// StartNode spawns a replacement process for a stopped node on the same directory
// and socket, and blocks until it is serving. Waiting on the socket rather than the
// gateway roster is deliberate: the roster still lists the previous process as
// online until its offline grace expires, so it cannot distinguish the two.
func (c *Cluster) StartNode(id string) {
	c.t.Helper()
	n := c.nodes[id]
	if n == nil {
		c.t.Fatalf("StartNode: unknown node %q", id)
	}
	n.gen++
	n.logPath = filepath.Join(n.Dir, fmt.Sprintf("argus.%d.log", n.gen))
	n.cmd = c.spawn(id, n.logPath, n.env, n.args)
	waitFor(c.t, "node "+id+" serving its socket again", func() bool {
		sc, err := n.DialSocket()
		if err != nil {
			return false
		}
		sc.Close()
		return true
	})
}

// RestartNode stands in for a machine reboot: the process goes away and a new one
// comes up on the same directory, reloading whatever the old one persisted.
func (c *Cluster) RestartNode(id string) {
	c.t.Helper()
	c.StopNode(id)
	c.StartNode(id)
}

func (c *Cluster) WaitOnline(ids ...string) {
	c.t.Helper()
	waitFor(c.t, "nodes online", func() bool {
		cl, err := c.dialClient()
		if err != nil {
			return false
		}
		defer cl.Close()
		var r api.NodesListResult
		if cl.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		online := map[string]bool{}
		for _, nd := range r.Nodes {
			if nd.Online && nd.IdentityPubKey != "" {
				online[nd.ID] = true
			}
		}
		for _, id := range ids {
			if !online[id] {
				return false
			}
		}
		return true
	})
}

// WaitLockEnforcing blocks until the named node's `lock status` reports that it is
// enforcing, so a golden taken on a second node cannot race trust-log propagation.
func (c *Cluster) WaitLockEnforcing(id string) {
	c.t.Helper()
	n := c.nodes[id]
	if n == nil {
		c.t.Fatalf("WaitLockEnforcing: unknown node %q", id)
	}
	waitFor(c.t, "lock enforcing on "+id, func() bool {
		r := n.LockRun("status")
		return r.ExitCode == 0 && strings.Contains(r.Stdout, "locked mode: enforcing")
	})
}

// WaitTip blocks until the named node's `lock status` reports tip as its current
// tip. Propagation to a peer is bounded by the triggered-pull window, so a golden
// taken on a second node right after a write on the first would otherwise capture
// the peer's pre-change state.
func (c *Cluster) WaitTip(id, tip string) {
	c.t.Helper()
	n := c.nodes[id]
	if n == nil {
		c.t.Fatalf("WaitTip: unknown node %q", id)
	}
	waitFor(c.t, "tip "+tip+" on "+id, func() bool {
		r := n.LockRun("status")
		return r.ExitCode == 0 && strings.Contains(r.Stdout, "  tip:     "+tip)
	})
}

// WaitLockQuarantined blocks until the named node's `lock status` reports the
// quarantine headline, which it can only reach after seeing a chain rooted at a
// genesis it does not follow.
func (c *Cluster) WaitLockQuarantined(id string) {
	c.t.Helper()
	n := c.nodes[id]
	if n == nil {
		c.t.Fatalf("WaitLockQuarantined: unknown node %q", id)
	}
	waitFor(c.t, "lock quarantined on "+id, func() bool {
		r := n.LockRun("status")
		return r.ExitCode == 0 && strings.Contains(r.Stdout, "locked mode: QUARANTINED")
	})
}

func (c *Cluster) WaitLockDisabled(id string) {
	c.t.Helper()
	n := c.nodes[id]
	if n == nil {
		c.t.Fatalf("WaitLockDisabled: unknown node %q", id)
	}
	waitFor(c.t, "lock disabled on "+id, func() bool {
		r := n.LockRun("status")
		return r.ExitCode == 0 && strings.Contains(r.Stdout, "disabled network-wide")
	})
}

func (c *Cluster) NewClient() *client.ReconnectingE2EClient {
	c.t.Helper()
	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, c.GWURL+"/client", c.Token, nil)
	}
	cl, err := client.NewReconnectingE2EClient(c.ctx, dial)
	if err != nil {
		c.t.Fatalf("NewClient: %v", err)
	}
	c.t.Cleanup(func() { cl.Close() })
	return cl
}
