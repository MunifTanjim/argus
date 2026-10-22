package node

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/push"
	"github.com/MunifTanjim/argus/internal/session"
)

type recordingSink struct {
	mu   sync.Mutex
	got  []push.Notification
	seen chan push.Notification
}

func newRecordingSink() *recordingSink {
	return &recordingSink{seen: make(chan push.Notification, 8)}
}

func (r *recordingSink) wait(t *testing.T) push.Notification {
	t.Helper()
	select {
	case n := <-r.seen:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
		return push.Notification{}
	}
}

func (r *recordingSink) Notify(_ context.Context, n push.Notification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, n)
	r.seen <- n
}

func ownerSend(t *testing.T, d *Node, p api.OwnerSendParams) (api.AgentSendResult, error) {
	t.Helper()
	raw, _ := json.Marshal(p)
	out, err := d.handleMessagesSend(t.Context(), raw)
	if err != nil {
		return api.AgentSendResult{}, err
	}
	return out.(api.AgentSendResult), nil
}

func TestOwnerSendQueuesMessage(t *testing.T) {
	d, fp := deliverNode(t)
	b := apiSession(d, "b", session.StatusIdle, nil)
	res, err := ownerSend(t, d, api.OwnerSendParams{SessionID: b.ID, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	m, _, _ := d.mesh.Get(t.Context(), res.MessageID)
	if m.From.Key != api.PartyOwner || m.Kind != api.MessageText || m.State != api.MessageQueued {
		t.Fatalf("message = %+v", m)
	}
	d.meshPass(t.Context())
	if !strings.HasPrefix(fp.gotText, "[argus message "+m.ID+" from owner]") {
		t.Fatalf("prompt = %q", fp.gotText)
	}
	if msgState(t, d, m.ID) != api.MessageDelivered {
		t.Fatal("not delivered")
	}
}

func TestOwnerSendRejectsUnknownSession(t *testing.T) {
	d, _ := deliverNode(t)
	if _, err := ownerSend(t, d, api.OwnerSendParams{SessionID: "nope", Text: "x"}); err == nil || err.Error() != "unknown session: nope" {
		t.Fatalf("err = %v", err)
	}
}

func TestOwnerReplyNeedsOwnerRecipient(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	m1, err := d.send(t.Context(), a, b.ID, "q")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ownerSend(t, d, api.OwnerSendParams{ReplyTo: m1.MessageID, Text: "x"})
	if err == nil || err.Error() != "only messages to the owner can be answered here" {
		t.Fatalf("err = %v", err)
	}
}

func TestOwnerReplyQueuesToSender(t *testing.T) {
	d, _ := deliverNode(t)
	b := apiSession(d, "b", session.StatusIdle, nil)
	m1, _ := ownerSend(t, d, api.OwnerSendParams{SessionID: b.ID, Text: "q"})
	r1, err := d.reply(t.Context(), b, m1.MessageID, "a")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := ownerSend(t, d, api.OwnerSendParams{ReplyTo: r1.MessageID, Text: "thanks"})
	if err != nil {
		t.Fatal(err)
	}
	m, _, _ := d.mesh.Get(t.Context(), r2.MessageID)
	if m.Kind != api.MessageReply || m.From.Key != api.PartyOwner || m.To.Key != "opencode:b" || m.State != api.MessageQueued || m.ReplyTo != r1.MessageID {
		t.Fatalf("reply = %+v", m)
	}
	if msgState(t, d, r1.MessageID) != api.MessageReplied {
		t.Fatal("original not replied")
	}
}

func TestAgentReplyToOwnerNotifies(t *testing.T) {
	d, _ := deliverNode(t)
	b := apiSession(d, "b", session.StatusIdle, nil)
	sink := newRecordingSink()
	d.ownerSinks.Store(&[]push.Sink{sink})
	m1, _ := ownerSend(t, d, api.OwnerSendParams{SessionID: b.ID, Text: "q"})
	r, err := d.reply(t.Context(), b, m1.MessageID, "the answer")
	if err != nil {
		t.Fatal(err)
	}
	n := sink.wait(t)
	if n.Data["session_id"] != b.ID || n.Body != "the answer" {
		t.Fatalf("got %+v", n)
	}
	sink.mu.Lock()
	count := len(sink.got)
	sink.mu.Unlock()
	if count != 1 {
		t.Fatalf("notifications = %d", count)
	}
	if msgState(t, d, r.MessageID) != api.MessageDelivered {
		t.Fatal("reply not delivered")
	}
}

func TestMessagesListReturnsLinks(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	if err := d.mesh.PutLink(t.Context(), api.Link{Child: d.party(b), Parent: d.party(a)}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := ownerSend(t, d, api.OwnerSendParams{SessionID: b.ID, Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := d.handleMessagesList(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(api.MessagesListResult)
	if len(res.Messages) != 2 || len(res.Links) != 1 {
		t.Fatalf("got %+v", res)
	}
}

func TestNotifyOwnerTruncatesBody(t *testing.T) {
	d, _ := deliverNode(t)
	sink := newRecordingSink()
	d.ownerSinks.Store(&[]push.Sink{sink})
	d.notifyOwner(t.Context(), api.Message{Body: strings.Repeat("é", 300)}, "x")
	n := sink.wait(t)
	if got := len([]rune(n.Body)); got != 200 || n.Title != "x replied" {
		t.Fatalf("runes = %d title = %q", got, n.Title)
	}
}

func TestNotifyOwnerWithoutSinksIsNoop(t *testing.T) {
	d, _ := deliverNode(t)
	d.notifyOwner(t.Context(), api.Message{Body: "x"}, "x")
}
