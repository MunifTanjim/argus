package tunnel

import (
	"log/slog"
	"testing"
)

func TestNgrokName(t *testing.T) {
	if (Ngrok{}).Name() != "ngrok" {
		t.Errorf("Name = %q", (Ngrok{}).Name())
	}
}

func TestNgrokCommandEphemeral(t *testing.T) {
	n := Ngrok{Bin: "/usr/bin/ngrok"}
	spec, err := n.Command("http://127.0.0.1:8443")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if spec.Path != "/usr/bin/ngrok" {
		t.Errorf("Path = %q", spec.Path)
	}
	want := []string{"http", "http://127.0.0.1:8443", "--log", "stdout", "--log-format", "logfmt"}
	if !equal(spec.Args, want) {
		t.Errorf("Args = %v, want %v", spec.Args, want)
	}
}

func TestNgrokCommandReservedDomain(t *testing.T) {
	n := Ngrok{Bin: "ngrok", Domain: "argus.example.com"}
	spec, err := n.Command("http://127.0.0.1:9000")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := []string{"http", "http://127.0.0.1:9000", "--log", "stdout", "--log-format", "logfmt", "--url", "argus.example.com"}
	if !equal(spec.Args, want) {
		t.Errorf("Args = %v, want %v", spec.Args, want)
	}
}

func TestNgrokExtractURL(t *testing.T) {
	n := Ngrok{}
	cases := []struct {
		line    string
		want    string
		matches bool
	}{
		// started-tunnel line: url= is public, addr= is local
		{`t=2026-07-14T00:00:00+0000 lvl=info msg="started tunnel" obj=tunnels name=command_line addr=http://127.0.0.1:8443 url=https://abc123.ngrok-free.app`, "https://abc123.ngrok-free.app", true},
		{`lvl=info msg="started tunnel" addr=http://127.0.0.1:8443 url=https://argus.example.com`, "https://argus.example.com", true},
		// non-tunnel line with a url= field must not match
		{`lvl=info msg="join connections" url=https://abc123.ngrok-free.app`, "", false},
		{`lvl=info msg="client session established"`, "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := n.ExtractURL(tc.line)
		if ok != tc.matches || got != tc.want {
			t.Errorf("ExtractURL(%q) = (%q, %v), want (%q, %v)", tc.line, got, ok, tc.want, tc.matches)
		}
	}
}

func TestNgrokExtractURLIgnoresLocalAddr(t *testing.T) {
	// A started-tunnel line whose only URL-like field is the local addr must not match.
	n := Ngrok{}
	if got, ok := n.ExtractURL(`lvl=info msg="started tunnel" addr=http://127.0.0.1:8443`); ok {
		t.Errorf("ExtractURL matched local addr: %q", got)
	}
}

func TestNgrokClassifyLine(t *testing.T) {
	n := Ngrok{}
	cases := []struct {
		line string
		want slog.Level
	}{
		{`t=... lvl=info msg="started tunnel"`, slog.LevelDebug}, // chatty info demoted
		{`t=... lvl=debug msg="x"`, slog.LevelDebug},
		{`t=... lvl=warn msg="x"`, slog.LevelWarn},
		{`t=... lvl=eror msg="x"`, slog.LevelError}, // ngrok's spelling
		{`t=... lvl=crit msg="x"`, slog.LevelError},
		{"a plain continuation line", slog.LevelInfo},
		{"", slog.LevelInfo},
	}
	for _, tc := range cases {
		if got := n.ClassifyLine(tc.line); got != tc.want {
			t.Errorf("ClassifyLine(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}
