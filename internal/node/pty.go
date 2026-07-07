package node

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// startPTY starts cmd attached to a new PTY of the given size and returns the master.
func startPTY(cmd *exec.Cmd, cols, rows int) (*os.File, error) {
	return pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// resizePTY resizes the PTY master (drives SIGWINCH to the child).
func resizePTY(f *os.File, cols, rows int) error {
	return pty.Setsize(f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}
