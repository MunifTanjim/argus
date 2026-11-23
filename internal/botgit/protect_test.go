package botgit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOpenReportsCreated(t *testing.T) {
	setupEnv(t)
	dir := filepath.Join(t.TempDir(), "a")
	ctx := context.Background()
	r, err := Open(ctx, dir, ModeNested)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Created {
		t.Fatal("Created = false on first open")
	}
	if r, err = Open(ctx, dir, ModeNested); err != nil || r.Created {
		t.Fatalf("second open: Created = %v, %v", r.Created, err)
	}
}

func TestProtectedSeesHooksAndConfig(t *testing.T) {
	setupEnv(t)
	dir := filepath.Join(t.TempDir(), "a")
	ctx := context.Background()
	r, err := Open(ctx, dir, ModeNested)
	if err != nil {
		t.Fatal(err)
	}
	before, err := r.Protected(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := before[".git/config"]; !ok {
		t.Fatalf("config missing: %v", before)
	}
	again, _ := r.Protected(ctx)
	if got := ProtectedChanges(before, again); len(got) != 0 {
		t.Fatalf("unchanged repo reports %v", got)
	}

	hook := filepath.Join(r.GitDir, "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/usr/bin/env python3\nprint('hi')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "config", "core.fsmonitor", "true")
	after, err := r.Protected(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".git/config (changed)", ".git/hooks/pre-commit (added)"}
	if got := ProtectedChanges(before, after); !slices.Equal(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}

	if err := os.Chmod(hook, 0o644); err != nil {
		t.Fatal(err)
	}
	moved, _ := r.Protected(ctx)
	if got := ProtectedChanges(after, moved); !slices.Equal(got, []string{".git/hooks/pre-commit (changed)"}) {
		t.Fatalf("mode change = %v", got)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	gone, _ := r.Protected(ctx)
	if got := ProtectedChanges(moved, gone); !slices.Equal(got, []string{".git/hooks/pre-commit (removed)"}) {
		t.Fatalf("removal = %v", got)
	}
}

func TestProtectedFollowsHooksPath(t *testing.T) {
	setupEnv(t)
	dir := filepath.Join(t.TempDir(), "a")
	ctx := context.Background()
	r, err := Open(ctx, dir, ModeNested)
	if err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(t.TempDir(), "hooks")
	write(t, filepath.Join(custom, "post-commit"), "#!/bin/sh\n")
	run(t, dir, "config", "core.hooksPath", custom)
	before, err := r.Protected(ctx)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(custom, "post-commit"), "#!/bin/sh\ntouch /tmp/x\n")
	after, _ := r.Protected(ctx)
	got := ProtectedChanges(before, after)
	if len(got) != 1 || filepath.Base(got[0]) != "post-commit (changed)" {
		t.Fatalf("changes = %v", got)
	}
}

func TestGuardRefusesBeforeGit(t *testing.T) {
	setupEnv(t)
	dir := filepath.Join(t.TempDir(), "a")
	ctx := context.Background()
	r, err := Open(ctx, dir, ModeNested)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "m.md"), "x\n")
	if _, err := r.CommitPaths(ctx, "first", nil, []string{"m.md"}, true); err != nil {
		t.Fatal(err)
	}
	first := run(t, dir, "rev-parse", "HEAD")
	// A hook that would leave a mark if git ran it.
	mark := filepath.Join(t.TempDir(), "ran")
	write(t, filepath.Join(r.GitDir, "hooks", "pre-commit"), "#!/bin/sh\ntouch "+mark+"\n")
	if err := os.Chmod(filepath.Join(r.GitDir, "hooks", "pre-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	refuse := errors.New("refused")
	r.Guard = func(context.Context) error { return refuse }
	write(t, filepath.Join(dir, "m.md"), "y\n")
	if _, err := r.CommitPaths(ctx, "second", nil, []string{"m.md"}, true); !errors.Is(err, refuse) {
		t.Fatalf("CommitPaths err = %v", err)
	}
	if _, err := r.CommitUserEdits(ctx, []string{"m.md"}, "a", "id"); !errors.Is(err, refuse) {
		t.Fatalf("CommitUserEdits err = %v", err)
	}
	if _, err := r.Revert(ctx, first, nil); !errors.Is(err, refuse) {
		t.Fatalf("Revert err = %v", err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("hook ran despite the guard")
	}
	if got := run(t, dir, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("index touched: %q", got)
	}
}
