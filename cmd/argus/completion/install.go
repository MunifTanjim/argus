package completion

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/util"
	"github.com/spf13/cobra"
)

// ErrCompletionNotEnabled means the completion file was written but the shell isn't
// configured to load completions. The install succeeded; callers decide the exit code.
var ErrCompletionNotEnabled = errors.New("shell completion installed but not enabled")

func InstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install shell completion for the current shell",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := Install(cmd); err != nil {
				if errors.Is(err, ErrCompletionNotEnabled) {
					shell.Exit(1)
				}
				return err
			}
			shell.StdErrLn("Installed shell completion!")
			return nil
		},
	}
}

func Install(cmd *cobra.Command) error {
	rootCmd := cmd.Root()

	switch sh := shell.DetectShell(); sh {
	case "zsh":
		shell.StdErrF("Detected Shell: %s\n", sh)

		completionDir, err := PickZshShellCompletionDir()
		if err != nil {
			return err
		}
		if err := util.EnsureDirExists(completionDir); err != nil {
			return err
		}

		completionFile := filepath.Join(completionDir, ZshCompletionFileName())
		f, err := os.Create(completionFile)
		if err != nil {
			return err
		}
		defer f.Close()

		if err := rootCmd.GenZshCompletion(f); err != nil {
			return err
		}

		if isEnabled, err := shell.IsCompletionEnabled(sh); err != nil {
			return err
		} else if !isEnabled {
			shell.StdErrLn()
			shell.StdErrLn("Shell completion installed, but not enabled.")
			shell.SuggestShellRCLines(
				"autoload -Uz compinit",
				"compinit",
			)
			return ErrCompletionNotEnabled
		}
		return nil
	default:
		return fmt.Errorf("unsupported shell for automatic completion installation")
	}
}
