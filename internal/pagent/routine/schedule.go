package routine

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func parseCron(expr string) (cron.Schedule, error) {
	s, err := cronParser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid schedule %q: %w", expr, err)
	}
	return s, nil
}

// ParseDuration parses a Go duration that may start with a whole or decimal
// number of days, such as "7d", "1d12h" or "90m".
func ParseDuration(s string) (time.Duration, error) {
	days, rest, ok := strings.Cut(s, "d")
	if !ok {
		return time.ParseDuration(s)
	}
	n, err := strconv.ParseFloat(days, 64)
	if err != nil || n < 0 || strings.ContainsAny(days, "+-eE") {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	d := time.Duration(n * float64(24*time.Hour))
	if rest != "" {
		r, err := time.ParseDuration(rest)
		if err != nil || r < 0 || strings.HasPrefix(rest, "-") || strings.HasPrefix(rest, "+") {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		d += r
	}
	return d, nil
}

// ParseEvery parses an interval such as "2h", "30m" or "1d".
func ParseEvery(s string) (time.Duration, error) {
	d, err := ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid every %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid every %q: must be positive", s)
	}
	return d, nil
}

func parseAt(s string, loc *time.Location) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid at %q", s)
}

// Location resolves the routine's timezone; "" and "auto" give local.
func Location(r Routine, local *time.Location) (*time.Location, error) {
	if r.Timezone == "" || r.Timezone == "auto" {
		if local == nil {
			return time.UTC, nil
		}
		return local, nil
	}
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", r.Timezone, err)
	}
	return loc, nil
}

// NextDue returns the earliest scheduled time strictly after `after` across
// the routine's schedule, every and at starts. Intervals are aligned to anchor
// (the routine's first-seen time). check-back and event starts never schedule.
func NextDue(r Routine, after, anchor time.Time, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = time.UTC
	}
	var best time.Time
	consider := func(t time.Time) {
		if best.IsZero() || t.Before(best) {
			best = t
		}
	}
	for _, s := range r.Starts {
		switch {
		case s.Schedule != "":
			sch, err := parseCron(s.Schedule)
			if err != nil {
				continue
			}
			if t := sch.Next(after.In(loc)); !t.IsZero() {
				consider(t)
			}
		case s.Every != "":
			d, err := ParseEvery(s.Every)
			if err != nil {
				continue
			}
			if after.Before(anchor) {
				consider(anchor.Add(d))
				continue
			}
			k := after.Sub(anchor)/d + 1
			consider(anchor.Add(k * d))
		case s.At != "":
			t, err := parseAt(s.At, loc)
			if err != nil {
				continue
			}
			if t.After(after) {
				consider(t)
			}
		}
	}
	return best, !best.IsZero()
}

// DueBetween lists due times in (from, to] in order.
func DueBetween(r Routine, from, to, anchor time.Time, loc *time.Location) []time.Time {
	var out []time.Time
	for t := from; len(out) < 10000; {
		next, ok := NextDue(r, t, anchor, loc)
		if !ok || next.After(to) {
			break
		}
		out = append(out, next)
		t = next
	}
	return out
}

var weekdayNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// Describe renders a start in words.
func Describe(s Start) string {
	switch {
	case s.Schedule != "":
		return describeCron(s.Schedule)
	case s.Every != "":
		d, err := ParseEvery(s.Every)
		if err != nil {
			return "every " + s.Every
		}
		return "every " + durationWords(d)
	case s.At != "":
		return "once at " + s.At
	case s.CheckBack != "":
		return "self-paced check-back"
	case s.Event != "":
		return "on " + s.Event
	}
	return ""
}

func plural(n int, unit string) string {
	if n == 1 {
		return unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func durationWords(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return plural(int(d/(24*time.Hour)), "day")
	case d%time.Hour == 0:
		return plural(int(d/time.Hour), "hour")
	case d%time.Minute == 0:
		return plural(int(d/time.Minute), "minute")
	}
	return d.String()
}

func describeCron(expr string) string {
	fallback := "cron: " + expr
	switch strings.TrimSpace(expr) {
	case "@hourly":
		return "every hour"
	case "@daily", "@midnight":
		return "daily at 00:00"
	case "@weekly":
		return "weekly on Sun at 00:00"
	case "@monthly":
		return "monthly on day 1 at 00:00"
	case "@yearly", "@annually":
		return fallback
	}
	f := strings.Fields(expr)
	if len(f) != 5 {
		return fallback
	}
	min, hour, dom, mon, dow := f[0], f[1], f[2], f[3], strings.ToUpper(f[4])
	if mon != "*" {
		return fallback
	}
	if n, ok := step(min); ok && hour == "*" && dom == "*" && dow == "*" {
		return "every " + plural(n, "minute")
	}
	if min == "0" && dom == "*" && dow == "*" {
		if n, ok := step(hour); ok {
			return "every " + plural(n, "hour")
		}
	}
	m, okM := num(min, 59)
	h, okH := num(hour, 23)
	if !okM || !okH {
		return fallback
	}
	at := fmt.Sprintf("%02d:%02d", h, m)
	switch {
	case dom == "*" && dow == "*":
		return "daily at " + at
	case dom == "*" && (dow == "1-5" || dow == "MON-FRI"):
		return "weekdays at " + at
	case dom == "*":
		if d, ok := weekday(dow); ok {
			return "weekly on " + weekdayNames[d] + " at " + at
		}
	case dow == "*":
		if d, ok := num(dom, 31); ok && d >= 1 {
			return fmt.Sprintf("monthly on day %d at %s", d, at)
		}
	}
	return fallback
}

func step(f string) (int, bool) {
	rest, ok := strings.CutPrefix(f, "*/")
	if !ok {
		return 0, false
	}
	return num(rest, 1<<30)
}

func num(f string, max int) (int, bool) {
	var n int
	if f == "" {
		return 0, false
	}
	for _, c := range f {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > max {
			return 0, false
		}
	}
	return n, n > 0 || f == "0" || f == "00"
}

func weekday(f string) (int, bool) {
	for i, n := range weekdayNames {
		if strings.EqualFold(f, n) {
			return i, true
		}
	}
	if d, ok := num(f, 7); ok {
		return d % 7, true
	}
	return 0, false
}
