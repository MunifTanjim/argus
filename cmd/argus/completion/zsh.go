package completion

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/tui"
	"github.com/MunifTanjim/argus/internal/util"
)

func ZshCompletionFileName() string {
	return shell.CompletionFilename("zsh")
}

func PickZshShellCompletionDir() (string, error) {
	sh := filepath.Base(os.Getenv("SHELL"))
	if sh != "zsh" {
		return "", nil
	}

	filename := ZshCompletionFileName()

	fpaths, err := shell.CompletionDirs("zsh")
	if err != nil {
		return "", err
	}
	for _, fpath := range fpaths {
		if exists, _ := util.FileExists(filepath.Join(fpath, filename)); exists {
			return fpath, nil
		}
	}

	selected, err := tui.NewFZFSearch(tui.FZFSearchConfig[string]{
		Header:        "Select directory for completion",
		Items:         fpaths,
		AutoSelectOne: true,
	}).Run()
	if err != nil {
		return "", err
	}

	if len(selected) == 0 {
		return "", fmt.Errorf("no directory selected")
	}

	return selected[0], nil
}
