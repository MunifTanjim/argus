package antigravity

import (
	"path/filepath"
	"testing"
)

func TestPathsHonorOverride(t *testing.T) {
	dir := t.TempDir()
	homeDirOverride = filepath.Join(dir, "antigravity-cli")
	t.Cleanup(func() { homeDirOverride = "" })

	got, err := hooksJSONPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "antigravity-cli", "hooks.json"); got != want {
		t.Fatalf("hooksJSONPath = %q, want %q", got, want)
	}
	cgot, err := configHooksJSONPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "config", "hooks.json"); cgot != want {
		t.Fatalf("configHooksJSONPath = %q, want %q", cgot, want)
	}
}

func TestTranscriptPathFor(t *testing.T) {
	dir := t.TempDir()
	homeDirOverride = filepath.Join(dir, "antigravity-cli")
	t.Cleanup(func() { homeDirOverride = "" })

	convID := "247419d6-eef1-49cb-855c-609e7e13849b"
	want := filepath.Join(dir, "antigravity-cli", "brain", convID, ".system_generated", "logs", "transcript_full.jsonl")
	if got := transcriptPathFor(convID); got != want {
		t.Fatalf("transcriptPathFor(%q) = %q, want %q", convID, got, want)
	}

	// Blank/unsafe ids yield no path.
	for _, bad := range []string{"", "../escape", `a\b`, "a/b"} {
		if got := transcriptPathFor(bad); got != "" {
			t.Errorf("transcriptPathFor(%q) = %q, want empty", bad, got)
		}
	}
}
