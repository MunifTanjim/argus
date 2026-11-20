package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	lua "github.com/yuin/gopher-lua"

	"github.com/MunifTanjim/argus/internal/session"
)

func withLua(t *testing.T, m model, src string) model {
	t.Helper()
	rt, errs := loadLuaSource(src, t.TempDir())
	if len(errs) > 0 {
		t.Fatalf("load: %v", errs)
	}
	if m.keys == nil {
		m.keys, _ = buildKeymap(nil, 0, "")
	}
	return m.withLua(rt)
}

func luaGlobal(m model, name string) lua.LValue { return m.lua.L.GetGlobal(name) }

func tmuxScript(t *testing.T) (src, logPath string) {
	dir := t.TempDir()
	logPath = filepath.Join(dir, "log")
	bin := filepath.Join(dir, "tmux")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" >> "+logPath+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	t.Setenv("TMUX_PANE", "%7")
	src = `
local pane = os.getenv("TMUX") and os.getenv("TMUX_PANE")

local function nav(action, dir)
  return function(ctx)
    local r, err = argus.invoke(action)
    if not r then
      return false
    end
    if pane and not r.ok then
      argus.exec({ "tmux", "select-pane", "-t", pane, "-" .. dir })
    end
    return true
  end
end

argus.bind("ctrl+h", nav("client.pane.focus.left", "L"))
argus.bind("ctrl+j", nav("client.pane.focus.down", "D"))
argus.bind("ctrl+k", nav("client.pane.focus.up", "U"))
argus.bind("ctrl+l", nav("client.pane.focus.right", "R"))
`
	return src, logPath
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestLuaTmuxHandOff(t *testing.T) {
	prompt := &session.Interaction{Kind: session.InteractionPermission}
	cases := []struct {
		name    string
		m       func() model
		key     rune
		wantLog string
		want    container
	}{
		{"left from the tree", wideWorkspace, 'h', "select-pane -t %7 -L", leftSidebar},
		{"right from the files", func() model { return withFocus(wideWorkspace(), rightSidebar) }, 'l', "select-pane -t %7 -R", rightSidebar},
		{"up from the pane", func() model { return withFocus(wideWorkspace(), mainPane) }, 'k', "select-pane -t %7 -U", mainPane},
		{"down with no dock", func() model { return withFocus(wideWorkspace(), mainPane) }, 'j', "select-pane -t %7 -D", mainPane},
		{"down to the dock", func() model { return withFocus(workspaceSession(prompt), mainPane) }, 'j', "", sessionDock},
		{"left from main", func() model { return withFocus(wideWorkspace(), mainPane) }, 'h', "", leftSidebar},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, logPath := tmuxScript(t)
			m := pressKeys(withLua(t, c.m(), src), ctrlKey(c.key))
			if got := readLog(t, logPath); got != c.wantLog {
				t.Errorf("log = %q, want %q", got, c.wantLog)
			}
			if m.focused != c.want {
				t.Errorf("focus = %v, want %v", m.focused, c.want)
			}
		})
	}
}

func TestLuaTmuxSettingsKeepsKey(t *testing.T) {
	src, logPath := tmuxScript(t)
	m := withLua(t, openedSettings(t, &fakeSettings{lock: signerLock()}), src)
	if _, _, ok := m.runLua(ctrlKey('h')); ok {
		t.Error("<C-h> on settings: the lua key handled it")
	}
	pressKeys(m, ctrlKey('h'))
	if got := readLog(t, logPath); got != "" {
		t.Errorf("log = %q, want none", got)
	}
}

func TestLuaTmuxOutsideTmux(t *testing.T) {
	src, logPath := tmuxScript(t)
	os.Unsetenv("TMUX")
	m := pressKeys(withLua(t, wideWorkspace(), src), ctrlKey('h'))
	if got := readLog(t, logPath); got != "" {
		t.Errorf("log = %q, want none", got)
	}
	if m.focused != leftSidebar {
		t.Errorf("focus = %v, want the tree", m.focused)
	}
}

func TestLuaCtrlWHDoesNotHandOff(t *testing.T) {
	src, logPath := tmuxScript(t)
	pressKeys(withLua(t, wideWorkspace(), src), cw('h')...)
	if got := readLog(t, logPath); got != "" {
		t.Errorf("log = %q, want none", got)
	}
}

