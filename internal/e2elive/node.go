package e2elive

import (
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/config"
)

type Node struct {
	ID     string
	Dir    string
	Socket string

	cluster   *Cluster
	container string
	env       []string
	args      []string
}

// Container returns the docker container name backing this node.
func (n *Node) Container() string { return n.container }

// Log returns what the node's current container has written so far, for
// assertions about behaviour that has no CLI surface — a boot-time warning, say.
// A restart replaces the container, so this is always the current generation.
func (n *Node) Log() string {
	out, _ := dockerLogs(n.container)
	return out
}

// StatePath resolves a file in this node's state directory on the host side of
// the bind mount, so a test can reach the pin and chain the node persisted
// without restating the layout.
func (n *Node) StatePath(name string) string {
	return filepath.Join(n.Dir, "state", config.ProjectName, name)
}

// StartAgent launches the fake claude in a detached tmux session inside the
// node's container. Discovery correlates it through the container's own process
// table and tmux server, so it is visible on this node only.
func (n *Node) StartAgent(sessionName, sessionID string) {
	n.cluster.t.Helper()
	r := dockerExec(n.cluster.ctx, n.container, nil, []string{
		"tmux", "new", "-d",
		"-s", sessionName,
		"-c", containerHome,
		"-e", "FAKE_CLAUDE_SESSION_ID=" + sessionID,
		"-e", "FAKE_CLAUDE_NAME=" + sessionName,
		"claude",
	})
	if r.ExitCode != 0 {
		n.cluster.t.Fatalf("StartAgent %s on %s: exit %d\n%s%s", sessionName, n.ID, r.ExitCode, r.Stdout, r.Stderr)
	}
}
