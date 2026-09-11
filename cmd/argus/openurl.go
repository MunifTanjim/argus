package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

var openURLFn = openURL

func browserCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}

func openURL(url string) error {
	name, args := browserCommand(runtime.GOOS, url)
	if err := exec.Command(name, args...).Run(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}
