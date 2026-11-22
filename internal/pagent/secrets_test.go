package pagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretBoxRoundTripAndKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "pagent-secrets.key")
	b, err := OpenSecretBox(path)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 || fi.Size() != 32 {
		t.Fatalf("key = %v %v", fi, err)
	}
	enc, err := b.Seal("ag1", "TOKEN", "s3cr3t-value")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(enc), "s3cr3t") {
		t.Fatal("value not encrypted")
	}
	b2, err := OpenSecretBox(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := b2.Open("ag1", "TOKEN", enc); err != nil || v != "s3cr3t-value" {
		t.Fatalf("open = %q %v", v, err)
	}
	if _, err := b2.Open("ag2", "TOKEN", enc); err == nil {
		t.Error("value opened under another agent")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSecretBox(path); err == nil {
		t.Error("world-readable key accepted")
	}
}

func TestSecretStore(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	b, err := OpenSecretBox(filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{{"B_TOKEN", "bbbb-1"}, {"A_KEY", "aaaa-1"}, {"A_KEY", "aaaa-2"}} {
		enc, _ := b.Seal("ag1", kv[0], kv[1])
		if err := s.SetSecret(ctx, "ag1", kv[0], "desc "+kv[0], enc); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.SecretValues(ctx, b, "ag1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "A_KEY" || list[0].Value != "aaaa-2" || list[1].Value != "bbbb-1" {
		t.Fatalf("values = %+v", list)
	}
	meta, _ := s.Secrets(ctx, "ag1")
	if meta[0].Value != "" || meta[0].Description != "desc A_KEY" {
		t.Fatalf("meta = %+v", meta)
	}
	if err := s.DeleteSecret(ctx, "ag1", "A_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSecret(ctx, "ag1", "A_KEY"); err != ErrNotFound {
		t.Fatalf("second delete = %v", err)
	}
}

func TestRedact(t *testing.T) {
	secrets := []Secret{{Name: "TOKEN", Value: `ab"cd\ef`}, {Name: "LONG", Value: "abcdefgh-long"}, {Name: "SHORT", Value: "ab"}}
	got := Redact(`curl -H "x: abcdefgh-long" {"t":"ab\"cd\\ef"} ab ab"cd\ef`, secrets)
	want := `curl -H "x: «LONG»" {"t":"«TOKEN»"} ab «TOKEN»`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if !ValidSecretName("GH_TOKEN") || ValidSecretName("gh") || ValidSecretName("1X") {
		t.Error("ValidSecretName")
	}
}
