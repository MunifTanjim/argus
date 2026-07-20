package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNodeDescriptorMarshalsWithTags(t *testing.T) {
	nd := NodeDescriptor{
		ID: "n1", Label: "n1-box", Version: "9",
		Capabilities:   NodeCapabilities{SpawnSession: true},
		IdentityPubKey: "PUB1", Online: true,
	}
	b, err := json.Marshal(nd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"id"`, `"identity_pubkey"`, `"online"`, `"spawn_session"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("NodeDescriptor JSON missing %s: %s", key, b)
		}
	}
	var back NodeDescriptor
	if err := json.Unmarshal(b, &back); err != nil || back.IdentityPubKey != "PUB1" || !back.Online {
		t.Errorf("round-trip = %+v (err=%v)", back, err)
	}
}

func TestNodeEventRoundTrips(t *testing.T) {
	ev := NodeEvent{Type: NodeEventOffline, Node: NodeDescriptor{ID: "n1"}}
	b, _ := json.Marshal(ev)
	var back NodeEvent
	if err := json.Unmarshal(b, &back); err != nil || back.Type != NodeEventOffline || back.Node.ID != "n1" {
		t.Errorf("round-trip = %+v (err=%v)", back, err)
	}
}

func TestIdentifyResultCarriesPubKey(t *testing.T) {
	b, _ := json.Marshal(IdentifyResult{ID: "n1", IdentityPubKey: "PUB1"})
	if !strings.Contains(string(b), `"identity_pubkey":"PUB1"`) {
		t.Errorf("IdentifyResult missing identity_pubkey: %s", b)
	}
	// omitempty: absent when unset
	b2, _ := json.Marshal(IdentifyResult{ID: "n1"})
	if strings.Contains(string(b2), "identity_pubkey") {
		t.Errorf("empty identity_pubkey must be omitted: %s", b2)
	}
}
