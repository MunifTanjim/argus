package pi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionFileUpdateReadsAppendedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write := func(flag int, s string) {
		t.Helper()
		f, err := os.OpenFile(path, flag|os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(sf *sessionFile) []string {
		var out []string
		for _, e := range sf.entries {
			out = append(out, e.ID)
		}
		return out
	}
	check := func(sf *sessionFile, want ...string) {
		t.Helper()
		if err := sf.update(path); err != nil {
			t.Fatal(err)
		}
		got := ids(sf)
		if len(got) != len(want) {
			t.Fatalf("entries = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] || sf.byID[want[i]] != i {
				t.Fatalf("entries = %v (byID %v), want %v", got, sf.byID, want)
			}
		}
	}

	sf := &sessionFile{}
	check(sf)
	write(os.O_TRUNC, `{"type":"session","version":3,"id":"s","cwd":"/w"}`+"\n"+`{"type":"message","id":"a"}`+"\n")
	check(sf, "a")
	// A line still being written waits for its newline.
	write(os.O_APPEND, `{"type":"message","id":"b"`)
	check(sf, "a")
	write(os.O_APPEND, `,"parentId":"a"}`+"\n")
	check(sf, "a", "b")
	// A migration rewrites the file from its header, longer than before.
	write(os.O_TRUNC, `{"type":"session","version":4,"id":"s","cwd":"/w"}`+"\n"+`{"type":"message","id":"c","parentId":null}`+"\n"+`{"type":"message","id":"d","parentId":"c"}`+"\n"+`{"type":"message","id":"e","parentId":"d"}`+"\n")
	check(sf, "c", "d", "e")
	// A shorter file with the same header starts over too.
	write(os.O_TRUNC, `{"type":"session","version":4,"id":"s","cwd":"/w"}`+"\n"+`{"type":"message","id":"f"}`+"\n")
	check(sf, "f")
}
