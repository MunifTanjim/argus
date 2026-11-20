package tui

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	lua "github.com/yuin/gopher-lua"
)

type luaKey struct {
	screen string
	seq    keySeq
	fn     *lua.LFunction
	action string
	title  string // of the custom action, as last defined
}

type luaAction struct {
	title string
	run   *lua.LFunction
}

var luaActionName = regexp.MustCompile(`^[a-z0-9_]+$`)

const luaCallTimeout = 2 * time.Second

type luaRuntime struct {
	L        *lua.LState
	keys     []luaKey
	actions  map[string]luaAction
	loading  bool
	cur      *model
	cmds     []tea.Cmd
	deadline *luaDeadline
}

// luaDeadline is a context that expires luaCallTimeout after it starts, not
// counting the time it is paused. Coroutines derive their context from it, so
// a pause covers them too.
type luaDeadline struct {
	done  chan struct{}
	once  sync.Once
	timer *time.Timer
	left  time.Duration
	start time.Time
}

func newLuaDeadline() *luaDeadline {
	d := &luaDeadline{done: make(chan struct{}), left: luaCallTimeout, start: time.Now()}
	d.timer = time.AfterFunc(luaCallTimeout, d.expire)
	return d
}

func (d *luaDeadline) expire() { d.once.Do(func() { close(d.done) }) }

func (d *luaDeadline) Deadline() (time.Time, bool) { return time.Time{}, false }
func (d *luaDeadline) Done() <-chan struct{}       { return d.done }
func (d *luaDeadline) Value(any) any               { return nil }

