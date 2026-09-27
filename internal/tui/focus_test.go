package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestFocusTransitions(t *testing.T) {
	permission := &session.Interaction{Kind: session.InteractionPermission}
	idle := &session.Interaction{Kind: session.InteractionIdle}
	keys := func(ks ...tea.KeyPressMsg) []tea.Msg {
		out := make([]tea.Msg, len(ks))
		for i, k := range ks {
			out[i] = k
		}
		return out
	}
	text := func(s string) []tea.Msg {
		var out []tea.Msg
		for _, r := range s {
			out = append(out, keyMsg(string(r)))
		}
		return out
	}
	dockGone := func(m model) model {
		s := m.sessions["n1:s1"]
		s.Interaction = nil
		m.sessions["n1:s1"] = s
		return m
	}
	cases := []struct {
		name  string
		start func() model
		msgs  []tea.Msg
		want  container
		view  shownView
	}{
		{"focusPane from the tree", wideWorkspace, keys(keyMsg("enter")), mainPane, viewTree},
		{"cycleFocus to the pane", wideWorkspace, keys(cw('w')...), mainPane, viewTree},
		{"cycleFocus to the files", wideWorkspace, keys(append(cw('w'), cw('w')...)...), rightSidebar, viewTree},
		{"cycleFocus back to the tree", wideWorkspace, keys(append(cw('w'), append(cw('w'), cw('w')...)...)...), leftSidebar, viewTree},
		{"cycleFocus backward to the files", wideWorkspace, keys(cw('W')...), rightSidebar, viewTree},
		{"pane left to the tree", wideWorkspace, keys(append(cw('l'), cw('h')...)...), leftSidebar, viewTree},
		{"option A from Home", homeTestModel, keys(cw('h')...), leftSidebar, viewTree},
		{"option A from a session", func() model { return workspaceSession(nil) }, keys(cw('h')...), leftSidebar, viewTree},
		{"enterHome from the Home row", func() model {
			m := wideWorkspace()
			m.left.tree.cursor = 0
			return m
		}, keys(keyMsg("enter")), mainPane, viewHome},
		{"hidden tree repair on a workspace", wideWorkspace, text(" o"), mainPane, viewTree},
		{"session tab to the dock", func() model { return workspaceSession(permission) }, keys(keyMsg("tab")), sessionDock, viewSession},
		{"session tab back to the transcript", func() model { return workspaceSession(permission) }, keys(keyMsg("tab"), keyMsg("tab")), mainPane, viewSession},
		{"session esc back to the transcript", func() model { return workspaceSession(permission) }, keys(keyMsg("tab"), keyMsg("esc")), mainPane, viewSession},
		{"session esc leaves an idle reply", func() model { return workspaceSession(idle) }, keys(keyMsg("tab"), keyMsg("esc")), mainPane, viewSession},
		{"session pane down to the dock", func() model { return workspaceSession(permission) }, keys(cw('j')...), sessionDock, viewSession},
		{"session cycle to the dock", func() model { return workspaceSession(permission) }, keys(cw('W')...), sessionDock, viewSession},
		{"session files", func() model { return workspaceSession(nil) }, keys(cw('l')...), rightSidebar, viewSession},
		{"session files back", func() model { return workspaceSession(nil) }, keys(append(cw('l'), cw('h')...)...), mainPane, viewSession},
		{"dock vanishing", func() model {
			return dockGone(pressKeys(workspaceSession(permission), keyMsg("tab")))
		}, keys(keyMsg("j")), mainPane, viewSession},
		{"open a file", filesFocused, keys(keyMsg("enter")), mainPane, viewTree},
		{"closeFileView", filesFocused, keys(keyMsg("enter"), keyMsg("esc")), rightSidebar, viewTree},
		{"enterSession resets files focus", func() model {
			return pressKeys(workspaceSession(nil), cw('l')...)
		}, []tea.Msg{resumeResultMsg{sessionID: "n1:s1"}}, mainPane, viewSession},
		{"enterSession takes focus off the tree", wideWorkspace, []tea.Msg{resumeResultMsg{sessionID: "n1:s1"}}, mainPane, viewSession},
		{"right sidebar repair to the pane", filesFocused, text(" e"), mainPane, viewTree},
		{"right sidebar repair to the tree", filesFocused, []tea.Msg{projectsTreeMsg{tree: []api.ProjectNode{}}}, leftSidebar, viewTree},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := c.start()
			for _, msg := range c.msgs {
				m, _ = upd(m, msg)
			}
			if m.focused != c.want || viewOf(m) != c.view {
				t.Errorf("focused = %v view = %v, want %v view %v", m.focused, viewOf(m), c.want, c.view)
			}
		})
	}
}

