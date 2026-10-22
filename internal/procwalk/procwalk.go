package procwalk

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/shell"
)

type Proc struct {
	PID, PPID int
	Args      string
}

func (p Proc) Name() string {
	fields := strings.Fields(p.Args)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

type Table map[int]Proc

func Parse(psOutput string) Table {
	t := Table{}
	for line := range strings.SplitSeq(psOutput, "\n") {
		pidStr, rest, ok := cutField(line)
		if !ok {
			continue
		}
		ppidStr, args, _ := cutField(rest)
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(ppidStr)
		if err != nil {
			continue
		}
		t[pid] = Proc{PID: pid, PPID: ppid, Args: strings.TrimSpace(args)}
	}
	return t
}

func cutField(s string) (field, rest string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	field, rest, _ = strings.Cut(s, " ")
	return field, strings.TrimLeft(rest, " "), true
}

func Snapshot(ctx context.Context) (Table, error) {
	cmd := shell.NewCommandContext(ctx, "ps", "-ax", "-o", "pid=", "-o", "ppid=", "-o", "args=")
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return Parse(cmd.StdOut().String()), nil
}

func (t Table) Ancestors(pid int) []Proc {
	var out []Proc
	seen := map[int]bool{pid: true}
	cur, ok := t[pid]
	for ok {
		if seen[cur.PPID] {
			break
		}
		seen[cur.PPID] = true
		cur, ok = t[cur.PPID]
		if ok {
			out = append(out, cur)
		}
	}
	return out
}
