package node

// rejectsChannels reports whether this node must refuse all E2E channels because
// it is unpinned on a network that has a trust log. local-disable overrides it:
// the two escape hatches stay independent, so re-pinning never silently re-enables
// enforcement an operator turned off, and local-disable never drops the pin.
func (d *Node) rejectsChannels() bool {
	return d.trustGate.Tripped() && !d.localDisabled()
}
