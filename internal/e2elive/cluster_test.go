package e2elive

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestNewIsolatesBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("real-process e2e; skipped under -short")
	}
	c := New(t)

	if !strings.HasPrefix(c.GWURL, "ws://127.0.0.1:") {
		t.Fatalf("GWURL = %q", c.GWURL)
	}
	if _, err := os.Stat(c.Root); err != nil {
		t.Fatalf("root missing: %v", err)
	}

	// The built binary runs under an isolated env and prints usage.
	env, err := isolatedEnv(filepath.Join(c.Root, "probe"))
	if err != nil {
		t.Fatalf("isolatedEnv: %v", err)
	}
	cmd := exec.Command(argusBin, "--help")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("argus --help: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Usage") {
		t.Fatalf("help output missing Usage:\n%s", out)
	}
}

func TestGatewayServesClient(t *testing.T) {
	if testing.Short() {
		t.Skip("real-process e2e; skipped under -short")
	}
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
