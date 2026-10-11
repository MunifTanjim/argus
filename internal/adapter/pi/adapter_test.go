package pi

import (
	"testing"
)

func TestAdapterShape(t *testing.T) {
	a := New()
	if a.Agent() != "pi" {
		t.Fatalf("Agent() = %q", a.Agent())
	}
	if !a.IsHeadless() {
		t.Fatal("pi adapter must be headless")
	}
	if name, _ := a.SpawnCommand("x"); name != "pi" {
		t.Fatalf("SpawnCommand binary = %q; want pi (PATH probe)", name)
	}
	if name, args, ok := a.ResumeCommand("s1"); !ok || name != "pi" || len(args) != 2 || args[0] != "--session" || args[1] != "s1" {
		t.Fatalf("ResumeCommand = %q %v %v", name, args, ok)
	}
}

func TestProjectGroupDir(t *testing.T) {
	if got := projectGroupDir("/work/proj"); got != "--work-proj--" {
		t.Fatalf("group = %q", got)
	}
	if got := projectGroupDir("/mnt/ephemeral/tmp/x"); got != "--mnt-ephemeral-tmp-x--" {
		t.Fatalf("group = %q", got)
	}
}
