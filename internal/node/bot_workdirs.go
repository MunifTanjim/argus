package node

import (
	"context"
	"os"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/bot"
)

type workdirsKey struct{}

// withBotWorkdirs carries the bot's workdirs to the session a spawn or resume
// starts. A workdir may start with ~; a relative one is under the bot's
// directory. Missing directories are left out.
func (d *Node) withBotWorkdirs(ctx context.Context, a bot.Bot) context.Context {
	def, err := bot.LoadDefinition(a.Dir)
	if err != nil || len(def.Workdirs) == 0 {
		return ctx
	}
	home, _ := os.UserHomeDir()
	var dirs []string
	for _, w := range def.Workdirs {
		dir := expandHome(w, home)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(a.Dir, dir)
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			d.log.Warn("bot: workdir is not a directory", "bot", a.Name, "workdir", w)
			continue
		}
		dirs = append(dirs, filepath.Clean(dir))
	}
	if len(dirs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, workdirsKey{}, dirs)
}

func workdirsFrom(ctx context.Context) []string {
	dirs, _ := ctx.Value(workdirsKey{}).([]string)
	return dirs
}

// addDirArgs prepends the arguments granting ctx's workdirs to an agent's
// launch arguments, when its CLI takes them.
func addDirArgs(ctx context.Context, a adapter.Adapter, args []string) []string {
	dirs := workdirsFrom(ctx)
	da, ok := a.(adapter.DirAdder)
	if len(dirs) == 0 || !ok {
		return args
	}
	return append(da.AddDirArgs(dirs), args...)
}
