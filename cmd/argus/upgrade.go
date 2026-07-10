package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/cmd/argus/completion"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/util"
)

// upgradeAssetName builds the release-asset filename (<binary>-<tag>-<os>-<arch>)
func upgradeAssetName(tag, goos, goarch string) string {
	return fmt.Sprintf("%s-%s-%s-%s", config.BinaryName, tag, goos, goarch)
}

func downloadLatestBinary(ctx context.Context, dst string) error {
	useGH := util.HasTool("gh")

	tag, err := latestTag(ctx, useGH)
	if err != nil {
		return fmt.Errorf("failed to resolve latest release: %w", err)
	}
	shell.StdErrF("Latest version: %s\n", tag)

	assetName := upgradeAssetName(tag, runtime.GOOS, runtime.GOARCH)
	shell.StdErrF("Downloading %s...\n", assetName)

	if useGH {
		dl := shell.NewCommand(
			"gh", "release", "download", tag,
			"--repo", config.Repo,
			"--pattern", assetName,
			"--output", dst,
			"--clobber",
		)
		if err := dl.Run(); err != nil {
			return fmt.Errorf("%w: %s", err, dl.StdErr().TrimSpace())
		}
		return os.Chmod(dst, 0o755)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	return downloadReleaseAsset(ctx, client, tag, assetName, dst)
}

func latestTag(ctx context.Context, useGH bool) (string, error) {
	if useGH {
		out := shell.NewCommand("gh", "release", "view", "--repo", config.Repo, "--json", "tagName", "--jq", ".tagName")
		if err := out.Run(); err != nil {
			return "", fmt.Errorf("%w: %s", err, out.StdErr().TrimSpace())
		}
		tag := out.StdOut().TrimSpace().String()
		if tag == "" {
			return "", errors.New("release tag missing in response")
		}
		return tag, nil
	}
	return latestReleaseTag(ctx, &http.Client{Timeout: 5 * time.Minute})
}

func latestReleaseTag(ctx context.Context, client *http.Client) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", config.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	if release.TagName == "" {
		return "", errors.New("release tag missing in response")
	}
	return release.TagName, nil
}

func downloadReleaseAsset(ctx context.Context, client *http.Client, tag, name, dst string) error {
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", config.Repo, tag, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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

			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()

			// Keep the temp file beside the target so the final rename stays on the
			// same filesystem (atomic, and safe to swap a running binary on Unix).
			tempPath := execPath + ".tmp"

			if err := downloadLatestBinary(ctx, tempPath); err != nil {
				os.Remove(tempPath)
				return fail(cmd, fmt.Errorf("failed to download: %w", err))
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
