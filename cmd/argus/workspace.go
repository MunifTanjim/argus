package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
)

type caller func(method string, params, out any) error

type callerFor func(cmd *cobra.Command) (call caller, close func(), err error)

func localCallerFor(cmd *cobra.Command) (caller, func(), error) {
	cfg, err := resolveConfig(cmd)
	if err != nil {
		return nil, nil, err
	}
	dial, err := gatewayDialer("", "", cfg.Socket)
	if err != nil {
		return nil, nil, err
	}
	conn, err := dial(context.Background())
	if err != nil {
		return nil, nil, err
	}
	c := api.NewClient(conn)
	return c.Call, func() { c.Close() }, nil
}

type registryRun func(call caller, list api.ProjectListResult, args []string, asJSON bool) error

// registryCmd makes a command that runs against the node's project list and
// takes --json.
func registryCmd(dial callerFor, use, short string, args cobra.PositionalArgs, run registryRun) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:           use,
		Short:         short,
		Args:          args,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			call, closeConn, err := dial(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			defer closeConn()
			var list api.ProjectListResult
			if err := call(api.MethodProjectList, nil, &list); err != nil {
				return fail(cmd, err)
			}
			if err := run(call, list, args, asJSON); err != nil {
				return fail(cmd, err)
			}
			return nil
		},
	}
	addSocketFlag(cmd.Flags())
	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return cmd
}

// setupPoll is how often --wait checks a setup.
var setupPoll = time.Second

// findWorkspace fetches the workspace wsID; ok is false when the node no
// longer lists it.
func findWorkspace(call caller, wsID string) (api.WorkspaceNode, bool, error) {
	var list api.ProjectListResult
	if err := call(api.MethodProjectList, nil, &list); err != nil {
		return api.WorkspaceNode{}, false, err
	}
	for _, p := range list.Projects {
		for _, w := range p.Workspaces {
			if w.ID == wsID {
				return w, true, nil
			}
		}
	}
	return api.WorkspaceNode{}, false, nil
}

// waitSetup polls until run, the setup as last seen, stops running, and
// returns the run as last seen. A nil run means there is no setup to wait for.
func waitSetup(call caller, wsID string, run *api.ScriptRun) (*api.ScriptRun, error) {
	for run != nil && run.State == "running" {
		time.Sleep(setupPoll)
		w, ok, err := findWorkspace(call, wsID)
		if err != nil {
			return run, err
		}
		if !ok || w.Setup == nil {
			return run, errors.New("setup state lost (node restarted or workspace removed)")
		}
		run = w.Setup
	}
	return run, nil
}

// setupErr is the error a finished setup run makes --wait return.
func setupErr(run *api.ScriptRun) error {
	switch {
	case run == nil || run.State != "failed":
		return nil
	case run.ExitCode <= 0:
		return errors.New("setup failed") // no failed exit: a settings error, a file copy, a timeout, or a stop
	}
	return fmt.Errorf("setup failed (exit %d)", run.ExitCode)
}

// printSetupResult prints how a waited-on setup run ended.
func printSetupResult(run *api.ScriptRun) {
	switch {
	case run == nil:
	case run.State == "ok":
		shell.StdOutF("setup done\n")
	case run.State == "failed" && run.OutputTail != "":
		shell.StdOutF("%s\n", run.OutputTail)
	}
}

// finishSetup prints out, as JSON or with human, after waiting for its setup
// when wait is set.
func finishSetup(call caller, wait, asJSON bool, out *workspaceResult, human func()) error {
	if !asJSON {
		human()
	}
	var err error
	if wait {
		if out.Setup == nil { // no setup, or a node older than this CLI that does not report it
			var w api.WorkspaceNode
			w, _, err = findWorkspace(call, out.WorkspaceID)
			out.Setup = w.Setup
		}
		if err == nil {
			out.Setup, err = waitSetup(call, out.WorkspaceID, out.Setup)
		}
		if err == nil {
			if !asJSON {
				printSetupResult(out.Setup)
			}
			err = setupErr(out.Setup)
		}
	}
	if asJSON {
		// Print even when the wait failed: the workspace exists either way.
		if perr := printJSON(out); perr != nil {
			return perr
		}
	}
	return err
}