func TestLuaKeySkipsTextInput(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("ctrl+h", function() hit = true; return true end)`)
	m = typeKeys(m, "/")
	if !m.keysRaw() {
		t.Fatal("/ did not focus a text input")
	}
	m = pressKeys(m, ctrlKey('h'))
	if luaGlobal(m, "hit") != lua.LNil {
		t.Error("the lua key ran in a text input")
	}
}

func TestLuaErrorKeepsInvokeEffect(t *testing.T) {
	m := withLua(t, withFocus(wideWorkspace(), mainPane), `argus.bind("ctrl+h", function()
  argus.invoke("client.pane.focus.left")
  error("boom")
end)`)
	m = pressKeys(m, ctrlKey('h'))
	if m.focused != leftSidebar {
		t.Errorf("focus = %v, want the tree", m.focused)
	}
	if !strings.HasPrefix(m.flash, "init.lua:") || !strings.Contains(m.flash, "boom") || strings.Contains(m.flash, "\n") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestLuaTruthyReturnHandles(t *testing.T) {
	m := withLua(t, homeTestModel(), `argus.bind("ctrl+k", function() return {} end)`)
	m = pressKeys(m, ctrlKey('k'))
	if _, ok := paletteOf(m); ok {
		t.Error("the palette opened")
	}
}

func TestLuaFalseFallsThrough(t *testing.T) {
	m := withLua(t, homeTestModel(), `argus.bind("ctrl+k", function() return false end)`)
	m = pressKeys(m, ctrlKey('k'))
	if _, ok := paletteOf(m); !ok {
		t.Error("the palette did not open")
	}
}

func TestLuaPrefixWaitsForTimeout(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("ctrl+w", function() hit = true; return true end)`)
	m, cmd := upd(m, ctrlKey('w'))
	if luaGlobal(m, "hit") != lua.LNil {
		t.Fatal("the function ran before the timeout")
	}
	var tick keyTimeoutMsg
	found := false
	for _, msg := range execCmd(cmd) {
		if k, ok := msg.(keyTimeoutMsg); ok {
			tick, found = k, true
		}
	}
	if !found {
		t.Fatal("<C-w> returned no keyTimeoutMsg")
	}
	m, _ = upd(m, tick)
	if luaGlobal(m, "hit") != lua.LTrue {
		t.Error("the function did not run after the timeout")
	}
}

func TestLuaPendingFooterListsLuaKeys(t *testing.T) {
	m := withLua(t, wideWorkspace(), `
argus.bind("ctrl+a", function() return true end)
argus.bind("ctrl+a>w", "client.pane.focus.right")
argus.bind("ctrl+a>x", function() return true end)`)
	m, _ = upd(m, ctrlKey('a'))
	hint := ansi.Strip(m.keyHint())
	for _, want := range []string{"w focus right", "x <lua>", "(wait) <lua>"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint = %q, want %q", hint, want)
		}
	}
}

func TestLuaActionTarget(t *testing.T) {
	src := `argus.bind("ctrl+h", "client.pane.focus.left")`
	m := pressKeys(withLua(t, withFocus(wideWorkspace(), mainPane), src), ctrlKey('h'))
	if m.focused != leftSidebar {
		t.Errorf("focus = %v, want the tree", m.focused)
	}
	s := withLua(t, openedSettings(t, &fakeSettings{lock: signerLock()}), src)
	if _, _, ok := s.runLua(ctrlKey('h')); ok {
		t.Error("settings: the action key did not fall through")
	}
}

func TestLuaScreenKeyOnlyOnItsScreen(t *testing.T) {
	src := `argus.bind("settings:ctrl+h", function() hit = true; return true end)`
	m := pressKeys(withLua(t, wideWorkspace(), src), ctrlKey('h'))
	if luaGlobal(m, "hit") != lua.LNil {
		t.Error("the settings key ran on the tree")
	}
	m = pressKeys(withLua(t, openedSettings(t, &fakeSettings{lock: signerLock()}), src), ctrlKey('h'))
	if luaGlobal(m, "hit") != lua.LTrue {
		t.Error("the settings key did not run on settings")
	}
}

