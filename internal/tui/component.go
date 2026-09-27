package tui

import tea "charm.land/bubbletea/v2"

type container int

const (
	mainPane container = iota
	leftSidebar
	rightSidebar
	sessionDock
)

// component is one thing a container shows. Components are values: a method
// that changes one returns the new value, and the container stores it.
type component interface {
	section() string
	raw(c *ctx) bool
	handleKey(c *ctx, msg tea.KeyPressMsg) (component, tea.Cmd, bool)
	update(c *ctx, msg tea.Msg) (component, tea.Cmd)
	view(c *ctx, w, h int) string
	footer(c *ctx) []binding
	spins(c *ctx) bool
	fullScreen(c *ctx) fullLevel
	close(c *ctx) tea.Cmd
	// offers is the focus manager's help and quit that the component takes now.
	offers(c *ctx) []binding
	layer() layer
	// pageStep is how many cards a half-page jump moves.
	pageStep(c *ctx) int
}

// layer is how a component stacks on the main pane.
type layer int

const (
	// baseLayer replaces what the main pane shows.
	baseLayer layer = iota
	// fileLayer is an open file: over the base, which stays in place.
	fileLayer
	// screenLayer is the live screen: a base of its own, over a file that stays
	// open.
	screenLayer
	// overLayer is a picker or the spawn flow: over the base and an open file,
	// which stay in place.
	overLayer
)

// over reports whether the layer leaves the base under it in place.
func (l layer) over() bool { return l == fileLayer || l == overLayer }

// coversFile reports whether the layer leaves an open file under it open.
func (l layer) coversFile() bool { return l == screenLayer || l == overLayer }

// fullLevel is how much of the screen a component asks the frame for.
type fullLevel int

const (
	notFull fullLevel = iota
	// fullWidth spans the frame: no left sidebar and no margin. The title, the
	// footer, and the right sidebar stay.
	fullWidth
	// fullTerminal takes the whole terminal; the component draws its own title.
	fullTerminal
)

// ctx is what a component sees of the model: state to read, and the changes
// it asks for. The model applies the changes after it stores the component's
// new value, so a change never overwrites that value.
type ctx struct {
	m       *model
	actions []action
}

type actionKind int

const (
	actOpen actionKind = iota
	actBack
	actReplaceBase
	actReplaceFile
	actCloseFile
	actCloseScreen
	actFocus
	actFlash
	actSyncPane
	actHome
	actOpenTree
	actOpenSession
	actStepDiff
	actTree
	actResizeTree
)

type action struct {
	kind  actionKind
	comp  component
	focus container
	flash string
	id    string
	step  int
	op    treeOp
}

func (c *ctx) open(comp component) { c.actions = append(c.actions, action{kind: actOpen, comp: comp}) }
func (c *ctx) back()               { c.actions = append(c.actions, action{kind: actBack}) }
func (c *ctx) focusOn(k container) { c.actions = append(c.actions, action{kind: actFocus, focus: k}) }
func (c *ctx) setFlash(s string)   { c.actions = append(c.actions, action{kind: actFlash, flash: s}) }

func (c *ctx) replaceBase(comp component) {
	c.actions = append(c.actions, action{kind: actReplaceBase, comp: comp})
}

// replaceFile replaces the open file, which a picker, the spawn flow, or the
// live screen can cover.
func (c *ctx) replaceFile(f fileComp) {
	c.actions = append(c.actions, action{kind: actReplaceFile, comp: f})
}

// closeScreen takes the live screen of attach termID off the main pane,
// wherever it is in the stack.
func (c *ctx) closeScreen(termID string) {
	c.actions = append(c.actions, action{kind: actCloseScreen, id: termID})
}

// closeFile takes the open file off the main pane, wherever it is under the
// top.
func (c *ctx) closeFile() { c.actions = append(c.actions, action{kind: actCloseFile}) }

// syncPane puts the pane of the tree's cursor row at the main pane's root.
func (c *ctx) syncPane() { c.actions = append(c.actions, action{kind: actSyncPane}) }

// home moves the tree cursor to the Home row and focus to the Home pane.
func (c *ctx) home() { c.actions = append(c.actions, action{kind: actHome}) }

// openTree leaves the main pane's component for the tree (option A of the
// directional pane moves).
func (c *ctx) openTree() { c.actions = append(c.actions, action{kind: actOpenTree}) }

func (c *ctx) openSession(id string) {
	c.actions = append(c.actions, action{kind: actOpenSession, id: id})
}

// stepDiff opens the diff d places from the open one in the Changes tab.
func (c *ctx) stepDiff(d int) { c.actions = append(c.actions, action{kind: actStepDiff, step: d}) }

func (c *ctx) onTree(op treeOp) { c.actions = append(c.actions, action{kind: actTree, op: op}) }

