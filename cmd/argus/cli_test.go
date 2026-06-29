package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommandTreeWiring(t *testing.T) {
	root := newRootCmd("test")
	want := map[string]bool{"start": false, "hooks": false, "hook": false, "upgrade": false}
	for _, c := range root.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not attached to root", name)
		}
	}
}

func TestHookCommandHidden(t *testing.T) {
	for _, c := range newRootCmd("test").Commands() {
		if c.Name() == "hook" {
			if !c.Hidden {
				t.Error("hook command should be hidden from help")
			}
			return
		}
	}
	t.Fatal("hook command not found")
}

func TestVersionTemplate(t *testing.T) {
	root := newRootCmd("1.2.3")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if got := out.String(); got != "argus 1.2.3\n" {
		t.Errorf("version output = %q, want %q", got, "argus 1.2.3\n")
	}
}

func TestUnknownCommandErrors(t *testing.T) {
	root := newRootCmd("test")
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"bogus"})
	if err := root.Execute(); err == nil {
		t.Fatal("unknown command should return an error")
	}
}

func TestHooksRequiresSubcommand(t *testing.T) {
	root := newRootCmd("test")
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"hooks"})
	err := root.Execute()
	if err == nil {
		t.Fatal("bare hooks should return an error")
	}
	if !strings.Contains(err.Error(), "subcommand") {
		t.Errorf("error = %q, want it to mention a required subcommand", err.Error())
	}
}
