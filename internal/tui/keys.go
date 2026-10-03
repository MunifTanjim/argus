package tui

import (
	"reflect"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Live screen passthrough keys are not here: they go to the PTY directly.
//
// Paired actions (labelPairs) share one footer label on the binding that has the
// help text; the partner's help is empty, so the footer shows one entry.

// binding is a key binding, the command name that keymaps use for it, and its
// default keys in Vim notation.
type binding struct {
	key.Binding
	name     string
	defaults string
	quiet    string // default keys that work but that the help does not list
	defKey   string // name and default keys: bindings with equal defKey have equal default keys
}

func nb(name, defaults, desc string) binding {
	return binding{
		Binding:  key.NewBinding(key.WithKeys([]string{}...), key.WithHelp("", desc)),
		name:     name,
		defaults: defaults,
		defKey:   name + "\x00" + defaults,
	}
}

func (b binding) alt(keys string) binding {
	b.quiet = keys
	b.defKey = b.name + "\x00" + b.defaults + "\x00" + keys
	return b
}

// keyAction applies a matched key to a transcript. Method expressions (e.g.
// tview.actTop) satisfy this, so tables can name transcript methods directly.
type keyAction = func(m tview, msg tea.KeyPressMsg) tea.Cmd

// keyTableEntry pairs a binding with the action it triggers.
type keyTableEntry struct {
	b   binding
	act keyAction
}

// dispatch runs the first table entry whose binding matches msg. ok is false when
// nothing matched (the caller falls back, e.g. to text input).
func (m tview) dispatch(msg tea.KeyPressMsg, table []keyTableEntry) (tea.Cmd, bool) {
	for _, e := range table {
		if m.c.m.matches(msg, e.b) {
			return e.act(m, msg), true
		}
	}
	return nil, false
}

func (m model) matches(msg tea.KeyPressMsg, bs ...binding) bool {
	sk := m.keymap().screenKeys(m.screen())
	if strictScreens {
		for _, b := range bs {
			checkListed(m.screen(), b)
		}
	}
	k := msg.String()
	if name, ok := strings.CutPrefix(k, cmdMark); ok {
		return slices.ContainsFunc(bs, func(b binding) bool { return b.Enabled() && b.name == name })
	}
	for _, b := range bs {
		if !b.Enabled() {
			continue
		}
		if sk.has(b, k) {
			return true
		}
	}
	return false
}

// --- binding sets -------------------------------------------------------------

var listKeys = struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown                      binding
	Open, Jump, TabPrev, TabNext, New, Kill, Refresh, Back, Quit binding
	ActiveOnly, Filter                                           binding
}{
	Up:       nb("prev", "<Up> k", "move"),
	Down:     nb("next", "<Down> j", ""),
	Top:      nb("goto top", "gg", ""),
	Bottom:   nb("goto bottom", "G", "ends"),
	HalfUp:   nb("scroll half-page-up", "<C-u> <PageUp>", ""),
	HalfDown: nb("scroll half-page-down", "<C-d> <PageDown>", ""),
	Open:     nb("open", "<CR>", "open"),
	Jump:     nb("open tmux-pane", "O", "jump"),
	TabPrev:  nb("tab prev", "gT", ""),
	TabNext:  nb("tab next", "gt", "tabs"),
	New:      nb("session spawn", "s", "spawn"),
	Kill:     nb("session kill", "dd", "kill"),
	Refresh:  nb("refresh", "gr", "refresh"),
	Back:     nb("back", "<Esc>", "tree"),
	Quit:     nb("quit", "Q", "quit"),

	ActiveOnly: nb("toggle active-only", "za", "active"),
	Filter:     nb("filter-sessions", "/", "filter"),
}

var terminalKeys = struct {
	New, Rename, Kill binding
}{
	New:    nb("terminal new", "a", "new"),
	Rename: nb("terminal rename", "r", "rename"),
	Kill:   nb("terminal kill", "dd", "kill"),
}

var nodeKeys = struct {
	Wakelock binding
}{
	Wakelock: nb("node wakelock", "w", "wakelock"),
}

