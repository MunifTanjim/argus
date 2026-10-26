package adapter

import (
	"net"
	"testing"
	"time"
)

func TestMCPEndpointLive(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	if !MCPEndpointLive("codex", "http://"+l.Addr().String()+"/mcp/codex") {
		t.Fatal("live listener not detected")
	}
	for _, url := range []string{"", "://bad", "http://127.0.0.1/mcp"} {
		if MCPEndpointLive("codex", url) {
			t.Fatalf("MCPEndpointLive(%q) = true", url)
		}
	}
}

func TestMCPEndpointLiveDialsHostPort(t *testing.T) {
	var got string
	var timeout time.Duration
	t.Cleanup(SetMCPDialerForTest(func(addr string, d time.Duration) (net.Conn, error) {
		got, timeout = addr, d
		return nil, net.ErrClosed
	}))
	if MCPEndpointLive("opencode", "http://127.0.0.1:7734/mcp/opencode") {
		t.Fatal("failed dial reported live")
	}
	if got != "127.0.0.1:7734" || timeout != 200*time.Millisecond {
		t.Fatalf("dialed %q with %v", got, timeout)
	}
}
