package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadProcSession(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("37699.json", `{"pid":37699,"sessionId":"d10ac1db","cwd":"/repo/argus","name":"vendor-parser","status":"busy","version":"2.1.177"}`)
	write("1.json", `not json`)             // malformed
	write("2.json", `{"pid":2,"cwd":"/x"}`) // no sessionId

	ps, ok := readProcSession(dir, 37699)
	if !ok {
		t.Fatal("expected a hit for 37699.json")
	}
	if ps.SessionID != "d10ac1db" || ps.Cwd != "/repo/argus" || ps.Name != "vendor-parser" {
		t.Fatalf("parsed wrong: %+v", ps)
	}

	if _, ok := readProcSession(dir, 1); ok {
		t.Error("malformed json should return ok=false")
	}
	if _, ok := readProcSession(dir, 2); ok {
		t.Error("missing sessionId should return ok=false")
	}
	if _, ok := readProcSession(dir, 99999); ok {
		t.Error("missing file should return ok=false")
	}
	if _, ok := readProcSession(dir, 0); ok {
		t.Error("pid<=0 should return ok=false")
	}
}