func TestLuaScreenKeyBeatsGlobal(t *testing.T) {
	global := `argus.bind("ctrl+h", function() g = 1; return true end)`
	screen := `argus.bind("project-tree:ctrl+h", function() g = 2; return true end)`
	for _, src := range []string{global + "\n" + screen, screen + "\n" + global} {
		m := pressKeys(withLua(t, wideWorkspace(), src), ctrlKey('h'))
		if got := luaGlobal(m, "g"); got != lua.LNumber(2) {
			t.Errorf("g = %v, want 2, for:\n%s", got, src)
		}
	}
}

func TestLuaCtxSection(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("ctrl+h", function(ctx) s = ctx.section; return true end)`)
	m = pressKeys(m, ctrlKey('h'))
	if got := luaGlobal(m, "s"); got != lua.LString("project-tree") {
		t.Errorf("ctx.section = %v", got)
	}
}

func TestLuaExecResult(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("ctrl+h", function()
  local r = argus.exec({ "sh", "-c", "exit 3" })
  code, ok, sig = r.code, r.ok, r.signal
  return true
end)`)
	m = pressKeys(m, ctrlKey('h'))
	if luaGlobal(m, "code") != lua.LNumber(3) || luaGlobal(m, "ok") != lua.LFalse || luaGlobal(m, "sig") != lua.LNil {
		t.Errorf("code = %v ok = %v signal = %v", luaGlobal(m, "code"), luaGlobal(m, "ok"), luaGlobal(m, "sig"))
	}
}

func TestLuaExecStartFailure(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("ctrl+h", function()
  r, err = argus.exec({ "argus-no-such-program" })
  return true
end)`)
	m = pressKeys(m, ctrlKey('h'))
	if luaGlobal(m, "r") != lua.LNil {
		t.Errorf("r = %v, want nil", luaGlobal(m, "r"))
	}
	if s, ok := luaGlobal(m, "err").(lua.LString); !ok || s == "" {
		t.Errorf("err = %v, want a string", luaGlobal(m, "err"))
	}
}

func TestLuaExecUnknownOptionErrors(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("ctrl+h", function()
  argus.exec({ "sh", "-c", "exit 0" }, { time_out = 1 })
  after = true
  return true
end)`)
	m = pressKeys(m, ctrlKey('h'))
	if luaGlobal(m, "after") != lua.LNil || !strings.Contains(m.flash, `unknown option "time_out"`) {
		t.Errorf("after = %v, flash = %q", luaGlobal(m, "after"), m.flash)
	}
}

func TestLuaBindAfterLoadErrors(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("ctrl+h", function()
  argus.bind("x", "client.pane.focus.left")
end)`)
	m = pressKeys(m, ctrlKey('h'))
	if !strings.HasPrefix(m.flash, "init.lua:") {
		t.Errorf("flash = %q", m.flash)
	}
	if len(m.lua.keys) != 1 {
		t.Errorf("keys = %d, want 1", len(m.lua.keys))
	}
}

func TestLuaNilReturnKeepsInvokeEffect(t *testing.T) {
	m := withLua(t, withFocus(wideWorkspace(), mainPane), `argus.bind("f9", function() argus.invoke("client.pane.focus.left") end)`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if m.focused != leftSidebar {
		t.Errorf("focus = %v, want the tree", m.focused)
	}
}

func TestLuaKeyLoopTimesOut(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("f9", function() while true do end end)`)
	start := time.Now()
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
	if !strings.HasPrefix(m.flash, "init.lua:") || !strings.Contains(m.flash, "deadline") || strings.Contains(m.flash, "\n") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestLuaExecTimeDoesNotCount(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("f9", function()
  argus.exec({ "sleep", "1.5" }, { timeout = 5 })
  argus.exec({ "sleep", "1.5" }, { timeout = 5 })
  done = true
  return true
end)`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if luaGlobal(m, "done") != lua.LTrue {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestLuaExecKeepsTheTimeSpent(t *testing.T) {
	m := withLua(t, wideWorkspace(), `local function spin(s) local t = os.clock() + s; while os.clock() < t do end end
argus.bind("f9", function()
  spin(1.5)
  argus.exec({ "true" })
  spin(1.5)
  done = true
  return true
end)`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if luaGlobal(m, "done") == lua.LTrue || !strings.Contains(m.flash, "deadline") {
		t.Errorf("done = %v, flash = %q", luaGlobal(m, "done"), m.flash)
	}
}

func TestLuaInvokeReturnsTwoValues(t *testing.T) {
	m := withLua(t, withFocus(wideWorkspace(), mainPane), `argus.bind("f9", function()
  n = select("#", argus.invoke("client.pane.focus.up"))
  r, e = argus.invoke("client.pane.focus.left")
  return true
end)`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	r, ok := luaGlobal(m, "r").(*lua.LTable)
	if luaGlobal(m, "n") != lua.LNumber(2) || luaGlobal(m, "e") != lua.LNil || !ok || r.RawGetString("ok") != lua.LTrue {
		t.Errorf("n = %v, r = %v, e = %v, flash = %q", luaGlobal(m, "n"), luaGlobal(m, "r"), luaGlobal(m, "e"), m.flash)
	}
}

func TestLuaInvokeUnknownReturnsError(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("f9", function() r, e = argus.invoke("nope"); return true end)`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if luaGlobal(m, "r") != lua.LNil || !strings.Contains(lua.LVAsString(luaGlobal(m, "e")), `unknown action "nope"`) || m.flash != "" {
		t.Errorf("r = %v, e = %v, flash = %q", luaGlobal(m, "r"), luaGlobal(m, "e"), m.flash)
	}
}