func setupField(run *api.ScriptRun) string {
	switch {
	case run == nil:
		return ""
	case run.Command == "":
		return "(" + run.State + ")"
	}
	return run.Command + " (" + run.State + ")"
}

func branchField(branch, target string) string {
	if target != "" && target != branch {
		return branch + " → " + target
	}
	return branch
}

type workspaceEntry struct {
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	api.WorkspaceNode
}

// workspaceResult is the --json output of a command that changes a workspace.
type workspaceResult struct {
	WorkspaceID  string         `json:"workspace_id"`
	ProjectID    string         `json:"project_id,omitempty"`
	ProjectName  string         `json:"project_name,omitempty"`
	Dir          string         `json:"dir"`
	Branch       string         `json:"branch,omitempty"`
	TargetBranch string         `json:"target_branch,omitempty"`
	Setup        *api.ScriptRun `json:"setup,omitempty"`
	Warning      string         `json:"warning,omitempty"`
}

type workspaceSetupLogOutput struct {
	WorkspaceID string `json:"workspace_id"`
	Output      string `json:"output"`
}

func newWorkspaceCmd() *cobra.Command { return workspaceCmd(localCallerFor) }

func workspaceCmd(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "List, create, remove, and retarget workspaces on the local node",
	}
	cmd.AddCommand(
		registryCmd(dial, "list [project]", "List workspaces, of every project or of one", cobra.MaximumNArgs(1), func(_ caller, list api.ProjectListResult, args []string, asJSON bool) error {
			projects := list.Projects
			if len(args) == 1 {
				p, err := resolveProject(list, args[0])
				if err != nil {
					return err
				}
				projects = []api.ProjectNode{p}
			}
			if !asJSON {
				printWorkspaceTable(projects, len(args) == 0)
				return nil
			}
			entries := []workspaceEntry{}
			for _, p := range projects {
				for _, w := range p.Workspaces {
					entries = append(entries, workspaceEntry{ProjectID: p.ID, ProjectName: p.Name, WorkspaceNode: w})
				}
			}
			return printJSON(entries)
		}),
		newWorkspaceCreateCmd(dial),
		newWorkspaceRemoveCmd(dial),
		newWorkspaceSetupCmd(dial),
		registryCmd(dial, "setup-log <workspace>", "Print the output of a workspace's last setup run", cobra.ExactArgs(1), func(call caller, list api.ProjectListResult, args []string, asJSON bool) error {
			w, err := resolveWorkspace(list, args[0])
			if err != nil {
				return err
			}
			var res api.SetupLogResult
			if err := call(api.MethodWorkspaceSetupLog, api.WorkspaceRef{WorkspaceID: w.ID}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(workspaceSetupLogOutput{WorkspaceID: w.ID, Output: res.Output})
			}
			shell.StdOutF("%s", res.Output)
			return nil
		}),
		registryCmd(dial, "target <workspace> <branch>", "Set the branch a workspace compares against", cobra.ExactArgs(2), func(call caller, list api.ProjectListResult, args []string, asJSON bool) error {
			w, err := resolveWorkspace(list, args[0])
			if err != nil {
				return err
			}
			res := api.WorkspaceSetTargetResult{TargetBranch: args[1]} // an older node replies null
			if err := call(api.MethodWorkspaceSetTarget, api.WorkspaceSetTargetParams{WorkspaceID: w.ID, TargetBranch: args[1]}, &res); err != nil {
				return err
			}
			// Report the branch it resolves to, as workspace list does.
			p, _, err := workspaceAtPath(list, w.Dir)
			if err != nil {
				return err
			}
			target := cmp.Or(res.TargetBranch, p.DefaultBranch)
			if asJSON {
				return printJSON(workspaceResult{WorkspaceID: w.ID, Dir: w.Dir, Branch: w.Branch, TargetBranch: target})
			}
			if res.TargetBranch == "" {
				target = strings.TrimSpace(target + " (default branch)")
			}
			printFields("set target of workspace "+shortID(w.ID), [][2]string{{"dir", w.Dir}, {"branch", w.Branch}, {"target", target}})
			return nil
		}),
	)
	return cmd
}