var projectsKeys = struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown, Left, Right, Enter       binding
	Widen, Narrow, ToggleSidebar, Filter, Help                        binding
	New, Rename, Hide, Pin, Remove, ForceRemove, ShowHidden, ShowGone binding
	Unhide, Unpin                                                     binding
	Target, DiffMode, Spawn, ToggleFiles                              binding
	SideTabPrev, SideTabNext, Refresh, Back                           binding
	Forget                                                            binding
	RunSetup, SetupLog, Palette, ToggleMouse                          binding
}{
	Up:            nb("prev", "<Up> k", "move"),
	Down:          nb("next", "<Down> j", ""),
	Top:           nb("goto top", "gg", ""),
	Bottom:        nb("goto bottom", "G", "ends"),
	HalfUp:        nb("scroll half-page-up", "<C-u> <PageUp>", ""),
	HalfDown:      nb("scroll half-page-down", "<C-d> <PageDown>", ""),
	Left:          nb("fold close", "h <Left> zc", "fold"),
	Right:         nb("fold open", "l <Right> zo", ""),
	Enter:         nb("open", "<CR>", "open"),
	Widen:         nb("sidebar wider", "<C-w>>", "resize"),
	Narrow:        nb("sidebar narrower", "<C-w><lt>", ""),
	ToggleSidebar: nb("toggle left-sidebar", "<Leader>o", "sidebar"),
	Filter:        nb("filter-projects", "/", "filter"),
	Help:          nb("help", "g?", "help"),
	New:           nb("workspace new", "a", "new"),
	Rename:        nb("project rename", "r", "rename"),
	Hide:          nb("project hide", "H", "hide"),
	Unhide:        nb("project unhide", "H", ""),
	Pin:           nb("project pin", "P", "pin"),
	Unpin:         nb("project unpin", "P", ""),
	Remove:        nb("workspace remove", "dd", "remove"),
	ForceRemove:   nb("workspace force-remove", "D", ""),
	ShowHidden:    nb("toggle show-hidden", "z.", "hidden"),
	ShowGone:      nb("toggle show-gone", "zg", "gone"),
	Target:        nb("workspace change-target", "T", "target"),
	DiffMode:      nb("toggle diff-vs-target", "t", "vs target"),
	Spawn:         nb("session spawn", "s", "spawn"),
	ToggleFiles:   nb("toggle right-sidebar", "<Leader>e", "files"),
	SideTabPrev:   nb("tab prev", "gT", ""),
	SideTabNext:   nb("tab next", "gt", "tabs"),
	Refresh:       nb("refresh", "gr", "refresh"),
	Forget:        nb("project forget", "F", "forget"),
	Back:          nb("back", "<Esc>", "back"),
	RunSetup:      nb("workspace rerun-setup", "S", "setup again"),
	SetupLog:      nb("open setup-log", "L", "setup log"),
	Palette:       nb("open palette", "<C-k>", "palette"),
	ToggleMouse:   nb("toggle mouse", "", "mouse"),
}

var createKeys = struct {
	Target binding
}{
	Target: nb("workspace pick-target", "<C-t>", "target"),
}

var transcriptKeys = struct {
	ScrollUp, ScrollDown, CardNext, CardPrev, HalfUp, HalfDown binding
	Top, Bottom, Collapse, Expand, Detail                      binding
	Answer, Export, Back, Resume                               binding
	Redact, RedactSave, RedactList                             binding
}{
	ScrollUp:   nb("scroll up", "<Up> k", "scroll"),
	ScrollDown: nb("scroll down", "<Down> j", ""),
	CardNext:   nb("next card", "}", "card"),
	CardPrev:   nb("prev card", "{", ""),
	HalfUp:     nb("scroll half-page-up", "<C-u> <PageUp>", ""),
	HalfDown:   nb("scroll half-page-down", "<C-d> <PageDown>", ""),
	Top:        nb("goto top", "gg", ""),
	Bottom:     nb("goto bottom", "G", "ends"),
	Collapse:   nb("fold close", "h <Left> zc", "fold"),
	Expand:     nb("fold open", "l <Right> zo", ""),
	Detail:     nb("open", "<CR>", "detail"),
	Answer:     nb("focus prompt", "<Tab>", "answer"),
	Export:     nb("transcript export", "E", "export"),
	Back:       nb("back", "<Esc>", "back"),
	Resume:     nb("session resume", "R", "resume"),
	Redact:     nb("redaction add", "d", "redact"),
	RedactList: nb("redaction list", "D", "redactions"),
	RedactSave: nb("redaction save", "W", "save redacted"),
}

