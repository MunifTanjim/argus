package codextool

import (
	"reflect"
	"testing"
)

func TestParsePatch(t *testing.T) {
	in := "*** Begin Patch\n" +
		"*** Add File: /r/new.txt\n+hello\n+world\n" +
		"*** Update File: /r/a.go\n*** Move to: /r/b.go\n@@ func main\n-old\n+new\n same\n" +
		"*** Delete File: /r/gone.txt\n" +
		"*** End Patch\n"
	files, ok := ParsePatch(in)
	if !ok {
		t.Fatal("valid patch rejected")
	}
	want := []PatchFile{
		{Op: PatchAdd, Path: "/r/new.txt", Lines: []PatchLine{{'+', "hello"}, {'+', "world"}}},
		{Op: PatchUpdate, Path: "/r/a.go", MoveTo: "/r/b.go", Lines: []PatchLine{{'@', "func main"}, {'-', "old"}, {'+', "new"}, {' ', "same"}}},
		{Op: PatchDelete, Path: "/r/gone.txt"},
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files =\n%+v\nwant\n%+v", files, want)
	}
}

func TestParsePatchRejectsNonPatch(t *testing.T) {
	for _, in := range []string{"", "echo hi", "*** Begin Patch\n+orphan line\n*** End Patch\n"} {
		if files, ok := ParsePatch(in); ok {
			t.Errorf("ParsePatch(%q) = %+v, want !ok", in, files)
		}
	}
}

func TestPatchSummary(t *testing.T) {
	one := []PatchFile{{Op: PatchUpdate, Path: "/r/a.go"}}
	moved := []PatchFile{{Op: PatchUpdate, Path: "/r/a.go", MoveTo: "/r/b.go"}}
	two := []PatchFile{{Path: "/r/a"}, {Path: "/r/b"}}
	for _, c := range []struct {
		files []PatchFile
		want  string
	}{{one, "/r/a.go"}, {moved, "/r/b.go"}, {two, "2 files"}, {nil, ""}} {
		if got := PatchSummary(c.files); got != c.want {
			t.Errorf("PatchSummary(%+v) = %q, want %q", c.files, got, c.want)
		}
	}
}
