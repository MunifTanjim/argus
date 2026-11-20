package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/MunifTanjim/argus/internal/config"
)

func loadSrc(t *testing.T, src string) (*luaRuntime, []string) {
	t.Helper()
	return loadLuaSource(src, t.TempDir())
}

func seqOf(t *testing.T, s string) keySeq {
	t.Helper()
	seq, err := parseRexKeySeq(s)
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func TestLuaBindRegistersKeys(t *testing.T) {
	rt, errs := loadSrc(t, `
argus.bind("ctrl+h", function() end)
argus.bind("ctrl+l", "client.pane.focus.right")`)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(rt.keys) != 2 {
		t.Fatalf("keys = %d, want 2", len(rt.keys))
	}
	if rt.keys[0].seq.id() != seqOf(t, "ctrl+h").id() || rt.keys[0].fn == nil || rt.keys[0].action != "" {
		t.Errorf("key 0 = %+v", rt.keys[0])
	}
	if rt.keys[1].seq.id() != seqOf(t, "ctrl+l").id() || rt.keys[1].fn != nil || rt.keys[1].action != "client.pane.focus.right" {
		t.Errorf("key 1 = %+v", rt.keys[1])
	}
}

func TestLuaBindRejectsModePrefix(t *testing.T) {
	for _, key := range []string{"ab/c", "workspace/ctrl+h", "nav/ctrl+w"} {
		_, errs := loadSrc(t, `argus.bind("`+key+`", function() end)`)
		if len(errs) != 1 || !strings.Contains(errs[0], "mode prefixes are not supported") {
			t.Errorf("%s: errs = %v", key, errs)
		}
	}
	for _, key := range []string{"/", "ctrl+/", "alt+/", "ctrl+a>/", "ctrl+/>x", "project-tree:/", "ctrl+:", "ctrl+a>:"} {
		rt, errs := loadSrc(t, `argus.bind("`+key+`", function() end)`)
		if len(errs) != 0 || len(rt.keys) != 1 {
			t.Errorf("%s: errs = %v, keys = %d", key, errs, len(rt.keys))
		}
	}
}

func TestLuaBindRejectsCmdLineKey(t *testing.T) {
	for _, key := range []string{":", "workspace::", ":>x"} {
		_, errs := loadSrc(t, `argus.bind("`+key+`", function() end)`)
		if len(errs) != 1 || !strings.Contains(errs[0], `":" opens the command line`) {
			t.Errorf("%s: errs = %v", key, errs)
		}
	}
}

func TestLuaBindScreenPrefix(t *testing.T) {
	rt, errs := loadSrc(t, `
argus.bind("workspace:ctrl+h", function() end)
argus.bind("global:ctrl+l", function() end)`)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if rt.keys[0].screen != "workspace" || rt.keys[0].seq.id() != seqOf(t, "ctrl+h").id() {
		t.Errorf("key 0 = %+v", rt.keys[0])
	}
	if rt.keys[1].screen != "" {
		t.Errorf("key 1 screen = %q", rt.keys[1].screen)
	}

	_, errs = loadSrc(t, `argus.bind("nowhere:ctrl+h", function() end)`)
	if len(errs) != 1 || !strings.Contains(errs[0], "unknown section") {
		t.Errorf("errs = %v", errs)
	}

	for old, now := range oldSections {
		_, errs = loadSrc(t, `argus.bind("`+old+`:ctrl+h", function() end)`)
		if len(errs) != 1 || !strings.Contains(errs[0], "(now "+now+")") {
			t.Errorf("%s: errs = %v", old, errs)
		}
	}
}

func TestLuaBindRejects(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`argus.bind("ctrl+h", "client.pane.nowhere")`, "unknown action"},
		{`argus.bind("ctrl+h", 1)`, "target must be an action name or a function"},
		{`argus.bind("foo", function() end)`, `unknown key "foo"`},
		{`argus.bind("<C-h>", function() end)`, `"<C-h>": unknown key "<C-h"`},
		{`argus.bind("<Leader>x", function() end)`, "unknown key"},
	} {
		_, errs := loadSrc(t, tc.src)
		if len(errs) != 1 || !strings.Contains(errs[0], tc.want) {
			t.Errorf("%s: errs = %v", tc.src, errs)
		}
	}
}

func TestLuaErrorKeepsEarlierKeys(t *testing.T) {
	rt, errs := loadSrc(t, `
argus.bind("ctrl+h", function() end)
error("boom")
argus.bind("ctrl+l", function() end)`)
	if len(rt.keys) != 1 {
		t.Errorf("keys = %d, want 1", len(rt.keys))
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "boom") {
		t.Errorf("errs = %v", errs)
	}
}

func TestLuaLoadErrorIsOneLine(t *testing.T) {
	_, errs := loadSrc(t, `error("boom")`)
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "init.lua:") || !strings.Contains(errs[0], "boom") || strings.Contains(errs[0], "\n") {
		t.Errorf("errs = %q", errs)
	}
}

