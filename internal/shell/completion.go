package shell

import (
	"os"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/util"
)

func CompletionFilename(shellName string) string {
	switch shellName {
	case "zsh":
		return "_" + config.BinaryName
	default:
		return config.BinaryName
	}
}

func CompletionDirs(shellName string) ([]string, error) {
	switch shellName {
	case "zsh":
		cmd := NewCommand(os.Getenv("SHELL"), "-lic", "echo $fpath")
		err := cmd.Run()
		if err != nil {
			return nil, err
		}
		fpaths := cmd.StdOut().Split(" ")
		return fpaths, nil
	default:
		return nil, nil
	}
}

func IsCompletionInstalled(shellName string) (bool, error) {
	filename := CompletionFilename(shellName)
	fpaths, err := CompletionDirs(shellName)
	if err != nil {
		return false, nil
	}
	for _, fpath := range fpaths {
		if exists, _ := util.FileExists(filepath.Join(fpath, filename)); exists {
			return true, nil
		}
	}
	return false, nil
}

func IsCompletionEnabled(shellName string) (bool, error) {
	switch shellName {
	case "zsh":
		cmd := NewCommand(os.Getenv("SHELL"), "-lic", "type -w compdef")
		err := cmd.Run()
		if err != nil {
			if exitErr, ok := IsExitError(err); ok && exitErr.ExitCode() == 1 && cmd.StdOut().TrimSpace() == "compdef: none" {
				return false, nil
			}
			return false, err
		}
		return cmd.StdOut().TrimSpace() == "compdef: function", nil
	default:
		return false, nil
	}
}
