// Package e2elive spins up the real argus binary as isolated OS processes —
// a blind gateway plus nodes, each rooted in its own directory under a single
// scoped temp root — to exercise the end-to-end encryption path against real
// processes. The heavyweight, process-spawning tests build the binary and bind
// loopback ports; run go test -short to skip them.
//
// Scenario tests capture each `argus lock` invocation as a golden file under
// testdata/<TestName>/, holding the normalized command line, exit code, stdout and
// stderr. Regenerate them with:
//
//	go test ./internal/e2elive -run <TestName> -update
//
// Per-run values (keys, genesis hashes, secrets, blobs, paths, ports) must be
// registered with Cluster.Redact; anything volatile that survives normalization
// fails the step rather than being baked into a golden.
package e2elive