func TestLuaSyntaxError(t *testing.T) {
	rt, errs := loadSrc(t, `argus.bind(`)
	if rt != nil && len(rt.keys) != 0 {
		t.Errorf("keys = %d", len(rt.keys))
	}
	if len(errs) != 1 {
		t.Errorf("errs = %v", errs)
	}
	_, errs = loadSrc(t, `x = = 1`)
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "init.lua line:1") || strings.HasSuffix(errs[0], "\n") {
		t.Errorf("errs = %q", errs)
	}
}

func TestLuaSandbox(t *testing.T) {
	for _, src := range []string{`os.execute("true")`, `io.popen("true")`, `os.exit(0)`, `io.write("x")`, `io.read()`,
		`dofile()`, `dofile(nil)`, `dofile("")`, `loadfile()`, `loadfile("")`} {
		_, errs := loadSrc(t, src)
		if len(errs) != 1 || !strings.Contains(errs[0], "disabled") {
			t.Errorf("%s: errs = %v", src, errs)
		}
	}
}

func TestLuaStdFilesDisabled(t *testing.T) {
	for _, name := range []string{"stdin", "stdout", "stderr"} {
		for _, method := range []string{"write", "read", "lines", "close", "flush", "seek", "setvbuf"} {
			src := "io." + name + ":" + method + "()"
			_, errs := loadSrc(t, src)
			if want := "io." + name + " is disabled"; len(errs) != 1 || !strings.Contains(errs[0], want) {
				t.Errorf("%s: errs = %v, want %q", src, errs, want)
			}
		}
	}
	other := filepath.Join(t.TempDir(), "f")
	for src, want := range map[string]string{
		`io.lines()`:                        "io.lines is disabled",
		`io.input()`:                        "io.input is disabled",
		fmt.Sprintf(`io.input(%q)`, other):  "io.input is disabled",
		`io.output()`:                       "io.output is disabled",
		fmt.Sprintf(`io.output(%q)`, other): "io.output is disabled",
	} {
		_, errs := loadSrc(t, src)
		if len(errs) != 1 || !strings.Contains(errs[0], want) {
			t.Errorf("%s: errs = %v, want %q", src, errs, want)
		}
	}
}

func TestLuaFileIOWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, errs := loadSrc(t, fmt.Sprintf(`local path = %q
lines = ""
for l in io.lines(path) do lines = lines .. l .. ";" end
local f = assert(io.open(path))
whole = f:read("*a")
f:close()`, path))
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if got := rt.L.GetGlobal("lines").String(); got != "a;b;" {
		t.Errorf("lines = %q", got)
	}
	if got := rt.L.GetGlobal("whole").String(); got != "a\nb\n" {
		t.Errorf("whole = %q", got)
	}
}

func TestLuaCloseWithoutFileDisabled(t *testing.T) {
	_, errs := loadSrc(t, `io.close()`)
	if want := "io.close is disabled without a file"; len(errs) != 1 || !strings.Contains(errs[0], want) {
		t.Errorf("errs = %v, want %q", errs, want)
	}
}

func TestLuaCloseFileWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	rt, errs := loadSrc(t, fmt.Sprintf(`local f = assert(io.open(%q, "w"))
f:write("x")
closed = io.close(f)
kind = io.type(f)`, path))
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if rt.L.GetGlobal("closed") != lua.LTrue || rt.L.GetGlobal("kind").String() != "closed file" {
		t.Errorf("closed = %v, kind = %v", rt.L.GetGlobal("closed"), rt.L.GetGlobal("kind"))
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "x" {
		t.Errorf("file = %q, %v", b, err)
	}
}

func TestLuaRequireNextToInit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mod.lua"), []byte("return {v=1}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errs := loadLuaSource(`assert(require("mod").v == 1)`, dir)
	if len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
}

