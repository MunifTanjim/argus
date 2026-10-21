package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLaunchStateRoundTrip(t *testing.T) {
	sock := shortSocket(t)
	want := launchState{Mode: launchModeGateway, GatewayURL: "ws://127.0.0.1:1", Token: "tok"}
	if err := saveLaunchState(sock, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadLaunchState(sock)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if !got.needsGateway() || (launchState{Mode: launchModeIsolated}).needsGateway() {
		t.Fatal("needsGateway must be true only for the gateway mode")
	}
}

func TestLaunchStateMode0600(t *testing.T) {
	sock := shortSocket(t)
	if err := saveLaunchState(sock, launchState{Mode: launchModeIsolated}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(launchStatePath(sock))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
	entries, _ := os.ReadDir(filepath.Dir(sock))
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want only the state file", len(entries))
	}
}

func TestLoadLaunchStateMissing(t *testing.T) {
	st, err := loadLaunchState(shortSocket(t))
	if st != nil || err != nil {
		t.Fatalf("got %v, %v; want nil, nil", st, err)
	}
}

func TestRemoveLaunchStateMissing(t *testing.T) {
	if err := removeLaunchState(shortSocket(t)); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLaunchStateCorrupt(t *testing.T) {
	sock := shortSocket(t)
	if err := os.WriteFile(launchStatePath(sock), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLaunchState(sock); err == nil {
		t.Fatal("want an error for invalid JSON")
	}
}
