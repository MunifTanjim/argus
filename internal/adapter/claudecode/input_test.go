package claudecode

import (
	"context"
	"errors"
	"testing"
)

// fakePane records the keystrokes/cancels PrepareTextInput issues.
type fakePane struct {
	inMode    bool
	cancelErr error
	modeErr   error
	sendErr   error
	sent      []string
	canceled  bool
}

func (f *fakePane) PaneInMode(context.Context, string) (bool, error) {
	return f.inMode, f.modeErr
}

func (f *fakePane) CancelMode(context.Context, string) error {
	f.canceled = true
	f.inMode = false
	return f.cancelErr
}

func (f *fakePane) SendKeys(_ context.Context, _ string, keys ...string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, keys...)
	return nil
}

// PrepareTextInput normalizes to INSERT the same way from every starting state:
// `i` then BSpace, with no screen reads. The keystrokes are identical whether the
// pane is in vim NORMAL, vim INSERT, or non-vim — that uniformity is the point.
func TestPrepareSendsProbeAndErase(t *testing.T) {
	f := &fakePane{}
	if err := PrepareTextInput(context.Background(), f, "%1"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"i", "BSpace"}; !equal(f.sent, want) {
		t.Errorf("want %v, got %v", want, f.sent)
	}
	if f.canceled {
		t.Error("a pane not in copy mode should not be canceled")
	}
}

func TestPrepareExitsCopyModeBeforeKeys(t *testing.T) {
	f := &fakePane{inMode: true}
	if err := PrepareTextInput(context.Background(), f, "%1"); err != nil {
		t.Fatal(err)
	}
	if !f.canceled {
		t.Error("a pane in copy mode should be canceled before sending keys")
	}
	if want := []string{"i", "BSpace"}; !equal(f.sent, want) {
		t.Errorf("want %v, got %v", want, f.sent)
	}
}

func TestPrepareModeCheckErrorPropagates(t *testing.T) {
	f := &fakePane{modeErr: errors.New("boom")}
	if err := PrepareTextInput(context.Background(), f, "%1"); err == nil {
		t.Error("a PaneInMode error should propagate")
	}
	if len(f.sent) != 0 {
		t.Errorf("no keys should be sent when the mode check fails, got %v", f.sent)
	}
}

func TestPrepareCancelErrorPropagates(t *testing.T) {
	f := &fakePane{inMode: true, cancelErr: errors.New("boom")}
	if err := PrepareTextInput(context.Background(), f, "%1"); err == nil {
		t.Error("a CancelMode error should propagate")
	}
	if len(f.sent) != 0 {
		t.Errorf("no keys should be sent when copy-mode exit fails, got %v", f.sent)
	}
}

func TestPrepareSendErrorPropagates(t *testing.T) {
	f := &fakePane{sendErr: errors.New("boom")}
	if err := PrepareTextInput(context.Background(), f, "%1"); err == nil {
		t.Error("a SendKeys error should propagate")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func TestHasDraftReadsInputLine(t *testing.T) {
	rule := "────────────────────────────────"
	cases := map[string]struct {
		screen string
		want   bool
	}{
		"empty":       {rule + "\n❯ \n" + rule + "\n  ? for shortcuts", false},
		"draft":       {rule + "\n❯ fix the tests\n" + rule, true},
		"placeholder": {rule + "\n❯ Try \"write a test for main.go\"\n" + rule, false},
		"old box":     {"╭──────╮\n│ > hello │\n╰──────╯", true},
		"old empty":   {"╭──────╮\n│ >       │\n╰──────╯", false},
		"no prompt":   {"some output\nmore output", false},
	}
	for name, tc := range cases {
		if got := (ccAdapter{}).HasDraft(tc.screen); got != tc.want {
			t.Errorf("%s: HasDraft = %v, want %v", name, got, tc.want)
		}
	}
}

func TestHasDraftNeedsTheInputBox(t *testing.T) {
	screen := "> an old prompt echoed in the transcript\nthe agent's answer"
	if (ccAdapter{}).HasDraft(screen) {
		t.Fatal("an echoed prompt line outside the input box counted as a draft")
	}
}
