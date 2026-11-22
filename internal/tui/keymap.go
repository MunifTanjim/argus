package tui

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MunifTanjim/argus/internal/config"
)

type defaultKeys struct {
	seqs  []keySeq
	ids   []string // a single key lists every form
	quiet []string // ids of the keys the help does not list
}

type keymap struct {
	timeout  time.Duration
	screens  map[string]*screenKeys
	bare     *screenKeys            // defaults only, for screens with no keymap table
	defaults map[string]defaultKeys // keyed by binding.defKey
	// kittyKey names the leader or the first mapped key that a terminal can
	// only send with the Kitty keyboard protocol, "" when there is none.
	kittyKey string
	// userDescs are the user's mappings that have a desc: global first, then
	// each section in name order, as buildKeymap parses them.
	userDescs []userDesc
	// mappings are the userDescs that no init.lua binding hides, then the
	// init.lua bindings other than built-in actions, for the help.
	mappings []userDesc
	sections map[string][]keyEntry // parsed mappings by section, "global" included
	leader   keyStep
}

// userDesc is a user mapping with a desc. action is its command, or its key
// RHS as written. For an init.lua binding, action is its custom action name.
type userDesc struct {
	section, action, desc string
	seq                   keySeq
}

// keyDesc is a user mapping with a desc that applies on a screen.
type keyDesc struct {
	id   string
	cmds []string
	desc string
}

type screenKeys struct {
	add      map[string][]keySeq    // command → keys the user mapped to it
	addIDs   map[string][]string    // command → ids of those keys; a single key lists every form
	takenBy  map[string][]string    // key id → the commands its mapping runs; empty removes it
	seqs     []screenSeq            // every effective key the resolver serves on the screen
	base     []screenSeq            // seqs without the Lua function bindings, for their fall-through
	descs    []keyDesc              // the user's mappings with a desc, in the order they apply
	defaults map[string]defaultKeys // shared with the keymap
	lua      map[string]*luaKey     // key id → the init.lua handler; a single key lists every form
}

type screenSeq struct {
	seq           keySeq
	id, cmd, desc string
}

// keyEntry is one mapping. cmds is what it runs; rhs is a key RHS that
// resolveScreen turns into cmds per screen. Neither removes the key. lua is an
// init.lua function or custom action binding.
type keyEntry struct {
	seq  keySeq
	cmds []string
	rhs  keySeq
	desc string
	lua  *luaKey
}

// keyLayer is one precedence level of a screen's mappings. filter drops the
// commands that the screen does not have.
type keyLayer struct {
	entries     []keyEntry
	filter, lua bool
}

