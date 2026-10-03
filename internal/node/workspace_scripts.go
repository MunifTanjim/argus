package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/wsscript"
)

func (d *Node) scriptEnv(ctx context.Context, wsID, dir, mainDir string) (wsscript.Env, error) {
	target, ok, err := d.projreg.TargetBranch(ctx, wsID)
	if err != nil {
		return wsscript.Env{}, err
	}
	if !ok {
		return wsscript.Env{}, fmt.Errorf("unknown workspace: %s", wsID)
	}
	return wsscript.Env{WorkspacePath: dir, RootPath: mainDir, WorkspaceName: filepath.Base(dir), TargetBranch: target}, nil
}

var errNoSetup = errors.New("no setup script")

// startSetup starts wsID's setup: it copies the .worktreeinclude files and then
// runs the setup script, and returns the script command. A settings parse
// error fails the run. A project with neither a script nor a .worktreeinclude,
// or with no main working tree, has no setup (errNoSetup).
func (d *Node) startSetup(ctx context.Context, wsID, mainDir string) (string, error) {
	if mainDir == "" {
		return "", errNoSetup
	}
	s, loadErr := wsscript.Load(mainDir)
	include := fileExists(filepath.Join(mainDir, ".worktreeinclude"))
	if loadErr == nil && s.Setup == "" && !include {
		return "", errNoSetup
	}
	dir, _, _, _, err := d.projreg.WorkspaceInfo(ctx, wsID)
	if err != nil {
		return s.Setup, err
	}
	env, err := d.scriptEnv(ctx, wsID, dir, mainDir)
	if err != nil {
		return s.Setup, err
	}
	prepare := func(ctx context.Context, out io.Writer) bool {
		ok := loadErr == nil
		if !ok {
			fmt.Fprintln(out, loadErr)
		}
		if include {
			ok = copyIncluded(ctx, mainDir, dir, out) && ok
		}
		return ok
	}
	if err := d.scripts.StartSetup(wsID, s.Setup, env, prepare); err != nil {
		return s.Setup, err
	}
	return s.Setup, nil
}

func (d *Node) handleWorkspaceRunSetup(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkspaceRef](params)
	if err != nil {
		return nil, err
	}
	if d.projreg == nil {
		return nil, invalid("project registry disabled")
	}
	_, _, projID, ok, err := d.projreg.WorkspaceInfo(ctx, p.WorkspaceID)
	if err != nil || !ok {
		return nil, invalid("unknown workspace: %s", p.WorkspaceID)
	}
	_, mainDir, _, err := d.projreg.ProjectInfo(ctx, projID)
	if err != nil {
		return nil, invalid("%s", err)
	}
	kind, err := d.projreg.ProjectKind(ctx, projID)
	if err != nil {
		return nil, invalid("%s", err)
	}
	if kind != "git" {
		return nil, invalid("%s", errNoSetup)
	}
	if _, err := d.startSetup(ctx, p.WorkspaceID, mainDir); err != nil {
		return nil, invalid("%s", err)
	}
	return nil, nil
}

func (d *Node) handleWorkspaceSetupLog(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkspaceRef](params)
	if err != nil {
		return nil, err
	}
	if d.demo {
		return api.SetupLogResult{Output: d.demoSetupLogs[p.WorkspaceID]}, nil
	}
	return api.SetupLogResult{Output: d.scripts.Output(p.WorkspaceID)}, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

var teardownTimeout = 20 * time.Second

// runTeardown runs wsID's teardown script in dir and returns why it failed, or
// "" when it passed or there is none to run. A gone workspace has nowhere to
// run, and a project with no main working tree has no scripts.
func (d *Node) runTeardown(ctx context.Context, wsID, dir, mainDir string) string {
	if mainDir == "" || !dirExists(dir) {
		return ""
	}
	s, err := wsscript.Load(mainDir)
	if err != nil {
		return "teardown: " + err.Error()
	}
	if s.Teardown == "" {
		return ""
	}
	env, err := d.scriptEnv(ctx, wsID, dir, mainDir)
	if err != nil {
		return "teardown: " + err.Error()
	}
	code, timedOut, last, err := wsscript.RunTeardown(ctx, s.Teardown, env, teardownTimeout)
	switch {
	case timedOut:
		return fmt.Sprintf("teardown timed out after %s: %s", teardownTimeout, last)
	case err != nil:
		return "teardown failed: " + err.Error()
	case code != 0:
		return fmt.Sprintf("teardown failed (exit %d): %s", code, last)
	}
	return ""
}

func scriptRun(run wsscript.Run, output string) *api.ScriptRun {
	sr := &api.ScriptRun{
		State:      string(run.State),
		Command:    run.Command,
		ExitCode:   run.ExitCode,
		StartedAt:  run.Started.UTC().Format(time.RFC3339),
		OutputTail: wsscript.Tail(output, 20, 2<<10),
	}
	if !run.Ended.IsZero() {
		sr.EndedAt = run.Ended.UTC().Format(time.RFC3339)
	}
	return sr
}
