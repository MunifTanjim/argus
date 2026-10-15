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
	defaults string // built-in keys
	quiet    string // built-in keys that work but that the help does not list
	maps     string // keys that the default keymap maps to :name<CR>
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

// nm is a binding with no built-in keys, whose keys come from the default
// keymap.
func nm(name, maps, desc string) binding {
	b := nb(name, "", desc)
	b.maps = maps
	return b
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
	Up, Down, Top, Bottom, HalfUp, HalfDown, PageUp, PageDown            binding
	Open, Jump, TabPrev, TabNext, New, Kill, Rename, Refresh, Back, Quit binding
	ActiveOnly, WorkspaceOnly, Filter, GroupBy                           binding
}{
	Up:       nb("prev", "<Up> k", "move"),
	Down:     nb("next", "<Down> j", ""),
	Top:      nb("goto top", "gg", ""),
	Bottom:   nb("goto bottom", "G", "ends"),
	HalfUp:   nb("scroll half-page-up", "<C-u>", ""),
	HalfDown: nb("scroll half-page-down", "<C-d>", ""),
	PageUp:   nb("scroll page-up", "<C-b> <PageUp>", ""),
	PageDown: nb("scroll page-down", "<C-f> <PageDown>", ""),
	Open:     nb("open", "<CR>", "open"),
	Jump:     nm("open tmux-pane", "O", "jump"),
	TabPrev:  nb("tab prev", "gT", ""),
	TabNext:  nb("tab next", "gt", "tabs"),
	New:      nm("session spawn", "s", "spawn"),
	Kill:     nm("session kill", "dd", "kill"),
	Rename:   nm("session rename", "r", "rename"),
	Refresh:  nm("refresh", "gr", "refresh"),
	Back:     nb("back", "<Esc>", "tree"),
	Quit:     nm("quit", "Q", "quit"),

	ActiveOnly:    nm("toggle active-only", "za", "active"),
	WorkspaceOnly: nm("toggle workspace-only", "zw", "workspace"),
	Filter:        nb("filter-sessions", "/", "filter"),
	GroupBy:       nm("cycle group-by", "zg", "group"),
}

var spawnKeys = struct {
	Filter binding
}{
	Filter: nb("filter-directories", "/", "filter"),
}

var terminalKeys = struct {
	Spawn, Rename, Kill binding
}{
	Spawn:  nm("terminal spawn", "a", "spawn"),
	Rename: nm("terminal rename", "r", "rename"),
	Kill:   nm("terminal kill", "dd", "kill"),
}

var nodeKeys = struct {
	Wakelock binding
}{
	Wakelock: nm("node wakelock", "w", "wakelock"),
}

var projectsKeys = struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown, PageUp, PageDown, Left, Right, Enter   binding
	Widen, Narrow, ToggleSidebar, Filter, Help                                      binding
	New, Rename, ToggleHidden, TogglePin, Remove, ForceRemove, ShowHidden, ShowGone binding
	Target, DiffMode, Spawn, ToggleFiles                                            binding
	SideTabPrev, SideTabNext, Refresh, Back                                         binding
	Forget                                                                          binding
	RunSetup, SetupLog, Palette, ToggleMouse, ToggleVerbose, Hover                  binding
}{
	Up:            nb("prev", "<Up> k", "move"),
	Down:          nb("next", "<Down> j", ""),
	Top:           nb("goto top", "gg", ""),
	Bottom:        nb("goto bottom", "G", "ends"),
	HalfUp:        nb("scroll half-page-up", "<C-u>", ""),
	HalfDown:      nb("scroll half-page-down", "<C-d>", ""),
	PageUp:        nb("scroll page-up", "<C-b> <PageUp>", ""),
	PageDown:      nb("scroll page-down", "<C-f> <PageDown>", ""),
	Left:          nb("fold close", "h <Left> zc", "fold"),
	Right:         nb("fold open", "l <Right> zo", ""),
	Enter:         nb("open", "<CR>", "open"),
	Widen:         nb("sidebar wider", "<C-w>>", "resize"),
	Narrow:        nb("sidebar narrower", "<C-w><lt>", ""),
	ToggleSidebar: nm("toggle left-sidebar", "<Leader>o", "sidebar"),
	Filter:        nb("filter-projects", "/", "filter"),
	Help:          nm("help", "g?", "help"),
	New:           nm("workspace new", "a", "new"),
	Rename:        nm("project rename", "r", "rename"),
	ToggleHidden:  nm("toggle hidden", "H", "hide"),
	TogglePin:     nm("toggle pin", "P", "pin"),
	Remove:        nm("workspace remove", "dd", "remove"),
	ForceRemove:   nm("workspace force-remove", "D", ""),
	ShowHidden:    nm("toggle show-hidden", "z.", "hidden"),
	ShowGone:      nm("toggle show-gone", "zx", "gone"),
	Target:        nm("workspace change-target", "T", "target"),
	DiffMode:      nm("toggle diff-vs-target", "t", "vs target"),
	Spawn:         nm("session spawn", "s", "spawn"),
	ToggleFiles:   nm("toggle right-sidebar", "<Leader>e", "files"),
	SideTabPrev:   nb("tab prev", "gT", ""),
	SideTabNext:   nb("tab next", "gt", "tabs"),
	Refresh:       nm("refresh", "gr", "refresh"),
	Forget:        nm("project forget", "F", "forget"),
	Back:          nb("back", "<Esc>", "back"),
	RunSetup:      nm("workspace rerun-setup", "S", "setup again"),
	SetupLog:      nm("open setup-log", "L", "setup log"),
	Palette:       nm("open palette", "<C-k>", "palette"),
	ToggleMouse:   nb("toggle mouse", "", "mouse"),
	ToggleVerbose: nb("toggle verbose-transcript", "", "verbose transcript"),
	Hover:         nm("hover", "K", "hover"),
}

