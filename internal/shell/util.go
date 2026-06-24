package shell

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/config"
)

func ExecutableExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func ExecutablePath(cmd string) string {
	path, _ := exec.LookPath(cmd)
	return path
}

func DetectShell() string {
	shell := filepath.Base(os.Getenv("SHELL"))
	switch shell {
	case "zsh":
		return shell
	default:
		return ""
	}
}

func DetectShellRCFile() string {
	shell := DetectShell()
	switch shell {
	case "zsh":
		if zdotDir := os.Getenv("ZDOTDIR"); zdotDir != "" {
			return filepath.Join(zdotDir, ".zshrc")
		}
		return filepath.Join(config.HomeDir, ".zshrc")
	default:
		return ""
	}
}

func SuggestShellRCLines(lines ...string) error {
	switch DetectShell() {
	case "zsh":
		shellRcFile := DetectShellRCFile()
		StdErrF("Add the following lines to your %s file:\n", shellRcFile)
		StdErrLn()
		for _, line := range lines {
			StdErrLn("    " + line)
		}
		StdErrLn()
		StdErrLn("Then restart your shell. Or run:")
		StdErrLn()
		StdErrLn("    exec zsh")
		StdErrLn()
		return nil
	default:
		return nil
	}
}
