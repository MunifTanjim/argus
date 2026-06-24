package tunnel

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestCloudflareCommandQuick(t *testing.T) {
	c := Cloudflare{Bin: "/usr/bin/cloudflared"} // empty Token => quick tunnel
	spec, err := c.Command("http://127.0.0.1:8443")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if spec.Path != "/usr/bin/cloudflared" {
		t.Errorf("Path = %q", spec.Path)
	}
	want := []string{"tunnel", "--no-autoupdate", "--url", "http://127.0.0.1:8443"}
	if !equal(spec.Args, want) {
		t.Errorf("Args = %v, want %v", spec.Args, want)
	}
}

func TestCloudflareCommandNamed(t *testing.T) {
	c := Cloudflare{Bin: "cloudflared", Token: "tok123"}
	spec, err := c.Command("http://127.0.0.1:8443")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := []string{"tunnel", "--no-autoupdate", "run", "--token", "tok123"}
	if !equal(spec.Args, want) {
		t.Errorf("Args = %v, want %v", spec.Args, want)
	}
}

func TestCloudflareExtractURLQuick(t *testing.T) {
	c := Cloudflare{Bin: "cloudflared"}
	line := "2026-06-18 INF |  https://fluffy-cat-123.trycloudflare.com  |"
	got, ok := c.ExtractURL(line)
	if !ok || got != "https://fluffy-cat-123.trycloudflare.com" {
		t.Errorf("ExtractURL = %q, %v", got, ok)
	}
	if _, ok := c.ExtractURL("2026-06-18 INF Starting tunnel"); ok {
		t.Error("non-URL line should not match")
	}
}

func TestCloudflareExtractURLNamedNeverMatches(t *testing.T) {
	c := Cloudflare{Bin: "cloudflared", Token: "tok"}
	// Named mode's hostname is configured on Cloudflare's side, not printed.
	if _, ok := c.ExtractURL("https://gateway.example.com is up"); ok {
		t.Error("named tunnel must not report a URL")
	}
}

func TestCloudflareCommandLocal(t *testing.T) {
	c := Cloudflare{Bin: "cloudflared", Tunnel: "argus"}
	spec, err := c.Command("http://127.0.0.1:8443")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := []string{"tunnel", "--no-autoupdate", "run", "--url", "http://127.0.0.1:8443", "argus"}
	if !equal(spec.Args, want) {
		t.Errorf("Args = %v, want %v", spec.Args, want)
	}
}

func TestCloudflareExtractURLLocalNeverMatches(t *testing.T) {
	c := Cloudflare{Bin: "cloudflared", Tunnel: "argus", Hostname: "argus.example.com"}
	// Locally-managed mode's hostname is known ahead of time (reported by Prepare),
	// not scraped from output.
	if _, ok := c.ExtractURL("https://argus.example.com is up"); ok {
		t.Error("locally-managed tunnel must not report a URL via ExtractURL")
	}
}

func TestCloudflareCommandLogLevel(t *testing.T) {
	cases := []struct {
		name string
		c    Cloudflare
		want []string
	}{
		{
			name: "quick",
			c:    Cloudflare{Bin: "cloudflared", LogLevel: "warn"},
			want: []string{"tunnel", "--no-autoupdate", "--loglevel", "warn", "--url", "http://127.0.0.1:8443"},
		},
		{
			name: "remote",
			c:    Cloudflare{Bin: "cloudflared", Token: "tok", LogLevel: "error"},
			want: []string{"tunnel", "--no-autoupdate", "--loglevel", "error", "run", "--token", "tok"},
		},
		{
			name: "local",
			c:    Cloudflare{Bin: "cloudflared", Tunnel: "argus", LogLevel: "debug"},
			want: []string{"tunnel", "--no-autoupdate", "--loglevel", "debug", "run", "--url", "http://127.0.0.1:8443", "argus"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := tc.c.Command("http://127.0.0.1:8443")
			if err != nil {
				t.Fatalf("Command: %v", err)
			}
			if !equal(spec.Args, tc.want) {
				t.Errorf("Args = %v, want %v", spec.Args, tc.want)
			}
		})
	}
}