var detailKeys = struct {
	Up, Down, HalfUp, HalfDown, Top, Bottom, Collapse, Expand, Drill, Back binding
}{
	Up:       nb("prev", "<Up> k", "move"),
	Down:     nb("next", "<Down> j", ""),
	HalfUp:   nb("scroll half-page-up", "<C-u> <PageUp>", ""),
	HalfDown: nb("scroll half-page-down", "<C-d> <PageDown>", ""),
	Top:      nb("goto top", "gg", ""),
	Bottom:   nb("goto bottom", "G", ""),
	Collapse: nb("fold close", "h <Left> zc", "fold"),
	Expand:   nb("fold open", "l <Right> zo", ""),
	Drill:    nb("open", "<CR>", "drill"),
	Back:     nb("back", "<Esc>", "back"),
}

// sessionKeys are the keys a live transcript and the session dock take before
// their own: the prompt focus moves and the live screen.
var sessionKeys = struct {
	FocusPrompt, FocusTranscript, Raw binding
}{
	FocusPrompt:     nb("focus prompt", "<Tab>", "answer"),
	FocusTranscript: nb("focus transcript", "<Tab>", "read"),
	Raw:             nb("open live-screen", "<C-t>", "raw"),
}

// paneKeys move focus between the tree, the main pane, the files sidebar, and
// the dock.
var paneKeys = struct {
	Left, Down, Up, Right, Next, Prev binding
}{
	Left:  nb("focus left", "<C-w>h", "tree").alt("<C-w><C-h>"),
	Down:  nb("focus down", "<C-w>j", "").alt("<C-w><C-j>"),
	Up:    nb("focus up", "<C-w>k", "").alt("<C-w><C-k>"),
	Right: nb("focus right", "<C-w>l", "files").alt("<C-w><C-l>"),
	Next:  nb("focus next", "<C-w>w", "pane").alt("<C-w><C-w>"),
	Prev:  nb("focus prev", "<C-w>W", ""),
}

// Prompt bindings (dock): drive dock footers; the prompt sub-views are modal text editors.
var promptKeys = struct {
	Up, Down, HalfUp, HalfDown, TabPrev, TabNext, Submit, Next, Select, Read binding
	Unselect                                                                 binding
	Back                                                                     binding
}{
	Up:       nb("prev", "<Up>", "select"),
	Down:     nb("next", "<Down>", ""),
	HalfUp:   nb("scroll half-page-up", "<C-u> <PageUp>", "scroll"),
	HalfDown: nb("scroll half-page-down", "<C-d> <PageDown>", ""),
	TabPrev:  nb("tab prev", "<Left>", "tabs"),
	TabNext:  nb("tab next", "<Right>", ""),
	Submit:   nb("answer submit", "<CR>", "submit"),
	Next:     nb("answer submit", "<CR>", "next"), // footer label for multi-question advance
	Select:   nb("option select", "<Space>", "toggle"),
	Unselect: nb("option unselect", "<Space>", ""),
	Read:     nb("focus transcript", "<Tab>", "read"),
	Back:     nb("back", "<Esc>", "back"),
}

var historyProjectsKeys = struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown, Open, Refresh, Back binding
}{
	Up:       nb("prev", "<Up> k", "move"),
	Down:     nb("next", "<Down> j", ""),
	Top:      nb("goto top", "gg", ""),
	Bottom:   nb("goto bottom", "G", "ends"),
	HalfUp:   nb("scroll half-page-up", "<C-u> <PageUp>", ""),
	HalfDown: nb("scroll half-page-down", "<C-d> <PageDown>", ""),
	Open:     nb("open", "<CR>", "open"),
	Refresh:  nb("refresh", "gr", "refresh"),
	Back:     nb("back", "<Esc>", "back"),
}

var historySessionsKeys = struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown, Open, More, Back, Resume binding
}{
	Up:       nb("prev", "<Up> k", "move"),
	Down:     nb("next", "<Down> j", ""),
	Top:      nb("goto top", "gg", ""),
	Bottom:   nb("goto bottom", "G", "ends"),
	HalfUp:   nb("scroll half-page-up", "<C-u> <PageUp>", ""),
	HalfDown: nb("scroll half-page-down", "<C-d> <PageDown>", ""),
	Open:     nb("open", "<CR>", "open"),
	More:     nb("session load-more", "m", "more"),
	Back:     nb("back", "<Esc>", "back"),
	Resume:   nb("session resume", "R", "resume"),
}

