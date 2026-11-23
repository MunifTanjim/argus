package bot

import (
	"strings"
	"testing"
	"time"
)

func TestRenderListsSecretNamesOnly(t *testing.T) {
	out := RenderInstructions(InstructionInput{Def: &Definition{}, Now: time.Now(), Secrets: []Secret{
		{Name: "GH_TOKEN", Description: "GitHub token for issues", Value: "ghp_never_shown"},
		{Name: "PLAIN"},
	}})
	for _, want := range []string{"## Secrets", "- $GH_TOKEN: GitHub token for issues", "- $PLAIN\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ghp_never_shown") {
		t.Error("value in instructions")
	}
	if strings.Contains(RenderInstructions(InstructionInput{Def: &Definition{}, Now: time.Now()}), "## Secrets") {
		t.Error("secrets section without secrets")
	}
}
