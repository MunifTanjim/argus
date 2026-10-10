package main

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/projectreg"
	"github.com/MunifTanjim/argus/internal/shell"
)

// enableProjectRegistry leaves the registry off on a database failure, so it
// never blocks the node from serving sessions.
func enableProjectRegistry(d *node.Node, cfg *config.Config, log *slog.Logger) error {
	sqlDB, err := db.Open(config.GetDataPath("argus.db"))
	if err != nil {
		if log != nil {
			log.Warn("project registry disabled", "err", err)
		}
		return nil
	}
	reg := projectreg.New(sqlDB)
	if err := reg.SetAutoAdoptDirs(cfg.Workspace.AutoAdoptDirs); err != nil {
		sqlDB.Close()
		return fmt.Errorf("workspace.auto-adopt-dirs: %w", err)
	}
	d.SetProjectRegistry(reg)
	return nil
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

type projectOutput struct {
	ProjectID    string `json:"project_id"`
	Name         string `json:"name"`
	PreviousName string `json:"previous_name,omitempty"`
	Hidden       *bool  `json:"hidden,omitempty"`
	Pinned       *bool  `json:"pinned,omitempty"`
}

func projectCmd(dial callerFor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Inspect and curate the local project registry",
	}
	setHidden := func(o *projectOutput, v *bool) { o.Hidden = v }
	setPinned := func(o *projectOutput, v *bool) { o.Pinned = v }
	cmd.AddCommand(
		registryCmd(dial, "list", "List projects discovered on this node", cobra.NoArgs, func(_ caller, res api.ProjectListResult, _ []string, asJSON bool) error {
			if asJSON {
				return printJSON(res)
			}
			printProjectTable(res)
			return nil
		}),
		registryCmd(dial, "rename <project> <name>", "Rename a project", cobra.ExactArgs(2), func(call caller, list api.ProjectListResult, args []string, asJSON bool) error {
			p, err := resolveProject(list, args[0])
			if err != nil {
				return err
			}
			res := api.ProjectRenameResult{Name: args[1]} // an older node replies null
			if err := call(api.MethodProjectRename, api.ProjectRenameParams{ProjectID: p.ID, Name: args[1]}, &res); err != nil {
				return err
			}
			if asJSON {
				return printJSON(projectOutput{ProjectID: p.ID, Name: res.Name, PreviousName: p.Name})
			}
			printFields("renamed project "+shortID(p.ID), [][2]string{{"from", p.Name}, {"to", res.Name}})
			return nil
		}),
		projectFlagCmd(dial, "hide", "Hide a project", api.MethodProjectSetHidden, setHidden, true, "hid"),
		projectFlagCmd(dial, "unhide", "Show a hidden project", api.MethodProjectSetHidden, setHidden, false, "unhid"),
		projectFlagCmd(dial, "pin", "Pin a project to the top", api.MethodProjectSetPinned, setPinned, true, "pinned"),
		projectFlagCmd(dial, "unpin", "Unpin a project", api.MethodProjectSetPinned, setPinned, false, "unpinned"),
		registryCmd(dial, "forget <project>", "Drop a project from the registry; its files stay", cobra.ExactArgs(1), func(call caller, list api.ProjectListResult, args []string, asJSON bool) error {
			p, err := resolveProject(list, args[0])
			if err != nil {
				return err
			}
			if err := call(api.MethodProjectForget, api.ProjectRef{ProjectID: p.ID}, nil); err != nil {
				return err
			}
			if asJSON {
				return printJSON(projectOutput{ProjectID: p.ID, Name: p.Name})
			}
			printFields("forgot project "+shortID(p.ID), [][2]string{{"name", p.Name}})
			return nil
		}),
	)
	return cmd
}

// projectFlagCmd sets a project flag through method; set records the new value
// in the --json output.
func projectFlagCmd(dial callerFor, verb, short, method string, set func(*projectOutput, *bool), value bool, done string) *cobra.Command {
	return registryCmd(dial, verb+" <project>", short, cobra.ExactArgs(1), func(call caller, list api.ProjectListResult, args []string, asJSON bool) error {
		p, err := resolveProject(list, args[0])
		if err != nil {
			return err
		}
		if err := call(method, api.ProjectFlagParams{ProjectID: p.ID, Value: value}, nil); err != nil {
			return err
		}
		if asJSON {
			out := projectOutput{ProjectID: p.ID, Name: p.Name}
			set(&out, &value)
			return printJSON(out)
		}
		printFields(done+" project "+shortID(p.ID), [][2]string{{"name", p.Name}})
		return nil
	})
}

func printProjectTable(res api.ProjectListResult) {
	if len(res.Projects) == 0 {
		shell.StdOutF("no projects\n")
		return
	}
	rows := make([][]string, len(res.Projects))
	for i, p := range res.Projects {
		root := p.Root
		if root == "" {
			root = p.Dir // a bare repo has no main working tree
		}
		rows[i] = []string{shortID(p.ID), p.Name, p.Kind, orDash(p.DefaultBranch), strconv.Itoa(len(p.Workspaces)), orDash(projectStatus(p)), root}
	}
	printTable([]string{"ID", "NAME", "KIND", "DEFAULT", "WORKSPACES", "STATUS", "ROOT"}, rows)
}

func projectStatus(p api.ProjectNode) string {
	var s []string
	if p.Pinned {
		s = append(s, "pinned")
	}
	if p.Hidden {
		s = append(s, "hidden")
	}
	if p.IsGone {
		s = append(s, "gone")
	}
	if p.Error != "" {
		s = append(s, "git error: "+p.Error)
	}
	return strings.Join(s, ", ")
}