func TestCloudflareClassifyLine(t *testing.T) {
	c := Cloudflare{Bin: "cloudflared"}
	cases := []struct {
		line string
		want slog.Level
	}{
		{"2026-06-19T16:29:34Z DBG connecting", slog.LevelDebug},
		{"2026-06-19T16:29:34Z INF Registered tunnel connection", slog.LevelInfo},
		{"2026-06-19T16:29:34Z WRN failed to serve tunnel connection", slog.LevelWarn},
		{"2026-06-19T16:29:34Z ERR no connection", slog.LevelError},
		{"2026-06-19T16:29:34Z FTL fatal boom", slog.LevelError},
		{"a continuation line with no level token", slog.LevelInfo},
		{"", slog.LevelInfo},
	}
	for _, tc := range cases {
		if got := c.ClassifyLine(tc.line); got != tc.want {
			t.Errorf("ClassifyLine(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// recordingRunner captures each command's args and replies from a scripted queue
// keyed by the subcommand verb (the first non-flag arg after "tunnel").
type recordingRunner struct {
	calls   [][]string
	replies map[string]runnerReply
}

type runnerReply struct {
	stdout []byte
	stderr []byte
	err    error
}

func (r *recordingRunner) run(_ context.Context, _ string, args ...string) (stdout, stderr []byte, err error) {
	r.calls = append(r.calls, args)
	rep := r.replies[verb(args)]
	return rep.stdout, rep.stderr, rep.err
}

// verb returns the cloudflared subcommand from a `tunnel <sub...>` arg vector.
func verb(args []string) string {
	if len(args) < 2 {
		return ""
	}
	return args[1]
}

func (r *recordingRunner) called(v string) bool {
	for _, c := range r.calls {
		if verb(c) == v {
			return true
		}
	}
	return false
}

func TestCloudflarePrepareCreatesWhenAbsent(t *testing.T) {
	rr := &recordingRunner{replies: map[string]runnerReply{
		// Noisy stderr alongside the stdout array would corrupt parsing if the two
		// streams were merged; this asserts the list is read from stdout only.
		"list":   {stdout: []byte(`[{"name":"other"}]`), stderr: []byte(`{"level":"info","msg":"x"}`)},
		"create": {},
		"route":  {},
	}}
	c := Cloudflare{Bin: "cloudflared", Tunnel: "argus", Hostname: "argus.example.com", runner: rr.run}

	url, err := c.Prepare(context.Background())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if url != "https://argus.example.com" {
		t.Errorf("url = %q", url)
	}
	if !rr.called("create") {
		t.Error("expected a create call when tunnel is absent")
	}
	// Every cloudflared invocation is a `tunnel <sub...>` call.
	for _, call := range rr.calls {
		if len(call) == 0 || call[0] != "tunnel" {
			t.Errorf("call not a tunnel subcommand: %v", call)
		}
	}
	// route dns must carry --overwrite-dns, the tunnel, and the hostname.
	var routeCall []string
	for _, call := range rr.calls {
		if verb(call) == "route" {
			routeCall = call
		}
	}
	if !contains(routeCall, "--overwrite-dns") || !contains(routeCall, "argus") || !contains(routeCall, "argus.example.com") {
		t.Errorf("route call = %v", routeCall)
	}
}

func TestCloudflarePrepareReusesWhenPresent(t *testing.T) {
	rr := &recordingRunner{replies: map[string]runnerReply{
		"list":  {stdout: []byte(`[{"name":"argus"},{"name":"other"}]`)},
		"route": {},
	}}
	c := Cloudflare{Bin: "cloudflared", Tunnel: "argus", Hostname: "argus.example.com", runner: rr.run}

	if _, err := c.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if rr.called("create") {
		t.Error("must not create when the tunnel already exists")
	}
	if !rr.called("route") {
		t.Error("must still route dns when reusing")
	}
}

func TestCloudflarePrepareReturnsRunnerError(t *testing.T) {
	rr := &recordingRunner{replies: map[string]runnerReply{
		"list": {err: errors.New("boom"), stderr: []byte("api down")},
	}}
	c := Cloudflare{Bin: "cloudflared", Tunnel: "argus", Hostname: "argus.example.com", runner: rr.run}

	_, err := c.Prepare(context.Background())
	if err == nil || !strings.Contains(err.Error(), "list tunnels") {
		t.Fatalf("err = %v, want list-tunnels failure", err)
	}
}

func TestCloudflarePrepareNoopWhenNotLocal(t *testing.T) {
	rr := &recordingRunner{}
	c := Cloudflare{Bin: "cloudflared", Token: "tok", runner: rr.run}
	url, err := c.Prepare(context.Background())
	if err != nil || url != "" {
		t.Fatalf("Prepare = (%q, %v), want empty", url, err)
	}
	if len(rr.calls) != 0 {
		t.Errorf("runner should not be invoked, got %v", rr.calls)
	}
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func equal(a, b []string) bool {
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