func TestLuaInvokeUnavailableReturnsError(t *testing.T) {
	s := openedSettings(t, &fakeSettings{lock: signerLock()})
	s.flash = ""
	focused := s.focused
	m := withLua(t, s, `argus.bind("f9", function() r, e = argus.invoke("client.pane.focus.left"); return true end)`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if luaGlobal(m, "r") != lua.LNil || !strings.Contains(lua.LVAsString(luaGlobal(m, "e")), "not available") || m.flash != "" || m.focused != focused {
		t.Errorf("r = %v, e = %v, flash = %q, focus = %v", luaGlobal(m, "r"), luaGlobal(m, "e"), m.flash, m.focused)
	}
}

func TestLuaActionAfterLoadErrors(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("f9", function()
  argus.action{ name = "late", title = "Late", run = function() end }
end)`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if !strings.Contains(m.flash, "argus.action: only available during load") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestLuaCustomActionKey(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.action{ name = "mark", title = "Mark", run = function(ctx, args) got, sec = args.n, ctx.section end }
argus.bind("f9", "mark", { n = 2 })`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if luaGlobal(m, "got") != lua.LNumber(2) || luaGlobal(m, "sec") != lua.LString(m.screen()) {
		t.Errorf("got = %v, sec = %v, flash = %q", luaGlobal(m, "got"), luaGlobal(m, "sec"), m.flash)
	}
}

func TestLuaCustomActionArgsDefaultToTable(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.action{ name = "kind", title = "Kind", run = function(ctx, args) kind = type(args) end }
argus.bind("f9", "kind")`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if luaGlobal(m, "kind") != lua.LString("table") {
		t.Errorf("kind = %v, flash = %q", luaGlobal(m, "kind"), m.flash)
	}
}

func TestLuaCustomActionKeyDoesNotFallThrough(t *testing.T) {
	m := withLua(t, homeTestModel(), `argus.action{ name = "no", title = "No", run = function() return false end }
argus.bind("ctrl+k", "no")`)
	m = pressKeys(m, ctrlKey('k'))
	if _, ok := paletteOf(m); ok {
		t.Error("the palette opened")
	}
}

func TestLuaCustomActionErrorFlashes(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.action{ name = "bad", title = "Bad", run = function() error("boom") end }
argus.bind("f9", "bad")`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if !strings.HasPrefix(m.flash, "init.lua:") || !strings.Contains(m.flash, "boom") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestLuaCustomActionRedefinedLaterWins(t *testing.T) {
	buf := captureLog(t)
	m := withLua(t, wideWorkspace(), `argus.action{ name = "x", title = "A", run = function() v = 1 end }
argus.bind("f9", "x")
argus.action{ name = "x", title = "B", run = function() v = 2 end }`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if luaGlobal(m, "v") != lua.LNumber(2) {
		t.Errorf("v = %v, flash = %q", luaGlobal(m, "v"), m.flash)
	}
	if got := m.lua.keys[0].label(); got != "B" {
		t.Errorf("label = %q, want B", got)
	}
	if !strings.Contains(buf.String(), `argus.action: "x" is defined again`) {
		t.Errorf("log = %q", buf.String())
	}
}

func TestLuaInvokeCustomAction(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.action{ name = "dbl", title = "Double", run = function(ctx, args) return args.v * 2 end }
argus.action{ name = "none_ret", title = "None", run = function() end }
argus.bind("f9", function()
  r, e = argus.invoke("dbl", { v = 3 })
  n = select("#", argus.invoke("none_ret"))
  return true
end)`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if luaGlobal(m, "r") != lua.LNumber(6) || luaGlobal(m, "e") != lua.LNil || luaGlobal(m, "n") != lua.LNumber(2) {
		t.Errorf("r = %v, e = %v, n = %v, flash = %q", luaGlobal(m, "r"), luaGlobal(m, "e"), luaGlobal(m, "n"), m.flash)
	}
}

func TestLuaInvokeCustomActionErrorIsReturned(t *testing.T) {
	m := withLua(t, withFocus(wideWorkspace(), mainPane), `argus.action{ name = "bad", title = "Bad", run = function()
  argus.invoke("client.pane.focus.left")
  error("boom")
end }
argus.bind("f9", function() r, e = argus.invoke("bad"); return true end)`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if luaGlobal(m, "r") != lua.LNil || !strings.Contains(lua.LVAsString(luaGlobal(m, "e")), "boom") || m.flash != "" {
		t.Errorf("r = %v, e = %v, flash = %q", luaGlobal(m, "r"), luaGlobal(m, "e"), m.flash)
	}
	if m.focused != leftSidebar {
		t.Errorf("focus = %v, want the tree", m.focused)
	}
}

func TestLuaInvokeCustomActionPastDeadline(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.action{ name = "spin", title = "Spin", run = function()
  local t = os.clock() + 3
  while os.clock() < t do end
end }
argus.bind("f9", function() argus.invoke("spin"); after = true; return true end)`)
	start := time.Now()
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
	if luaGlobal(m, "after") != lua.LNil || !strings.Contains(m.flash, "deadline") {
		t.Errorf("after = %v, flash = %q", luaGlobal(m, "after"), m.flash)
	}
}

func TestLuaPendingFooterShowsActionTitle(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.action{ name = "mark", title = "Mark", run = function() end }
argus.bind("ctrl+a>m", "mark")`)
	m, _ = upd(m, ctrlKey('a'))
	if hint := ansi.Strip(m.keyHint()); !strings.Contains(hint, "m Mark") {
		t.Errorf("hint = %q", hint)
	}
}

func TestLuaInvokeNonTableArgsErrors(t *testing.T) {
	for _, name := range []string{"a", "client.pane.focus.left"} {
		m := withLua(t, wideWorkspace(), `argus.action{ name = "a", title = "A", run = function() end }
argus.bind("f9", function() argus.invoke("`+name+`", "x"); after = true; return true end)`)
		m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
		if luaGlobal(m, "after") != lua.LNil || !strings.Contains(m.flash, "argus.invoke: args must be a table") {
			t.Errorf("%s: after = %v, flash = %q", name, luaGlobal(m, "after"), m.flash)
		}
	}
}

func TestLuaInvokeOutsideBoundFunction(t *testing.T) {
	m := withLua(t, wideWorkspace(), `function later() argus.invoke("client.pane.focus.left") end`)
	err := m.lua.L.CallByParam(lua.P{Fn: luaGlobal(m, "later"), Protect: true})
	if err == nil || !strings.Contains(err.Error(), "argus.invoke: only inside a bound function") {
		t.Errorf("err = %v", err)
	}
}

func TestLuaCoroutineFromEarlierCallTimesOut(t *testing.T) {
	m := withLua(t, wideWorkspace(), `co = coroutine.create(function() coroutine.yield(); while true do end end)
coroutine.resume(co)
argus.bind("f9", function()
  local ok, err = coroutine.resume(co)
  if not ok then error(err) end
end)`)
	start := time.Now()
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
	if !strings.HasPrefix(m.flash, "init.lua:") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestLuaSuspendedCoroutinesDoNotLeakGoroutines(t *testing.T) {
	m := withLua(t, wideWorkspace(), `argus.bind("f9", function()
  local co = coroutine.create(function() coroutine.yield() end)
  coroutine.resume(co)
  return true
end)`)
	m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	before := runtime.NumGoroutine()
	for range 50 {
		m = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyF9})
	}
	after := runtime.NumGoroutine()
	for i := 0; i < 50 && after > before+5; i++ {
		time.Sleep(20 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > before+5 {
		t.Errorf("goroutines: %d before, %d after", before, after)
	}
}
