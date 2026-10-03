package node

import "github.com/MunifTanjim/argus/internal/api"

func (d *Node) notifyProjectsChanged()  { d.notifyConns(api.MethodProjectChanged) }
func (d *Node) notifyTerminalsChanged() { d.notifyConns(api.MethodTerminalChanged) }

// notifyConns sends method with empty params to every connected client, in the
// background so a stalled connection does not delay the caller.
func (d *Node) notifyConns(method string) {
	d.subsMu.Lock()
	conns := make([]api.Notifier, 0, len(d.conns))
	for n := range d.conns {
		conns = append(conns, n)
	}
	d.subsMu.Unlock()
	for _, n := range conns {
		go func() { _ = n.Notify(method, struct{}{}) }()
	}
}
