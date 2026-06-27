package push

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	argus "github.com/MunifTanjim/argus"
)

func TestRendererSelection(t *testing.T) {
	cases := []struct {
		name       string
		goos       string
		hasAlerter bool
		hasHS      bool
		hsReady    bool // hs CLI reaches a running Hammerspoon (ipc bridge up)
		click      bool
		want       string
	}{
		{"darwin alerter click", "darwin", true, false, false, true, "alerter"},
		{"darwin alerter preferred over hs", "darwin", true, true, true, true, "alerter"},
		{"darwin hs click (no alerter)", "darwin", false, true, true, true, "hammerspoon"},
		{"darwin hs ipc down (no alerter)", "darwin", false, true, false, true, "osascript"},
		{"darwin no click ignores alerter/hs", "darwin", true, true, true, false, "osascript"},
		{"darwin nothing clickable", "darwin", false, false, false, true, "osascript"},
		{"linux now unsupported", "linux", true, true, true, true, ""},
		{"unsupported", "plan9", true, true, true, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var click clickCmd
			if tc.click {
				click = func(id string) []string { return []string{"argus", "focus", id} }
			}
			o := NewOSNotifier(nil, click)
			o.goos = tc.goos
			o.lookPath = func(name string) (string, error) {
				if name == "alerter" && tc.hasAlerter {
					return "/opt/homebrew/bin/alerter", nil
				}
				if name == "hs" && tc.hasHS {
					return "/usr/local/bin/hs", nil
				}
				return "", errNotFound
			}
			o.hsAvailable = func() bool { return tc.hsReady }
			if got := o.rendererName(); got != tc.want {
				t.Fatalf("renderer = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAlerterFiresClickOnContentClicked(t *testing.T) {
	clicked := make(chan string, 1)
	o := NewOSNotifier(nil, func(id string) []string { clicked <- id; return []string{"argus", "focus", id} })
	o.goos = "darwin"
	o.lookPath = func(name string) (string, error) {
		if name == "alerter" {
			return "/opt/homebrew/bin/alerter", nil
		}
		return "", errNotFound
	}
	o.iconPath = func() (string, bool) { return "/tmp/argus-icon.png", true }
	var gotArgs []string
	o.output = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte("@CONTENTCLICKED\n"), nil
	}
	o.run = func(_ context.Context, _ string, _ ...string) error { return nil }
	o.Notify(context.Background(), Notification{Title: "t", Body: "b", Data: map[string]string{"session_id": "abc"}})

	select {
	case id := <-clicked:
		if id != "abc" {
			t.Fatalf("clicked id = %q, want abc", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("click not fired within timeout")
	}
	// per-session de-dupe is delegated to alerter via --group <session_id>
	if !containsPair(gotArgs, "--group", "abc") {
		t.Fatalf("alerter args %v missing --group abc", gotArgs)
	}
	// argus branding via --app-icon
	if !containsPair(gotArgs, "--app-icon", "/tmp/argus-icon.png") {
		t.Fatalf("alerter args %v missing --app-icon", gotArgs)
	}
}

func TestAlerterNoClickOnTimeout(t *testing.T) {
	o := NewOSNotifier(nil, func(id string) []string { t.Fatalf("click fired on timeout for %q", id); return nil })
	o.goos = "darwin"
	o.lookPath = func(name string) (string, error) {
		if name == "alerter" {
			return "/opt/homebrew/bin/alerter", nil
		}
		return "", errNotFound
	}
	o.iconPath = func() (string, bool) { return "", false }
	done := make(chan struct{})
	o.output = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		defer close(done)
		return []byte("@TIMEOUT\n"), nil
	}
	o.run = func(_ context.Context, _ string, _ ...string) error { return nil }
	o.Notify(context.Background(), Notification{Title: "t", Body: "b", Data: map[string]string{"session_id": "abc"}})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("alerter goroutine did not finish")
	}
}

// containsPair reports whether args contains a,b adjacently (flag, value).
func containsPair(args []string, a, b string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}

func TestNotifyArgvPerOS(t *testing.T) {
	n := Notification{Title: "repo", Body: "Permission: Bash"}
	cases := []struct {
		goos     string
		wantName string
		wantOK   bool
	}{
		{"darwin", "osascript", true},
		{"linux", "", false},
		{"plan9", "", false},
	}
	for _, tc := range cases {
		name, args, ok := notifyArgv(tc.goos, n)
		if ok != tc.wantOK || name != tc.wantName {
			t.Fatalf("%s: name=%q ok=%v, want %q/%v", tc.goos, name, ok, tc.wantName, tc.wantOK)
		}
		if ok && len(args) == 0 {
			t.Fatalf("%s: empty args", tc.goos)
		}
	}
}

func TestDesktopTitleHasArgusPrefix(t *testing.T) {
	// Desktop notifications are delivered by Hammerspoon/the terminal, so the
	// title is branded with an "Argus · " prefix (mobile is unaffected).
	var script string
	o := NewOSNotifier(nil, nil) // no click -> osascript on darwin
	o.goos = "darwin"
	o.lookPath = func(string) (string, error) { return "", errNotFound }
	o.run = func(_ context.Context, _ string, args ...string) error {
		script = args[len(args)-1] // osascript -e <script>
		return nil
	}
	o.Notify(context.Background(), Notification{Title: "repo", Body: "b"})
	if !strings.Contains(script, `with title "Argus · repo"`) {
		t.Fatalf("osascript missing Argus-prefixed title:\n%s", script)
	}
}

func TestEmbeddedIconIsPNG(t *testing.T) {
	if len(argus.IconPNG) < 8 {
		t.Fatalf("embedded argus icon too small: %d bytes", len(argus.IconPNG))
	}
	// PNG signature: 0x89 'P' 'N' 'G' 0x0D 0x0A 0x1A 0x0A
	if argus.IconPNG[0] != 0x89 || string(argus.IconPNG[1:4]) != "PNG" {
		t.Fatalf("embedded icon is not a PNG (first bytes % x)", argus.IconPNG[:8])
	}
}

func TestQuotingHelpers(t *testing.T) {
	if got := appleQuote(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("appleQuote = %s, want \"a\\\"b\\\\c\"", got)
	}
}

func TestOSNotifierUnsupportedOSNoop(t *testing.T) {
	called := false
	o := NewOSNotifier(nil, nil)
	o.goos = "plan9"
	o.run = func(_ context.Context, _ string, _ ...string) error { called = true; return nil }
	o.Notify(context.Background(), Notification{Title: "T", Body: "B"})
	if called {
		t.Fatal("ran a command on an unsupported OS; want no-op")
	}
}

func TestLuaQuote(t *testing.T) {
	if got := luaQuote(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("luaQuote = %s, want \"a\\\"b\\\\c\"", got)
	}
	// control chars must be escaped so Lua short strings remain valid syntax
	if got := luaQuote("a\nb\tc"); got != `"a\nb\tc"` {
		t.Errorf("luaQuote ctrl = %s, want \"a\\nb\\tc\"", got)
	}
}

func TestLuaQuoteRoundTrip(t *testing.T) {
	// round-trip: luaUnescape(body(luaQuote(s))) == s
	inputs := []string{
		`plain`,
		`a"b\c`,
		"newline\nand\ttab",
		"carriage\rreturn",
		"mixed\n\"quote\"\tand\\back",
	}
	for _, s := range inputs {
		quoted := luaQuote(s)
		// strip surrounding quotes
		body := quoted[1 : len(quoted)-1]
		if got := luaUnescape(body); got != s {
			t.Errorf("round-trip failed for %q: luaUnescape(body) = %q", s, got)
		}
	}
}

// luaUnescape reverses luaQuote's escaping of a Lua double-quoted string body
// (the content between the surrounding quotes): \\ -> \, \" -> ", \n -> newline,
// \r -> CR, \t -> tab.
func luaUnescape(s string) string {
	r := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n", `\r`, "\r", `\t`, "\t")
	return r.Replace(s)
}

func TestHammerspoonEscapesSpecialChars(t *testing.T) {
	id := "weird'id\"with\\back\nand\ttab"
	o := NewOSNotifier(nil, func(s string) []string { return []string{"/usr/bin/argus", "focus", s} })
	o.goos = "darwin"
	o.lookPath = func(name string) (string, error) {
		if name == "hs" {
			return "/usr/local/bin/hs", nil
		}
		return "", errNotFound
	}
	o.hsAvailable = func() bool { return true }
	var lua string
	o.run = func(_ context.Context, _ string, args ...string) error {
		lua = args[len(args)-1]
		return nil
	}
	o.Notify(context.Background(), Notification{Title: "t", Body: "b", Data: map[string]string{"session_id": id}})

	// Extract the body of hs.execute("...") — the first double-quoted literal
	// after `hs.execute(`.
	const marker = `hs.execute("`
	i := strings.Index(lua, marker)
	if i < 0 {
		t.Fatalf("no hs.execute literal in lua:\n%s", lua)
	}
	rest := lua[i+len(marker):]
	// The literal ends at `", true)`.
	j := strings.Index(rest, `", true)`)
	if j < 0 {
		t.Fatalf("malformed hs.execute literal in lua:\n%s", lua)
	}
	got := luaUnescape(rest[:j])
	want := shellJoin([]string{"/usr/bin/argus", "focus", id})
	if got != want {
		t.Fatalf("escaped command mismatch:\n got=%s\nwant=%s", got, want)
	}
}

func TestHammerspoonBuildsClickLua(t *testing.T) {
	var gotName string
	var gotArgs []string
	o := NewOSNotifier(nil, func(id string) []string { return []string{"/usr/bin/argus", "focus", id} })
	o.goos = "darwin"
	o.lookPath = func(name string) (string, error) {
		if name == "hs" {
			return "/usr/local/bin/hs", nil
		}
		return "", errNotFound
	}
	o.hsAvailable = func() bool { return true }
	o.run = func(_ context.Context, name string, args ...string) error {
		gotName, gotArgs = name, args
		return nil
	}
	o.Notify(context.Background(), Notification{Title: "repo", Body: "Permission: Bash", Data: map[string]string{"session_id": "nodeA:abc"}})

	if gotName != "hs" || len(gotArgs) != 2 || gotArgs[0] != "-c" {
		t.Fatalf("argv = %s %v, want hs -c <lua>", gotName, gotArgs)
	}
	lua := gotArgs[1]
	for _, want := range []string{`hs.notify.new(`, `"Argus · repo"`, `"Permission: Bash"`, `focus`, `nodeA:abc`} {
		if !strings.Contains(lua, want) {
			t.Fatalf("lua missing %q:\n%s", want, lua)
		}
	}
}

func TestHammerspoonDedupesBySession(t *testing.T) {
	capture := func(data map[string]string) string {
		o := NewOSNotifier(nil, func(id string) []string { return []string{"/usr/bin/argus", "focus", id} })
		o.goos = "darwin"
		o.lookPath = func(name string) (string, error) {
			if name == "hs" {
				return "/usr/local/bin/hs", nil
			}
			return "", errNotFound
		}
		o.hsAvailable = func() bool { return true }
		var lua string
		o.run = func(_ context.Context, _ string, args ...string) error {
			lua = args[len(args)-1]
			return nil
		}
		o.Notify(context.Background(), Notification{Title: "t", Body: "b", Data: data})
		return lua
	}

	withID := capture(map[string]string{"session_id": "nodeA:abc"})
	for _, want := range []string{"_argus", ":withdraw()", `"nodeA:abc"`} {
		if !strings.Contains(withID, want) {
			t.Fatalf("dedupe lua missing %q:\n%s", want, withID)
		}
	}

	// A notification with no session id is sent without the de-dupe table.
	noID := capture(nil)
	if strings.Contains(noID, "_argus") || strings.Contains(noID, ":withdraw()") {
		t.Fatalf("no-session-id notification should not de-dupe:\n%s", noID)
	}
}

func TestHammerspoonFallsBackToOsascriptOnFailure(t *testing.T) {
	// hs is on PATH (renderer selected) but the hs -c call fails (e.g. the
	// hs.ipc bridge isn't loaded → exit 69). The user must still get a plain
	// osascript banner instead of nothing.
	var names []string
	o := NewOSNotifier(nil, func(id string) []string { return []string{"/usr/bin/argus", "focus", id} })
	o.goos = "darwin"
	o.lookPath = func(name string) (string, error) {
		if name == "hs" {
			return "/usr/local/bin/hs", nil
		}
		return "", errNotFound
	}
	o.hsAvailable = func() bool { return true }
	o.run = func(_ context.Context, name string, _ ...string) error {
		names = append(names, name)
		if name == "hs" {
			return errors.New("exit status 69")
		}
		return nil
	}
	o.Notify(context.Background(), Notification{Title: "repo", Body: "b", Data: map[string]string{"session_id": "abc"}})

	if len(names) != 2 || names[0] != "hs" || names[1] != "osascript" {
		t.Fatalf("calls = %v, want [hs osascript] (fallback after hs failure)", names)
	}
}
