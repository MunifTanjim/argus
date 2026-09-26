package main

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/projectreg"
	"github.com/MunifTanjim/argus/internal/shell"
)

// enableProjectRegistry logs and leaves the registry off on failure, so a
// database problem never blocks the node from serving sessions.
func enableProjectRegistry(d *node.Node, log *slog.Logger) {
	sqlDB, err := db.Open(config.GetDataPath("argus.db"))
	if err != nil {
		if log != nil {
			log.Warn("project registry disabled", "err", err)
		}
		return
	}
	d.SetProjectRegistry(projectreg.New(sqlDB))
}

func setWorkspaceTemplates(d *node.Node, cfg *config.Config) error {
	if err := d.SetWorktreeDirTemplate(cfg.Workspace.WorktreeDirTemplate); err != nil {
		return fmt.Errorf("workspace.worktree-dir-template: %w", err)
	}
	if err := d.SetIssueBranchTemplate(cfg.Workspace.IssueBranchTemplate); err != nil {
		return fmt.Errorf("workspace.issue-branch-template: %w", err)
	}
	return nil
}

func newProjectCmd() *cobra.Command { return projectCmd(localCallerFor) }

func projectCmd(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Inspect and curate the local project registry",
	}
	cmd.AddCommand(
		newProjectListCmd(dial),
		registryCmd(dial, "rename <project> <name>", "Rename a project", 2, func(call caller, list api.ProjectListResult, args []string) error {
			p, err := resolveProject(list, args[0])
			if err != nil {
				return err
			}
			if err := call(api.MethodProjectRename, api.ProjectRenameParams{ProjectID: p.ID, Name: args[1]}, nil); err != nil {
				return err
			}
			shell.StdOutF("renamed %s to %s\n", p.Name, args[1])
			return nil
		}),
		projectFlagCmd(dial, "hide", "Hide a project", api.MethodProjectSetHidden, true, "hid"),
		projectFlagCmd(dial, "unhide", "Show a hidden project", api.MethodProjectSetHidden, false, "unhid"),
		projectFlagCmd(dial, "pin", "Pin a project to the top", api.MethodProjectSetPinned, true, "pinned"),
		projectFlagCmd(dial, "unpin", "Unpin a project", api.MethodProjectSetPinned, false, "unpinned"),
		registryCmd(dial, "forget <project>", "Drop a project from the registry; its files stay", 1, func(call caller, list api.ProjectListResult, args []string) error {
			p, err := resolveProject(list, args[0])
			if err != nil {
				return err
			}
			if err := call(api.MethodProjectForget, api.ProjectRef{ProjectID: p.ID}, nil); err != nil {
				return err
			}
			shell.StdOutF("forgot %s\n", p.Name)
			return nil
		}),
	)
	return cmd
}

func projectFlagCmd(dial callerFor, verb, short, method string, value bool, done string) *cobra.Command {
	return registryCmd(dial, verb+" <project>", short, 1, func(call caller, list api.ProjectListResult, args []string) error {
		p, err := resolveProject(list, args[0])
		if err != nil {
			return err
		}
		if err := call(method, api.ProjectFlagParams{ProjectID: p.ID, Value: value}, nil); err != nil {
			return err
		}
		shell.StdOutF("%s %s\n", done, p.Name)
		return nil
	})
}

func newProjectListCmd(dial callerFor) *cobra.Command {
	var asJSON bool
	cmd := registryCmd(dial, "list", "List projects and workspaces discovered on this node", 0, func(_ caller, res api.ProjectListResult, _ []string) error {
		if !asJSON {
			printProjectTree(res)
			return nil
		}
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		shell.StdOutF("%s\n", b)
		return nil
	})
	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return cmd
}

func printProjectTree(res api.ProjectListResult) {
	if len(res.Projects) == 0 {
		shell.StdOutF("no projects\n")
		return
	}
	for _, p := range res.Projects {
		var marks string
		for _, m := range []struct {
			on   bool
			text string
		}{{p.IsGone, "gone"}, {p.Error != "", "git error: " + p.Error}, {p.Hidden, "hidden"}, {p.Pinned, "pinned"}} {
			if m.on {
				marks += " (" + m.text + ")"
			}
		}
		root := p.Root
		if root == "" {
			root = p.Dir // a bare repo has no main working tree
		}
		shell.StdOutF("%s  %s%s  [%s]\n  %s\n", p.Kind, p.Name, marks, shortID(p.ID), root)
		for _, w := range p.Workspaces {
			marker := " "
			if w.IsMain {
				marker = "*"
			}
			shell.StdOutF("  %s %-24s %-8s %s%s%s  [%s]\n", marker, workspaceBranch(p.Kind, w), w.Head, w.Dir, goneMark(w.IsGone), setupMark(w.Setup), shortID(w.ID))
		}
	}
}

func workspaceBranch(kind string, w api.WorkspaceNode) string {
	switch {
	case w.Branch != "" && w.TargetBranch != "" && w.TargetBranch != w.Branch:
		return w.Branch + " → " + w.TargetBranch
	case w.Branch != "":
		return w.Branch
	case kind == "git" && !w.IsGone:
		return "(detached)"
	}
	return "-"
}

func goneMark(gone bool) string {
	if gone {
		return " (gone)"
	}
	return ""
}

func setupMark(run *api.ScriptRun) string {
	switch {
	case run == nil:
		return ""
	case run.State == "running":
		return " (setting up)"
	case run.State == "failed":
		return " (setup failed)"
	}
	return ""
}