func TestLuaRequireSkipsWorkingDir(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("cwdmod.lua", []byte("return {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errs := loadSrc(t, `assert(not pcall(require, "cwdmod"))`)
	if len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
}

func TestLuaChannelLibRemoved(t *testing.T) {
	_, errs := loadSrc(t, `assert(channel == nil and package.loaded.channel == nil)`)
	if len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
}

func TestLuaGetenv(t *testing.T) {
	t.Setenv("ARGUS_T", "1")
	_, errs := loadSrc(t, `assert(os.getenv("ARGUS_T") == "1")`)
	if len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
}

func TestLuaInvokeDuringLoadErrors(t *testing.T) {
	for _, src := range []string{`argus.invoke("client.pane.focus.left")`, `argus.invoke("client.pane.focus.left", {})`} {
		_, errs := loadSrc(t, src)
		if len(errs) != 1 || !strings.Contains(errs[0], "not available during load") {
			t.Errorf("%s: errs = %v", src, errs)
		}
	}
}

func TestLoadLuaMissingFile(t *testing.T) {
	rt, errs := loadLua(filepath.Join(t.TempDir(), "init.lua"))
	if rt != nil || errs != nil {
		t.Errorf("rt = %v, errs = %v", rt, errs)
	}
}

func TestWithKeymapsShowsLuaError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.lua")
	if err := os.WriteFile(path, []byte(`error("boom")`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := model{}.withKeymaps(config.TUIConfig{}, path)
	if !strings.Contains(m.flash, "boom") {
		t.Errorf("flash = %q", m.flash)
	}
	if m.lua == nil {
		t.Errorf("lua runtime not wired")
	}
}

func TestLuaLoadLoopTimesOut(t *testing.T) {
	start := time.Now()
	_, errs := loadSrc(t, `while true do end`)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "init.lua:") {
		t.Errorf("errs = %v", errs)
	}
}

func TestLuaErrTextPrefix(t *testing.T) {
	cases := map[string]string{
		"init.lua:5: boom":        "init.lua:5: boom",
		"boom":                    "init.lua: boom",
		"open x: init.lua:3: bad": "init.lua: open x: init.lua:3: bad",
	}
	for in, want := range cases {
		if got := luaErrText(errors.New(in)); got != want {
			t.Errorf("luaErrText(%q) = %q, want %q", in, got, want)
		}
	}
	_, errs := loadSrc(t, "\n\nerror(\"boom\")")
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "init.lua:3: ") {
		t.Errorf("errs = %q", errs)
	}
}

func TestLuaExecRejectsBadArguments(t *testing.T) {
	for _, call := range []string{
		`argus.exec({ "sh", 1 })`,
		`argus.exec({})`,
		`argus.exec("sh")`,
		`argus.exec({ "true" }, "x")`,
		`argus.exec({ "true" }, { env = "A=1" })`,
		`argus.exec({ "true" }, { env = { [1] = "x" } })`,
		`argus.exec({ "true" }, { env = { A = 1 } })`,
		`argus.exec({ "true" }, { env = { A = true } })`,
		`argus.exec({ "true" }, { timeout = 0 })`,
		`argus.exec({ "true" }, { timeout = -1 })`,
		`argus.exec({ "true" }, { timeout = math.huge })`,
		`argus.exec({ "true" }, { timeout = 0/0 })`,
		`argus.exec({ "true" }, { timeout = "1" })`,
		`argus.exec({ "true" }, { cwd = 1 })`,
		`argus.exec({ "true" }, { stdin = 1 })`,
		`argus.exec({ "true" }, { stdout = 0 })`,
		`argus.exec({ "true" }, { stderr = "no" })`,
		`argus.exec({ "true" }, { clear_env = "yes" })`,
	} {
		_, errs := loadSrc(t, call)
		if len(errs) != 1 || !strings.Contains(errs[0], "bad argument") {
			t.Errorf("%s: errs = %v", call, errs)
		}
	}
	_, errs := loadSrc(t, `assert(argus.exec({ "true" }, { env = { A = "1", B = false }, timeout = 1.5, stdout = false, stderr = true }).ok)`)
	if len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

func TestLuaActionDefinitionErrors(t *testing.T) {
	for src, want := range map[string]string{
		`argus.action("x")`: "argus.action: definition must be a table",
		`argus.action{ title = "T", run = function() end }`:                                            "name",
		`argus.action{ name = "a.b", title = "T", run = function() end }`:                              "lowercase letters, digits, and underscores",
		`argus.action{ name = "A", title = "T", run = function() end }`:                                "lowercase letters, digits, and underscores",
		`argus.action{ name = "a", run = function() end }`:                                             "title",
		`argus.action{ name = "a", title = "", run = function() end }`:                                 "title must be a non-empty string",
		`argus.action{ name = "a", title = "T", run = 1 }`:                                             "run",
		`argus.bind("f9", "later"); argus.action{ name = "later", title = "T", run = function() end }`: `unknown action "later"`,
		`argus.bind("f9", "client.pane.focus.left", {})`:                                               "takes no args",
		`argus.bind("f9", function() end, {})`:                                                         "args need an action name",
		`argus.action{ name = "a", title = "T", run = function() end }; argus.bind("f9", "a", 1)`:      "args must be a table",
	} {
		_, errs := loadSrc(t, src)
		if len(errs) != 1 || !strings.Contains(errs[0], want) {
			t.Errorf("%s: errs = %v, want %q", src, errs, want)
		}
	}
}

func TestLuaActionUnknownFieldIsLogged(t *testing.T) {
	buf := captureLog(t)
	_, errs := loadSrc(t, `argus.action{ name = "a", title = "T", description = "d", run = function() end }`)
	if len(errs) != 0 || !strings.Contains(buf.String(), `argus.action: unknown field "description"`) {
		t.Errorf("errs = %v, log = %q", errs, buf.String())
	}
}

func TestLuaActionUnknownNonStringFieldIsNamed(t *testing.T) {
	buf := captureLog(t)
	_, errs := loadSrc(t, `argus.action{ name = "a", title = "T", run = function() end, [true] = 1 }`)
	if len(errs) != 0 || !strings.Contains(buf.String(), `argus.action: unknown field "true"`) {
		t.Errorf("errs = %v, log = %q", errs, buf.String())
	}
}

func TestLuaPrintWritesToLog(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	_, errs := loadSrc(t, `print("a", 1, nil)`)
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)

	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(out) != 0 {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(buf.String(), "init.lua: a\t1\tnil") {
		t.Errorf("log = %q", buf.String())
	}
}
