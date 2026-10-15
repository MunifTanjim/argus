package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/host"
)

func TestHandleHostInfo(t *testing.T) {
	d := newTestNode(t)
	out, err := d.DispatchFunc()(context.Background(), api.MethodHostInfo, nil)
	if err != nil {
		t.Fatal(err)
	}
	info, ok := out.(api.HostInfo)
	if !ok {
		t.Fatalf("result %T", out)
	}
	if info.UptimeSeconds < 0 {
		t.Fatalf("uptime %d", info.UptimeSeconds)
	}
	if info.OS == "" {
		t.Fatal("host.info must report the OS")
	}
}

func TestHandleHostSetWakelockRejectsPast(t *testing.T) {
	d := newTestNode(t)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	params, _ := json.Marshal(api.HostSetWakelockParams{Until: past})
	_, err := d.DispatchFunc()(context.Background(), api.MethodHostSetWakelock, params)
	var rpcErr *api.RPCError
	if err == nil {
		t.Fatal("past until must fail")
	}
	if !errors.As(err, &rpcErr) || rpcErr.Code != api.CodeInvalidRequest {
		t.Fatalf("err %v, want RPCError CodeInvalidRequest", err)
	}
}

func TestCapabilitiesReportWakelock(t *testing.T) {
	d := newTestNode(t)
	_, lookErr := exec.LookPath("caffeinate")
	want := runtime.GOOS == "darwin" && lookErr == nil
	if got := d.Capabilities().HostWakelock; got != want {
		t.Fatalf("caps.HostWakelock = %v, want %v", got, want)
	}
}

func TestWakelockRPCErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{fmt.Errorf("%w: x", host.ErrInvalidUntil), api.CodeInvalidRequest},
		{host.ErrWakelockUnsupported, api.CodeInvalidRequest},
		{errors.New("exec: caffeinate failed"), api.CodeInternalError},
	} {
		var rpcErr *api.RPCError
		if !errors.As(wakelockRPCError(tc.err), &rpcErr) || rpcErr.Code != tc.code || rpcErr.Message != tc.err.Error() {
			t.Errorf("wakelockRPCError(%v) = %v, want code %d", tc.err, rpcErr, tc.code)
		}
	}
}

func TestHandleHostCompleteDir(t *testing.T) {
	d := newTestNode(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, dir := range []string{"Dev", "Desktop", "Docs", ".dots", "Dev/argus"} {
		if err := os.Mkdir(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "Dexter"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "Docs"), filepath.Join(home, "Delta")); err != nil {
		t.Fatal(err)
	}
	complete := func(path string) api.HostCompleteDirResult {
		t.Helper()
		params, _ := json.Marshal(api.HostCompleteDirParams{Path: path})
		out, err := d.DispatchFunc()(context.Background(), api.MethodHostCompleteDir, params)
		if err != nil {
			t.Fatal(err)
		}
		return out.(api.HostCompleteDirResult)
	}
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"", []string{home + "/Delta/", home + "/Desktop/", home + "/Dev/", home + "/Docs/"}},
		{"~", []string{"~/Delta/", "~/Desktop/", "~/Dev/", "~/Docs/"}},
		{"~/De", []string{"~/Delta/", "~/Desktop/", "~/Dev/"}},
		{home + "/D", []string{home + "/Delta/", home + "/Desktop/", home + "/Dev/", home + "/Docs/"}},
		{"~/", []string{"~/Delta/", "~/Desktop/", "~/Dev/", "~/Docs/"}},
		{"~/.", []string{"~/.dots/"}},
		{"~/Dev/", []string{"~/Dev/argus/"}},
		{"~/nope/", []string{}},
		{"relative", []string{}},
	} {
		res := complete(tc.path)
		if res.Home != home {
			t.Fatalf("%q: home %q, want %q", tc.path, res.Home, home)
		}
		if fmt.Sprint(res.Paths) != fmt.Sprint(tc.want) {
			t.Errorf("%q: paths %v, want %v", tc.path, res.Paths, tc.want)
		}
	}
}

func TestExpandHome(t *testing.T) {
	for in, want := range map[string]string{
		"~":       "/home/u",
		"~/p":     "/home/u/p",
		"~other":  "~other",
		"/abs/~/": "/abs/~/",
		"":        "",
	} {
		if got := expandHome(in, "/home/u"); got != want {
			t.Errorf("expandHome(%q) = %q, want %q", in, got, want)
		}
	}
}