func (d *luaDeadline) Err() error {
	select {
	case <-d.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// pause stops the clock. It returns false when the deadline already passed.
func (d *luaDeadline) pause() bool {
	if !d.timer.Stop() {
		return false
	}
	d.left -= time.Since(d.start)
	return true
}

func (d *luaDeadline) resume() {
	d.start = time.Now()
	d.timer.Reset(d.left)
}

// end expires the deadline at once. Coroutines still suspended then end with
// it, and their context goroutines exit.
func (d *luaDeadline) end() {
	d.timer.Stop()
	d.expire()
}

func (rt *luaRuntime) pcall(nargs, nret int) error {
	rt.deadline = newLuaDeadline()
	rt.L.SetContext(rt.deadline)
	defer func() {
		rt.deadline.end()
		rt.L.RemoveContext()
		rt.deadline = nil
	}()
	return rt.L.PCall(nargs, nret, nil)
}

// splitScreenPrefix splits an optional "screen:" prefix off a key. The text
// before the first ":" is a prefix only when it is not empty and has no "+"
// or ">", so ":" and "ctrl+:" stay keys.
func splitScreenPrefix(key string) (screen, rest string, err error) {
	i := strings.Index(key, ":")
	if i <= 0 || strings.ContainsAny(key[:i], "+>") {
		return "", key, nil
	}
	name := key[:i]
	if err := checkScreen(name); err != nil {
		return "", "", err
	}
	if name == "global" {
		name = ""
	}
	return name, key[i+1:], nil
}

// checkScreen accepts "global" and the keymap screen names.
func checkScreen(name string) error {
	switch {
	case name == "global" || screenBindings[name] != nil:
		return nil
	case oldSections[name] != "":
		return fmt.Errorf("unknown section %q (now %s)", name, oldSections[name])
	}
	return fmt.Errorf("unknown section %q", name)
}

func loadLua(path string) (*luaRuntime, []string) {
	if path == "" {
		return nil, nil
	}
	src, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, []string{luaErrText(err)}
	}
	return loadLuaSource(string(src), filepath.Dir(path))
}

func loadLuaSource(src, dir string) (*luaRuntime, []string) {
	L := lua.NewState()
	rt := &luaRuntime{L: L, loading: true, actions: map[string]luaAction{}}

	disabled := func(msg string) *lua.LFunction {
		return L.NewFunction(func(L *lua.LState) int {
			L.RaiseError("%s", msg)
			return 0
		})
	}
	disable := func(table, name, hint string) {
		if t, ok := L.GetGlobal(table).(*lua.LTable); ok {
			L.SetField(t, name, disabled(table+"."+name+" is disabled"+hint))
		}
	}
	disable("os", "execute", "; use argus.exec")
	disable("os", "exit", "")
	disable("io", "popen", "; use argus.exec")
	disable("io", "write", "")
	disable("io", "read", "")
	disable("io", "input", "")
	disable("io", "output", "")
	// Without a file, these read stdin or close stdout.
	requireArg := func(t *lua.LTable, name, msg string) {
		fn := L.GetField(t, name)
		L.SetField(t, name, L.NewFunction(func(L *lua.LState) int {
			n := L.GetTop()
			if first := L.Get(1); n == 0 || first == lua.LNil || first == lua.LString("") {
				L.RaiseError("%s", msg)
			}
			L.Insert(fn, 1)
			L.Call(n, lua.MultRet)
			return L.GetTop()
		}))
	}
	requireArg(L.G.Global, "dofile", "dofile is disabled without a file name")
	requireArg(L.G.Global, "loadfile", "loadfile is disabled without a file name")
	if io, ok := L.GetGlobal("io").(*lua.LTable); ok {
		for _, name := range []string{"stdin", "stdout", "stderr"} {
			f := L.NewTable()
			for _, method := range []string{"write", "read", "lines", "close", "flush", "seek", "setvbuf"} {
				L.SetField(f, method, disabled("io."+name+" is disabled"))
			}
			L.SetField(io, name, f)
		}
		requireArg(io, "lines", "io.lines is disabled without a file name")
		requireArg(io, "close", "io.close is disabled without a file")
	}
	L.SetGlobal("print", L.NewFunction(luaPrint))

	// channel operations block without checking the call deadline.
	L.SetGlobal(lua.ChannelLibName, lua.LNil)
	if pkg, ok := L.GetGlobal("package").(*lua.LTable); ok {
		if loaded, ok := L.GetField(pkg, "loaded").(*lua.LTable); ok {
			L.SetField(loaded, lua.ChannelLibName, lua.LNil)
		}
		L.SetField(pkg, "path", lua.LString(dir+"/?.lua;"+dir+"/?/init.lua"))
	}

	argus := L.NewTable()
	L.SetField(argus, "action", L.NewFunction(rt.action))
	L.SetField(argus, "bind", L.NewFunction(rt.bind))
	L.SetField(argus, "invoke", L.NewFunction(rt.invoke))
	L.SetField(argus, "exec", L.NewFunction(rt.exec))
	L.SetGlobal("argus", argus)

	fn, err := L.Load(strings.NewReader(src), "init.lua")
	if err == nil {
		L.Push(fn)
		err = rt.pcall(0, 0)
	}
	rt.loading = false
	for i, k := range rt.keys {
		rt.keys[i].title = rt.actions[k.action].title
	}
	if err != nil {
		return rt, []string{luaErrText(err)}
	}
	return rt, nil
}

func luaPrint(L *lua.LState) int {
	parts := make([]string, L.GetTop())
	for i := range parts {
		parts[i] = L.ToStringMeta(L.Get(i + 1)).String()
	}
	log.Printf("init.lua: %s", strings.Join(parts, "\t"))
	return 0
}

func (rt *luaRuntime) bind(L *lua.LState) int {
	if !rt.loading {
		L.RaiseError("argus.bind: only available during load")
	}
	key := L.CheckString(1)
	screen, rest, err := splitScreenPrefix(key)
	if err != nil {
		L.RaiseError("argus.bind: %v", err)
	}
	if isModePrefixed(rest) {
		L.RaiseError("argus.bind: %q: mode prefixes are not supported", key)
	}
	seq, err := parseRexKeySeq(rest)
	if err != nil {
		L.RaiseError("argus.bind: %v", err)
	}
	if seq[0][0] == ":" {
		L.RaiseError(`argus.bind: %q: ":" opens the command line`, key)
	}
	args := luaOptTable(L, 3, "argus.bind")
	k := luaKey{screen: screen, seq: seq}
	switch target := L.Get(2).(type) {
	case lua.LString:
		name := string(target)
		_, custom := rt.actions[name]
		_, builtin := actions[name]
		switch {
		case custom:
			k.fn = L.NewFunction(func(L *lua.LState) int {
				pushActionCall(L, rt.actions[name], L.Get(1), args)
				L.Call(2, 0)
				L.Push(lua.LTrue)
				return 1
			})
		case !builtin:
			L.RaiseError("argus.bind: unknown action %q", name)
		case args != nil:
			L.RaiseError("argus.bind: %s takes no args", name)
		}
		k.action = name
	case *lua.LFunction:
		if args != nil {
			L.RaiseError("argus.bind: args need an action name")
		}
		k.fn = target
	default:
		L.RaiseError("argus.bind: target must be an action name or a function")
	}
	rt.keys = append(rt.keys, k)
	return 0
}

func pushActionCall(L *lua.LState, a luaAction, ctx lua.LValue, args *lua.LTable) {
	L.Push(a.run)
	L.Push(ctx)
	if args == nil {
		args = L.NewTable()
	}
	L.Push(args)
}

func (rt *luaRuntime) action(L *lua.LState) int {
	if !rt.loading {
		L.RaiseError("argus.action: only available during load")
	}
	def, ok := L.Get(1).(*lua.LTable)
	if !ok {
		L.RaiseError("argus.action: definition must be a table")
	}
	name, ok := def.RawGetString("name").(lua.LString)
	if !ok {
		L.RaiseError("argus.action: name must be a string")
	}
	if !luaActionName.MatchString(string(name)) {
		L.RaiseError("argus.action: %q: a name has only lowercase letters, digits, and underscores", string(name))
	}
	title, ok := def.RawGetString("title").(lua.LString)
	if !ok || title == "" {
		L.RaiseError("argus.action: title must be a non-empty string")
	}
	run, ok := def.RawGetString("run").(*lua.LFunction)
	if !ok {
		L.RaiseError("argus.action: run must be a function")
	}
	def.ForEach(func(k, _ lua.LValue) {
		switch f := k.String(); f {
		case "name", "title", "run":
		default:
			log.Printf("init.lua: argus.action: unknown field %q", f)
		}
	})
	if _, ok := rt.actions[string(name)]; ok {
		log.Printf("init.lua: argus.action: %q is defined again; the later definition wins", string(name))
	}
	rt.actions[string(name)] = luaAction{title: string(title), run: run}
	return 0
}

// isModePrefixed reports a Rex mode prefix, as in "nav/h". The syntax is
// reserved.
func isModePrefixed(key string) bool {
	i := strings.Index(key, "/")
	return i > 0 && i+1 < len(key) && !strings.ContainsAny(key[:i], "+>")
}

func luaOptTable(L *lua.LState, n int, fn string) *lua.LTable {
	switch a := L.Get(n).(type) {
	case *lua.LNilType:
		return nil
	case *lua.LTable:
		return a
	}
	L.RaiseError("%s: args must be a table", fn)
	return nil
}

func (rt *luaRuntime) invoke(L *lua.LState) int {
	if rt.loading {
		L.RaiseError("argus.invoke: not available during load")
	}
	if rt.cur == nil {
		L.RaiseError("argus.invoke: only inside a bound function")
	}
	name := L.CheckString(1)
	args := luaOptTable(L, 2, "argus.invoke")
	if a, ok := rt.actions[name]; ok {
		return rt.invokeCustom(L, a, args)
	}
	if args != nil {
		return luaFail(L, name+" takes no args")
	}
	r, err := rt.runAction(L, name)
	if err != nil {
		return luaFail(L, err.Error())
	}
	L.Push(r)
	L.Push(lua.LNil)
	return 2
}

func (rt *luaRuntime) invokeCustom(L *lua.LState, a luaAction, args *lua.LTable) int {
	pushActionCall(L, a, luaCtx(L, *rt.cur), args)
	if err := L.PCall(2, 1, nil); err != nil {
		return luaFail(L, luaErrText(err))
	}
	L.Push(lua.LNil)
	return 2
}

func (rt *luaRuntime) runAction(L *lua.LState, name string) (lua.LValue, error) {
	before := *rt.cur
	after, c, err := before.invokeAction(name)
	*rt.cur = after
	rt.cmds = append(rt.cmds, c)
	if err != nil {
		return nil, err
	}
	t := L.NewTable()
	L.SetField(t, "ok", lua.LBool(actions[name].changed(before, after)))
	return t, nil
}

func (rt *luaRuntime) exec(L *lua.LState) int {
	argv := luaExecArgv(L)
	var o execOpts
	if opts := L.OptTable(2, nil); opts != nil {
		opts.ForEach(func(k, v lua.LValue) {
			switch name := lua.LVAsString(k); name {
			case "cwd":
				o.cwd = luaExecString(L, name, v)
			case "env":
				o.env = luaExecEnv(L, v)
			case "clear_env":
				o.clearEnv = luaExecBool(L, name, v)
			case "stdin":
				s := luaExecString(L, name, v)
				o.stdin = &s
			case "timeout":
				n, ok := v.(lua.LNumber)
				f := float64(n)
				if !ok || math.IsNaN(f) || f <= 0 {
					L.ArgError(2, "timeout must be a positive number")
				}
				if f >= float64(math.MaxInt64)/float64(time.Second) {
					L.ArgError(2, "timeout is too large")
				}
				o.timeout = time.Duration(f * float64(time.Second))
			case "stdout":
				o.discardStdout = !luaExecBool(L, name, v)
			case "stderr":
				o.discardStderr = !luaExecBool(L, name, v)
			default:
				L.ArgError(2, fmt.Sprintf("unknown option %q", name))
			}
		})
	}
	if d := rt.deadline; d != nil && d.pause() {
		defer d.resume()
	}
	res, err := runExec(argv, o)
	if err != nil {
		return luaFail(L, err.Error())
	}
	t := L.NewTable()
	L.SetField(t, "code", lua.LNumber(res.code))
	if res.signal != "" {
		L.SetField(t, "signal", lua.LString(res.signal))
	}
	L.SetField(t, "stdout", lua.LString(res.stdout))
	L.SetField(t, "stderr", lua.LString(res.stderr))
	L.SetField(t, "timed_out", lua.LBool(res.timedOut))
	L.SetField(t, "ok", lua.LBool(res.code == 0 && !res.timedOut))
	L.Push(t)
	return 1
}

func luaExecArgv(L *lua.LState) []string {
	t := L.CheckTable(1)
	if t.Len() == 0 {
		L.ArgError(1, "argv must be a non-empty list of strings")
	}
	argv := make([]string, 0, t.Len())
	for i := 1; i <= t.Len(); i++ {
		s, ok := t.RawGetInt(i).(lua.LString)
		if !ok {
			L.ArgError(1, "argv must be a non-empty list of strings")
		}
		argv = append(argv, string(s))
	}
	return argv
}

func luaExecEnv(L *lua.LState, v lua.LValue) map[string]*string {
	t, ok := v.(*lua.LTable)
	if !ok {
		L.ArgError(2, "env must be a table")
	}
	env := map[string]*string{}
	t.ForEach(func(k, v lua.LValue) {
		name, ok := k.(lua.LString)
		if !ok {
			L.ArgError(2, "env keys must be strings")
		}
		switch v := v.(type) {
		case lua.LString:
			s := string(v)
			env[string(name)] = &s
		case lua.LBool:
			if v {
				L.ArgError(2, "env values must be strings or false")
			}
			env[string(name)] = nil
		default:
			L.ArgError(2, "env values must be strings or false")
		}
	})
	return env
}

func luaExecString(L *lua.LState, name string, v lua.LValue) string {
	s, ok := v.(lua.LString)
	if !ok {
		L.ArgError(2, name+" must be a string")
	}
	return string(s)
}

func luaExecBool(L *lua.LState, name string, v lua.LValue) bool {
	b, ok := v.(lua.LBool)
	if !ok {
		L.ArgError(2, name+" must be a boolean")
	}
	return bool(b)
}

func (m model) withLua(rt *luaRuntime) model {
	m.lua = rt
	if rt != nil && len(rt.keys) > 0 {
		km := *m.keymap()
		km.resolve(rt.keys)
		m.keys = &km
	}
	return m
}

// label names k in the footer and the help.
func (k luaKey) label() string {
	if k.title != "" {
		return k.title
	}
	if k.action != "" {
		return k.action
	}
	return "<lua>"
}

func (m model) runLua(k tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if m.lua == nil {
		return m, nil, false
	}
	key, ok := m.keymap().screenKeys(m.screen()).lua[k.String()]
	if !ok || m.keysRaw() {
		return m, nil, false
	}
	return m.lua.call(m, *key)
}

// call is apart from runLua so that only a matched key moves m to the heap.
func (rt *luaRuntime) call(m model, key luaKey) (model, tea.Cmd, bool) {
	rt.cur = &m
	L := rt.L
	L.Push(key.fn)
	L.Push(luaCtx(L, m))
	err := rt.pcall(1, 1)
	m = *rt.cur
	rt.cur = nil
	cmd := tea.Batch(rt.cmds...)
	rt.cmds = nil
	if err != nil {
		m.flash = luaErrText(err)
		return m, cmd, true
	}
	ret := L.Get(-1)
	L.Pop(1)
	return m, cmd, lua.LVAsBool(ret)
}

func luaCtx(L *lua.LState, m model) *lua.LTable {
	ctx := L.NewTable()
	L.SetField(ctx, "section", lua.LString(m.screen()))
	return ctx
}

func luaFail(L *lua.LState, msg string) int {
	L.Push(lua.LNil)
	L.Push(lua.LString(msg))
	return 2
}

// luaErrText drops the traceback, which does not fit the footer.
func luaErrText(err error) string {
	msg := err.Error()
	if ae, ok := err.(*lua.ApiError); ok {
		msg = ae.Object.String()
	}
	msg = strings.TrimSpace(msg)
	// Syntax errors read "init.lua line:1(column:5) ...".
	if !strings.HasPrefix(msg, "init.lua:") && !strings.HasPrefix(msg, "init.lua line:") {
		msg = "init.lua: " + msg
	}
	return msg
}
