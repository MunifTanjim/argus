package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type defaultKeys struct {
	seqs []keySeq
	ids  []string // a single key lists every form
}

type keymap struct {
	timeout  time.Duration
	screens  map[string]*screenKeys
	bare     *screenKeys            // defaults only, for screens with no keymap table
	defaults map[string]defaultKeys // keyed by binding.defKey
	// kittyKey names the leader or the first mapped key that a terminal can
	// only send with the Kitty keyboard protocol, "" when there is none.
	kittyKey string
}

type screenKeys struct {
	add      map[string][]keySeq    // command → keys the user mapped to it
	addIDs   map[string][]string    // command → ids of those keys; a single key lists every form
	takenBy  map[string]string      // key id → the command the user mapped it to; "" removes it
	seqs     []screenSeq            // every effective key the resolver serves on the screen
	defaults map[string]defaultKeys // shared with the keymap
}

type screenSeq struct {
	seq     keySeq
	id, cmd string
}

type keyEntry struct {
	lhs string
	seq keySeq
	cmd string
}

// buildKeymap resolves raw (screen → key → command) into one table per screen.
// It skips each bad entry and returns one message for it. leader is the key
// that <Leader> stands for; "" defaults to <Space>.
func buildKeymap(raw map[string]map[string]string, timeout time.Duration, leader string) (*keymap, []string) {
	if timeout <= 0 {
		timeout = time.Second
	}
	km := &keymap{timeout: timeout, screens: map[string]*screenKeys{}, defaults: map[string]defaultKeys{}}
	var errs []string

	leaderStep := parseLeader(leader, &errs)
	if needsKitty(keySeq{leaderStep}) {
		km.kittyKey = fmt.Sprintf("tui.leader-key %q", leader)
	}

	for _, s := range slices.Sorted(maps.Keys(raw)) {
		switch {
		case s == "global" || screenBindings[s] != nil:
		case oldSections[s] != "":
			errs = append(errs, fmt.Sprintf("keymap: unknown screen %q (now %s)", s, oldSections[s]))
		default:
			errs = append(errs, fmt.Sprintf("keymap: unknown screen %q", s))
		}
	}
	for _, b := range bindingsOf(append(allBindingSets(), screenLeave)...) {
		if _, ok := km.defaults[b.defKey]; ok {
			continue
		}
		var dk defaultKeys
		for _, tok := range strings.Fields(b.defaults) {
			seq, err := parseKeySeq(tok)
			if err != nil {
				panic(fmt.Sprintf("buildKeymap: binding %q token %q: %v", b.name, tok, err))
			}
			seq = replaceLeader(seq, leaderStep)
			dk.seqs = append(dk.seqs, seq)
			if len(seq) == 1 {
				dk.ids = append(dk.ids, seq[0]...)
			} else {
				dk.ids = append(dk.ids, seq.id())
			}
		}
		km.defaults[b.defKey] = dk
	}
	km.bare = &screenKeys{defaults: km.defaults, add: map[string][]keySeq{}, addIDs: map[string][]string{}, takenBy: map[string]string{}}
	parse := func(section string) []keyEntry {
		var out []keyEntry
		for _, lhs := range slices.Sorted(maps.Keys(raw[section])) {
			cmd := normalizeCommand(raw[section][lhs])
			bad := func(format string, a ...any) {
				errs = append(errs, fmt.Sprintf("keymap: %s %q: ", section, lhs)+fmt.Sprintf(format, a...))
			}
			seq, err := parseKeySeq(lhs)
			switch {
			case err != nil:
				bad("%v", err)
				continue
			case cmd != "" && strings.ContainsAny(cmd[:1], "$!&%"):
				bad("shell commands are not supported yet")
				continue
			case cmd != "" && commandError(cmd) != "":
				bad("%s", commandError(cmd))
				continue
			case cmd != "" && !commandOn(section, cmd):
				bad("unknown command %q", cmd)
				continue
			case cmd != "" && len(seq) > 1 && textInputOnly(cmd):
				bad("%s works only in a text input, which has no sequences", cmd)
				continue
			}
			seq = replaceLeader(seq, leaderStep)
			if km.kittyKey == "" && needsKitty(seq) {
				km.kittyKey = lhs
			}
			out = append(out, keyEntry{lhs, seq, cmd})
		}
		return out
	}
	global := parse("global")
	for _, s := range slices.Sorted(maps.Keys(screenBindings)) {
		km.screens[s] = resolveScreen(s, parse(s), global, km.defaults)
	}
	return km, errs
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

func resolveScreen(screen string, own, global []keyEntry, defaults map[string]defaultKeys) *screenKeys {
	sk := &screenKeys{defaults: defaults, add: map[string][]keySeq{}, addIDs: map[string][]string{}, takenBy: map[string]string{}}
	apply := func(e keyEntry) {
		ids := []string{e.seq.id()}
		if len(e.seq) == 1 {
			ids = e.seq[0]
		}
		for _, id := range ids {
			sk.takenBy[id] = e.cmd
		}
		if e.cmd != "" {
			sk.add[e.cmd] = append(sk.add[e.cmd], e.seq)
			sk.addIDs[e.cmd] = append(sk.addIDs[e.cmd], ids...)
		}
	}
	ownIDs := map[string]bool{}
	for _, e := range own {
		ownIDs[e.seq.id()] = true
		apply(e)
	}
	for _, e := range global {
		if ownIDs[e.seq.id()] || (e.cmd != "" && !screenHas(screen, e.cmd)) {
			continue
		}
		apply(e)
	}
	seen := map[string]bool{}
	for _, b := range screenBindings[screen] {
		if textInputOnly(b.name) {
			continue
		}
		for _, s := range sk.seqsOf(b) {
			if id := s.id(); !seen[id] {
				seen[id] = true
				sk.seqs = append(sk.seqs, screenSeq{seq: s, id: id, cmd: b.name})
			}
		}
	}
	return sk
}

// keys is b's effective keys on the screen: the user's keys first, then the
// defaults that the user did not remove or give to another command.
func (sk *screenKeys) keys(b binding) []string {
	out := slices.Clone(sk.addIDs[b.name])
	for _, k := range sk.defaults[b.defKey].ids {
		if sk.defaultKept(b, k) {
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

// defaultKept reports whether b's default key k survives: the user did not
// remove it or give it to another command.
func (sk *screenKeys) defaultKept(b binding, k string) bool {
	cmd, ok := sk.takenBy[k]
	return !ok || cmd == b.name
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
	"redaction": true, "option": true, "answer": true, "tab": true,
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

func normalizeCommand(s string) string { return strings.Join(strings.Fields(s), " ") }

func commandError(cmd string) string {
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
