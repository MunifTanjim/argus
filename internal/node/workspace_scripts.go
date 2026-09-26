package node

import (
	"context"
	"encoding/json"
	"fmt"
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

// startSetup starts wsID's setup script if the project has one and returns the
// command that started. A settings parse error is recorded as a failed setup.
// A project with no main working tree has no scripts.
func (d *Node) startSetup(ctx context.Context, wsID, mainDir string) string {
	if mainDir == "" {
		return ""
	}
	s, err := wsscript.Load(mainDir)
	if err != nil {
		d.scripts.Fail(wsID, "", err.Error())
		return ""
	}
	if s.Setup == "" {
		return ""
	}
	dir, _, _, _, err := d.projreg.WorkspaceInfo(ctx, wsID)
	if err != nil {
		d.scripts.Fail(wsID, s.Setup, err.Error())
		return ""
	}
	env, err := d.scriptEnv(ctx, wsID, dir, mainDir)
	if err != nil {
		d.scripts.Fail(wsID, s.Setup, err.Error())
		return ""
	}
	if err := d.scripts.StartSetup(wsID, s.Setup, env); err != nil {
		return ""
	}
	return s.Setup
}

func (d *Node) handleWorkspaceRunSetup(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkspaceRef](params)
	if err != nil {
		return nil, err
	}
	if d.projreg == nil {
		return nil, invalid("project registry disabled")
	}
	dir, _, projID, ok, err := d.projreg.WorkspaceInfo(ctx, p.WorkspaceID)
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
	if kind != "git" || mainDir == "" {
		return nil, invalid("no setup script")
	}
	s, err := wsscript.Load(mainDir)
	if err != nil {
		return nil, invalid("%s", err)
	}
	if s.Setup == "" {
		return nil, invalid("no setup script")
	}
	env, err := d.scriptEnv(ctx, p.WorkspaceID, dir, mainDir)
	if err != nil {
		return nil, invalid("%s", err)
	}
	if err := d.scripts.StartSetup(p.WorkspaceID, s.Setup, env); err != nil {
		return nil, invalid("%s", err)
	}
	return nil, nil
}

func (d *Node) handleWorkspaceSetupLog(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkspaceRef](params)
	if err != nil {
		return nil, err
	}
	return api.SetupLogResult{Output: d.scripts.Output(p.WorkspaceID)}, nil
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
