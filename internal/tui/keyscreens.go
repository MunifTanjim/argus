package tui

import (
	"fmt"
	"slices"
	"sync"
)

// focusMoves are the focus manager's pane moves and sidebar toggles, which
// apply in every section whose component takes resolved keys.
var focusMoves = bindingsOf(paneKeys, projectsKeys.ToggleSidebar, projectsKeys.ToggleFiles)

// offer is the focus manager's help and quit that a section's component
// offers, and the condition under which it offers them ("" for always). Each
// component's offers method answers from here.
type offer struct {
	keys  []binding
	where string
}

var sectionOffers = func() map[string]offer {
	help := []binding{projectsKeys.Help}
	helpQuit := []binding{projectsKeys.Help, listKeys.Quit}
	// A file and the right sidebar's tabs offer what the main pane's component
	// offers: the workspace pane's help and quit, and nothing over a transcript.
	withPane := offer{keys: helpQuit, where: "over the workspace pane"}
	return map[string]offer{
		"project-tree": {keys: helpQuit},
		"workspace":    {keys: helpQuit},
		"home":         {keys: helpQuit},
		"logs":         {keys: help},
		"history":      {keys: help, where: "project list"},
		"file":         withPane,
		"file-tree":    withPane,
		"changes":      withPane,
	}
}()

func focusFor(s string) []binding { return slices.Concat(focusMoves, sectionOffers[s].keys) }

var (
	leftSidebarKeys  = []any{projectsKeys.Widen, projectsKeys.Narrow}
	rightSidebarKeys = []any{projectsKeys.Widen, projectsKeys.Narrow, projectsKeys.SideTabPrev, projectsKeys.SideTabNext}
)

// treeKeys act on the tree from the tree and from a workspace pane, which a
// file open over it passes them to. From the pane, the tree's manage keys only
// say where they work.
var (
	treeKeys = []any{
		projectsKeys.Filter, projectsKeys.Spawn, projectsKeys.SetupLog, projectsKeys.ShowHidden, projectsKeys.ShowGone,
	}
	manageKeys = []any{
		projectsKeys.New, projectsKeys.Rename, projectsKeys.Hide, projectsKeys.Unhide, projectsKeys.Pin,
		projectsKeys.Unpin, projectsKeys.Target, projectsKeys.ForceRemove, projectsKeys.Forget, projectsKeys.RunSetup,
	}
)

// summaryKeys are the workspace section's keys that a project or node summary
// takes: the ones that act on the tree.
var summaryKeys = bindingsOf(slices.Concat(treeKeys, manageKeys, leftSidebarKeys,
	[]any{projectsKeys.Refresh, projectsKeys.Back})...)

// transcriptViewKeys are the transcript keys that both live and history
// transcripts read.
var transcriptViewKeys = []any{
	transcriptKeys.ScrollUp, transcriptKeys.ScrollDown, transcriptKeys.CardNext, transcriptKeys.CardPrev,
	transcriptKeys.HalfUp, transcriptKeys.HalfDown,
	transcriptKeys.Top, transcriptKeys.Bottom, transcriptKeys.Collapse, transcriptKeys.Expand,
	transcriptKeys.Detail, transcriptKeys.Back,
}

var sidebarListKeys = []any{
	projectsKeys.Back, projectsKeys.Refresh, projectsKeys.Up, projectsKeys.Down, projectsKeys.Top,
	projectsKeys.Bottom, projectsKeys.HalfUp, projectsKeys.HalfDown, projectsKeys.Left,
	projectsKeys.Right, projectsKeys.Enter,
}

// sectionList is what a keymap section lists: the bindings its component
// matches, the ones its container runs, and the focus manager's that apply.
type sectionList struct {
	own       []binding
	container []binding
	focus     []binding
}