var createKeys = struct {
	Target binding
}{
	Target: nb("workspace pick-target", "<C-t>", "target"),
}

var transcriptKeys = struct {
	ScrollUp, ScrollDown, PromptNext, PromptPrev, HalfUp, HalfDown, PageUp, PageDown binding
	Top, Bottom, Collapse, Expand, Detail, Copy                                      binding
	Answer, Export, Back, Resume                                                     binding
	Redact, RedactSave, RedactList                                                   binding
	Visual, VisualLine                                                               binding
}{
	ScrollUp:   nb("scroll up", "<Up> k", "scroll"),
	ScrollDown: nb("scroll down", "<Down> j", ""),
	PromptNext: nb("next prompt", "}", "prompt"),
	PromptPrev: nb("prev prompt", "{", ""),
	HalfUp:     nb("scroll half-page-up", "<C-u>", ""),
	HalfDown:   nb("scroll half-page-down", "<C-d>", ""),
	PageUp:     nb("scroll page-up", "<C-b> <PageUp>", ""),
	PageDown:   nb("scroll page-down", "<C-f> <PageDown>", ""),
	Top:        nb("goto top", "gg", ""),
	Bottom:     nb("goto bottom", "G", "ends"),
	Collapse:   nb("fold close", "h <Left> zc", "fold"),
	Expand:     nb("fold open", "l <Right> zo", ""),
	Detail:     nb("open", "<CR>", "detail"),
	Copy:       nb("copy", "y", "copy"),
	Answer:     nm("focus prompt", "<Tab>", "answer"),
	Export:     nm("transcript export", "E", "export"),
	Back:       nb("back", "<Esc>", "back"),
	Resume:     nm("session resume", "R", "resume"),
	Redact:     nm("redaction add", "d", "redact"),
	RedactList: nm("redaction list", "D", "redactions"),
	RedactSave: nm("redaction save", "W", "save redacted"),
	Visual:     nb("visual char", "v", "visual"),
	VisualLine: nb("visual line", "V", ""),
}

var detailKeys = struct {
	Up, Down, HalfUp, HalfDown, PageUp, PageDown, Top, Bottom, Collapse, Expand, Drill, Back, Copy binding
	Visual, VisualLine                                                                             binding
}{
	Up:         nb("prev", "<Up> k", "move"),
	Down:       nb("next", "<Down> j", ""),
	HalfUp:     nb("scroll half-page-up", "<C-u>", ""),
	HalfDown:   nb("scroll half-page-down", "<C-d>", ""),
	PageUp:     nb("scroll page-up", "<C-b> <PageUp>", ""),
	PageDown:   nb("scroll page-down", "<C-f> <PageDown>", ""),
	Top:        nb("goto top", "gg", ""),
	Bottom:     nb("goto bottom", "G", ""),
	Collapse:   nb("fold close", "h <Left> zc", "fold"),
	Expand:     nb("fold open", "l <Right> zo", ""),
	Drill:      nb("open", "<CR>", "drill"),
	Back:       nb("back", "<Esc>", "back"),
	Copy:       nb("copy", "y", "copy"),
	Visual:     nb("visual char", "v", "visual"),
	VisualLine: nb("visual line", "V", ""),
}

