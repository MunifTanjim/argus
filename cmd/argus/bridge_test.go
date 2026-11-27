package main

import (
	"bytes"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBridgeCopiesBothWays(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4)
		if _, err := io.ReadFull(c, buf); err != nil {
			return
		}
		_, _ = c.Write(append([]byte("echo:"), buf...))
	}()

	var out bytes.Buffer
	cmd := newRootCmd("test")
	cmd.SetIn(strings.NewReader("ping"))
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"bridge", "--socket", sock})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("bridge: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not exit")
	}
	if got := out.String(); got != "echo:ping" {
		t.Fatalf("stdout = %q, want %q", got, "echo:ping")
	}
}

func TestBridgeNoSocketFails(t *testing.T) {
	cmd := newRootCmd("test")
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetArgs([]string{"bridge", "--socket", filepath.Join(t.TempDir(), "missing.sock")})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error for missing socket")
	}
}
