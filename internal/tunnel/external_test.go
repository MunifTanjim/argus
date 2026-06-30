package tunnel

import (
	"context"
	"errors"
	"testing"
)

func TestExternalReportsURLViaPrepare(t *testing.T) {
	e := External{URL: "wss://argus.example.com"}
	if e.Name() != "external" {
		t.Errorf("Name = %q", e.Name())
	}
	url, err := e.Prepare(context.Background())
	if err != nil || url != "wss://argus.example.com" {
		t.Fatalf("Prepare = (%q, %v)", url, err)
	}
}

func TestExternalCommandIsErrNoProcess(t *testing.T) {
	_, err := External{URL: "wss://h"}.Command("ignored")
	if !errors.Is(err, ErrNoProcess) {
		t.Fatalf("Command err = %v, want ErrNoProcess", err)
	}
}

func TestExternalExtractURLNeverMatches(t *testing.T) {
	if u, ok := (External{}).ExtractURL("https://anything"); ok {
		t.Fatalf("ExtractURL matched %q", u)
	}
}
