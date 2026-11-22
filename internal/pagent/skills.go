package pagent

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// SkillsDirFor returns the directory inside an agent's directory where cli
// looks for skills.
func SkillsDirFor(agentDir, cli string) string {
	if cli == "claude" {
		return filepath.Join(agentDir, ".claude", "skills")
	}
	return filepath.Join(agentDir, ".agents", "skills")
}

// SkillExcludes are the placement directories kept out of git.
var SkillExcludes = []string{".claude/skills/", ".agents/skills/"}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func validSkillName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsAny(n, `/\`)
}

// PlaceSkills links the agent's enabled skills into the skill directory of
// cli: those named in def from sharedDir, and everything under
// <agentDir>/skills. Symlinks of an earlier placement, in either CLI's
// directory, are removed first. It returns the names that could not be found.
func PlaceSkills(agentDir, cli, sharedDir string, def *Definition) (missing []string, err error) {
	own := filepath.Join(agentDir, "skills")
	managed := func(target string) bool {
		for _, root := range []string{sharedDir, own} {
			if root == "" {
				continue
			}
			if strings.HasPrefix(filepath.Clean(target), filepath.Clean(root)+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	for _, c := range []string{"claude", "codex"} {
		dir := SkillsDirFor(agentDir, c)
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			p := filepath.Join(dir, e.Name())
			if target, err := os.Readlink(p); err == nil && managed(target) {
				if err := os.Remove(p); err != nil {
					return nil, err
				}
			}
		}
	}

	enabled := map[string]string{}
	if def != nil {
		for _, n := range def.Skills {
			switch src := filepath.Join(sharedDir, n); {
			case !validSkillName(n) || sharedDir == "" || !isDir(src):
				missing = append(missing, n)
			default:
				enabled[n] = src
			}
		}
	}
	if ents, err := os.ReadDir(own); err == nil {
		for _, e := range ents {
			if src := filepath.Join(own, e.Name()); isDir(src) {
				enabled[e.Name()] = src
			}
		}
	}
	if len(enabled) == 0 {
		return missing, nil
	}
	dir := SkillsDirFor(agentDir, cli)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return missing, err
	}
	names := make([]string, 0, len(enabled))
	for n := range enabled {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		link := filepath.Join(dir, n)
		if _, err := os.Lstat(link); err == nil {
			continue // a real file or directory of the user's
		}
		if err := os.Symlink(enabled[n], link); err != nil {
			return missing, fmt.Errorf("place skill %s: %w", n, err)
		}
	}
	return missing, nil
}
