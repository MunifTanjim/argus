package spawn

import (
	"os"
	"testing"
)

// tmux runs a pane command through $SHELL. A login zsh still starting when its
// test server dies blocks forever opening the dead pty and leaks it.
func TestMain(m *testing.M) {
	os.Setenv("SHELL", "/bin/sh")
	os.Exit(m.Run())
}