var visualKeys = struct {
	Left, Right, Up, Down, WordNext, WordPrev, WordEnd, LineStart, FirstNonBlank, LineEnd binding
	Top, Bottom, HalfUp, HalfDown, PageUp, PageDown, Swap, Char, Line, Copy, Back         binding
}{
	Left:          nb("cursor left", "h <Left>", "move"),
	Right:         nb("cursor right", "l <Right>", ""),
	Up:            nb("cursor up", "k <Up>", ""),
	Down:          nb("cursor down", "j <Down>", ""),
	WordNext:      nb("cursor word-next", "w", ""),
	WordPrev:      nb("cursor word-prev", "b", ""),
	WordEnd:       nb("cursor word-end", "e", ""),
	LineStart:     nb("cursor line-start", "0", ""),
	FirstNonBlank: nb("cursor first-non-blank", "^", ""),
	LineEnd:       nb("cursor line-end", "$", ""),
	Top:           nb("goto top", "gg", ""),
	Bottom:        nb("goto bottom", "G", ""),
	HalfUp:        nb("scroll half-page-up", "<C-u>", ""),
	HalfDown:      nb("scroll half-page-down", "<C-d>", ""),
	PageUp:        nb("scroll page-up", "<C-b> <PageUp>", ""),
	PageDown:      nb("scroll page-down", "<C-f> <PageDown>", ""),
	Swap:          nb("visual swap", "o", "other end"),
	Char:          nb("visual char", "v", ""),
	Line:          nb("visual line", "V", ""),
	Copy:          nb("copy", "y", "copy"),
	Back:          nb("back", "<Esc>", "cancel"),
}

// sessionKeys are the keys a live transcript and the session dock take before
// their own: the prompt focus moves and the live screen.
var sessionKeys = struct {
	FocusPrompt, FocusTranscript, Raw binding
}{
	FocusPrompt:     nm("focus prompt", "<Tab>", "prompt"),
	FocusTranscript: nm("focus transcript", "<Tab>", "read"),
	Raw:             nm("open live-screen", "<C-t>", "raw"),
}

// paneKeys move focus between the tree, the main pane, the files sidebar, and
// the dock.
var paneKeys = struct {
	Left, Down, Up, Right, Next, Prev binding
}{
	Left:  nb("focus left", "<C-w>h", "tree").alt("<C-w><C-h> <C-w><Left>"),
	Down:  nb("focus down", "<C-w>j", "").alt("<C-w><C-j> <C-w><Down>"),
	Up:    nb("focus up", "<C-w>k", "").alt("<C-w><C-k> <C-w><Up>"),
	Right: nb("focus right", "<C-w>l", "files").alt("<C-w><C-l> <C-w><Right>"),
	Next:  nb("focus next", "<C-w>w", "pane").alt("<C-w><C-w>"),
	Prev:  nb("focus prev", "<C-w>W", ""),
}

// Prompt bindings (dock): drive dock footers; the prompt sub-views are modal text editors.
var promptKeys = struct {
	Up, Down, HalfUp, HalfDown, PageUp, PageDown, TabPrev, TabNext, Submit, Next, Select, Read binding
	Back                                                                                       binding
	Note                                                                                       binding
}{
	Up:       nb("prev", "<Up>", "select"),
	Down:     nb("next", "<Down>", ""),
	HalfUp:   nb("scroll half-page-up", "<C-u>", "scroll"),
	HalfDown: nb("scroll half-page-down", "<C-d>", ""),
	PageUp:   nb("scroll page-up", "<PageUp>", ""),
	PageDown: nb("scroll page-down", "<PageDown>", ""),
	TabPrev:  nb("tab prev", "<Left>", "tabs"),
	TabNext:  nb("tab next", "<Right>", ""),
	Submit:   nb("answer submit", "<CR>", "submit"),
	Next:     nb("answer submit", "<CR>", "next"), // footer label for multi-question advance
	Select:   nb("toggle selection", "<Space>", "toggle"),
	Read:     nb("focus transcript", "<Tab>", "read"),
	Back:     nb("back", "<Esc>", "back"),
	Note:     nb("answer note", "n", "note"),
}

var historyProjectsKeys = struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown, PageUp, PageDown, Open, Refresh, Back binding
}{
	Up:       nb("prev", "<Up> k", "move"),
	Down:     nb("next", "<Down> j", ""),
	Top:      nb("goto top", "gg", ""),
	Bottom:   nb("goto bottom", "G", "ends"),
	HalfUp:   nb("scroll half-page-up", "<C-u>", ""),
	HalfDown: nb("scroll half-page-down", "<C-d>", ""),
	PageUp:   nb("scroll page-up", "<C-b> <PageUp>", ""),
	PageDown: nb("scroll page-down", "<C-f> <PageDown>", ""),
	Open:     nb("open", "<CR>", "open"),
	Refresh:  nm("refresh", "gr", "refresh"),
	Back:     nb("back", "<Esc>", "back"),
}

