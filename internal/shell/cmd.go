package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
)

type Command struct {
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
}

type CommandOutput string

func (o CommandOutput) TrimSpace() CommandOutput {
	return CommandOutput(strings.TrimSpace(string(o)))
}

func (o CommandOutput) Lines() []string {
	str := o.TrimSpace().String()
	if str == "" {
		return nil
	}
	return strings.Split(str, "\n")
}

func (o CommandOutput) Split(sep string) []string {
	return strings.Split(string(o), sep)
}

func (o CommandOutput) String() string {
	return string(o)
}

func (o CommandOutput) JSONUnmarshal(v any) error {
	return json.Unmarshal([]byte(o.TrimSpace().String()), v)
}

func (cmd *Command) Run() error {
	return cmd.cmd.Run()
}

func (cmd *Command) StdOut() CommandOutput {
	return CommandOutput(cmd.stdout.String())
}

func (cmd *Command) StdErr() CommandOutput {
	return CommandOutput(cmd.stderr.String())
}

func (cmd *Command) WithStdOut(w io.Writer) *Command {
	cmd.cmd.Stdout = w
	return cmd
}

func (cmd *Command) WithStdErr(w io.Writer) *Command {
	cmd.cmd.Stderr = w
	return cmd
}

func (cmd *Command) WithStdIn(r io.Reader) *Command {
	cmd.cmd.Stdin = r
	return cmd
}

func (cmd *Command) WithEnv(env []string) *Command {
	cmd.cmd.Env = env
	return cmd
}

func (cmd *Command) String() string {
	return cmd.cmd.String()
}

func NewCommand(name string, args ...string) *Command {
	return NewCommandContext(context.Background(), name, args...)
}

func NewCommandContext(ctx context.Context, name string, args ...string) *Command {
	cmd := Command{}
	cmd.cmd = exec.CommandContext(ctx, name, args...)
	cmd.cmd.Stdout, cmd.cmd.Stderr = &cmd.stdout, &cmd.stderr
	return &cmd
}

func IsExitError(err error) (exitErr *exec.ExitError, ok bool) {
	if errors.As(err, &exitErr) {
		return exitErr, true
	}
	return nil, false
}
