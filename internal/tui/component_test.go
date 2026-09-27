package tui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type stubComp struct {
	name   string
	closed *[]string
}

func (s stubComp) section() string                                            { return s.name }
func (s stubComp) raw(*ctx) bool                                              { return false }
func (s stubComp) handleKey(*ctx, tea.KeyPressMsg) (component, tea.Cmd, bool) { return s, nil, false }
func (s stubComp) update(*ctx, tea.Msg) (component, tea.Cmd)                  { return s, nil }
func (s stubComp) view(*ctx, int, int) string                                 { return s.name }
func (s stubComp) footer(*ctx) []binding                                      { return nil }
func (s stubComp) spins(*ctx) bool                                            { return false }
func (s stubComp) fullScreen(*ctx) fullLevel                                  { return notFull }
func (s stubComp) offers(*ctx) []binding                                      { return nil }
func (s stubComp) layer() layer                                               { return baseLayer }
func (s stubComp) pageStep(*ctx) int                                          { return 1 }
func (s stubComp) close(*ctx) tea.Cmd {
	if s.closed != nil {
		*s.closed = append(*s.closed, s.name)
	}
	return nil
}

func TestBackStackKeepsItsRoot(t *testing.T) {
	var s backStack
	s = s.push(stubComp{name: "home"}).push(stubComp{name: "transcript"})
	s, popped := s.pop()
	if popped.section() != "transcript" || s.top().section() != "home" {
		t.Fatalf("pop: popped %q, top %q", popped.section(), s.top().section())
	}
	s, popped = s.pop()
	if popped != nil || s.top().section() != "home" {
		t.Errorf("the root is never popped: popped %v, top %q", popped, s.top().section())
	}
}

func TestActionsApplyAfterTheComponentIsStored(t *testing.T) {
	var closed []string
	m := model{}
	m.main = backStack{stubComp{name: "home"}, stubComp{name: "transcript", closed: &closed}}
	c := &ctx{m: &m}
	c.back()
	c.open(stubComp{name: "file"})
	c.focusOn(rightSidebar)
	c.setFlash("hi")
	m.apply(c)
	if got := m.main.top().section(); got != "file" {
		t.Errorf("top = %q, want file", got)
	}
	if len(m.main) != 2 || len(closed) != 1 || closed[0] != "transcript" {
		t.Errorf("back closes the popped component: stack %d, closed %v", len(m.main), closed)
	}
	if m.focused != rightSidebar || m.flash != "hi" {
		t.Errorf("focus = %v flash = %q", m.focused, m.flash)
	}
}

func contractComponents(m model) []component {
	return []component{
		m.left.tree,
		m.right.fileTree,
		m.right.changes,
		homeComp{},
		historyComp{},
		historyComp{inProject: true},
		newLogsComp(),
		workspaceComp{ws: "n1:w1"},
		fileComp{ws: "n1:w1", path: "go.mod"},
		newLiveTranscript("n1:s1"),
		newTranscript(),
		screenComp{sessionID: "n1:s1"},
		spawnComp{},
		newDock(),
	}
}

// componentTypes takes every type with a section method as a component.
func componentTypes(t *testing.T) map[string]bool {
	t.Helper()
	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, f := range pkgs["tui"].Files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "section" {
				continue
			}
			if id, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
				out[id.Name] = true
			}
		}
	}
	return out
}

// Every component answers every method of the contract, and offers only the
// help and quit its keymap section lists.
func TestEveryComponentAnswersTheContract(t *testing.T) {
	m := workspaceSession(dockPermission)
	comps := contractComponents(m)
	listed := map[string]bool{}
	for _, comp := range comps {
		listed[strings.TrimPrefix(fmt.Sprintf("%T", comp), "tui.")] = true
	}
	for name := range componentTypes(t) {
		if !listed[name] {
			t.Errorf("contractComponents misses %s", name)
		}
	}
	w, h := m.mainSize()
	// A component asked out of focus matches keys under the focused section.
	strictScreens = false
	defer func() { strictScreens = true }()
	for _, comp := range comps {
		name := fmt.Sprintf("%T", comp)
		call := func(method string, f func(c *ctx)) {
			t.Helper()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s.%s panics: %v", name, method, r)
				}
			}()
			mm := m
			f(&ctx{m: &mm})
		}
		call("section", func(*ctx) { _ = comp.section() })
		call("raw", func(c *ctx) { _ = comp.raw(c) })
		call("handleKey", func(c *ctx) { _, _, _ = comp.handleKey(c, keyMsg("z")) })
		call("update", func(c *ctx) { _, _ = comp.update(c, spinTickMsg{}) })
		call("view", func(c *ctx) { _ = comp.view(c, w, h) })
		call("footer", func(c *ctx) { _ = comp.footer(c) })
		call("spins", func(c *ctx) { _ = comp.spins(c) })
		call("fullScreen", func(c *ctx) { _ = comp.fullScreen(c) })
		call("close", func(c *ctx) { _ = comp.close(c) })
		call("layer", func(*ctx) { _ = comp.layer() })
		call("pageStep", func(c *ctx) {
			if n := comp.pageStep(c); n < 1 {
				t.Errorf("%s.pageStep = %d, want at least 1", name, n)
			}
		})
		call("offers", func(c *ctx) {
			for _, b := range comp.offers(c) {
				if !offersKey(sectionOffers[comp.section()].keys, b) {
					t.Errorf("%s offers %s, which section %q does not list", name, b.name, comp.section())
				}
			}
		})
	}
}
