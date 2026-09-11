package main

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/gateway"
)

func TestRegistrationURL(t *testing.T) {
	tests := []struct {
		base  string
		appID string
		want  string
	}{
		{
			"https://pushport.muniftanjim.dev",
			"argus",
			"https://pushport.muniftanjim.dev/apps/argus/instances/register",
		},
		{
			"https://pushport.muniftanjim.dev/",
			"argus",
			"https://pushport.muniftanjim.dev/apps/argus/instances/register",
		},
		{
			"https://example.com//",
			"myapp",
			"https://example.com/apps/myapp/instances/register",
		},
	}
	for _, tt := range tests {
		got := registrationURL(tt.base, tt.appID)
		if got != tt.want {
			t.Errorf("registrationURL(%q, %q) = %q, want %q", tt.base, tt.appID, got, tt.want)
		}
	}
}

func TestPushPortRegisterEmptyAppID(t *testing.T) {
	opened := false
	orig := openURLFn
	openURLFn = func(string) error { opened = true; return nil }
	t.Cleanup(func() { openURLFn = orig })

	origReader := pushportStdinReader
	pushportStdinReader = strings.NewReader("pit_tok\n")
	t.Cleanup(func() { pushportStdinReader = origReader })

	root := newRootCmd("test") // pushPortAppID is "" in tests
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"pushport", "register", "--no-config", "--gateway", "ws://127.0.0.1:1", "--token", "tok"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for empty app id")
	}
	if opened {
		t.Error("opener must not be called when app id is empty")
	}
}

func TestPushPortRegisterMissingGateway(t *testing.T) {
	t.Setenv("ARGUS_GATEWAY_URL", "") // prevent shell env from bypassing the guard

	origAppID := pushPortAppID
	pushPortAppID = "testapp"
	t.Cleanup(func() { pushPortAppID = origAppID })

	orig := openURLFn
	openURLFn = func(string) error { return nil }
	t.Cleanup(func() { openURLFn = orig })

	origReader := pushportStdinReader
	pushportStdinReader = strings.NewReader("pit_tok\n")
	t.Cleanup(func() { pushportStdinReader = origReader })

	root := newRootCmd("test")
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	// --no-config isolates the test from any real config file, so it cannot
	// resolve (and mutate) a live gateway.
	root.SetArgs([]string{"pushport", "register", "--no-config"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error when --gateway is not set")
	}
}

func TestPushPortRegisterEmptyToken(t *testing.T) {
	origAppID := pushPortAppID
	pushPortAppID = "testapp"
	t.Cleanup(func() { pushPortAppID = origAppID })

	orig := openURLFn
	openURLFn = func(string) error { return nil }
	t.Cleanup(func() { openURLFn = orig })

	origReader := pushportStdinReader
	pushportStdinReader = strings.NewReader("\n")
	t.Cleanup(func() { pushportStdinReader = origReader })

	root := newRootCmd("test")
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"pushport", "register", "--no-config", "--gateway", "ws://127.0.0.1:1"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestPushPortRegisterCallsGateway(t *testing.T) {
	origAppID := pushPortAppID
	pushPortAppID = "testapp"
	t.Cleanup(func() { pushPortAppID = origAppID })

	var received string
	hsrv := gateway.NewServer(gateway.New(0), tokenAuth("admin"), tokenAuth("admin"))
	hsrv.SetClientTokens(nil, "admin") // make "admin" token grant admin principal
	hsrv.SetPushPortTokenSetter(func(tok string) error { received = tok; return nil })
	ts := httptest.NewServer(hsrv.Handler())
	defer ts.Close()

	orig := openURLFn
	openURLFn = func(string) error { return nil }
	t.Cleanup(func() { openURLFn = orig })

	origReader := pushportStdinReader
	pushportStdinReader = strings.NewReader("pit_newtoken\n")
	t.Cleanup(func() { pushportStdinReader = origReader })

	root := newRootCmd("test")
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"pushport", "register", "--no-config", "--gateway", wsURL(ts.URL), "--token", "admin"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if received != "pit_newtoken" {
		t.Errorf("received token = %q, want pit_newtoken", received)
	}
}
