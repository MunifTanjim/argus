// Package e2elive spins up the real argus binary as isolated containers — a
// blind gateway plus nodes, each on its own container with its own directory
// bind-mounted under one per-run root at ~/.cache/argus-e2elive/<runID> — to
// exercise the end-to-end encryption path against real processes. The root
// deliberately does not live under TMPDIR: the VM-backed docker runtimes on
// macOS share the home directory but not the private directory TMPDIR names,
// and a bind mount they cannot resolve mounts empty instead of failing (see
// scopedRootBase). The heavyweight tests build a docker image and bind a
// loopback port; run go test -short to skip them.
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
