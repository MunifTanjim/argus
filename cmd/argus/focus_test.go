package main

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/spf13/cobra"
)

func TestFocusAcceptsClickCmdArgv(t *testing.T) {
	// The desktop click runs `desktopClickCmd(cfg)(id)`; the focus command must
	// accept the flags/args that argv carries (regression guard for the missing
	// --socket flag that once broke every click).
	argv := desktopClickCmd(&config.Config{Socket: "/tmp/argus.sock"})("nodeA:default:%7")
	cmd := newFocusCmd()
	cmd.RunE = func(*cobra.Command, []string) error { return nil } // don't dial
	cmd.SetArgs(argv[2:])                                          // drop bin + "_focus" subcommand name
	if err := cmd.Execute(); err != nil {
		t.Fatalf("focus rejected clickCmd argv %v: %v", argv, err)
	}
}

func TestFocusCmdRequiresSessionID(t *testing.T) {
	cmd := newFocusCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when no session id is given")
	}
}

func TestFocusCmdName(t *testing.T) {
	cmd := newFocusCmd()
	if got := cmd.Name(); got != "_focus" {
		t.Fatalf("command name = %q, want _focus", got)
	}
	if !cmd.Hidden {
		t.Fatal("_focus command should be hidden")
	}
}
