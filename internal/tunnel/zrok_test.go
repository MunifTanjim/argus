package tunnel

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

func TestZrokName(t *testing.T) {
	if (&Zrok{}).Name() != "zrok" {
		t.Errorf("Name = %q", (&Zrok{}).Name())
	}
}

func TestZrokCommandDefaultNamespace(t *testing.T) {
	z := Zrok{Bin: "/usr/bin/zrok2", Selection: "myapp"}
	spec, err := z.Command("http://127.0.0.1:8443")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if spec.Path != "/usr/bin/zrok2" {
		t.Errorf("Path = %q", spec.Path)
	}
	want := []string{"share", "public", "http://127.0.0.1:8443", "--headless", "-n", "public:myapp"}
	if !equal(spec.Args, want) {
		t.Errorf("Args = %v, want %v", spec.Args, want)
	}
}

func TestZrokCommandExplicitNamespace(t *testing.T) {
	z := Zrok{Bin: "zrok2", Selection: "custom:app"}
	spec, err := z.Command("http://127.0.0.1:9000")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := []string{"share", "public", "http://127.0.0.1:9000", "--headless", "-n", "custom:app"}
	if !equal(spec.Args, want) {
		t.Errorf("Args = %v, want %v", spec.Args, want)
	}
}

func TestZrokExtractURL(t *testing.T) {
	z := &Zrok{Selection: "myapp", host: "myapp.shares.zrok.io"} // host as resolved by Prepare
	cases := []struct {
		line    string
		want    string
		matches bool
	}{
		// real zrok2 headless output: JSON with the bare, scheme-less endpoint host
		{`{"level":"INFO","msg":"access your zrok share at the following endpoints:\n myapp.shares.zrok.io"}`, "https://myapp.shares.zrok.io", true},
		// a different host (note: share vs shares) must not match
		{"access your zrok share at myapp.share.zrok.io", "", false},
		// the API-endpoint line must not be mistaken for the share URL
		{"connecting to https://api-v2.zrok.io", "", false},
		{"booting", "", false},
	}
	for _, tc := range cases {
		got, ok := z.ExtractURL(tc.line)
		if ok != tc.matches || got != tc.want {
			t.Errorf("ExtractURL(%q) = (%q, %v), want (%q, %v)", tc.line, got, ok, tc.want, tc.matches)
		}
	}
}

func TestZrokExtractURLNoHostBeforePrepare(t *testing.T) {
	// Without a resolved host (Prepare not run), nothing matches.
	z := &Zrok{Selection: "myapp"}
	if _, ok := z.ExtractURL("myapp.shares.zrok.io"); ok {
		t.Error("ExtractURL must not match before the host is resolved")
	}
}

