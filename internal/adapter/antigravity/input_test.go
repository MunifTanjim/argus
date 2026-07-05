package antigravity

import (
	"context"
	"testing"
)

type fakePC struct {
	inMode   bool
	canceled bool
	keys     []string
}

func (f *fakePC) PaneInMode(context.Context, string) (bool, error) { return f.inMode, nil }
func (f *fakePC) CancelMode(context.Context, string) error         { f.canceled = true; return nil }
func (f *fakePC) SendKeys(_ context.Context, _ string, keys ...string) error {
	f.keys = append(f.keys, keys...)
	return nil
}

func TestPrepareTextInputCancelsModeThenSends(t *testing.T) {
	pc := &fakePC{inMode: true}
	if err := PrepareTextInput(context.Background(), pc, "%1"); err != nil {
		t.Fatal(err)
	}
	if !pc.canceled {
		t.Fatal("copy/view mode should be canceled")
	}
	if len(pc.keys) == 0 {
		t.Fatal("expected keys sent")
	}
}
