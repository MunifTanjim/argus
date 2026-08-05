package e2elive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const (
	testImage     = "argus-e2elive:test"
	runLabel      = "argus.e2elive"
	containerHome = "/home/argus"
)

// repoRoot walks up from the package directory until it finds go.mod. Tests run
// with the package directory as their working directory.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repoRoot: no go.mod above " + dir)
		}
		dir = parent
	}
}

func dockerOut(args ...string) (string, error) {
	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

func dockerRun(args ...string) error {
	_, err := dockerOut(args...)
	return err
}

var (
	buildOnce sync.Once
	buildErr  error
)

// buildTestImage builds the argus-test target once per test binary. Building it
// here rather than by hand is what stops a stale image from testing old code.
func buildTestImage() error {
	buildOnce.Do(func() {
		root, err := repoRoot()
		if err != nil {
			buildErr = err
			return
		}
		cmd := exec.Command("docker", "build",
			"--target", "argus-test",
			"-t", testImage,
			"-f", filepath.Join(root, "docker", "Dockerfile"),
			root)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			buildErr = fmt.Errorf("build %s: %w", testImage, err)
		}
	})
	return buildErr
}

// sweepStale removes containers and networks left behind by a run that was
// killed before its cleanup could run.
func sweepStale() {
	if out, err := dockerOut("ps", "-aq", "--filter", "label="+runLabel); err == nil {
		for _, id := range strings.Fields(out) {
			_ = dockerRun("rm", "-f", id)
		}
	}
	if out, err := dockerOut("network", "ls", "-q", "--filter", "label="+runLabel); err == nil {
		for _, id := range strings.Fields(out) {
			_ = dockerRun("network", "rm", id)
		}
	}
}

type dockerResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// dockerExec runs argv inside a running container. A non-zero exit is data, not
// an error: the error-path scenario tests assert on it.
func dockerExec(ctx context.Context, container string, env, argv []string) dockerResult {
	args := []string{"exec"}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, container)
	args = append(args, argv...)

	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
			stderr.WriteString("\nharness: " + err.Error() + "\n")
		}
	}
	return dockerResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}
}