var logsKeys = struct {
	Up, Down, HalfUp, HalfDown, Top, Bottom, Back binding
}{
	Up:       nb("scroll up", "<Up> k", "scroll"),
	Down:     nb("scroll down", "<Down> j", ""),
	HalfUp:   nb("scroll half-page-up", "<C-u> <PageUp>", ""),
	HalfDown: nb("scroll half-page-down", "<C-d> <PageDown>", ""),
	Top:      nb("goto top", "gg", ""),
	Bottom:   nb("goto bottom", "G", "ends"),
	Back:     nb("back", "<Esc>", "back"),
}

// fileViewKeys drive the file, diff, or setup log open in the projects pane.
var fileViewKeys = struct {
	Up, Down, HalfUp, HalfDown, Top, Bottom, Wrap, NextFile, PrevFile, Refresh, Back binding
}{
	Up:       nb("scroll up", "<Up> k", "scroll"),
	Down:     nb("scroll down", "<Down> j", ""),
	HalfUp:   nb("scroll half-page-up", "<C-u> <PageUp>", ""),
	HalfDown: nb("scroll half-page-down", "<C-d> <PageDown>", ""),
	Top:      nb("goto top", "gg", ""),
	Bottom:   nb("goto bottom", "G", "ends"),
	Wrap:     nb("toggle line-wrap", "yow", "wrap"),
	NextFile: nb("next diff-file", "]f", "file"),
	PrevFile: nb("prev diff-file", "[f", ""),
	Refresh:  nb("refresh", "gr", "refresh"),
	Back:     nb("back", "<Esc>", "back"),
}

var redactListKeys = struct {
	Up, Down, Remove binding
}{
	Up:     nb("prev", "<Up> k", ""),
	Down:   nb("next", "<Down> j", "move"),
	Remove: nb("redaction remove", "u", "remove"),
}

// screenLeave is the only app binding in live-screen passthrough; every other key
// is forwarded to the pane.
var screenLeave = nb("", "<C-]>", "leave")

// textInputSets are the binding sets that only text inputs read, where the
// resolver builds no sequences.
func textInputSets() []any { return []any{createKeys, promptKeys} }

func keySets() []any {
	return []any{listKeys, projectsKeys, fileViewKeys, transcriptKeys, redactListKeys,
		detailKeys, sessionKeys, paneKeys, historyProjectsKeys, historySessionsKeys, logsKeys, terminalKeys, nodeKeys}
}

func allBindingSets() []any { return append(keySets(), textInputSets()...) }

// bindingsOf flattens binding sets (structs of binding fields) and single
// bindings into one list.
func bindingsOf(items ...any) []binding {
	var out []binding
	for _, it := range items {
		if b, ok := it.(binding); ok {
			out = append(out, b)
			continue
		}
		v := reflect.ValueOf(it)
		for i := range v.NumField() {
			if b, ok := v.Field(i).Interface().(binding); ok {
				out = append(out, b)
			}
		}
	}
	return out
}

// defaultKeymap serves models built without withKeymaps, such as test models.
var defaultKeymap, _ = buildKeymap(nil, 0, "")

func (m model) keymap() *keymap {
	if m.keys != nil {
		return m.keys
	}
	return defaultKeymap
}

// --- footers ------------------------------------------------------------------

// footer renders a one-line help view from the given bindings (empty-help ones are
// skipped). Built per call so it needs no model state and works in tests with a
// model literal.
func (m model) footer(bindings ...binding) string {
	w := m.width - 2*screenMargin
	if m.width <= 0 {
		w = 200 // no viewport yet (e.g. tests): don't truncate
	}
	return m.hints(w, bindings...)
}

func (m model) hints(w int, bindings ...binding) string {
	h := help.New()
	h.Styles = help.DefaultStyles(m.hasDark)
	h.Styles.ShortKey = StyleSecondary
	h.Styles.ShortDesc = StyleDim
	h.Styles.ShortSeparator = StyleDim
	h.ShortSeparator = " · "
	h.SetWidth(w)
	kb := make([]key.Binding, len(bindings))
	for i, b := range bindings {
		kb[i] = m.helpBinding(b)
	}
	return h.ShortHelpView(kb)
}

func helpAs(b binding, desc string) binding {
	b.SetHelp("", desc)
	return b
}

// hint is a footer entry for a key that its handler reads as typed, so
// helpBinding never relabels it. Its key list is empty but not nil because help
// skips a binding with nil keys.
func hint(label, desc string) binding {
	return binding{Binding: key.NewBinding(key.WithKeys([]string{}...), key.WithHelp(label, desc)), defaults: "", defKey: ""}
}