// buildKeymap resolves raw (screen → key → RHS in Vim notation) into one table
// per screen, with the default keymap under it.
// It skips each bad entry and returns one message for it. leader is the key
// that <Leader> stands for; "" defaults to <Space>.
func buildKeymap(raw map[string]map[string]config.Mapping, timeout time.Duration, leader string) (*keymap, []string) {
	if timeout <= 0 {
		timeout = time.Second
	}
	km := &keymap{timeout: timeout, defaults: map[string]defaultKeys{}, sections: map[string][]keyEntry{}}
	var errs []string

	km.leader = parseLeader(leader, &errs)
	if needsKitty(keySeq{km.leader}) {
		km.kittyKey = fmt.Sprintf("tui.leader-key %q", leader)
	}

	for _, s := range slices.Sorted(maps.Keys(raw)) {
		if err := checkScreen(s); err != nil {
			errs = append(errs, "keymap: "+err.Error())
		}
	}
	for _, b := range bindingsOf(append(allBindingSets(), screenLeave)...) {
		if _, ok := km.defaults[b.defKey]; ok {
			continue
		}
		var dk defaultKeys
		quiet := strings.Fields(b.quiet)
		for _, tok := range append(strings.Fields(b.defaults), quiet...) {
			seq := mustParseDefault(b, tok, km.leader)
			dk.seqs = append(dk.seqs, seq)
			ids := seq[0]
			if len(seq) > 1 {
				ids = []string{seq.id()}
			}
			dk.ids = append(dk.ids, ids...)
			if slices.Contains(quiet, tok) {
				dk.quiet = append(dk.quiet, ids...)
			}
		}
		km.defaults[b.defKey] = dk
	}
	km.bare = resolveScreen("", nil, km.defaults)
	parse := func(section string) []keyEntry {
		var out []keyEntry
		for _, lhs := range slices.Sorted(maps.Keys(raw[section])) {
			mp := raw[section][lhs]
			rhs := mp.Action
			bad := func(format string, a ...any) {
				errs = append(errs, fmt.Sprintf("keymap: %s %q: ", section, lhs)+fmt.Sprintf(format, a...))
			}
			seq, err := parseKeySeq(lhs)
			switch {
			case err != nil:
				bad("%v", err)
				continue
			case seq[0][0] == ":":
				bad(`":" opens the command line`)
				continue
			case mp.Problem != "":
				bad("%s", mp.Problem)
				continue
			}
			e := keyEntry{seq: replaceLeader(seq, km.leader), desc: mp.Desc}
			switch kind, cmd := parseRHS(rhs); kind {
			case rhsNop:
				if mp.Desc != "" {
					bad("a removed key has no desc")
					continue
				}
			case rhsEmpty:
				bad(`use "<Nop>" to remove a key`)
				continue
			case rhsUnsupported:
				bad("not supported")
				continue
			case rhsCommand:
				switch {
				case strings.ContainsAny(cmd[:1], "$!&%"):
					bad("shell commands are not supported yet")
					continue
				case commandError(cmd) != "":
					bad("%s", commandError(cmd))
					continue
				case !commandOn(section, cmd):
					bad("unknown command %q", cmd)
					continue
				case len(seq) > 1 && textInputOnly(cmd):
					bad("%s works only in a text input, which has no sequences", cmd)
					continue
				}
				e.cmds = []string{cmd}
			case rhsKeys:
				r, err := parseKeySeq(rhs)
				if err != nil {
					bad("%v", err)
					continue
				}
				e.rhs = replaceLeader(r, km.leader)
				cmds := keyRHSCmds(section, e.rhs, km.defaults)
				switch {
				case len(cmds) == 0:
					c := normalizeCommand(rhs)
					if to, ok := removedCommands[c]; ok {
						c = to
					}
					if commandError(c) == "" {
						bad(`use ":%s<CR>"`, c)
					} else {
						bad("no built-in key %q in %s", rhs, section)
					}
					continue
				case len(seq) > 1 && !slices.ContainsFunc(cmds, func(c string) bool { return !textInputOnly(c) }):
					bad("%s works only in a text input, which has no sequences", cmds[0])
					continue
				}
			}
			if km.kittyKey == "" && needsKitty(e.seq) {
				km.kittyKey = lhs
			}
			out = append(out, e)
			if e.desc != "" {
				action := rhs
				if len(e.cmds) == 1 {
					action = e.cmds[0]
				}
				km.userDescs = append(km.userDescs, userDesc{section: section, action: action, desc: e.desc, seq: e.seq})
			}
		}
		return out
	}
	km.sections["global"] = parse("global")
	for _, s := range slices.Sorted(maps.Keys(screenBindings)) {
		km.sections[s] = parse(s)
	}
	km.resolve(nil)
	return km, errs
}

// resolve builds the screen tables and the help mappings with the init.lua
// bindings lua above the user's mappings.
func (km *keymap) resolve(lua []luaKey) {
	km.screens = map[string]*screenKeys{}
	luaGlobal := luaEntries(lua, "")
	for s := range screenBindings {
		km.screens[s] = resolveScreen(s, []keyLayer{
			{entries: luaEntries(lua, s), filter: true, lua: true},
			{entries: luaGlobal, filter: true, lua: true},
			{entries: km.sections[s]},
			{entries: km.sections["global"], filter: true},
			{entries: defaultEntries(s, km.leader)},
		}, km.defaults)
	}
	km.mappings = km.helpMappings(lua)
}

// luaEntries is screen's init.lua bindings, the last argus.bind first. A
// built-in action binding is a mapping to its command.
func luaEntries(keys []luaKey, screen string) []keyEntry {
	var out []keyEntry
	for i, k := range slices.Backward(keys) {
		switch b, builtin := actions[k.action]; {
		case k.screen != screen:
		case builtin:
			out = append(out, keyEntry{seq: k.seq, cmds: []string{b.name}})
		default:
			out = append(out, keyEntry{seq: k.seq, lua: &keys[i]})
		}
	}
	return out
}