func (c *ctx) resizeTree(d int) {
	c.actions = append(c.actions, action{kind: actResizeTree, step: d})
}

// backStack is the main pane's components; the last one shows. The first one
// is the root and is never popped.
type backStack []component

func (s backStack) top() component {
	if len(s) == 0 {
		return nil
	}
	return s[len(s)-1]
}

func (s backStack) push(c component) backStack { return append(s[:len(s):len(s)], c) }

func (s backStack) pop() (backStack, component) {
	if len(s) <= 1 {
		return s, nil
	}
	return s[: len(s)-1 : len(s)-1], s[len(s)-1]
}

func (s backStack) replaceTop(c component) backStack {
	if len(s) == 0 {
		return backStack{c}
	}
	return s.replaceAt(len(s)-1, c)
}

func (s backStack) removeAt(i int) backStack { return append(s[:i:i], s[i+1:]...) }

func (s backStack) replaceAt(i int, c component) backStack {
	out := append(backStack(nil), s...)
	out[i] = c
	return out
}

func (m *model) apply(c *ctx) tea.Cmd {
	var cmds []tea.Cmd
	for _, a := range c.actions {
		switch a.kind {
		case actOpen:
			m.main = m.main.push(a.comp)
		case actBack:
			var popped component
			if m.main, popped = m.main.pop(); popped != nil {
				cmds = append(cmds, popped.close(c))
			}
		case actReplaceBase:
			m.underOverlays(func() {
				if old := m.main.top(); old != nil {
					cmds = append(cmds, old.close(c))
				}
				m.main = m.main.replaceTop(a.comp)
			})
		case actReplaceFile:
			if i := m.fileAt(); i >= 0 {
				cmds = append(cmds, m.main[i].close(c))
				m.main = m.main.replaceAt(i, a.comp)
			}
		case actCloseFile:
			if i := m.fileAt(); i >= 0 {
				cmds = append(cmds, m.main[i].close(c))
				m.main = m.main.removeAt(i)
			}
		case actCloseScreen:
			if i := m.screenAt(a.id); i >= 0 {
				cmds = append(cmds, m.main[i].close(c))
				m.main = m.main.removeAt(i)
			}
		case actFocus:
			m.focused = a.focus
			if m.idleComposerActive() {
				m.sizeIdleReply()
			}
		case actFlash:
			m.flash = a.flash
		case actSyncPane:
			cmds = append(cmds, m.syncPane())
		case actHome:
			m.left.tree.cursor = 0
			cmds = append(cmds, m.enterHome())
		case actOpenTree:
			res, cmd := m.openTree()
			*m = res.(model)
			cmds = append(cmds, cmd)
		case actOpenSession:
			res, cmd := m.enterSession(a.id)
			*m = res
			cmds = append(cmds, cmd)
		case actStepDiff:
			res, cmd := m.stepDiff(a.step)
			*m = res.(model)
			cmds = append(cmds, cmd)
		case actTree:
			tc := &ctx{m: m}
			var cmd tea.Cmd
			m.left.tree, cmd = m.left.tree.run(tc, a.op)
			cmds = append(cmds, cmd, m.apply(tc))
		case actResizeTree:
			m.left = m.left.resize(&ctx{m: m}, a.step)
		}
	}
	c.actions = nil
	return tea.Batch(cmds...)
}

func (m model) baseTop() int {
	i := len(m.main)
	for i > 0 && m.main[i-1].layer().over() {
		i--
	}
	return i
}

func (m model) baseComp() component {
	if i := m.baseTop(); i > 0 {
		return m.main[i-1]
	}
	return nil
}

// underOverlays keeps an open file across a switch of the component under it,
// until syncSidebar drops the file for another workspace.
func (m *model) underOverlays(change func()) {
	m.keepPanes()
	i := m.baseTop()
	over := m.main[i:]
	m.main = m.main[:i:i]
	change()
	m.main = append(m.main[:len(m.main):len(m.main)], over...)
}

func (m *model) resetMain(root func() component) tea.Cmd {
	c := &ctx{m: m}
	var cmds []tea.Cmd
	m.underOverlays(func() {
		for _, comp := range m.main {
			cmds = append(cmds, comp.close(c))
		}
		m.main = backStack{root()}
	})
	return tea.Batch(append(cmds, m.apply(c))...)
}

func (m *model) enterMain(comp component) {
	m.disarmRoot()
	m.underOverlays(func() { m.main = m.main.push(comp) })
}

func (m model) inSession() bool {
	t, ok := m.baseComp().(transcriptComp)
	return ok && t.live
}

// onTreeScreen reports whether the main pane shows the pane of the tree's
// cursor row: a workspace pane, or the Home pane while the tree has focus.
func (m model) onTreeScreen() bool {
	switch m.baseComp().(type) {
	case workspaceComp:
		return true
	case homeComp:
		return m.homeOnTree()
	}
	return false
}
