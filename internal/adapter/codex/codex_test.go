package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSummaryForResolvesModelName(t *testing.T) {
	names := map[string]string{"gpt-5.5": "GPT-5.5"}

	s := summaryFor("gpt-5.5", "t", 5, names)
	if s == nil || s.ModelName != "GPT-5.5" || s.ModelColor != modelBrandColor {
		t.Errorf("model slug not resolved to display name: %+v", s)
	}
	s = summaryFor("gpt-x", "", 0, names)
	if s == nil || s.ModelName != "gpt-x" {
		t.Errorf("unknown slug should fall back to raw: %+v", s)
	}
	if s := summaryFor("", "", 0, names); s != nil {
		t.Errorf("empty meta should yield nil summary, got %+v", s)
	}
}

func TestLoadModelNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	body := `{"models":[
		{"slug":"gpt-5.5","display_name":"GPT-5.5"},
		{"slug":"gpt-5","display_name":"GPT-5"},
		{"slug":"","display_name":"skip"}
	]}`
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadModelNames()
	if got["gpt-5.5"] != "GPT-5.5" || got["gpt-5"] != "GPT-5" {
		t.Errorf("unexpected model map: %v", got)
	}
	if _, ok := got[""]; ok {
		t.Error("blank slug should be skipped")
	}

	t.Setenv("CODEX_HOME", t.TempDir())
	if got := loadModelNames(); got != nil {
		t.Errorf("missing cache should yield nil, got %v", got)
	}
}

type fakePane struct {
	inMode   bool
	modeErr  error
	cancErr  error
	sendErr  error
	canceled bool
	sent     []string
}

func (f *fakePane) PaneInMode(context.Context, string) (bool, error) { return f.inMode, f.modeErr }
func (f *fakePane) CancelMode(context.Context, string) error {
	f.canceled = true
	f.inMode = false
	return f.cancErr
}
func (f *fakePane) SendKeys(_ context.Context, _ string, keys ...string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, keys...)
	return nil
}

func TestPrepareTextInput(t *testing.T) {
	f := &fakePane{}
	if err := PrepareTextInput(context.Background(), f, "%1"); err != nil {
		t.Fatal(err)
	}
	if f.canceled {
		t.Error("a pane not in copy mode should not be canceled")
	}
	if want := []string{"i", "BSpace"}; !equalStrings(f.sent, want) {
		t.Errorf("want %v, got %v", want, f.sent)
	}

	f = &fakePane{inMode: true}
	if err := PrepareTextInput(context.Background(), f, "%1"); err != nil {
		t.Fatal(err)
	}
	if !f.canceled {
		t.Error("a pane in copy mode should be canceled")
	}
	if want := []string{"i", "BSpace"}; !equalStrings(f.sent, want) {
		t.Errorf("want %v, got %v", want, f.sent)
	}

	f = &fakePane{modeErr: errors.New("boom")}
	if err := PrepareTextInput(context.Background(), f, "%1"); err == nil {
		t.Error("a PaneInMode error should propagate")
	}
	if len(f.sent) != 0 {
		t.Errorf("no keys should be sent when the mode check fails, got %v", f.sent)
	}

	f = &fakePane{sendErr: errors.New("boom")}
	if err := PrepareTextInput(context.Background(), f, "%1"); err == nil {
		t.Error("a SendKeys error should propagate")
	}
}

func equalStrings(a, b []string) bool {
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
