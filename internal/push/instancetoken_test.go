package push

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceTokenRoundTripAndMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "pushport-token")

	got, err := ReadInstanceToken(path)
	if err != nil || got != "" {
		t.Fatalf("missing file: got %q, err %v; want empty, nil", got, err)
	}
	if err := WriteInstanceToken(path, "pit_abc\n"); err != nil {
		t.Fatal(err)
	}
	got, err = ReadInstanceToken(path)
	if err != nil || got != "pit_abc" {
		t.Fatalf("round-trip: got %q, err %v", got, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}
