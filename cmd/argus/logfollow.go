package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"time"
)

const logFollowBacklog = 64 << 10

var logFollowInterval = 250 * time.Millisecond

// followLog tails a server log file that the server rotates by renaming it to
// <path>.1. It writes whole lines only and holds a partial last line until its
// newline arrives.
func followLog(ctx context.Context, path string, dst io.Writer) {
	backlog := true
	for ctx.Err() == nil {
		f, err := os.Open(path)
		if err != nil {
			if !sleepCtx(ctx, logFollowInterval) {
				return
			}
			continue
		}
		followFile(ctx, f, path, dst, backlog)
		f.Close()
		backlog = false
	}
}

// followFile returns when ctx is done, the file at path is replaced, or it
// shrinks.
func followFile(ctx context.Context, f *os.File, path string, dst io.Writer, backlog bool) {
	var offset int64
	skipPartial := false
	if backlog {
		if fi, err := f.Stat(); err == nil && fi.Size() > logFollowBacklog {
			offset = fi.Size() - logFollowBacklog
			skipPartial = true
		}
	}
	var pending []byte
	buf := make([]byte, 32<<10)

	drain := func() {
		for {
			n, err := f.ReadAt(buf, offset)
			chunk := buf[:n]
			offset += int64(n)
			if skipPartial && n > 0 {
				i := bytes.IndexByte(chunk, '\n')
				if i < 0 {
					chunk = nil
				} else {
					chunk = chunk[i+1:]
					skipPartial = false
				}
			}
			pending = append(pending, chunk...)
			if i := bytes.LastIndexByte(pending, '\n'); i >= 0 {
				_, _ = dst.Write(pending[:i+1])
				pending = append(pending[:0], pending[i+1:]...)
			}
			if err != nil || n == 0 {
				return
			}
		}
	}

	flush := func() {
		if len(pending) > 0 {
			_, _ = dst.Write(append(pending, '\n'))
		}
	}

	for {
		drain()
		cur, err := f.Stat()
		if err != nil {
			return
		}
		if disk, err := os.Stat(path); err == nil {
			if !os.SameFile(cur, disk) {
				drain()
				flush()
				return
			}
			if disk.Size() < offset {
				flush()
				return
			}
		}
		if !sleepCtx(ctx, logFollowInterval) {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