// helpMappings hides a mapping that an action binding of its section takes,
// and a section mapping that its screen does not use. A function binding hides
// nothing, as the key falls through to the mapping. Of the bindings of one key
// and section, the last argus.bind shows, at the place of the first.
func (km *keymap) helpMappings(lua []luaKey) []userDesc {
	var bound []luaKey
	at := map[[2]string]int{}
	for _, k := range lua {
		key := [2]string{cmp.Or(k.screen, "global"), k.seq.id()}
		if i, ok := at[key]; ok {
			bound[i] = k
			continue
		}
		at[key] = len(bound)
		bound = append(bound, k)
	}
	var out []userDesc
	for _, d := range km.userDescs {
		id := d.seq.id()
		if i, ok := at[[2]string{d.section, id}]; ok && bound[i].action != "" {
			continue
		}
		if sk := km.screens[d.section]; sk != nil && !slices.ContainsFunc(sk.descs, func(kd keyDesc) bool { return kd.id == id }) {
			continue
		}
		out = append(out, d)
	}
	for _, k := range bound {
		if _, builtin := actions[k.action]; builtin {
			continue
		}
		d := userDesc{section: cmp.Or(k.screen, "global"), desc: k.label(), seq: k.seq}
		if k.title != "" {
			d.action = k.action
		}
		out = append(out, d)
	}
	return out
}

func parseLeader(leader string, errs *[]string) keyStep {
	space := keyStep{"space"}
	if leader == "" {
		return space
	}
	seq, err := parseKeySeq(leader)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("keymap: tui.leader-key %q: %v", leader, err))
		return space
	}
	if len(seq) != 1 || (len(seq[0]) == 1 && seq[0][0] == leaderToken) {
		*errs = append(*errs, fmt.Sprintf("keymap: tui.leader-key %q: must be one key", leader))
		return space
	}
	if seq[0][0] == ":" {
		*errs = append(*errs, fmt.Sprintf(`keymap: tui.leader-key %q: ":" opens the command line`, leader))
		return space
	}
	return seq[0]
}

func replaceLeader(seq keySeq, leader keyStep) keySeq {
	copied := false
	for i, st := range seq {
		if len(st) == 1 && st[0] == leaderToken {
			if !copied {
				seq = slices.Clone(seq)
				copied = true
			}
			seq[i] = leader
		}
	}
	return seq
}

// screenKeys is the screen's table, or the defaults alone for a screen with no
// keymap (the live screen).
func (km *keymap) screenKeys(screen string) *screenKeys {
	if sk := km.screens[screen]; sk != nil {
		return sk
	}
	return km.bare
}

// resolveScreen applies layers over the built-in keys. The first layer that
// maps a key wins, except that a Lua function binding leaves the key to the
// non-Lua layers, which run when the function does not handle it.
func resolveScreen(screen string, layers []keyLayer, defaults map[string]defaultKeys) *screenKeys {
	sk := &screenKeys{defaults: defaults, add: map[string][]keySeq{}, addIDs: map[string][]string{}, takenBy: map[string][]string{}, lua: map[string]*luaKey{}}
	taken, luaSeen, seen, baseSeen := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, l := range layers {
		for _, e := range l.entries {
			id := e.seq.id()
			if taken[id] || l.lua && luaSeen[id] {
				continue
			}
			if l.lua {
				luaSeen[id] = true
			}
			cmds := e.cmds
			if e.rhs != nil {
				cmds = builtinCmds(screen, e.rhs, defaults)
			}
			if l.filter {
				cmds = slices.DeleteFunc(slices.Clone(cmds), func(c string) bool { return !screenHas(screen, c) })
				if len(cmds) == 0 && (len(e.cmds) > 0 || e.rhs != nil) {
					continue
				}
			}
			ids := []string{id}
			if len(e.seq) == 1 {
				ids = e.seq[0]
			}
			if e.lua != nil {
				for _, i := range ids {
					sk.lua[i] = e.lua
				}
				seen[id] = true
				sk.seqs = append(sk.seqs, screenSeq{seq: e.seq, id: id, cmd: e.lua.label()})
				if e.lua.action == "" {
					continue
				}
			}
			taken[id] = true
			for _, i := range ids {
				sk.takenBy[i] = cmds
			}
			for _, c := range cmds {
				sk.add[c] = append(sk.add[c], e.seq)
				sk.addIDs[c] = append(sk.addIDs[c], ids...)
			}
			if e.desc != "" && len(cmds) > 0 {
				sk.descs = append(sk.descs, keyDesc{id: id, cmds: cmds, desc: e.desc})
			}
		}
	}
	for _, b := range screenBindings[screen] {
		if textInputOnly(b.name) {
			continue
		}
		for _, s := range sk.seqsOf(b) {
			id := s.id()
			ss := screenSeq{seq: s, id: id, cmd: b.name, desc: sk.descOf(id)}
			if !seen[id] {
				seen[id] = true
				sk.seqs = append(sk.seqs, ss)
			}
			if !baseSeen[id] {
				baseSeen[id] = true
				sk.base = append(sk.base, ss)
			}
		}
	}
	return sk
}

