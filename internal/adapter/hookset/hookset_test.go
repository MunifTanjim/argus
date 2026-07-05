package hookset

import "testing"

func newMemSpec(matcher string, store *Map, saves *int) Spec {
	return Spec{
		Marker:        ManagedMarker,
		Matcher:       matcher,
		Command:       func(bin, event string) string { return ManagedCommand(bin, "test-agent", event) },
		Timeout:       func(string) int { return 5 },
		DefaultEvents: []string{"PreToolUse", "PostToolUse"},
		Load:          func() (Map, error) { return *store, nil },
		Save: func(m Map) error {
			if saves != nil {
				*saves++
			}
			*store = m
			return nil
		},
	}
}

func TestReconcileFixesMissingMatcher(t *testing.T) {
	store := Map{
		"PreToolUse": []Group{{
			Matcher: "",
			Hooks:   []Cmd{{Type: "command", Command: ManagedCommand("/bin/argus", "test-agent", "PreToolUse"), Timeout: 5}},
		}},
		"PostToolUse": []Group{{
			Matcher: "",
			Hooks:   []Cmd{{Type: "command", Command: ManagedCommand("/bin/argus", "test-agent", "PostToolUse"), Timeout: 5}},
		}},
	}
	var saves int
	spec := newMemSpec(".*", &store, &saves)

	if _, err := spec.Reconcile("/bin/argus"); err != nil {
		t.Fatal(err)
	}
	if saves != 1 {
		t.Fatalf("expected 1 Save on first reconcile; got %d", saves)
	}
	for _, event := range spec.DefaultEvents {
		for _, g := range store[event] {
			for _, c := range g.Hooks {
				if spec.IsManaged(c) && g.Matcher != ".*" {
					t.Errorf("event %q: managed group matcher = %q; want \".*\"", event, g.Matcher)
				}
			}
		}
	}

	saves = 0
	if _, err := spec.Reconcile("/bin/argus"); err != nil {
		t.Fatal(err)
	}
	if saves != 0 {
		t.Errorf("second Reconcile rewrote correct hooks: Save called %d times", saves)
	}
}

func TestReconcileNoopForMatcherlessSpec(t *testing.T) {
	store := Map{
		"PreToolUse": []Group{{
			Matcher: "",
			Hooks:   []Cmd{{Type: "command", Command: ManagedCommand("/bin/argus", "test-agent", "PreToolUse"), Timeout: 5}},
		}},
		"PostToolUse": []Group{{
			Matcher: "",
			Hooks:   []Cmd{{Type: "command", Command: ManagedCommand("/bin/argus", "test-agent", "PostToolUse"), Timeout: 5}},
		}},
	}
	var saves int
	spec := newMemSpec("", &store, &saves)

	if _, err := spec.Reconcile("/bin/argus"); err != nil {
		t.Fatal(err)
	}
	if saves != 0 {
		t.Errorf("Reconcile spuriously rewrote matcher-less hooks: Save called %d times", saves)
	}
}
