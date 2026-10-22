package node

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestCallerFromLocalPID(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	ref, err := d.callerFrom(t.Context(), caller)
	if err != nil || ref.Local == nil || ref.Actor != (api.Actor{Node: "home", Key: "claude:p1"}) {
		t.Fatalf("local ref = %+v err=%v", ref, err)
	}
}

func TestReplyChecksActorNode(t *testing.T) {
	d, _ := orchNode(t)
	d.SetIdentity("home", "")
	if _, err := d.orch.Insert(t.Context(), api.Message{
		ID: "home:m-00000001", Kind: api.MessageText, State: api.MessageDelivered,
		From: api.Actor{Node: "home", Key: api.ActorOwner}, To: api.Actor{Node: "b", Key: "claude:k"}, Body: "hi",
	}); err != nil {
		t.Fatal(err)
	}
	ref := callerRef{Actor: api.Actor{Node: "c", Key: "claude:k"}}
	_, err := d.reply(t.Context(), ref, "home:m-00000001", "x")
	if err == nil || !strings.Contains(err.Error(), "only the recipient") {
		t.Fatalf("err = %v", err)
	}
}

func TestKillChecksActorNode(t *testing.T) {
	d, fd, _ := killNode(t)
	ref := callerRef{Actor: api.Actor{Node: "c", Key: "claude:p1"}}
	if _, err := d.kill(t.Context(), ref, "home:default:%p2"); err == nil || !strings.Contains(err.Error(), "only the parent") {
		t.Fatalf("err = %v", err)
	}
	if fd.called {
		t.Fatal("target was killed")
	}
}