// keys is b's effective keys on the screen: the user's keys first, then the
// defaults that the user did not map again, remove, or give to another command.
func (sk *screenKeys) keys(b binding) []string {
	out := slices.Clone(sk.addIDs[b.name])
	for _, k := range sk.defaults[b.defKey].ids {
		if sk.defaultKept(b, k) && !slices.Contains(sk.addIDs[b.name], k) {
			out = append(out, k)
		}
	}
	return out
}

// has reports whether key id k triggers b on the screen. It is keys without
// building the list, because handlers check many bindings on every key press.
func (sk *screenKeys) has(b binding, k string) bool {
	return slices.Contains(sk.addIDs[b.name], k) || slices.Contains(sk.defaults[b.defKey].ids, k) && sk.defaultKept(b, k)
}

// listedKeys is keys without the quiet defaults that the user did not also map
// to b: the keys that the help and the command line list.
func (sk *screenKeys) listedKeys(b binding) []string {
	return slices.DeleteFunc(sk.keys(b), func(k string) bool {
		return slices.Contains(sk.defaults[b.defKey].quiet, k) && !slices.Contains(sk.addIDs[b.name], k)
	})
}

// descOf is the desc of the user mapping of key id, "" for none.
func (sk *screenKeys) descOf(id string) string {
	for _, d := range sk.descs {
		if d.id == id {
			return d.desc
		}
	}
	return ""
}

// descFor is the first user mapping with a desc that runs name.
func (sk *screenKeys) descFor(name string) (keyDesc, bool) {
	for _, d := range sk.descs {
		if slices.Contains(d.cmds, name) {
			return d, true
		}
	}
	return keyDesc{}, false
}

// defaultKept reports whether b's built-in key k survives: no mapping took k,
// or the one that did runs b.
func (sk *screenKeys) defaultKept(b binding, k string) bool {
	cmds, ok := sk.takenBy[k]
	return !ok || slices.Contains(cmds, b.name)
}

func (sk *screenKeys) seqsOf(b binding) []keySeq {
	out := slices.Clone(sk.add[b.name])
	for _, s := range sk.defaults[b.defKey].seqs {
		if sk.defaultKept(b, s.id()) {
			out = append(out, s)
		}
	}
	return out
}

type rhsKind int

const (
	rhsKeys rhsKind = iota
	rhsCommand
	rhsNop
	rhsEmpty
	rhsUnsupported
)

// parseRHS classifies a mapping's RHS. cmd is the command of :cmd<CR>.
func parseRHS(rhs string) (kind rhsKind, cmd string) {
	switch {
	case rhs == "":
		return rhsEmpty, ""
	case strings.EqualFold(rhs, "<Nop>"):
		return rhsNop, ""
	case !strings.HasPrefix(rhs, ":"):
		return rhsKeys, ""
	}
	body := rhs[1:]
	for _, cr := range []string{"<cr>", "<enter>", "<return>"} {
		if n := len(body) - len(cr); n >= 0 && strings.EqualFold(body[n:], cr) {
			cmd = resolveCommand(body[:n])
			if cmd != "" && !strings.Contains(cmd, "<") {
				return rhsCommand, cmd
			}
		}
	}
	return rhsUnsupported, ""
}

// builtinCmds is the commands whose built-in keys on screen include rhs.
func builtinCmds(screen string, rhs keySeq, defaults map[string]defaultKeys) []string {
	id := rhs.id()
	var out []string
	for _, b := range screenBindings[screen] {
		if slices.ContainsFunc(defaults[b.defKey].seqs, func(s keySeq) bool { return s.id() == id }) && !slices.Contains(out, b.name) {
			out = append(out, b.name)
		}
	}
	return out
}

