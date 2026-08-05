package e2elive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoRootFindsTheModuleRoot(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod at %s: %v", root, err)
	}
	if !strings.HasPrefix(string(b), "module github.com/MunifTanjim/argus\n") {
		t.Fatalf("go.mod at %s is not the argus module", root)
	}
	if _, err := os.Stat(filepath.Join(root, "docker", "Dockerfile")); err != nil {
		t.Fatalf("docker/Dockerfile not under repo root %s: %v", root, err)
	}
}

func TestDockerExecReportsStdoutAndExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("container test; skipped under -short")
	}
	if err := buildTestImage(); err != nil {
		t.Fatalf("buildTestImage: %v", err)
	}
	name := "e2elive-selftest"
	_ = dockerRun("rm", "-f", name)
	if err := dockerRun("run", "-d", "--name", name,
		"--label", runLabel+"=1",
		"--entrypoint", "sh", testImage, "-c", "sleep 60"); err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Cleanup(func() { _ = dockerRun("rm", "-f", name) })

	ok := dockerExec(context.Background(), name, nil, []string{"sh", "-c", "echo out; echo err >&2"})
	if ok.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", ok.ExitCode)
	}
	if strings.TrimSpace(ok.Stdout) != "out" {
		t.Fatalf("stdout = %q, want out", ok.Stdout)
	}
	if strings.TrimSpace(ok.Stderr) != "err" {
		t.Fatalf("stderr = %q, want err", ok.Stderr)
	}

	bad := dockerExec(context.Background(), name, nil, []string{"sh", "-c", "exit 3"})
	if bad.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", bad.ExitCode)
	}

	withEnv := dockerExec(context.Background(), name, []string{"FOO=bar"}, []string{"sh", "-c", "echo $FOO"})
	if strings.TrimSpace(withEnv.Stdout) != "bar" {
		t.Fatalf("env stdout = %q, want bar", withEnv.Stdout)
	}
}

func TestSweepStaleRemovesLabelledContainers(t *testing.T) {
	if testing.Short() {
		t.Skip("container test; skipped under -short")
	}
	if err := buildTestImage(); err != nil {
		t.Fatalf("buildTestImage: %v", err)
	}
	name := "e2elive-stale"
	_ = dockerRun("rm", "-f", name)
	if err := dockerRun("run", "-d", "--name", name,
		"--label", runLabel+"=1",
		"--entrypoint", "sh", testImage, "-c", "sleep 60"); err != nil {
		t.Fatalf("run: %v", err)
	}

	sweepStale()

	out, err := dockerOut("ps", "-aq", "--filter", "name="+name)
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		_ = dockerRun("rm", "-f", name)
		t.Fatalf("container %s survived the sweep", name)
	}
}
