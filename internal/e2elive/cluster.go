package e2elive

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
)

type Cluster struct {
	t      *testing.T
	Root   string
	Token  string
	GWAddr string
	// GWURL is the gateway as the test process on the host reaches it.
	GWURL string
	// GWURLInternal is the gateway as a container reaches it. Every command that
	// runs through docker exec must use this one.
	GWURLInternal string

	ctx    context.Context
	cancel context.CancelFunc

	runID      string
	network    string
	containers []string
	nodes      map[string]*Node

	gwEnv  []string
	gwArgs []string

	redactions []redaction
	steps      int
	goldenDir  string // test override; empty means testdata/<TestName>
}

func New(t *testing.T) *Cluster {
	t.Helper()
	if testing.Short() {
		t.Skip("container e2e; skipped under -short")
	}
	if err := buildTestImage(); err != nil {
		t.Fatalf("build test image: %v", err)
	}
	base, err := scopedRootBase()
	if err != nil {
		t.Fatalf("scoped root base: %v", err)
	}
	root, err := os.MkdirTemp(base, "axe")
	if err != nil {
		t.Fatalf("scoped root: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runID := filepath.Base(root)
	c := &Cluster{
		t:       t,
		Root:    root,
		Token:   "devtoken",
		ctx:     ctx,
		cancel:  cancel,
		runID:   runID,
		network: runID + "-net",
		nodes:   map[string]*Node{},
	}
	c.GWAddr = freePort(t)
	c.GWURL = "ws://" + c.GWAddr
	c.GWURLInternal = "ws://" + runID + "-gw:8443"

	if err := dockerRun("network", "create", "--label", runLabel+"=1", c.network); err != nil {
		cancel()
		t.Fatalf("create network: %v", err)
	}

	t.Cleanup(func() {
		c.cancel()
		if t.Failed() {
			for _, name := range c.containers {
				if out, err := dockerLogs(name); err == nil {
					t.Logf("---- %s ----\n%s", name, out)
				}
			}
		}
		for i := len(c.containers) - 1; i >= 0; i-- {
			_ = dockerRun("rm", "-f", c.containers[i])
		}
		_ = dockerRun("network", "rm", c.network)
		_ = os.RemoveAll(root)
	})
	return c
}

// scopedRootBase returns the parent directory a run's scoped root is created in.
// It sits under the user's home because the VM-backed docker runtimes on macOS
// (colima, Docker Desktop) share the home directory but not the private
// directory TMPDIR names, and a bind mount they cannot resolve does not fail —
// it silently becomes an empty directory inside the VM.
func scopedRootBase() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := filepath.Join(home, ".cache", "argus-e2elive")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	return base, nil
}

// hostUser returns the --user value that makes bind-mounted files readable by
// the test process. On Linux the container would otherwise write 0600 files
// owned by the image's uid.
func hostUser() string {
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}

// runContainer starts one detached argus container on the run network. hostDir
// is bind-mounted at the container home, so Node.StatePath keeps working.
func (c *Cluster) runContainer(name, hostDir string, env, argus []string, publish string) {
	c.t.Helper()
	args := []string{
		"run", "-d",
		"--name", name,
		"--hostname", name,
		"--network", c.network,
		"--label", runLabel + "=1",
		"--user", hostUser(),
		"-v", hostDir + ":" + containerHome,
	}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	if publish != "" {
		args = append(args, "-p", publish)
	}
	args = append(args, testImage)
	args = append(args, argus...)

	if err := dockerRun(args...); err != nil {
		c.t.Fatalf("run %s: %v", name, err)
	}
	for _, existing := range c.containers {
		if existing == name {
			return
		}
	}
	c.containers = append(c.containers, name)
}

func (c *Cluster) removeContainer(name string) {
	c.t.Helper()
	_ = dockerRun("stop", "-t", "5", name)
	_ = dockerRun("rm", "-f", name)
}

func (c *Cluster) gatewayContainer() string { return c.runID + "-gw" }

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
		env, err := containerEnv(dir)
		if err != nil {
			c.t.Fatalf("gateway env: %v", err)
		}
		c.gwEnv = env
		c.gwArgs = []string{
			"start",
			"--mode=gateway",
			"--token=" + c.Token,
			"--listen-addr=:8443",
		}
	}
	_, port, err := net.SplitHostPort(c.GWAddr)
	if err != nil {
		c.t.Fatalf("split gateway addr %q: %v", c.GWAddr, err)
	}
	c.runContainer(c.gatewayContainer(), dir, c.gwEnv, c.gwArgs, "127.0.0.1:"+port+":8443")

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

// StopGateway removes the gateway container and waits until its port stops
// answering, so a caller observing the fleet afterwards is genuinely seeing a
// gateway-less network.
func (c *Cluster) StopGateway() {
	c.t.Helper()
	c.removeContainer(c.gatewayContainer())
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
	env, err := containerEnv(dir)
	if err != nil {
		c.t.Fatalf("node %s env: %v", id, err)
	}
	sock := containerHome + "/s"
	args := []string{
		"start",
		"--gateway=" + c.GWURLInternal,
		"--token=" + c.Token,
		"--id=" + id,
		"--label=" + id,
		"--socket=" + sock,
	}
	n := &Node{
		ID:        id,
		Dir:       dir,
		Socket:    sock,
		cluster:   c,
		container: c.runID + "-" + id,
		env:       env,
		args:      args,
	}
	c.runContainer(n.container, dir, env, args, "")
	c.nodes[id] = n
	return n
}

// StopNode removes the node's container, leaving its directory on disk for a
// later StartNode to reload.
func (c *Cluster) StopNode(id string) {
	c.t.Helper()
	n := c.nodes[id]
	if n == nil {
		c.t.Fatalf("StopNode: unknown node %q", id)
	}
	c.removeContainer(n.container)
}

// StartNode starts a replacement container for a stopped node on the same
// directory and socket, and blocks until it is serving. Waiting on the node's
// own socket rather than the gateway roster is deliberate: the roster still
// lists the previous process as online until its offline grace expires, so it
// cannot distinguish the two.
func (c *Cluster) StartNode(id string) {
	c.t.Helper()
	n := c.nodes[id]
	if n == nil {
		c.t.Fatalf("StartNode: unknown node %q", id)
	}
	c.runContainer(n.container, n.Dir, n.env, n.args, "")
	waitFor(c.t, "node "+id+" serving its socket again", func() bool {
		return n.LockRun("status").ExitCode == 0
	})
}

// RestartNode stands in for a machine reboot: the container goes away and a new
// one comes up on the same directory, reloading whatever the old one persisted.
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
