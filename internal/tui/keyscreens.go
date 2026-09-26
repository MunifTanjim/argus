package tui

import (
	"fmt"
	"slices"
	"sync"
)

var (
	sidebarKeys = []any{projectsKeys.ToggleSidebar, projectsKeys.ToggleFiles}
	framedKeys  = append(slices.Clone(sidebarKeys), projectsKeys.Help)
)

// filesSidebarKeys lists the bindings the files sidebar path can match
// (handleFilesKey → changesKey / commitFilesKey / fileTree.key, and handleFileViewKey).
var filesSidebarKeys = []any{
	fileViewKeys,
	projectsKeys.Up, projectsKeys.Down, projectsKeys.Top, projectsKeys.Bottom,
	projectsKeys.HalfUp, projectsKeys.HalfDown, projectsKeys.Back,
	projectsKeys.Left, projectsKeys.Right, projectsKeys.Enter,
	projectsKeys.Narrow, projectsKeys.Widen,
	projectsKeys.SideTabPrev, projectsKeys.SideTabNext,
	projectsKeys.DiffMode, projectsKeys.Refresh,
}

// transcriptViewKeys are the transcript keys that both live and history
// transcripts read.
var transcriptViewKeys = []any{
	transcriptKeys.ScrollUp, transcriptKeys.ScrollDown, transcriptKeys.CardNext, transcriptKeys.CardPrev,
	transcriptKeys.HalfUp, transcriptKeys.HalfDown,
	transcriptKeys.Top, transcriptKeys.Bottom, transcriptKeys.Collapse, transcriptKeys.Expand,
	transcriptKeys.Detail, transcriptKeys.Back,
}

// redactKeys are read only in a history transcript and its detail view.
var redactKeys = []any{transcriptKeys.Redact, transcriptKeys.RedactList, transcriptKeys.RedactSave, redactListKeys}

// screenBindings lists the bindings each keymap screen can match. In tests,
// strictScreens makes m.matches record a binding its screen does not list, and
// TestMain fails on a listed command that no m.matches call checks there.
var screenBindings = map[string][]binding{
	"home":     bindingsOf(append([]any{listKeys, paneKeys}, framedKeys...)...),
	"projects": bindingsOf(projectsKeys, paneKeys, fileViewKeys, createKeys, listKeys.Jump, listKeys.Kill, listKeys.Quit),
	"session":  bindingsOf(slices.Concat([]any{sessionKeys, paneKeys, promptKeys}, transcriptViewKeys, filesSidebarKeys, sidebarKeys)...),
	"transcript": bindingsOf(slices.Concat([]any{paneKeys}, transcriptViewKeys, redactKeys, sidebarKeys, []any{
		transcriptKeys.Export, transcriptKeys.Resume,
	})...),
	"detail": bindingsOf(slices.Concat([]any{detailKeys, sessionKeys, paneKeys, promptKeys}, redactKeys, filesSidebarKeys, sidebarKeys)...),
	"history": bindingsOf(append([]any{
		historyProjectsKeys, historySessionsKeys, listKeys.TabPrev, listKeys.TabNext, paneKeys,
		transcriptKeys.Export, // reachable from handleHistorySessionsKey
	}, framedKeys...)...),
	"logs": bindingsOf(append([]any{logsKeys, listKeys.TabPrev, listKeys.TabNext, paneKeys}, framedKeys...)...),
}

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
	switch m.mode {
	case modeList:
		return "home"
	case modeProjects:
		return "projects"
	case modeSession, modeHistoryTranscript:
		if m.historyView == histDetail {
			return "detail"
		}
		if m.mode == modeSession {
			return "session"
		}
		return "transcript"
	case modeHistoryProjects, modeHistorySessions:
		return "history"
	case modeLogs:
		return "logs"
	}
	return ""
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
