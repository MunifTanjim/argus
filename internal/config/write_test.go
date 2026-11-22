package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func testValues() map[string]any {
	return map[string]any{
		"gateway.url":  "https://gw.example",
		"token":        "tok",
		"e2ee.enabled": true,
	}
}

func TestSetValuesCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	conflicts, err := SetValues(path, testValues(), false)
	if err != nil || conflicts != nil {
		t.Fatalf("SetValues = %v, %v", conflicts, err)
	}
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	if v.GetString("gateway.url") != "https://gw.example" || v.GetString("token") != "tok" || !v.GetBool("e2ee.enabled") {
		t.Fatalf("unexpected config: %v", v.AllSettings())
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
}

func TestSetValuesKeepsComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("# top\nfoo: 1 # keep\n"), 0o600)
	if _, err := SetValues(path, testValues(), false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.Contains(s, "# top") || !strings.Contains(s, "# keep") {
		t.Fatalf("comments lost:\n%s", s)
	}
	if strings.Index(s, "foo") > strings.Index(s, "gateway") {
		t.Fatalf("order changed:\n%s", s)
	}
}

func TestSetValuesConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := []byte("gateway:\n  url: https://other\ntoken: old\n")
	os.WriteFile(path, orig, 0o600)
	conflicts, err := SetValues(path, testValues(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(conflicts, ",") != "gateway.url,token" {
		t.Fatalf("conflicts = %v", conflicts)
	}
	b, _ := os.ReadFile(path)
	if string(b) != string(orig) {
		t.Fatalf("file changed:\n%s", b)
	}
}

func TestSetValuesForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("gateway:\n  url: https://other\n"), 0o600)
	conflicts, err := SetValues(path, testValues(), true)
	if err != nil || conflicts != nil {
		t.Fatalf("SetValues = %v, %v", conflicts, err)
	}
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	if v.GetString("gateway.url") != "https://gw.example" || v.GetString("token") != "tok" {
		t.Fatalf("unexpected config: %v", v.AllSettings())
	}
}

func TestSetValuesEqualIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := []byte("# c\ngateway:\n    url: https://gw.example\ntoken:   tok\ne2ee: {enabled: true}\n")
	os.WriteFile(path, orig, 0o600)
	conflicts, err := SetValues(path, testValues(), false)
	if err != nil || conflicts != nil {
		t.Fatalf("SetValues = %v, %v", conflicts, err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != string(orig) {
		t.Fatalf("file changed:\n%s", b)
	}
}

func TestSetValuesFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.yaml")
	link := filepath.Join(dir, "config.yaml")
	os.WriteFile(target, []byte("foo: 1\n"), 0o600)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := SetValues(link, testValues(), false); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v, %v", fi, err)
	}
	b, _ := os.ReadFile(target)
	if !strings.Contains(string(b), "token: tok") {
		t.Fatalf("target not updated:\n%s", b)
	}
}

func TestSetValuesKeepsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("foo: 1\n"), 0o644)
	os.Chmod(path, 0o644)
	if _, err := SetValues(path, testValues(), false); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
}

func TestSetValuesCommentOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("# only comment\n"), 0o600)
	if _, err := SetValues(path, testValues(), false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "# only comment") || !strings.Contains(string(b), "token: tok") {
		t.Fatalf("unexpected output:\n%s", b)
	}
}

func TestSetValuesKeepsTwoSpaceIndent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	const in = "gateway:\n  url: https://gw.example\nlog:\n  level: debug\n"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetValues(path, map[string]any{"e2ee.enabled": true}, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := in + "e2ee:\n  enabled: true\n"
	if string(got) != want {
		t.Fatalf("config =\n%s\nwant\n%s", got, want)
	}
}
