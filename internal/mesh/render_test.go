package mesh_test

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/mesh"
)

func TestRenderText(t *testing.T) {
	m := api.Message{ID: "m-00000001", Kind: api.MessageText, Body: "hi\nthere"}
	want := "[argus message m-00000001 from worker-api (home:argus:%5)]\nhi\nthere\nReply with the argus `reply` tool (message_id m-00000001), or: argus session reply m-00000001 \"<answer>\""
	if got := mesh.Render(m, "worker-api", "home:argus:%5"); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRenderTask(t *testing.T) {
	m := api.Message{ID: "m-00000001", Kind: api.MessageTask, Body: "do it"}
	want := "[argus message m-00000001 from worker-api (home:argus:%5)]\ndo it\nWhen the task is done, report the result with the argus `reply` tool (message_id m-00000001), or: argus session reply m-00000001 \"<result>\""
	if got := mesh.Render(m, "worker-api", "home:argus:%5"); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRenderOwner(t *testing.T) {
	m := api.Message{ID: "m-00000001", Kind: api.MessageText, Body: "x"}
	got := mesh.Render(m, "owner", "")
	if want := "[argus message m-00000001 from owner]\n"; !strings.HasPrefix(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestRenderAnsweredNoticeHasNoReplyFooter(t *testing.T) {
	m := api.Message{ID: "m-00000001", Kind: api.MessageAnsweredNotice, Body: "The prompt m-00000002 was answered outside argus."}
	want := "[argus message m-00000001 from argus]\nThe prompt m-00000002 was answered outside argus."
	if got := mesh.Render(m, "argus", ""); got != want {
		t.Fatalf("got %q", got)
	}
}