type sectionState struct {
	name    string
	section string
	build   func() model
}

// sectionStates builds one state per keymap section, the way a user reaches it.
func sectionStates() []sectionState {
	overSession := func(m model) model {
		m.width = 160
		m.right.hidden = false
		m, _ = m.syncSidebar()
		return m
	}
	return []sectionState{
		{"tree", "project-tree", wideWorkspace},
		{"workspace pane", "workspace", func() model { return withFocus(wideWorkspace(), mainPane) }},
		{"file over a workspace", "file", func() model { return withFocus(openedFile(wideWorkspace()), mainPane) }},
		{"file over a session", "file", func() model { return openedFile(overSession(waitingSession())) }},
		{"Files tab", "file-tree", filesFocused},
		{"Files tab over a session", "file-tree", func() model {
			return withFocus(withTreeLoaded(overSession(waitingSession()), "n1:w1"), rightSidebar)
		}},
		{"Changes tab", "changes", func() model { return changesFocused() }},
		{"Home", "home", homeTestModel},
		{"History", "history", func() model { return withHistoryProjects(homeTestModel(), historyProjects()...) }},
		{"History sessions", "history", func() model { return withHistorySessions(homeTestModel(), histProj, historyPage()) }},
		{"Logs", "logs", func() model { return logsModel(120, false) }},
		{"live transcript", "transcript", waitingSession},
		{"history transcript", "transcript", func() model { return historyTranscript(false) }},
		{"session dock", "session-dock", func() model { return withFocus(waitingSession(), sessionDock) }},
	}
}

// TestEveryDefaultBindingReachesOneHandler presses every default key of every
// binding in each component's section. The focus manager takes its own
// bindings where the component offers them and no others; the container takes
// only its own bindings; the component gets the rest.
func TestEveryDefaultBindingReachesOneHandler(t *testing.T) {
	states := sectionStates()
	names := func(bs []binding) map[string]bool {
		out := map[string]bool{}
		for _, b := range bs {
			out[b.name] = true
		}
		return out
	}
	for s, l := range sectionLists {
		own := names(l.own)
		for n := range names(l.container) {
			if own[n] {
				t.Errorf("%s lists %s for both the component and the container", s, n)
			}
		}
	}
	for _, st := range states {
		l := sectionLists[st.section]
		byFocusList, byContainerList := names(l.focus), names(l.container)
		for _, b := range screenBindings[st.section] {
			if textInputOnly(b.name) {
				continue
			}
			for _, tok := range strings.Fields(b.defaults) {
				seq, err := parseKeySeq(tok)
				if err != nil {
					t.Fatal(err)
				}
				seq = replaceLeader(seq, keyStep{"space"})
				msg := tea.KeyPressMsg{Code: tea.KeyExtended, Text: seq[0][0]}
				if len(seq) > 1 {
					msg.Text = seq.id()
				}
				m := st.build()
				if m.screen() != st.section {
					t.Fatalf("%s: section %q, want %q", st.name, m.screen(), st.section)
				}
				if len(l.focus) == 0 {
					if !m.keysRaw() {
						t.Errorf("%s: a section with no focus manager bindings must take its keys raw", st.name)
					}
					continue
				}
				_, _, byFocus := m.focusKey(msg)
				_, _, byContainer := m.containerKey(msg)
				offered := true
				if b.name == projectsKeys.Help.name || b.name == listKeys.Quit.name {
					offered = m.offered(b)
				}
				switch {
				case byFocus && byContainer:
					t.Errorf("%s: %s %s reaches the focus manager and the container", st.name, b.name, tok)
				case byFocusList[b.name] && offered && !byFocus:
					t.Errorf("%s: the focus manager does not take its %s %s", st.name, b.name, tok)
				case byFocusList[b.name] && !offered && byFocus:
					t.Errorf("%s: the focus manager takes %s %s where it is not offered", st.name, b.name, tok)
				case !byFocusList[b.name] && byFocus:
					t.Errorf("%s: the focus manager takes the component's %s %s", st.name, b.name, tok)
				case byContainerList[b.name] && !byContainer:
					t.Errorf("%s: the container does not take its %s %s", st.name, b.name, tok)
				case !byContainerList[b.name] && byContainer:
					t.Errorf("%s: the container takes the component's %s %s", st.name, b.name, tok)
				}
			}
		}
	}
}