func TestZrokClassifyLine(t *testing.T) {
	z := Zrok{Selection: "myapp"}
	cases := []struct {
		line string
		want slog.Level
	}{
		{"[   0.123]    INFO main: started", slog.LevelDebug}, // chatty info demoted
		{"[   0.123]   DEBUG x", slog.LevelDebug},
		{"[   0.123] WARNING y", slog.LevelWarn},
		{"[   0.123]   ERROR z", slog.LevelError},
		{`{"level":"info","msg":"x"}`, slog.LevelDebug},
		{`{"level":"warning","msg":"x"}`, slog.LevelWarn},
		{`{"level":"error","msg":"x"}`, slog.LevelError},
		{"a plain continuation line", slog.LevelInfo},
		{"", slog.LevelInfo},
	}
	for _, tc := range cases {
		if got := z.ClassifyLine(tc.line); got != tc.want {
			t.Errorf("ClassifyLine(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// fakeZrokRunner records calls and replies keyed by the two-word subcommand
// ("list names", "list namespaces", "create name", "delete share").
type fakeZrokRunner struct {
	calls   [][]string
	stdouts map[string][]byte
	errs    map[string]error
	stderrs map[string][]byte
}

func zrokSub(args []string) string {
	if len(args) >= 2 {
		return args[0] + " " + args[1]
	}
	if len(args) == 1 {
		return args[0]
	}
	return ""
}

func (f *fakeZrokRunner) run(_ context.Context, _ string, args ...string) (stdout, stderr []byte, err error) {
	f.calls = append(f.calls, args)
	k := zrokSub(args)
	return f.stdouts[k], f.stderrs[k], f.errs[k]
}

func (f *fakeZrokRunner) called(sub string) bool { return f.callArgs(sub) != nil }

func (f *fakeZrokRunner) callArgs(sub string) []string {
	for _, c := range f.calls {
		if zrokSub(c) == sub {
			return c
		}
	}
	return nil
}

// nsList is the canned `zrok2 list namespaces --json` reply used by Prepare success paths.
var nsList = []byte(`[{"description":"*.shares.zrok.io","name":"shares.zrok.io","namespaceToken":"public"}]`)

func TestZrokPrepareCreatesNameWhenAbsent(t *testing.T) {
	fr := &fakeZrokRunner{stdouts: map[string][]byte{
		"list names":      []byte(`[]`),
		"list namespaces": nsList,
	}}
	z := &Zrok{Bin: "zrok2", Selection: "myapp", runner: fr.run}

	url, err := z.Prepare(context.Background())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if url != "" {
		t.Errorf("url = %q, want empty (reported from output once live)", url)
	}
	if z.host != "myapp.shares.zrok.io" {
		t.Errorf("resolved host = %q, want myapp.shares.zrok.io", z.host)
	}
	create := fr.callArgs("create name")
	if !contains(create, "myapp") || !contains(create, "public") {
		t.Errorf("create call = %v, want create name -n public myapp", create)
	}
}

func TestZrokPrepareSkipsCreateWhenNamePresent(t *testing.T) {
	fr := &fakeZrokRunner{stdouts: map[string][]byte{
		"list names":      []byte(`[{"name":"myapp","namespaceToken":"public","reserved":true}]`),
		"list namespaces": nsList,
	}}
	z := &Zrok{Bin: "zrok2", Selection: "myapp", runner: fr.run}

	if _, err := z.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if fr.called("create name") {
		t.Error("must not create when the name already exists")
	}
	if fr.called("delete share") {
		t.Error("must not delete when no share is bound")
	}
}

func TestZrokPrepareDeletesStaleShare(t *testing.T) {
	fr := &fakeZrokRunner{stdouts: map[string][]byte{
		"list names":      []byte(`[{"name":"myapp","namespaceToken":"public","reserved":true,"shareToken":"pvszalgfdji9"}]`),
		"list namespaces": nsList,
	}}
	z := &Zrok{Bin: "zrok2", Selection: "myapp", runner: fr.run}

	if _, err := z.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	del := fr.callArgs("delete share")
	if !contains(del, "pvszalgfdji9") {
		t.Errorf("delete call = %v, want delete share pvszalgfdji9", del)
	}
	if fr.called("create name") {
		t.Error("must not create an existing name")
	}
}

func TestZrokPrepareSurfacesDeleteError(t *testing.T) {
	fr := &fakeZrokRunner{
		stdouts: map[string][]byte{"list names": []byte(`[{"name":"myapp","shareToken":"abc"}]`)},
		errs:    map[string]error{"delete share": errors.New("exit 1")},
		stderrs: map[string][]byte{"delete share": []byte("error: boom")},
	}
	z := &Zrok{Bin: "zrok2", Selection: "myapp", runner: fr.run}

	if _, err := z.Prepare(context.Background()); err == nil {
		t.Fatal("Prepare should surface a delete-share error")
	}
}

func TestZrokPrepareToleratesDeleteNotFoundRace(t *testing.T) {
	fr := &fakeZrokRunner{
		stdouts: map[string][]byte{
			"list names":      []byte(`[{"name":"myapp","shareToken":"abc"}]`),
			"list namespaces": nsList,
		},
		errs:    map[string]error{"delete share": errors.New("exit 1")},
		stderrs: map[string][]byte{"delete share": []byte(`[ERROR]: unable to delete share ([DELETE /share][404] unshareNotFound "")`)},
	}
	z := &Zrok{Bin: "zrok2", Selection: "myapp", runner: fr.run}

	if _, err := z.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare should tolerate a delete not-found race, got %v", err)
	}
	if z.host != "myapp.shares.zrok.io" {
		t.Errorf("resolved host = %q, want myapp.shares.zrok.io", z.host)
	}
}

func TestZrokPrepareToleratesCreateConflictRace(t *testing.T) {
	fr := &fakeZrokRunner{
		stdouts: map[string][]byte{
			"list names":      []byte(`[]`),
			"list namespaces": nsList,
		},
		errs:    map[string]error{"create name": errors.New("exit 1")},
		stderrs: map[string][]byte{"create name": []byte(`[ERROR]: unable to create name ([POST /share/name][409] createShareNameConflict "")`)},
	}
	z := &Zrok{Bin: "zrok2", Selection: "myapp", runner: fr.run}

	if _, err := z.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare should tolerate a create conflict race, got %v", err)
	}
}

func TestZrokPrepareSurfacesListError(t *testing.T) {
	fr := &fakeZrokRunner{
		errs:    map[string]error{"list names": errors.New("exit 1")},
		stderrs: map[string][]byte{"list names": []byte("error: not enabled")},
	}
	z := &Zrok{Bin: "zrok2", Selection: "myapp", runner: fr.run}

	if _, err := z.Prepare(context.Background()); err == nil {
		t.Fatal("Prepare should surface a list-names error")
	}
}

func TestZrokPrepareSurfacesCreateError(t *testing.T) {
	fr := &fakeZrokRunner{
		stdouts: map[string][]byte{"list names": []byte(`[]`)},
		errs:    map[string]error{"create name": errors.New("exit 1")},
		stderrs: map[string][]byte{"create name": []byte("error: quota exceeded")},
	}
	z := &Zrok{Bin: "zrok2", Selection: "myapp", runner: fr.run}

	if _, err := z.Prepare(context.Background()); err == nil {
		t.Fatal("Prepare should surface a non-tolerated create error")
	}
}

func TestZrokPrepareSurfacesNamespaceError(t *testing.T) {
	fr := &fakeZrokRunner{
		stdouts: map[string][]byte{"list names": []byte(`[{"name":"myapp"}]`)},
		errs:    map[string]error{"list namespaces": errors.New("exit 1")},
		stderrs: map[string][]byte{"list namespaces": []byte("error: boom")},
	}
	z := &Zrok{Bin: "zrok2", Selection: "myapp", runner: fr.run}

	if _, err := z.Prepare(context.Background()); err == nil {
		t.Fatal("Prepare should surface a list-namespaces error")
	}
}
