package e2elive

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
)

type Node struct {
	ID     string
	Dir    string
	Socket string

	cluster *Cluster
	env     []string
	args    []string
	cmd     *exec.Cmd
	gen     int    // process generation, so a restart gets its own log file
	logPath string // current generation's log
}

// Log returns what the node's current process has written so far, for assertions
// about behaviour that has no CLI surface — a boot-time warning, say.
func (n *Node) Log() string {
	b, err := os.ReadFile(n.logPath)
	if err != nil {
		return ""
	}
	return string(b)
}

func (n *Node) DialSocket() (*api.Client, error) {
	conn, err := net.Dial("unix", n.Socket)
	if err != nil {
		return nil, err
	}
	return api.NewClient(conn), nil
}

// StatePath resolves a file in this node's state directory the same way the
// binary does under its isolated XDG_STATE_HOME, so a test can reach the pin and
// chain the node persisted without restating the layout.
func (n *Node) StatePath(name string) string {
	return filepath.Join(n.Dir, "state", config.ProjectName, name)
}
