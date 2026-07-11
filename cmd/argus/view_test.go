package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestViewCmdRejectsMissingFile(t *testing.T) {
	cmd := newViewCmd()
	cmd.SetArgs([]string{filepath.Join(t.TempDir(), "nope.argus")})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestViewCmdRejectsGarbage(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bad.argus")
	if err := os.WriteFile(f, []byte("not a gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newViewCmd()
	cmd.SetArgs([]string{f})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for non-gzip file")
	}
}
