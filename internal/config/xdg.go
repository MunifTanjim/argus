package config

import (
	"path/filepath"

	"github.com/adrg/xdg"
)

var HomeDir = xdg.Home
var CacheDir = filepath.Join(xdg.CacheHome, ProjectName)
var ConfigDir = filepath.Join(xdg.ConfigHome, ProjectName)
var StateDir = filepath.Join(xdg.StateHome, ProjectName)

func GetCachePath(name string) string {
	return filepath.Join(CacheDir, name)
}

func GetConfigPath(name string) string {
	return filepath.Join(ConfigDir, name)
}

func GetStatePath(name string) string {
	return filepath.Join(StateDir, name)
}
