package node

import "time"

// coalesce forwards chunks from in to flush. The first chunk after an idle
// period is flushed at once; chunks arriving within window of a flush are
// gathered and flushed together, at most max bytes per call. It returns nil
// once in is closed (after flushing what is gathered), or flush's error.
func coalesce(in <-chan []byte, flush func([]byte) error, window time.Duration, max int, after func(time.Duration) <-chan time.Time) error {
	var buf []byte
	var timer <-chan time.Time
	for {
		select {
		case chunk, ok := <-in:
			if !ok {
				if len(buf) > 0 {
					return flush(buf)
				}
				return nil
			}
			if timer == nil {
				if err := flush(chunk); err != nil {
					return err
				}
				timer = after(window)
				continue
			}
			buf = append(buf, chunk...)
			for len(buf) >= max {
				if err := flush(buf[:max]); err != nil {
					return err
				}
				buf = buf[max:]
			}
		case <-timer:
			if len(buf) == 0 {
				timer = nil
				continue
			}
			if err := flush(buf); err != nil {
				return err
			}
			buf = buf[:0]
			timer = after(window)
		}
	}
}
