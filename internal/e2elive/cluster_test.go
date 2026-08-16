package e2elive

import (
	"os"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestNewIsolatesRun(t *testing.T) {
	c := New(t)

	if !strings.HasPrefix(c.GWURL, "ws://127.0.0.1:") {
		t.Fatalf("GWURL = %q", c.GWURL)
	}
	if want := "ws://" + c.runID + "-gw:8443"; c.GWURLInternal != want {
		t.Fatalf("GWURLInternal = %q, want %q", c.GWURLInternal, want)
	}
	if _, err := os.Stat(c.Root); err != nil {
		t.Fatalf("root missing: %v", err)
	}

	// The image's argus is the entrypoint, so bare flags reach it.
	out, err := dockerOut("run", "--rm", "--label", runLabel+"=1", testImage, "--help")
	if err != nil {
		t.Fatalf("argus --help: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Usage") {
		t.Fatalf("help output missing Usage:\n%s", out)
	}
}

func TestGatewayServesClient(t *testing.T) {
	c := New(t)
	c.StartGateway()

	cl, err := c.dialClient()
	if err != nil {
		t.Fatalf("dialClient: %v", err)
	}
	defer cl.Close()

	var r api.NodesListResult
	if err := cl.Call(api.MethodNodesList, nil, &r); err != nil {
		t.Fatalf("nodes.list: %v", err)
	}
	if len(r.Nodes) != 0 {
		t.Fatalf("expected empty roster, got %d nodes", len(r.Nodes))
	}
}

func TestNodeJoinsAndLockStatus(t *testing.T) {
	c := New(t)
	c.StartGateway()
	c.AddNode("node-a")
	c.WaitOnline("node-a")

	a := c.nodes["node-a"]

	ok := a.LockRun("status")
	if ok.ExitCode != 0 {
		t.Fatalf("lock status exit = %d, stderr:\n%s", ok.ExitCode, ok.Stderr)
	}
	if !strings.Contains(ok.Stdout, "locked mode:") {
		t.Fatalf("lock status stdout missing headline:\n%s", ok.Stdout)
	}
	if !strings.Contains(strings.Join(ok.Args, " "), "lock status") {
		t.Fatalf("Args not recorded: %v", ok.Args)
	}

	bad := a.LockRun("init", "sigpub:zzzz")
	if bad.ExitCode == 0 {
		t.Fatalf("malformed key should fail, got exit 0:\n%s", bad.Stdout)
	}
	if bad.Stderr == "" {
		t.Fatalf("malformed key produced no stderr")
	}

	if r := a.LockRun("status"); r.ExitCode != 0 {
		t.Fatalf("lock status on node-a: exit %d\n%s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
}
