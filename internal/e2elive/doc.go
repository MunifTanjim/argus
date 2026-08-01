// Package e2elive spins up the real argus binary as isolated OS processes —
// a blind gateway plus nodes, each rooted in its own directory under a single
// scoped temp root — to exercise the end-to-end encryption path against real
// processes. The heavyweight, process-spawning tests build the binary and bind
// loopback ports; run go test -short to skip them.
package e2elive
