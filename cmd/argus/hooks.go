package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/mcphttp"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/shell"
)

// newHooksCmd: node start installs the integrations too; install is the
// foreground retry that reports errors.
func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "hooks",
		Short:         "Install or remove argus's agent integrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Reached only when no subcommand matched; returns a real error (printed by
		// main) rather than errSilent, since there's nothing of its own to print first.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("hooks: a subcommand is required (install or uninstall)")
			}
			return fmt.Errorf("unknown hooks subcommand %q", args[0])
		},
	}
	cmd.AddCommand(newHooksInstallCmd(), newHooksUninstallCmd())
	return cmd
}

func newHooksInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "install",
		Short:         "Install argus's agent integrations (hooks, Claude Code plugin)",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			opts, err := mcpInstallOptions(cfg, adapter.InstallOptions{ArgusBin: cfg.Bin, Socket: node.ModSocketPath(cfg.Socket)})
			if err != nil {
				shell.StdErrF("argus mcp server not installed: %v\n", err)
			}
			hooked, failed := false, false
			for _, a := range adapters.All() {
				line, ok, isHook := installAgent(a, opts)
				if !ok {
					shell.StdErrF("%s", line)
					failed = true
					continue
				}
				fmt.Print(line)
				hooked = hooked || isHook
			}
			if hooked {
				fmt.Printf("  command: %s hook <event>\n", cfg.Bin)
			}
			if failed {
				return errSilent // each failure was already printed
			}
			return nil
		},
	}
	addSocketFlag(cmd.Flags())
	cmd.Flags().String("bin", "", "argus binary the hooks invoke (default: argus) [$ARGUS_BIN]")
	return cmd
}

func newHooksUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "uninstall",
		Short:         "Remove argus's agent integrations",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			failed := false
			for _, a := range adapters.All() {
				line, err := uninstallAgent(a)
				if err != nil {
					shell.StdErrF("failed to remove argus integration for %s: %v\n", a.Agent(), err)
					failed = true
					continue
				}
				fmt.Print(line)
			}
			if failed {
				return errSilent // each failure was already printed
			}
			fmt.Println("argus installs them again the next time it starts")
			return nil
		},
	}
}

// installAgent's ok is false when line is an error message; isHook reports
// that argus hooks were installed.
func installAgent(a adapter.Adapter, opts adapter.InstallOptions) (line string, ok, isHook bool) {
	failure := func(err error) string {
		return fmt.Sprintf("failed to install argus integration for %s: %v\n", a.Agent(), err)
	}
	path, err := a.SettingsPath()
	if err != nil {
		return failure(err), false, false
	}
	mcpOnly := path == ""
	if mcpOnly && opts.MCPURL == "" && !opts.MCPOff {
		return fmt.Sprintf("skipped %s: no argus mcp token\n", a.Agent()), true, false
	}
	// Only install for agents the user actually has set up: a missing config
	// dir means the agent was never run, so don't materialize one for it.
	// Agents without a settings file check their own config dir.
	if dir := filepath.Dir(path); !mcpOnly && !adapter.DirExists(dir) {
		return fmt.Sprintf("skipped %s: %s does not exist (agent not set up)\n", a.Agent(), dir), true, false
	}
	if err := a.Install(opts); err != nil {
		if errors.Is(err, adapter.ErrAgentNotInstalled) {
			return fmt.Sprintf("skipped %s: not installed\n", a.Agent()), true, false
		}
		return failure(err), false, false
	}
	switch {
	case mcpOnly && opts.MCPOff:
		return fmt.Sprintf("argus mcp server is off; removed it from %s\n", a.Agent()), true, false
	case mcpOnly:
		return fmt.Sprintf("installed the argus mcp server for %s\n", a.Agent()), true, false
	}
	if events := a.DefaultHookEvents(); len(events) > 0 {
		return fmt.Sprintf("installed argus hooks for %s into %s\n  events: %v\n", a.Agent(), path, events), true, true
	}
	return fmt.Sprintf("installed the argus plugin for %s\n", a.Agent()), true, false
}

func uninstallAgent(a adapter.Adapter) (string, error) {
	path, err := a.SettingsPath()
	if err == nil {
		err = a.Uninstall()
	}
	switch {
	case err != nil:
		return "", err
	case path == "":
		return fmt.Sprintf("removed the argus mcp server for %s\n", a.Agent()), nil
	case len(a.DefaultHookEvents()) == 0:
		return fmt.Sprintf("removed argus integration for %s\n", a.Agent()), nil
	}
	return fmt.Sprintf("removed argus hooks for %s from %s\n", a.Agent(), path), nil
}

// mcpInstallOptions adds the MCP URL and token to opts, creating the token on
// first use, or marks MCP off when the port is 0. On a token error, opts leave
// the agents' MCP entries as they are.
func mcpInstallOptions(cfg *config.Config, opts adapter.InstallOptions) (adapter.InstallOptions, error) {
	if cfg.MCP.Port == 0 {
		opts.MCPOff = true
		return opts, nil
	}
	token, err := mcphttp.LoadOrCreateToken(config.GetStatePath("mcp.token"))
	if err != nil {
		return opts, err
	}
	opts.MCPURL, opts.MCPToken = mcpBaseURL(cfg), token
	return opts, nil
}

func mcpBaseURL(cfg *config.Config) string {
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", cfg.MCP.Port)
}
