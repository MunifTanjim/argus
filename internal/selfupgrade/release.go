package selfupgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/shell"
)

func AssetName(tag, goos, goarch string) string {
	return fmt.Sprintf("%s-%s-%s-%s", config.BinaryName, tag, goos, goarch)
}

// viaGH runs gh first and falls back to plain HTTP, because gh can be present
// but unauthenticated in a daemon's environment.
func viaGH[T any](useGH bool, gh, plain func() (T, error)) (T, error) {
	if !useGH {
		return plain()
	}
	v, ghErr := gh()
	if ghErr == nil {
		return v, nil
	}
	v, err := plain()
	if err != nil {
		return v, fmt.Errorf("%w (gh: %v)", err, ghErr)
	}
	return v, nil
}

func (u *Updater) fetchLatestTag(ctx context.Context) (string, error) {
	tag, err := viaGH(u.UseGH, func() (string, error) {
		out := shell.NewCommandContext(ctx, "gh", "release", "view", "--repo", config.Repo, "--json", "tagName", "--jq", ".tagName")
		if err := out.Run(); err != nil {
			return "", fmt.Errorf("%w: %s", err, out.StdErr().TrimSpace())
		}
		return out.StdOut().TrimSpace().String(), nil
	}, func() (string, error) {
		return u.httpLatestTag(ctx)
	})
	if err == nil && tag == "" {
		return "", errors.New("release tag missing in response")
	}
	return tag, err
}

func (u *Updater) httpLatestTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/releases/latest", u.APIBase, config.Repo), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := u.Client.Do(req)
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
	return release.TagName, nil
}

func (u *Updater) downloadAsset(ctx context.Context, tag, name, dst string) error {
	_, err := viaGH(u.UseGH, func() (struct{}, error) {
		dl := shell.NewCommandContext(ctx, "gh", "release", "download", tag,
			"--repo", config.Repo, "--pattern", name, "--output", dst, "--clobber")
		if err := dl.Run(); err != nil {
			return struct{}{}, fmt.Errorf("%w: %s", err, dl.StdErr().TrimSpace())
		}
		return struct{}{}, nil
	}, func() (struct{}, error) {
		return struct{}{}, u.httpDownloadAsset(ctx, tag, name, dst)
	})
	return err
}

func (u *Updater) httpDownloadAsset(ctx context.Context, tag, name, dst string) error {
	body, err := u.getAsset(ctx, tag, name)
	if err != nil {
		return err
	}
	defer body.Close()
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

const maxChecksumSize = 4 << 10

func (u *Updater) fetchAsset(ctx context.Context, tag, name string) ([]byte, error) {
	b, err := viaGH(u.UseGH, func() ([]byte, error) {
		dl := shell.NewCommandContext(ctx, "gh", "release", "download", tag,
			"--repo", config.Repo, "--pattern", name, "--output", "-")
		if err := dl.Run(); err != nil {
			return nil, fmt.Errorf("%w: %s", err, dl.StdErr().TrimSpace())
		}
		return []byte(dl.StdOut().String()), nil
	}, func() ([]byte, error) {
		body, err := u.getAsset(ctx, tag, name)
		if err != nil {
			return nil, err
		}
		defer body.Close()
		return io.ReadAll(io.LimitReader(body, maxChecksumSize+1))
	})
	if err != nil {
		return nil, err
	}
	if len(b) > maxChecksumSize {
		return nil, fmt.Errorf("%s: checksum file too large", name)
	}
	return b, nil
}

func (u *Updater) getAsset(ctx context.Context, tag, name string) (io.ReadCloser, error) {
	url := fmt.Sprintf("%s/%s/releases/download/%s/%s", u.DLBase, config.Repo, tag, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: unexpected status %s", name, resp.Status)
	}
	return resp.Body, nil
}
