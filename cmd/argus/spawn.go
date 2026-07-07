package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/spawn"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// newSpawnCmd builds `argus spawn <agent> [args…]`: run an agent inside tmux (on
// argus's private socket) and attach to it, tmux hidden. Self-contained — a node,
// if running, discovers the session; if not, it's just a normal agent run.
func newSpawnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "spawn <agent> [args…]",
		Short: "Run an agent inside tmux so argus can watch and control it",
		Long: "Launch an AI coding agent inside tmux and attach to it. tmux stays " +
			"invisible, so it feels like running the agent directly — but argus can now " +
			"watch and control the session. Everything after <agent> is passed straight " +
			"to the agent (e.g. `argus spawn claude --resume`).",
		Args:              cobra.MinimumNArgs(1),
		SilenceUsage:      true,
		SilenceErrors:     true,
		ValidArgsFunction: completeSpawnAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID, passthrough := args[0], args[1:]

			ad := adapters.ByAgent(agentID)
			if ad == nil {
				return fail(cmd, fmt.Errorf("unknown agent %q (valid: %s)", agentID, knownAgents()))
			}
			command, _ := ad.SpawnCommand("")
			if command == "" {
				return fail(cmd, fmt.Errorf("agent %q cannot be spawned", agentID))
			}

			ctx := context.Background()
			client := tmux.New("argus")
			if !client.Available(ctx) {
				return fail(cmd, fmt.Errorf("argus spawn requires tmux, which was not found on PATH"))
			}
			// Fail loudly rather than opening a pane that dies "command not found".
			if _, err := exec.LookPath(command); err != nil {
				return fail(cmd, fmt.Errorf("cannot spawn: %q is not installed", command))
			}

			cwd, _ := os.Getwd()
			name := spawn.SessionName(ctx, client, cwd)
			// The agent runs under a shell shim (spawnRunScript) but stays the pane's
			// foreground process, so tty-based discovery still finds it.
			runArgs := append([]string{"-c", spawnRunScript, "argus:spawn", command}, passthrough...)
			if _, err := client.NewSession(ctx, tmux.NewSessionOpts{
				Name: name, Cwd: cwd, Command: "sh", Args: runArgs,
			}); err != nil {
				return fail(cmd, err)
			}
			// Best-effort tweaks for a plain, scrollable feel: no status bar, mouse
			// scroll, vi keys for the pause scroll. Newer options (mouse ≥2.1,
			// mode-keys at session scope ~2.9) — ignore failures on older tmux.
			_ = client.SetOption(ctx, name, "status", "off")
			_ = client.SetOption(ctx, name, "mouse", "on")
			_ = client.SetOption(ctx, name, "mode-keys", "vi")
			if err := client.Attach(name); err != nil {
				return fail(cmd, err)
			}
			return nil
		},
	}
	// Stop parsing flags at the first positional so everything after <agent> is
	// passed through to the agent instead of being interpreted by argus.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// spawnRunScript is the POSIX-sh shim the agent runs under ($1 is the agent name,
// "$@" the full command). A quick or failed exit (`--help`, a bad flag, a startup
// crash) would otherwise vanish when tmux tears the pane down, so it pauses on
// exit-non-zero or under-10s; a normal session returns to the shell immediately.
// The pause enters copy-mode for scrolling and installs a session-scoped hook to
// close the pane on leaving it — set only here, since during the agent's run
// leaving copy-mode must not kill a live session. Errors are swallowed: on tmux
// without the hook, the read still closes the pane on Enter.
const spawnRunScript = `
start=$(date +%s)
"$@"
code=$?
elapsed=$(( $(date +%s) - start ))
if [ "$code" -ne 0 ] || [ "$elapsed" -lt 10 ]; then
	printf '\r\n\033[2m[%s exited (code %s) — scroll: j/k or ↑/↓ · Enter/q to close]\033[0m\r\n' "$1" "$code"
	tmux set-hook -t "$TMUX_PANE" pane-mode-changed 'if -F "#{!=:#{pane_in_mode},1}" kill-pane' 2>/dev/null
	tmux copy-mode -t "$TMUX_PANE" 2>/dev/null
	read -r _
fi
exit "$code"
`

// completeSpawnAgent completes the <agent> positional with installed agents; the
// rest are the agent's own args, which argus can't complete.
func completeSpawnAgent(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	var comps []string
	for _, a := range adapters.All() {
		if name, _ := a.SpawnCommand(""); name != "" {
			if _, err := exec.LookPath(name); err == nil {
				comps = append(comps, a.Agent()+"\t"+a.AgentName())
			}
		}
	}
	return comps, cobra.ShellCompDirectiveNoFileComp
}

// knownAgents lists agent ids for error messages, e.g. "claude, codex, antigravity".
func knownAgents() string {
	all := adapters.All()
	ids := make([]string, 0, len(all))
	for _, a := range all {
		ids = append(ids, a.Agent())
	}
	return strings.Join(ids, ", ")
}
