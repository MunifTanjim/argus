package main

import (
	"encoding/json"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"

	"github.com/MunifTanjim/argus/internal/shell"
)

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printTable prints rows under headers in aligned columns, without borders.
func printTable(headers []string, rows [][]string) {
	last := len(headers) - 1
	t := table.New().
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderHeader(false).BorderColumn(false).
		Wrap(false).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(_, col int) lipgloss.Style {
			if col == last {
				return lipgloss.NewStyle()
			}
			return lipgloss.NewStyle().PaddingRight(2)
		})
	for _, line := range strings.Split(t.String(), "\n") {
		shell.StdOutF("%s\n", strings.TrimRight(line, " "))
	}
}

// printFields prints a title and then label/value pairs, skipping empty values.
func printFields(title string, fields [][2]string) {
	shell.StdOutF("%s\n", title)
	width := 0
	for _, f := range fields {
		if f[1] != "" {
			width = max(width, len(f[0]))
		}
	}
	for _, f := range fields {
		if f[1] != "" {
			shell.StdOutF("  %-*s  %s\n", width, f[0], f[1])
		}
	}
}

func warn(msg string) {
	if msg != "" {
		shell.StdErrF("warning: %s\n", msg)
	}
}