// sectionLists names each keymap section after its component. The create
// picker reads the tree's section; the live screen, the retarget picker, and
// the spawn flow have none.
var sectionLists = map[string]sectionList{
	"project-tree": {
		own: bindingsOf(slices.Concat(treeKeys, manageKeys, []any{projectsKeys.Back, projectsKeys.Up, projectsKeys.Down,
			projectsKeys.Top, projectsKeys.Bottom, projectsKeys.HalfUp, projectsKeys.HalfDown, projectsKeys.Left,
			projectsKeys.Right, projectsKeys.Enter, projectsKeys.Remove, projectsKeys.Refresh, createKeys.Target})...),
		container: bindingsOf(leftSidebarKeys...),
		focus:     focusFor("project-tree"),
	},
	"workspace": {
		own: bindingsOf(slices.Concat(treeKeys, manageKeys, leftSidebarKeys, []any{projectsKeys.Up, projectsKeys.Down,
			projectsKeys.Top, projectsKeys.Bottom, projectsKeys.HalfUp, projectsKeys.HalfDown, projectsKeys.Enter,
			projectsKeys.Back, projectsKeys.Refresh, listKeys.Jump, listKeys.Kill, listKeys.ActiveOnly,
			listKeys.Filter})...),
		focus: focusFor("workspace"),
	},
	"file": {
		own:   bindingsOf(slices.Concat(treeKeys, manageKeys, leftSidebarKeys, []any{fileViewKeys, sessionKeys.FocusPrompt})...),
		focus: focusFor("file"),
	},
	"file-tree": {
		own:       bindingsOf(sidebarListKeys...),
		container: bindingsOf(rightSidebarKeys...),
		focus:     focusFor("file-tree"),
	},
	"changes": {
		own:       bindingsOf(append([]any{projectsKeys.DiffMode}, sidebarListKeys...)...),
		container: bindingsOf(rightSidebarKeys...),
		focus:     focusFor("changes"),
	},
	"home": {
		own: bindingsOf(listKeys.Up, listKeys.Down, listKeys.Top, listKeys.Bottom, listKeys.HalfUp, listKeys.HalfDown,
			listKeys.Open, listKeys.Jump, listKeys.TabPrev, listKeys.TabNext, listKeys.New, listKeys.Kill,
			listKeys.Refresh, listKeys.Back, listKeys.ActiveOnly, listKeys.Filter),
		focus: focusFor("home"),
	},
	"history": {
		own:   bindingsOf(historyProjectsKeys, historySessionsKeys, listKeys.TabPrev, listKeys.TabNext, transcriptKeys.Export),
		focus: focusFor("history"),
	},
	"logs": {
		own:   bindingsOf(logsKeys, listKeys.TabPrev, listKeys.TabNext),
		focus: focusFor("logs"),
	},
	"transcript": {
		own: bindingsOf(slices.Concat(transcriptViewKeys, []any{detailKeys, sessionKeys.FocusPrompt, sessionKeys.Raw,
			redactListKeys, transcriptKeys.Redact, transcriptKeys.RedactList, transcriptKeys.RedactSave,
			transcriptKeys.Export, transcriptKeys.Resume})...),
		focus: focusFor("transcript"),
	},
	"session-dock": {
		own: bindingsOf(promptKeys, sessionKeys.FocusTranscript, sessionKeys.Raw),
	},
}

// oldSections are the section names that component sections replaced, with
// the sections that took their commands.
var oldSections = map[string]string{
	"projects": "project-tree, workspace, file, file-tree, changes",
	"session":  "transcript, session-dock",
	"detail":   "transcript",
}

// In tests, strictScreens makes m.matches record a binding its section does
// not list, and TestMain fails on a listed command that no m.matches call
// checks there.
var screenBindings = func() map[string][]binding {
	out := map[string][]binding{}
	for s, l := range sectionLists {
		out[s] = slices.Concat(l.own, l.container, l.focus)
	}
	return out
}()

var screenNames = func() map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for s, bs := range screenBindings {
		out[s] = map[string]bool{}
		for _, b := range bs {
			out[s][b.name] = true
		}
	}
	return out
}()

func screenHas(screen, name string) bool { return screenNames[screen][name] }

func (m model) screen() string {
	if p := m.popups.front(); p != nil {
		return p.keySection()
	}
	return m.focusedComp().section()
}

var (
	strictScreens bool
	strictMu      sync.Mutex
	unlisted      = map[string]bool{}
	matchedNames  = map[string]map[string]bool{}
)

func checkListed(screen string, b binding) {
	if screen == "" || b.name == "" {
		return
	}
	strictMu.Lock()
	defer strictMu.Unlock()
	if matchedNames[screen] == nil {
		matchedNames[screen] = map[string]bool{}
	}
	matchedNames[screen][b.name] = true
	if !screenHas(screen, b.name) {
		unlisted[fmt.Sprintf("%s: %s %q", screen, b.name, b.defaults)] = true
	}
}
