package selfupgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"

	"golang.org/x/mod/semver"

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

type release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

func (r release) hasAssets(goos, goarch string) bool {
	want := AssetName(r.TagName, goos, goarch)
	var bin, sum bool
	for _, a := range r.Assets {
		switch a.Name {
		case want:
			bin = true
		case want + ".sha256":
			sum = true
		}
	}
	return bin && sum
}

// pickRelease returns the highest tag that has the binary and checksum for the
// platform, or "". The stable channel skips prereleases.
func pickRelease(releases []release, beta bool, goos, goarch string) string {
	best, bestV := "", ""
	for _, r := range releases {
		if r.Draft || !r.hasAssets(goos, goarch) {
			continue
		}
		v := canonicalVersion(r.TagName)
		if v == "" || !beta && (r.Prerelease || semver.Prerelease(v) != "") {
			continue
		}
		if bestV == "" || semver.Compare(v, bestV) > 0 {
			best, bestV = r.TagName, v
		}
	}
	return best
}

func (u *Updater) fetchLatestTag(ctx context.Context, beta bool) (string, error) {
	path := fmt.Sprintf("repos/%s/releases?per_page=100", config.Repo)
	releases, err := viaGH(u.UseGH, func() ([]release, error) {
		out := shell.NewCommandContext(ctx, "gh", "api", path)
		if err := out.Run(); err != nil {
			return nil, fmt.Errorf("%w: %s", err, out.StdErr().TrimSpace())
		}
		var rs []release
		if err := out.StdOut().JSONUnmarshal(&rs); err != nil {
			return nil, err
		}
		return rs, nil
	}, func() ([]release, error) {
		return u.httpReleases(ctx, path)
	})
	if err != nil {
		return "", err
	}
	tag := pickRelease(releases, beta, runtime.GOOS, runtime.GOARCH)
	if tag == "" {
		return "", fmt.Errorf("no release with assets for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return tag, nil
}

func (u *Updater) httpReleases(ctx context.Context, path string) ([]release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%s", u.APIBase, path), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	var rs []release
	if err := json.NewDecoder(resp.Body).Decode(&rs); err != nil {
		return nil, err
	}
	return rs, nil
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
