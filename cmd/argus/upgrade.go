package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/cmd/argus/completion"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/util"
)

// upgradeAssetPattern builds the release-asset glob for the given platform.
// Release assets are versioned (<binary>-<tag>-<os>-<arch>); the wildcard
// matches the latest tag, mirroring scripts/install.sh.
func upgradeAssetPattern(goos, goarch string) string {
	return fmt.Sprintf("%s-*-%s-%s", config.BinaryName, goos, goarch)
}

// newUpgradeCmd builds `argus upgrade`: download the latest release binary for the
// current platform and atomically swap it over the running executable.
func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "upgrade",
		Short:         "Upgrade to the latest version",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := util.EnsureTool("gh"); err != nil {
				return fail(cmd, err)
			}

			execPath, err := os.Executable()
			if err != nil {
				return fail(cmd, fmt.Errorf("failed to get executable path: %w", err))
			}
			if execPath, err = filepath.EvalSymlinks(execPath); err != nil {
				return fail(cmd, fmt.Errorf("failed to resolve executable path: %w", err))
			}
			// Refuse to swap anything that isn't the installed binary (e.g. `go run`
			// or a renamed copy), so upgrade only ever overwrites a real argus install.
			if filepath.Base(execPath) != config.BinaryName {
				return fail(cmd, fmt.Errorf("upgrade can only be run with the %s binary", config.BinaryName))
			}

			pattern := upgradeAssetPattern(runtime.GOOS, runtime.GOARCH)
			// Keep the temp file beside the target so the final rename stays on the
			// same filesystem (atomic, and safe to swap a running binary on Unix).
			tempPath := execPath + ".tmp"

			shell.StdErrF("Downloading %s...\n", pattern)
			// No tag argument: gh downloads the latest release.
			dl := shell.NewCommand(
				"gh", "release", "download",
				"--repo", config.Repo,
				"--pattern", pattern,
				"--output", tempPath,
				"--clobber",
			)
			if err := dl.Run(); err != nil {
				return fail(cmd, fmt.Errorf("failed to download: %w: %s", err, dl.StdErr().TrimSpace()))
			}

			if err := os.Chmod(tempPath, 0o755); err != nil {
				os.Remove(tempPath)
				return fail(cmd, fmt.Errorf("failed to set executable permissions: %w", err))
			}

			shell.StdErrLn("Verifying downloaded binary...")
			if err := shell.NewCommand(tempPath, "--version").Run(); err != nil {
				os.Remove(tempPath)
				return fail(cmd, fmt.Errorf("downloaded binary verification failed: %w", err))
			}

			if err := os.Rename(tempPath, execPath); err != nil {
				os.Remove(tempPath)
				return fail(cmd, fmt.Errorf("failed to replace binary: %w", err))
			}

			// Report success before refreshing completion: a completion problem must
			// not mask that the upgrade itself already succeeded.
			shell.StdErrLn("Upgraded to the latest version!")

			switch err := completion.Install(cmd); {
			case err == nil:
				shell.StdErrLn("Updated shell completion!")
			case errors.Is(err, completion.ErrCompletionNotEnabled):
			default:
				shell.StdErrF("Warning: failed to refresh shell completion: %v\n", err)
			}

			return nil
		},
	}
}
