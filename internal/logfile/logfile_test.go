package logfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if path := os.Getenv("ARGUS_TEST_LOGFILE_REDIRECT"); path != "" {
		os.Exit(redirectChild(path))
	}
	os.Exit(m.Run())
}

func redirectChild(path string) int {
	f, err := Open(path, 100)
	if err != nil {
		return 1
	}
	if err := f.RedirectStd(); err != nil {
		return 1
	}
	_, _ = os.Stderr.WriteString("first-stderr-line\n")
	for range 2 {
		if _, err := f.Write([]byte(strings.Repeat("x", 60))); err != nil {
			return 1
		}
	}
	_, _ = os.Stderr.WriteString("second-stderr-line\n")
	return 0
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOpenRotatesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "argus.log")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte("older"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := readFile(t, path+".1"); got != "old" {
		t.Fatalf("path.1 = %q, want old", got)
	}
	if got := readFile(t, path); got != "" {
		t.Fatalf("path = %q, want empty", got)
	}
}

func TestOpenCreatesDir0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs", "nested")
	path := filepath.Join(dir, "argus.log")
	f, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", di.Mode().Perm())
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestWriteRotatesAtMaxSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "argus.log")
	f, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	first, second := strings.Repeat("a", 60), strings.Repeat("b", 60)
	if _, err := f.Write([]byte(first)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(second)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path+".1"); got != first {
		t.Fatalf("path.1 = %q, want first write", got)
	}
	if got := readFile(t, path); got != second {
		t.Fatalf("path = %q, want second write", got)
	}
}

func TestWriteLargerThanMaxGoesToNewFileUnsplit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "argus.log")
	f, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write([]byte("small")); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("z", 50)
	if _, err := f.Write([]byte(big)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != big {
		t.Fatalf("path = %q, want the whole big write", got)
	}
	if got := readFile(t, path+".1"); got != "small" {
		t.Fatalf("path.1 = %q, want small", got)
	}
}

func TestRotationKeepsOnePrevious(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "argus.log")
	f, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, s := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc", "dddddddd"} {
		if _, err := f.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 || names[0] != "argus.log" || names[1] != "argus.log.1" {
		t.Fatalf("files = %v, want argus.log and argus.log.1", names)
	}
	if got := readFile(t, path+".1"); got != "cccccccc" {
		t.Fatalf("path.1 = %q, want cccccccc", got)
	}
	if got := readFile(t, path); got != "dddddddd" {
		t.Fatalf("path = %q, want dddddddd", got)
	}
}

func TestRedirectStdFollowsRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "argus.log")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "ARGUS_TEST_LOGFILE_REDIRECT="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if got := readFile(t, path+".1"); !strings.Contains(got, "first-stderr-line") {
		t.Fatalf("path.1 = %q, want first stderr line", got)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "second-stderr-line") || strings.Contains(got, "first-stderr-line") {
		t.Fatalf("path = %q, want only the second stderr line", got)
	}
}

func TestFailedRotationKeepsWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "argus.log")
	f, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write([]byte("aaaaaaaa")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	n, err := f.Write([]byte("bbbbbbbb"))
	if err == nil {
		t.Fatal("want the rotation error")
	}
	if n != 8 {
		t.Fatalf("n = %d, want the write to land", n)
	}
	if _, err := f.Write([]byte("cccccccc")); err == nil {
		t.Fatal("want the rotation error again")
	}
	if got := readFile(t, path); got != "aaaaaaaabbbbbbbbcccccccc" {
		t.Fatalf("path = %q, want every write", got)
	}
}
