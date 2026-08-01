package e2elive

import (
	"net"
	"os/exec"

	"github.com/MunifTanjim/argus/internal/api"
)

type Node struct {
	ID     string
	Dir    string
	Socket string

	cluster *Cluster
	env     []string
	cmd     *exec.Cmd
}

func (n *Node) DialSocket() (*api.Client, error) {
	conn, err := net.Dial("unix", n.Socket)
	if err != nil {
		return nil, err
	}
	return api.NewClient(conn), nil
}
