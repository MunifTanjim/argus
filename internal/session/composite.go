package session

import "strings"

// compositeSep separates a node id from a session id in a gateway composite id.
// Node ids must not contain it; session ids may (e.g. "default:%3"), which is
// why splitting happens on the first separator only.
const compositeSep = ":"

// CompositeID joins a node id and a session id into the globally-unique id the
// gateway exposes to clients (e.g. "home" + "default:%3" -> "home:default:%3").
func CompositeID(nodeID, id string) string {
	return nodeID + compositeSep + id
}

// SplitCompositeID reverses CompositeID, splitting on the first separator. ok is
// false when s carries no node prefix (a bare local id); id is then s verbatim.
func SplitCompositeID(s string) (nodeID, id string, ok bool) {
	nodeID, id, ok = strings.Cut(s, compositeSep)
	if !ok {
		return "", s, false
	}
	return nodeID, id, true
}