func printWorkspaceTable(projects []api.ProjectNode, withProject bool) {
	var rows [][]string
	for _, p := range projects {
		for _, w := range p.Workspaces {
			row := []string{shortID(w.ID)}
			if withProject {
				row = append(row, p.Name)
			}
			rows = append(rows, append(row, workspaceBranch(p.Kind, w), orDash(w.TargetBranch), orDash(w.Head), orDash(workspaceStatus(w)), w.Dir))
		}
	}
	if len(rows) == 0 {
		shell.StdOutF("no workspaces\n")
		return
	}
	headers := []string{"ID"}
	if withProject {
		headers = append(headers, "PROJECT")
	}
	printTable(append(headers, "BRANCH", "TARGET", "HEAD", "STATUS", "DIR"), rows)
}

func workspaceBranch(kind string, w api.WorkspaceNode) string {
	switch {
	case w.Branch != "":
		return w.Branch
	case kind == "git" && !w.IsGone:
		return "(detached)"
	}
	return "-"
}

func workspaceStatus(w api.WorkspaceNode) string {
	var s []string
	if w.IsMain {
		s = append(s, "main")
	}
	if w.IsGone {
		s = append(s, "gone")
	}
	switch {
	case w.Setup == nil:
	case w.Setup.State == "running":
		s = append(s, "setting up")
	case w.Setup.State == "failed":
		s = append(s, "setup failed")
	}
	return strings.Join(s, ", ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func newWorkspaceCreateCmd(dial callerFor) *cobra.Command {
	var source, target string
	var wait bool
	cmd := registryCmd(dial, "create <project> <name>", "Create a workspace: <name> is a branch, or a PR or issue number", cobra.ExactArgs(2), func(call caller, list api.ProjectListResult, args []string, asJSON bool) error {
		p, err := resolveProject(list, args[0])
		if err != nil {
			return err
		}
		params := api.WorkspaceCreateParams{ProjectID: p.ID, Source: source, TargetBranch: target}
		switch source {
		case api.SourceNew, api.SourceBranch:
			params.Branch = args[1]
		case api.SourcePR, api.SourceIssue:
			n, err := strconv.Atoi(strings.TrimPrefix(args[1], "#"))
			if err != nil {
				return fmt.Errorf("a %s is a number: %q", source, args[1])
			}
			params.Number = n
		default:
			return fmt.Errorf("--source is new, branch, pr, or issue: %q", source)
		}
		var res api.WorkspaceCreateResult
		if err := call(api.MethodWorkspaceCreate, params, &res); err != nil {
			return err
		}
		out := workspaceResult{
			WorkspaceID: res.WorkspaceID, ProjectID: p.ID, ProjectName: p.Name, Dir: res.Dir,
			Branch: res.Branch, TargetBranch: cmp.Or(res.TargetBranch, p.DefaultBranch), Setup: res.SetupRun, Warning: res.Warning,
		}
		return finishSetup(call, wait, asJSON, &out, func() {
			warn(res.Warning)
			printFields("created workspace "+shortID(res.WorkspaceID), [][2]string{
				{"project", p.Name},
				{"dir", res.Dir},
				{"branch", branchField(out.Branch, out.TargetBranch)},
				{"setup", setupField(res.SetupRun)},
			})
		})
	})
	cmd.Flags().StringVar(&source, "source", api.SourceNew, "what <name> is: new (a new branch), branch, pr, or issue")
	cmd.Flags().StringVar(&target, "target", "", "target branch to compare against (default: the repository's default branch)")
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the setup script to finish")
	return cmd
}

func newWorkspaceRemoveCmd(dial callerFor) *cobra.Command {
	var force bool
	cmd := registryCmd(dial, "remove <workspace>", "Remove a workspace's worktree; its branch stays", cobra.ExactArgs(1), func(call caller, list api.ProjectListResult, args []string, asJSON bool) error {
		w, err := resolveWorkspace(list, args[0])
		if err != nil {
			return err
		}
		var res api.WorkspaceRemoveResult
		if err := call(api.MethodWorkspaceRemove, api.WorkspaceRemoveParams{WorkspaceID: w.ID, Force: force}, &res); err != nil {
			return err
		}
		if asJSON {
			return printJSON(workspaceResult{WorkspaceID: w.ID, Dir: w.Dir, Branch: w.Branch, Warning: res.Warning})
		}
		warn(res.Warning)
		printFields("removed workspace "+shortID(w.ID), [][2]string{{"dir", w.Dir}, {"branch", w.Branch}})
		return nil
	})
	cmd.Flags().BoolVar(&force, "force", false, "discard uncommitted changes (live sessions still refuse)")
	return cmd
}

func newWorkspaceSetupCmd(dial callerFor) *cobra.Command {
	var wait bool
	cmd := registryCmd(dial, "setup <workspace>", "Run a workspace's setup script again", cobra.ExactArgs(1), func(call caller, list api.ProjectListResult, args []string, asJSON bool) error {
		w, err := resolveWorkspace(list, args[0])
		if err != nil {
			return err
		}
		var run *api.ScriptRun
		if err := call(api.MethodWorkspaceRunSetup, api.WorkspaceRef{WorkspaceID: w.ID}, &run); err != nil {
			return err
		}
		out := workspaceResult{WorkspaceID: w.ID, Dir: w.Dir, Setup: run}
		return finishSetup(call, wait, asJSON, &out, func() {
			printFields("started setup in workspace "+shortID(w.ID), [][2]string{{"dir", w.Dir}, {"setup", setupField(run)}})
		})
	})
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the setup script to finish")
	return cmd
}

const minIDPrefix = 6

func shortID(id string) string { return id[:min(len(id), 12)] }

// resolveProject finds a project by id, exact name, unique id prefix, or a path
// inside one of its workspaces.
func resolveProject(list api.ProjectListResult, arg string) (api.ProjectNode, error) {
	var byName, byPrefix []api.ProjectNode
	for _, p := range list.Projects {
		switch {
		case p.ID == arg:
			return p, nil
		case p.Name == arg:
			byName = append(byName, p)
		case len(arg) >= minIDPrefix && strings.HasPrefix(p.ID, arg):
			byPrefix = append(byPrefix, p)
		}
	}
	for _, m := range [][]api.ProjectNode{byName, byPrefix} {
		switch {
		case len(m) == 1:
			return m[0], nil
		case len(m) > 1:
			ids := make([]string, len(m))
			for i, p := range m {
				ids[i] = shortID(p.ID)
			}
			return api.ProjectNode{}, fmt.Errorf("%d projects match %q; use an id: %s", len(m), arg, strings.Join(ids, ", "))
		}
	}
	p, w, err := workspaceAtPath(list, arg)
	if err != nil {
		return api.ProjectNode{}, err
	}
	if w.ID == "" {
		return api.ProjectNode{}, fmt.Errorf("no project %q; see argus project list", arg)
	}
	return p, nil
}

// resolveWorkspace finds a workspace by id, unique id prefix, or a path inside
// it (the innermost workspace wins, so "." works in any worktree).
func resolveWorkspace(list api.ProjectListResult, arg string) (api.WorkspaceNode, error) {
	var byPrefix []api.WorkspaceNode
	for _, p := range list.Projects {
		for _, w := range p.Workspaces {
			switch {
			case w.ID == arg:
				return w, nil
			case len(arg) >= minIDPrefix && strings.HasPrefix(w.ID, arg):
				byPrefix = append(byPrefix, w)
			}
		}
	}
	if len(byPrefix) == 1 {
		return byPrefix[0], nil
	}
	if len(byPrefix) > 1 {
		return api.WorkspaceNode{}, fmt.Errorf("%d workspaces match %q; use a longer id", len(byPrefix), arg)
	}
	_, w, err := workspaceAtPath(list, arg)
	if err != nil {
		return api.WorkspaceNode{}, err
	}
	if w.ID == "" {
		return api.WorkspaceNode{}, fmt.Errorf("no workspace %q; see argus project list", arg)
	}
	return w, nil
}

// workspaceAtPath returns the innermost workspace that contains the path arg,
// and its project. The workspace id is empty when none contains it.
func workspaceAtPath(list api.ProjectListResult, arg string) (api.ProjectNode, api.WorkspaceNode, error) {
	path, err := filepath.Abs(arg)
	if err != nil {
		return api.ProjectNode{}, api.WorkspaceNode{}, err
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real // the registry stores resolved paths
	}
	var bestP api.ProjectNode
	var best api.WorkspaceNode
	for _, p := range list.Projects {
		for _, w := range p.Workspaces {
			inside := path == w.Dir || strings.HasPrefix(path, w.Dir+string(filepath.Separator))
			if inside && len(w.Dir) > len(best.Dir) {
				bestP, best = p, w
			}
		}
	}
	return bestP, best, nil
}
