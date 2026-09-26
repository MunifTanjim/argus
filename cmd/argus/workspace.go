package main

import (
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

func registryCmd(dial callerFor, use, short string, nargs int, run func(call caller, list api.ProjectListResult, args []string) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:           use,
		Short:         short,
		Args:          cobra.ExactArgs(nargs),
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
			if err := run(call, list, args); err != nil {
				return fail(cmd, err)
			}
			return nil
		},
	}
	addSocketFlag(cmd.Flags())
	return cmd
}

// setupPoll is how often --wait checks a setup.
var setupPoll = time.Second

// No run on the first poll means there is no setup to wait for.
func waitSetup(call caller, wsID string) error {
	for seen := false; ; seen = true {
		var list api.ProjectListResult
		if err := call(api.MethodProjectList, nil, &list); err != nil {
			return err
		}
		var run *api.ScriptRun
		for _, p := range list.Projects {
			for _, w := range p.Workspaces {
				if w.ID == wsID {
					run = w.Setup
				}
			}
		}
		switch {
		case run == nil && !seen:
			return nil
		case run == nil:
			return errors.New("setup state lost (node restarted or workspace removed)")
		case run.State == "ok":
			shell.StdOutF("setup done\n")
			return nil
		case run.State == "failed":
			if run.OutputTail != "" {
				shell.StdOutF("%s\n", run.OutputTail)
			}
			if run.ExitCode < 0 {
				return errors.New("setup failed") // it never exited: a settings error, a timeout, or a stop
			}
			return fmt.Errorf("setup failed (exit %d)", run.ExitCode)
		}
		time.Sleep(setupPoll)
	}
}

func newWorkspaceCmd() *cobra.Command { return workspaceCmd(localCallerFor) }

func workspaceCmd(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Create, remove, and retarget workspaces on the local node",
	}
	var source, target string
	var wait bool
	create := registryCmd(dial, "create <project> <name>", "Create a workspace: <name> is a branch, or a PR or issue number", 2, func(call caller, list api.ProjectListResult, args []string) error {
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
		shell.StdOutF("created workspace %s\n", res.Dir)
		if res.Warning != "" {
			shell.StdOutF("warning: %s\n", res.Warning)
		}
		if res.Setup != "" {
			shell.StdOutF("setup started: %s\n", res.Setup)
		}
		if wait {
			// A setup that failed before it started returns no command.
			return waitSetup(call, res.WorkspaceID)
		}
		return nil
	})
	create.Flags().StringVar(&source, "source", api.SourceNew, "what <name> is: new (a new branch), branch, pr, or issue")
	create.Flags().StringVar(&target, "target", "", "target branch to compare against (default: the repository's default branch)")
	create.Flags().BoolVar(&wait, "wait", false, "wait for the setup script to finish")

	var force bool
	remove := registryCmd(dial, "remove <workspace>", "Remove a workspace's worktree; its branch stays", 1, func(call caller, list api.ProjectListResult, args []string) error {
		w, err := resolveWorkspace(list, args[0])
		if err != nil {
			return err
		}
		var res api.WorkspaceRemoveResult
		if err := call(api.MethodWorkspaceRemove, api.WorkspaceRemoveParams{WorkspaceID: w.ID, Force: force}, &res); err != nil {
			return err
		}
		shell.StdOutF("removed %s\n", w.Dir)
		if res.Warning != "" {
			shell.StdOutF("warning: %s\n", res.Warning)
		}
		return nil
	})
	remove.Flags().BoolVar(&force, "force", false, "discard uncommitted changes (live sessions still refuse)")

	var setupWait bool
	setup := registryCmd(dial, "setup <workspace>", "Run a workspace's setup script again", 1, func(call caller, list api.ProjectListResult, args []string) error {
		w, err := resolveWorkspace(list, args[0])
		if err != nil {
			return err
		}
		if err := call(api.MethodWorkspaceRunSetup, api.WorkspaceRef{WorkspaceID: w.ID}, nil); err != nil {
			return err
		}
		shell.StdOutF("setup started in %s\n", w.Dir)
		if setupWait {
			return waitSetup(call, w.ID)
		}
		return nil
	})
	setup.Flags().BoolVar(&setupWait, "wait", false, "wait for the setup script to finish")

	setupLog := registryCmd(dial, "setup-log <workspace>", "Print the output of a workspace's last setup run", 1, func(call caller, list api.ProjectListResult, args []string) error {
		w, err := resolveWorkspace(list, args[0])
		if err != nil {
			return err
		}
		var res api.SetupLogResult
		if err := call(api.MethodWorkspaceSetupLog, api.WorkspaceRef{WorkspaceID: w.ID}, &res); err != nil {
			return err
		}
		shell.StdOutF("%s", res.Output)
		return nil
	})

	cmd.AddCommand(create, remove, setup, setupLog, registryCmd(dial, "target <workspace> <branch>", "Set the branch a workspace compares against", 2, func(call caller, list api.ProjectListResult, args []string) error {
		w, err := resolveWorkspace(list, args[0])
		if err != nil {
			return err
		}
		if err := call(api.MethodWorkspaceSetTarget, api.WorkspaceSetTargetParams{WorkspaceID: w.ID, TargetBranch: args[1]}, nil); err != nil {
			return err
		}
		shell.StdOutF("target of %s → %s\n", w.Dir, args[1])
		return nil
	}))
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
