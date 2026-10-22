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
	// Exec is the executable's file name as the kernel records it, which the
	// process cannot rewrite. The kernel truncates it to 15 or 16 bytes.
	Exec string
}

// Name is the base name of argv[0].
func (p Proc) Name() string {
	fields := strings.Fields(p.Args)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

// Runs reports whether the process runs the program name, by its executable or
// its argv[0]. A ".exe" suffix is ignored: npm installs opencode as opencode.exe.
func (p Proc) Runs(name string) bool {
	for _, n := range []string{p.Exec, p.Name()} {
		if n != "" && strings.TrimSuffix(n, ".exe") == name {
			return true
		}
	}
	return false
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

// ParseExec fills Exec from "pid exec" lines. Exec is the last column, so a
// name with spaces stays whole.
func (t Table) ParseExec(psOutput string) {
	for line := range strings.SplitSeq(psOutput, "\n") {
		pidStr, exec, ok := cutField(line)
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		if p, found := t[pid]; found {
			p.Exec = strings.TrimSpace(exec)
			t[pid] = p
		}
	}
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
	t := Parse(cmd.StdOut().String())
	cmd = shell.NewCommandContext(ctx, "ps", "-ax", "-o", "pid=", "-o", "ucomm=")
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	t.ParseExec(cmd.StdOut().String())
	return t, nil
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
