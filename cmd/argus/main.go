// Command argus is the terminal UI client. It connects to argusd over the
// JSON-RPC API and renders a dashboard of sessions, with views to stream
// output, send prompts, and control session lifecycle. The same binary also
// runs the node (argus start) and the Claude Code hook integration.
package main

import (
	"github.com/MunifTanjim/argus/internal/shell"
)

// version is overridden at build time via -ldflags.
var version = "dev"

func main() {
	cmd := newRootCmd(version)
	err := cmd.Execute()
	if err == nil {
		return
	}
	// Commands that printed their own diagnostic return errSilent; for everything else
	// (cobra's usage errors) print it ourselves, since the tree silences cobra's output.
	if err != errSilent {
		shell.StdErrLn("argus:", err)
	}
	shell.Exit(1)
}
