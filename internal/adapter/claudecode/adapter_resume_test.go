package claudecode

import "testing"

func TestResumeCommand(t *testing.T) {
	a := New()
	name, args, ok := a.ResumeCommand("sess-123")
	if !ok || name != "claude" || len(args) != 2 || args[0] != "--resume" || args[1] != "sess-123" {
		t.Fatalf("got name=%q args=%#v ok=%v", name, args, ok)
	}
}
