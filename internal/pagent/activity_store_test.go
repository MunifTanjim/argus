package pagent

import (
	"context"
	"testing"
)

func TestActivityListNewestFirstAndUnattended(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	for _, a := range []Activity{
		{AgentID: "ag1", StartedBy: "you", Tool: "Bash", Decision: "allow", DecidedBy: "user"},
		{AgentID: "ag1", StartedBy: "routine", Tool: "Bash", Target: "git", Class: "outward", Decision: "draft", DecidedBy: "floor"},
		{AgentID: "ag2", StartedBy: "routine", Decision: "deny", DecidedBy: "default"},
	} {
		if _, err := s.InsertActivity(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListActivity(ctx, "ag1", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Decision != "draft" || all[0].Target != "git" || all[1].Decision != "allow" {
		t.Fatalf("all = %+v", all)
	}
	un, _ := s.ListActivity(ctx, "ag1", true, 0)
	if len(un) != 1 || un[0].StartedBy != "routine" {
		t.Fatalf("unattended = %+v", un)
	}
	one, _ := s.ListActivity(ctx, "ag1", false, 1)
	if len(one) != 1 {
		t.Fatalf("limit = %+v", one)
	}
	if err := s.PurgeAgent(ctx, "ag1"); err != nil && err != ErrNotFound {
		t.Fatal(err)
	}
	if left, _ := s.ListActivity(ctx, "ag1", false, 0); len(left) != 0 {
		t.Fatalf("after purge = %+v", left)
	}
}
