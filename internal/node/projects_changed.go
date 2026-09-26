package node

import "github.com/MunifTanjim/argus/internal/api"

// notifyProjectsChanged tells every connected client to refetch project.list.
// It sends in the background so a stalled connection does not delay the caller.
func (d *Node) notifyProjectsChanged() {
	d.subsMu.Lock()
	conns := make([]api.Notifier, 0, len(d.conns))
	for n := range d.conns {
		conns = append(conns, n)
	}
	d.subsMu.Unlock()
	for _, n := range conns {
		go func() { _ = n.Notify(api.MethodProjectChanged, struct{}{}) }()
	}
}
