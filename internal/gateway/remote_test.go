package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestRemoteSourceRelaysUnknownCapabilities(t *testing.T) {
	var id api.IdentifyResult
	if err := json.Unmarshal([]byte(`{"id":"n1","label":"box","capabilities":{"spawn_session":true,"future_cap":true}}`), &id); err != nil {
		t.Fatal(err)
	}
	src := NewRemoteSource(id, nil)
	for name, v := range map[string]any{
		"nodes.list":  descriptor("n1", &srcState{src: src}),
		"server.info": api.NodeInfo{ID: "n1", Capabilities: src.Capabilities()},
	} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"future_cap":true`) {
			t.Errorf("%s dropped the unknown capability: %s", name, b)
		}
	}
}
