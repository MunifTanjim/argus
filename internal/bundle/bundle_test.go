package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadRoundtrip(t *testing.T) {
	src := t.TempDir()
	txn := filepath.Join(src, "session.jsonl")
	if err := os.WriteFile(txn, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(src, "subagents", "agent-a.jsonl")
	if err := os.MkdirAll(filepath.Dir(sub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sub, []byte("subdata"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := Manifest{
		FormatVersion: FormatVersion, ArgusVersion: "9.9.9", ExportedAt: "2026-07-10T00:00:00Z",
		Agent: "claude", Entry: "root/session.jsonl",
		Metadata: Metadata{Title: "hi", Tokens: 42},
	}
	files := []SourceFile{
		{ArchivePath: "root/session.jsonl", SourcePath: txn},
		{ArchivePath: "root/subagents/agent-a.jsonl", SourcePath: sub},
	}

	var buf bytes.Buffer
	if err := Write(&buf, m, files); err != nil {
		t.Fatalf("Write: %v", err)
	}

	dst := t.TempDir()
	got, err := Read(&buf, dst)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Entry != "root/session.jsonl" || got.Metadata.Tokens != 42 || got.Agent != "claude" {
		t.Fatalf("manifest mismatch: %+v", got)
	}
	b, err := os.ReadFile(filepath.Join(dst, "root", "session.jsonl"))
	if err != nil || string(b) != "line1\nline2\n" {
		t.Fatalf("extracted transcript wrong: %q err=%v", b, err)
	}
	b2, _ := os.ReadFile(filepath.Join(dst, "root", "subagents", "agent-a.jsonl"))
	if string(b2) != "subdata" {
		t.Fatalf("extracted subagent wrong: %q", b2)
	}
}

func TestReadPersistsManifestForReuse(t *testing.T) {
	src := t.TempDir()
	txn := filepath.Join(src, "session.jsonl")
	if err := os.WriteFile(txn, []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := Manifest{
		FormatVersion: FormatVersion, Agent: "claude", Entry: "root/session.jsonl",
		Metadata: Metadata{Title: "reuse", Tokens: 7},
	}
	var buf bytes.Buffer
	if err := Write(&buf, m, []SourceFile{{ArchivePath: "root/session.jsonl", SourcePath: txn}}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	dst := t.TempDir()
	if _, err := Read(&buf, dst); err != nil {
		t.Fatalf("Read: %v", err)
	}
	// ReadManifest reads back the persisted manifest without re-extracting.
	got, err := ReadManifest(dst)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if got.Entry != "root/session.jsonl" || got.Metadata.Title != "reuse" || got.Metadata.Tokens != 7 {
		t.Fatalf("manifest mismatch: %+v", got)
	}
	if _, err := ReadManifest(t.TempDir()); err == nil {
		t.Fatal("ReadManifest on an un-extracted dir should error")
	}
}

func TestWriteCapsGrowingTranscript(t *testing.T) {
	src := t.TempDir()
	txn := filepath.Join(src, "session.jsonl")
	if err := os.WriteFile(txn, bytes.Repeat([]byte("x"), 1<<16), 0o644); err != nil {
		t.Fatal(err)
	}
	m := Manifest{FormatVersion: FormatVersion, Agent: "claude", Entry: "root/session.jsonl"}
	files := []SourceFile{{ArchivePath: "root/session.jsonl", SourcePath: txn}}

	// A writer appends a bounded amount concurrently while we export. The header
	// size is fixed at Stat time, so an unbounded copy would trip "write too
	// long"; growth is capped so the temp file stays small.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		f, err := os.OpenFile(txn, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		chunk := bytes.Repeat([]byte("y"), 4096)
		for i := 0; i < 256; i++ { // ~1 MiB total
			if _, err := f.Write(chunk); err != nil {
				return
			}
		}
	}()

	dst := t.TempDir()
	for {
		var buf bytes.Buffer
		if err := Write(&buf, m, files); err != nil {
			t.Fatalf("Write on growing file: %v", err)
		}
		if _, err := Read(&buf, dst); err != nil {
			t.Fatalf("Read: %v", err)
		}
		select {
		case <-writerDone:
			return
		default:
		}
	}
}

func TestWriteSkipsVanishedSidecar(t *testing.T) {
	src := t.TempDir()
	txn := filepath.Join(src, "session.jsonl")
	if err := os.WriteFile(txn, []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := Manifest{FormatVersion: FormatVersion, Agent: "claude", Entry: "root/session.jsonl"}
	files := []SourceFile{
		{ArchivePath: "root/session.jsonl", SourcePath: txn},
		{ArchivePath: "root/subagents/gone.jsonl", SourcePath: filepath.Join(src, "subagents", "gone.jsonl")},
	}
	var buf bytes.Buffer
	if err := Write(&buf, m, files); err != nil {
		t.Fatalf("Write should skip vanished sidecar: %v", err)
	}
	dst := t.TempDir()
	if _, err := Read(&buf, dst); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "root", "session.jsonl")); err != nil {
		t.Fatalf("entry missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "root", "subagents", "gone.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("vanished sidecar should be absent, got err=%v", err)
	}
}

func TestWriteCapsTotalPayload(t *testing.T) {
	orig := maxBundleBytes
	maxBundleBytes = 16
	defer func() { maxBundleBytes = orig }()

	src := t.TempDir()
	txn := filepath.Join(src, "session.jsonl")
	if err := os.WriteFile(txn, bytes.Repeat([]byte("x"), 64), 0o644); err != nil {
		t.Fatal(err)
	}
	m := Manifest{FormatVersion: FormatVersion, Agent: "claude", Entry: "root/session.jsonl"}
	files := []SourceFile{{ArchivePath: "root/session.jsonl", SourcePath: txn}}
	var buf bytes.Buffer
	if err := Write(&buf, m, files); err == nil {
		t.Fatal("expected error when payload exceeds cap")
	}
}

// A symlinked sidecar is skipped (like a vanished one) rather than archived with
// its target's bytes; a symlinked entry is a hard error.
func TestWriteSkipsSymlinkSidecarAndFailsEntry(t *testing.T) {
	src := t.TempDir()
	txn := filepath.Join(src, "session.jsonl")
	if err := os.WriteFile(txn, []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(src, "evil.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	m := Manifest{FormatVersion: FormatVersion, Agent: "claude", Entry: "root/session.jsonl"}
	files := []SourceFile{
		{ArchivePath: "root/session.jsonl", SourcePath: txn},
		{ArchivePath: "root/evil.jsonl", SourcePath: link},
	}
	var buf bytes.Buffer
	if err := Write(&buf, m, files); err != nil {
		t.Fatalf("Write should skip symlink sidecar: %v", err)
	}
	dst := t.TempDir()
	if _, err := Read(&buf, dst); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "root", "evil.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("symlink sidecar should be absent, got err=%v", err)
	}

	// The same symlink as the entry must fail closed.
	m.Entry = "root/evil.jsonl"
	if err := Write(&buf, m, []SourceFile{{ArchivePath: "root/evil.jsonl", SourcePath: link}}); err == nil {
		t.Fatal("expected error when entry is a symlink")
	}
}

func TestWriteFailsOnMissingEntry(t *testing.T) {
	src := t.TempDir()
	m := Manifest{FormatVersion: FormatVersion, Agent: "claude", Entry: "root/session.jsonl"}
	files := []SourceFile{{ArchivePath: "root/session.jsonl", SourcePath: filepath.Join(src, "nope.jsonl")}}
	var buf bytes.Buffer
	if err := Write(&buf, m, files); err == nil {
		t.Fatal("expected error when entry transcript is missing")
	}
}

func TestReadRejectsNewerFormat(t *testing.T) {
	src := t.TempDir()
	f := filepath.Join(src, "x")
	if err := os.WriteFile(f, []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := Manifest{FormatVersion: FormatVersion + 1, Agent: "claude", Entry: "root/x"}
	var buf bytes.Buffer
	if err := Write(&buf, m, []SourceFile{{ArchivePath: "root/x", SourcePath: f}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(&buf, t.TempDir()); err == nil {
		t.Fatal("expected error for newer format_version")
	}
}

func TestReadRejectsPathTraversal(t *testing.T) {
	// A tar entry escaping destDir must be rejected.
	var buf bytes.Buffer
	if err := writeRawTarGz(&buf, Manifest{FormatVersion: FormatVersion, Agent: "claude", Entry: "root/x"},
		map[string][]byte{"../evil": []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(&buf, t.TempDir()); err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestReadRejectsBackslashPath(t *testing.T) {
	var buf bytes.Buffer
	if err := writeRawTarGz(&buf, Manifest{FormatVersion: FormatVersion, Agent: "claude", Entry: "root/x"},
		map[string][]byte{`..\evil`: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(&buf, t.TempDir()); err == nil {
		t.Fatal("expected error for backslash path")
	}
}

func TestReadRejectsOversizedPayload(t *testing.T) {
	orig := maxBundleBytes
	maxBundleBytes = 16
	defer func() { maxBundleBytes = orig }()

	var buf bytes.Buffer
	if err := writeRawTarGz(&buf, Manifest{FormatVersion: FormatVersion, Agent: "claude", Entry: "root/x"},
		map[string][]byte{"root/x": bytes.Repeat([]byte("z"), 64)}); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(&buf, t.TempDir()); err == nil {
		t.Fatal("expected error when extracted payload exceeds cap")
	}
}

func TestReadRejectsOversizedManifest(t *testing.T) {
	orig := maxManifestBytes
	maxManifestBytes = 8
	defer func() { maxManifestBytes = orig }()

	var buf bytes.Buffer
	if err := writeRawTarGz(&buf, Manifest{FormatVersion: FormatVersion, Agent: "claude", Entry: "root/x"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(&buf, t.TempDir()); err == nil {
		t.Fatal("expected error when manifest exceeds cap")
	}
}

func TestReadRejectsManifestNotFirst(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("x")
	tw.WriteHeader(&tar.Header{Name: "root/x", Mode: 0o644, Size: int64(len(body))})
	tw.Write(body)
	mb, _ := json.MarshalIndent(Manifest{FormatVersion: FormatVersion, Agent: "claude", Entry: "root/x"}, "", "  ")
	tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o644, Size: int64(len(mb))})
	tw.Write(mb)
	tw.Close()
	gz.Close()

	if _, err := Read(&buf, t.TempDir()); err == nil {
		t.Fatal("expected error when manifest is not the first entry")
	}
}

func TestReadRejectsSymlinkEntry(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	mb, _ := json.MarshalIndent(Manifest{FormatVersion: FormatVersion, Agent: "claude", Entry: "root/x"}, "", "  ")
	tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o644, Size: int64(len(mb))})
	tw.Write(mb)
	tw.WriteHeader(&tar.Header{Name: "root/link", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	tw.Close()
	gz.Close()

	if _, err := Read(&buf, t.TempDir()); err == nil {
		t.Fatal("expected error for symlink tar entry")
	}
}

func writeRawTarGz(w io.Writer, m Manifest, entries map[string][]byte) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	mb, _ := json.MarshalIndent(m, "", "  ")
	tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(mb))})
	tw.Write(mb)
	for name, b := range entries {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b))})
		tw.Write(b)
	}
	tw.Close()
	return gz.Close()
}
