package node

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func gitRun(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=argus demo", "GIT_AUTHOR_EMAIL=demo@argus.local",
		"GIT_COMMITTER_NAME=argus demo", "GIT_COMMITTER_EMAIL=demo@argus.local",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// MaterializeDemoRepos builds a throwaway git repo for each session that has a
// repo spec, so the real changed-files/diff/commit handlers serve real git
// output. It rewrites each such session's Cwd to the repo path and returns a
// cleanup that removes all temp dirs.
func MaterializeDemoRepos(dd *DemoData) (func(), error) {
	var dirs []string
	cleanup := func() {
		for _, d := range dirs {
			_ = os.RemoveAll(d)
		}
	}
	for ni := range dd.Nodes {
		n := &dd.Nodes[ni]
		for si := range n.Sessions {
			s := &n.Sessions[si]
			spec, ok := n.Repos[s.ID]
			if !ok {
				continue
			}
			dir, err := os.MkdirTemp("", "argus-demo-repo-")
			if err != nil {
				cleanup()
				return nil, err
			}
			dirs = append(dirs, dir)
			if err := buildRepo(dir, spec); err != nil {
				cleanup()
				return nil, fmt.Errorf("repo %s: %w", s.ID, err)
			}
			s.Cwd = dir
		}
	}
	return cleanup, nil
}

func buildRepo(dir, spec string) error {
	if err := gitRun(dir, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	v1 := filepath.Join(spec, "v1")
	if _, err := os.Stat(v1); err != nil {
		return fmt.Errorf("repo spec missing v1: %w", err)
	}
	if err := copyTree(v1, dir); err != nil {
		return err
	}
	if err := gitRun(dir, "add", "-A"); err != nil {
		return err
	}
	if err := gitRun(dir, "commit", "-q", "-m", "initial commit"); err != nil {
		return err
	}
	if v2 := filepath.Join(spec, "v2"); dirExists(v2) {
		if err := copyTree(v2, dir); err != nil {
			return err
		}
		if err := gitRun(dir, "add", "-A"); err != nil {
			return err
		}
		if err := gitRun(dir, "commit", "-q", "-m", "add feature"); err != nil {
			return err
		}
	}
	if work := filepath.Join(spec, "work"); dirExists(work) {
		if err := copyTree(work, dir); err != nil {
			return err
		}
	}
	return nil
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