var historySessionsKeys = struct {
	Up, Down, Top, Bottom, HalfUp, HalfDown, PageUp, PageDown, Open, More, Back, Resume binding
}{
	Up:       nb("prev", "<Up> k", "move"),
	Down:     nb("next", "<Down> j", ""),
	Top:      nb("goto top", "gg", ""),
	Bottom:   nb("goto bottom", "G", "ends"),
	HalfUp:   nb("scroll half-page-up", "<C-u>", ""),
	HalfDown: nb("scroll half-page-down", "<C-d>", ""),
	PageUp:   nb("scroll page-up", "<C-b> <PageUp>", ""),
	PageDown: nb("scroll page-down", "<C-f> <PageDown>", ""),
	Open:     nb("open", "<CR>", "open"),
	More:     nm("session load-more", "m", "more"),
	Back:     nb("back", "<Esc>", "back"),
	Resume:   nm("session resume", "R", "resume"),
}

var logsKeys = struct {
	Up, Down, HalfUp, HalfDown, PageUp, PageDown, Top, Bottom, Back binding
}{
	Up:       nb("scroll up", "<Up> k", "scroll"),
	Down:     nb("scroll down", "<Down> j", ""),
	HalfUp:   nb("scroll half-page-up", "<C-u>", ""),
	HalfDown: nb("scroll half-page-down", "<C-d>", ""),
	PageUp:   nb("scroll page-up", "<C-b> <PageUp>", ""),
	PageDown: nb("scroll page-down", "<C-f> <PageDown>", ""),
	Top:      nb("goto top", "gg", ""),
	Bottom:   nb("goto bottom", "G", "ends"),
	Back:     nb("back", "<Esc>", "back"),
}

// fileViewKeys drive the file, diff, or setup log open in the projects pane.
var fileViewKeys = struct {
	Up, Down, HalfUp, HalfDown, PageUp, PageDown, Top, Bottom, Wrap, NextFile, PrevFile, Refresh, Back binding
}{
	Up:       nb("scroll up", "<Up> k", "scroll"),
	Down:     nb("scroll down", "<Down> j", ""),
	HalfUp:   nb("scroll half-page-up", "<C-u>", ""),
	HalfDown: nb("scroll half-page-down", "<C-d>", ""),
	PageUp:   nb("scroll page-up", "<C-b> <PageUp>", ""),
	PageDown: nb("scroll page-down", "<C-f> <PageDown>", ""),
	Top:      nb("goto top", "gg", ""),
	Bottom:   nb("goto bottom", "G", "ends"),
	Wrap:     nm("toggle line-wrap", "yow", "wrap"),
	NextFile: nm("next diff-file", "]f", "file"),
	PrevFile: nm("prev diff-file", "[f", ""),
	Refresh:  nm("refresh", "gr", "refresh"),
	Back:     nb("back", "<Esc>", "back"),
}

var redactListKeys = struct {
	Up, Down, Remove binding
}{
	Up:     nb("prev", "<Up> k", ""),
	Down:   nb("next", "<Down> j", "move"),
	Remove: nm("redaction remove", "u", "remove"),
}

// screenLeave is the only app binding in live-screen passthrough; every other key
// is forwarded to the pane.
var screenLeave = nb("", "<C-]>", "leave")

// textInputSets are the binding sets that only text inputs read, where the
// resolver builds no sequences.
func textInputSets() []any { return []any{createKeys, promptKeys} }

var upgradeKeys = struct {
	Node, Gateway binding
}{
	Node:    nb("upgrade node", "", ""),
	Gateway: nb("upgrade gateway", "", ""),
}

func keySets() []any {
	return []any{listKeys, projectsKeys, fileViewKeys, transcriptKeys, redactListKeys,
		detailKeys, visualKeys, sessionKeys, paneKeys, historyProjectsKeys, historySessionsKeys, logsKeys, terminalKeys, nodeKeys, upgradeKeys, spawnKeys}
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
	sk := m.keymap().screenKeys(m.screen())
	shown := map[string]bool{}
	for _, b := range bindings {
		if d, ok := sk.descFor(b.name); ok && showsDesc(b) {
			shown[d.id] = true
		}
	}
	for _, d := range sk.descs {
		if !shown[d.id] && slices.ContainsFunc(d.cmds, func(c string) bool { return m.applies(binding{name: c}) }) {
			bindings = append(bindings, hint(keyLabel(d.id), d.desc))
		}
	}
	return m.plainFooter(bindings...)
}

// plainFooter is footer without the user's mappings, for screens where keys
// do not reach the focused component, such as the help.
func (m model) plainFooter(bindings ...binding) string {
	w := m.width - 2*screenMargin
	if m.width <= 0 {
		w = 200 // no viewport yet (e.g. tests): don't truncate
	}
	return m.hints(w, bindings...)
}

// showsDesc reports whether b's footer entry shows a desc mapping of its
// command in place of its own label: a shown entry for b alone, not one it
// shares with a label partner.
func showsDesc(b binding) bool {
	_, paired := labelPairs[b.name]
	return b.Enabled() && b.Help().Desc != "" && !paired
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
