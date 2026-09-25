package main

import (
	"context"
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

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Inspect the local project/workspace registry",
	}
	cmd.AddCommand(newProjectListCmd())
	return cmd
}

func newProjectListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List projects and workspaces discovered on this node",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			res, err := callLocal[api.ProjectListResult](ctx, cfg, api.MethodProjectList, nil)
			if err != nil {
				return fail(cmd, err)
			}
			if asJSON {
				b, err := json.MarshalIndent(res, "", "  ")
				if err != nil {
					return fail(cmd, err)
				}
				shell.StdOutF("%s\n", b)
				return nil
			}
			printProjectTree(res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	addClientFlags(cmd.Flags())
	return cmd
}

func printProjectTree(res api.ProjectListResult) {
	if len(res.Projects) == 0 {
		shell.StdOutF("no projects\n")
		return
	}
	for _, p := range res.Projects {
		gone := ""
		if p.IsGone {
			gone = " (gone)"
		}
		shell.StdOutF("%s  %s%s\n  %s\n", p.Kind, p.Name, gone, p.Dir)
		for _, w := range p.Workspaces {
			marker := " "
			if w.IsMain {
				marker = "*"
			}
			branch := w.Branch
			if branch == "" {
				branch = "(detached)"
			}
			wgone := ""
			if w.IsGone {
				wgone = " (gone)"
			}
			shell.StdOutF("  %s %-24s %-8s %s%s\n", marker, branch, w.Head, w.Dir, wgone)
		}
	}
}