// keyRHSCmds is the commands that rhs runs as a built-in key in section, or,
// for global, in any screen.
func keyRHSCmds(section string, rhs keySeq, defaults map[string]defaultKeys) []string {
	if section != "global" {
		return builtinCmds(section, rhs, defaults)
	}
	var out []string
	for _, s := range slices.Sorted(maps.Keys(screenBindings)) {
		for _, c := range builtinCmds(s, rhs, defaults) {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

// defaultEntries is screen's default keymap: each binding's maps keys run its
// command.
func defaultEntries(screen string, leader keyStep) []keyEntry {
	at := map[string]int{}
	var out []keyEntry
	for _, b := range screenBindings[screen] {
		for _, tok := range strings.Fields(b.maps) {
			seq := mustParseDefault(b, tok, leader)
			if i, ok := at[seq.id()]; ok {
				if !slices.Contains(out[i].cmds, b.name) {
					out[i].cmds = append(out[i].cmds, b.name)
				}
				continue
			}
			at[seq.id()] = len(out)
			out = append(out, keyEntry{seq: seq, cmds: []string{b.name}})
		}
	}
	return out
}

func commandOn(section, cmd string) bool {
	if section != "global" {
		return screenHas(section, cmd)
	}
	for s := range screenBindings {
		if screenHas(s, cmd) {
			return true
		}
	}
	return false
}

// keySetNames holds every command that some non-text-input set binds.
var keySetNames = func() map[string]bool {
	out := map[string]bool{}
	for _, b := range bindingsOf(keySets()...) {
		out[b.name] = true
	}
	return out
}()

func textInputOnly(cmd string) bool { return !keySetNames[cmd] }

// entityNames are the first words of commands that act on the selected
// entity: the rest of such a command is an action, not an argument.
var entityNames = map[string]bool{
	"project": true, "workspace": true, "session": true, "transcript": true,
	"redaction": true, "answer": true, "tab": true, "agent": true,
}

// commandArgs maps the first word of every command to the rest of each
// command that starts with it, sorted; "" stands for a bare command such as
// open.
var commandArgs = func() map[string][]string {
	out := map[string][]string{}
	for _, b := range bindingsOf(allBindingSets()...) {
		if b.name == "" {
			continue
		}
		verb, arg, _ := strings.Cut(b.name, " ")
		if !slices.Contains(out[verb], arg) {
			out[verb] = append(out[verb], arg)
		}
	}
	for _, args := range out {
		slices.Sort(args)
	}
	return out
}()

// renamedCommands keeps keymap files that name a command by its old name
// working.
var renamedCommands = map[string]string{"terminal new": "terminal spawn"}

// removedCommands maps the commands that a toggle replaced to the toggle, so
// that an old keymap gets a hint.
var removedCommands = map[string]string{
	"project pin": "toggle pin", "project unpin": "toggle pin",
	"project hide": "toggle hidden", "project unhide": "toggle hidden",
	"option select": "toggle selection", "option unselect": "toggle selection",
}

// mustParseDefault parses one of b's default key tokens, which are fixed in
// code, so an error is a bug.
func mustParseDefault(b binding, tok string, leader keyStep) keySeq {
	seq, err := parseKeySeq(tok)
	if err != nil {
		panic(fmt.Sprintf("binding %q token %q: %v", b.name, tok, err))
	}
	return replaceLeader(seq, leader)
}

// resolveCommand is the command that typed text names, the way the command
// line reads it: spaces normalized, renames and aliases applied.
func resolveCommand(s string) string {
	name := normalizeCommand(s)
	if full, ok := cmdAliases[name]; ok {
		return full
	}
	return name
}

func normalizeCommand(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if to, ok := renamedCommands[s]; ok {
		return to
	}
	return s
}

func commandError(cmd string) string {
	if to, ok := removedCommands[cmd]; ok {
		return fmt.Sprintf("%s is now %s", cmd, to)
	}
	verb, arg, _ := strings.Cut(cmd, " ")
	args, ok := commandArgs[verb]
	if !ok {
		return fmt.Sprintf("unknown command %q", cmd)
	}
	if slices.Contains(args, arg) {
		return ""
	}
	noun := "argument"
	if entityNames[verb] {
		noun = "action"
	}
	list := strings.Join(slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "" }), ", ")
	if arg == "" {
		return fmt.Sprintf("%s needs an %s (%s)", verb, noun, list)
	}
	return fmt.Sprintf("unknown %s %q for %s (%s)", noun, arg, verb, list)
}

// legacyCtrl reports whether a legacy terminal can send ctrl with character k.
func legacyCtrl(k string) bool {
	r, _ := utf8.DecodeRuneInString(k)
	return (r >= 'a' && r <= 'z') || strings.ContainsRune(`@[\]^_?`, r)
}

// needsKitty reports whether seq has a key that only the Kitty keyboard
// protocol can send. shift+tab is exempt: legacy terminals send it as backtab.
func needsKitty(seq keySeq) bool {
	for _, st := range seq {
		k := st[0]
		mods := map[string]bool{}
		for _, p := range []string{"ctrl", "alt", "shift", "super"} {
			if rest, ok := strings.CutPrefix(k, p+"+"); ok {
				mods[p], k = true, rest
			}
		}
		switch {
		case len(mods) >= 2, mods["super"]:
			return true
		case mods["ctrl"] && (k == "enter" || k == "tab" || k == "backspace"),
			mods["shift"] && (k == "enter" || k == "backspace"),
			mods["ctrl"] && utf8.RuneCountInString(k) == 1 && !legacyCtrl(k):
			return true
		}
	}
	return false
}
