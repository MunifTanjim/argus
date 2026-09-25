package node

import (
	"fmt"
	"slices"
	"strings"
	"text/template"
	"text/template/parse"
)

var (
	worktreeDirVars = []string{"Repo.Name", "Branch.Name", "Branch.Slug"}
	issueBranchVars = []string{"Issue.Number", "Issue.Title", "Issue.Slug"}
)

// parseTemplate parses a workspace template and rejects any field reference
// that is not one of vars. Execution alone does not catch a reference to a
// whole group such as {{.Branch}}: it prints the map.
func parseTemplate(name, text string, vars []string) (*template.Template, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, err
	}
	if err := checkFields(t.Root, vars); err != nil {
		return nil, err
	}
	return t, nil
}

func checkFields(n parse.Node, vars []string) error {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return nil
		}
		for _, c := range n.Nodes {
			if err := checkFields(c, vars); err != nil {
				return err
			}
		}
	case *parse.ActionNode:
		return checkFields(n.Pipe, vars)
	case *parse.TemplateNode:
		return checkFields(n.Pipe, vars)
	case *parse.PipeNode:
		if n == nil {
			return nil
		}
		for _, c := range n.Cmds {
			if err := checkFields(c, vars); err != nil {
				return err
			}
		}
	case *parse.CommandNode:
		for _, a := range n.Args {
			if err := checkFields(a, vars); err != nil {
				return err
			}
		}
	case *parse.ChainNode:
		return checkFields(n.Node, vars)
	case *parse.IfNode:
		return checkBranch(&n.BranchNode, vars)
	case *parse.RangeNode:
		return checkBranch(&n.BranchNode, vars)
	case *parse.WithNode:
		return checkBranch(&n.BranchNode, vars)
	case *parse.FieldNode:
		if name := strings.Join(n.Ident, "."); !slices.Contains(vars, name) {
			return fmt.Errorf("unknown variable .%s (want .%s)", name, strings.Join(vars, ", ."))
		}
	}
	return nil
}

func checkBranch(n *parse.BranchNode, vars []string) error {
	for _, c := range []parse.Node{n.Pipe, n.List, n.ElseList} {
		if err := checkFields(c, vars); err != nil {
			return err
		}
	}
	return nil
}
