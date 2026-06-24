package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/util"
)

type FZFSearchConfig[T any] struct {
	Header        string
	Items         []T
	AutoSelectOne bool
	Multi         bool
	Query         string

	Fields            func(T) []string
	FieldDelimiter    string
	FieldDisplayRange string

	PreviewCommand string
	PreviewWindow  string
}

type FZFSearch[T any] struct {
	config FZFSearchConfig[T]
}

func NewFZFSearch[T any](conf FZFSearchConfig[T]) *FZFSearch[T] {
	if conf.Fields == nil {
		conf.Fields = func(item T) []string {
			if s, ok := any(item).(fmt.Stringer); ok {
				return []string{s.String()}
			}
			return []string{fmt.Sprintf("%v", item)}
		}
	}
	if conf.FieldDelimiter == "" {
		conf.FieldDelimiter = "\t"
	}
	if conf.PreviewCommand != "" && conf.PreviewWindow == "" {
		// up split, 60% height; auto-hide the preview when the window is < 4 lines.
		conf.PreviewWindow = "up,60%,noinfo,<4(hidden)"
	}
	return &FZFSearch[T]{
		config: conf,
	}
}

var (
	ErrNoMatch   = errors.New("no match found")
	ErrCancelled = errors.New("cancelled")
)

func (f *FZFSearch[T]) Run() ([]T, error) {
	if err := util.EnsureTool("fzf"); err != nil {
		return nil, err
	}

	args := []string{}

	if f.config.Header != "" {
		args = append(args, "--header", f.config.Header)
	}

	if f.config.AutoSelectOne {
		args = append(args, "--select-1")
	}

	if f.config.Multi {
		args = append(args, "--multi")
	}

	if f.config.Query != "" {
		args = append(args, "--query", f.config.Query)
	}

	args = append(args, "--delimiter", f.config.FieldDelimiter)
	if f.config.FieldDisplayRange != "" {
		args = append(args, "--with-nth", f.config.FieldDisplayRange)
	}

	if f.config.PreviewCommand != "" {
		args = append(args, "--preview", f.config.PreviewCommand, "--preview-window", f.config.PreviewWindow)
	}

	items := make([]string, 0, len(f.config.Items))
	idxByFields := make(map[string]int, len(f.config.Items))
	for i, item := range f.config.Items {
		fields := strings.Join(f.config.Fields(item), f.config.FieldDelimiter)
		items = append(items, fields)
		idxByFields[strings.TrimSpace(fields)] = i
	}

	cmd := shell.NewCommand("fzf", args...).
		WithStdIn(strings.NewReader(strings.Join(items, "\n"))).
		WithStdErr(os.Stderr)

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 130 {
				return nil, ErrCancelled
			}
			if exitErr.ExitCode() == 1 {
				return nil, ErrNoMatch
			}
		}
		return nil, fmt.Errorf("fzf failed: %w", err)
	}

	output := cmd.StdOut().TrimSpace().String()
	if output == "" {
		return nil, ErrCancelled
	}

	selected := strings.Split(output, "\n")
	result := make([]T, 0, len(selected))
	for _, sel := range selected {
		if idx, ok := idxByFields[strings.TrimSpace(sel)]; ok {
			result = append(result, f.config.Items[idx])
		}
	}
	return result, nil
}
