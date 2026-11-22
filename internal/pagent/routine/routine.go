// Package routine models routine files (what / starts / does / until) and their schedule math.
package routine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Start is one trigger of a routine; exactly one field is set.
type Start struct {
	Schedule  string `yaml:"schedule,omitempty"`
	Every     string `yaml:"every,omitempty"`
	At        string `yaml:"at,omitempty"`
	CheckBack string `yaml:"check-back,omitempty"`
	Event     string `yaml:"event,omitempty"`
}

type Budget struct {
	Sessions int     `yaml:"sessions"`
	Cost     float64 `yaml:"cost"`
	Tokens   int64   `yaml:"tokens"`
}

type Routine struct {
	Slug          string  `yaml:"-"`
	What          string  `yaml:"what"`
	Starts        []Start `yaml:"starts"`
	Does          string  `yaml:"does"`
	Until         string  `yaml:"until"`
	Outcome       string  `yaml:"outcome,omitempty"`
	Verify        string  `yaml:"verify,omitempty"`
	Budget        Budget  `yaml:"budget"`
	Timezone      string  `yaml:"timezone"`
	IfUnavailable string  `yaml:"if-unavailable"`
	ReportTo      string  `yaml:"report-to"`
	Skill         string  `yaml:"skill,omitempty"`
	Overlap       string  `yaml:"overlap"`
	Enabled       bool    `yaml:"enabled"`
}

const fileExt = ".yaml"

func defaults() Routine {
	return Routine{
		Does: "act", Until: "stopped", Timezone: "auto",
		IfUnavailable: "report", ReportTo: "thread", Overlap: "skip", Enabled: true,
	}
}

// Parse decodes a routine file, applying defaults for omitted fields.
func Parse(slug string, data []byte) (Routine, error) {
	r := defaults()
	if err := yaml.Unmarshal(data, &r); err != nil {
		return Routine{}, err
	}
	r.Slug = slug
	return r, nil
}

// Load reads every routine file in dir. Files starting with "_" are skipped.
// Files that fail to parse are reported in the error list and omitted.
func Load(dir string) ([]Routine, []error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}
	var out []Routine
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, fileExt) || strings.HasPrefix(name, "_") {
			continue
		}
		slug := strings.TrimSuffix(name, fileExt)
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		r, err := Parse(slug, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, errs
}

// Save writes r to dir/<slug>.yaml.
func Save(dir string, r Routine) error {
	if r.Slug == "" {
		return errors.New("routine: empty slug")
	}
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, r.Slug+fileExt), data, 0o644)
}

// Limits are the caps Validate enforces; the caller fills them from config.
type Limits struct {
	MinInterval time.Duration
}

func oneOf(field, v string, allowed ...string) error {
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s, got %q", field, strings.Join(allowed, "|"), v)
}

// Validate checks enum values, start shapes and the minimum interval.
func Validate(r Routine, lim Limits) error {
	if strings.TrimSpace(r.What) == "" {
		return errors.New("what is required")
	}
	for _, c := range []error{
		oneOf("does", r.Does, "act", "tell"),
		oneOf("until", r.Until, "stopped", "outcome"),
		oneOf("if-unavailable", r.IfUnavailable, "report", "skip", "wait"),
		oneOf("report-to", r.ReportTo, "thread", "quiet"),
		oneOf("overlap", r.Overlap, "skip", "queue"),
	} {
		if c != nil {
			return c
		}
	}
	if r.Until == "outcome" && strings.TrimSpace(r.Outcome) == "" {
		return errors.New("outcome is required when until is outcome")
	}
	if _, err := Location(r, time.UTC); err != nil {
		return err
	}
	if len(r.Starts) == 0 {
		return errors.New("starts is required")
	}
	loc, _ := Location(r, time.UTC)
	for i, s := range r.Starts {
		if err := validateStart(s, lim, loc); err != nil {
			return fmt.Errorf("starts[%d]: %w", i, err)
		}
	}
	return nil
}

func validateStart(s Start, lim Limits, loc *time.Location) error {
	set := 0
	for _, v := range []string{s.Schedule, s.Every, s.At, s.CheckBack, s.Event} {
		if v != "" {
			set++
		}
	}
	if set != 1 {
		return errors.New("exactly one of schedule, every, at, check-back, event is required")
	}
	switch {
	case s.Schedule != "":
		sch, err := parseCron(s.Schedule)
		if err != nil {
			return err
		}
		if lim.MinInterval > 0 {
			t := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).In(loc)
			prev := sch.Next(t)
			for range 64 {
				next := sch.Next(prev)
				if next.IsZero() {
					break
				}
				if next.Sub(prev) < lim.MinInterval {
					return fmt.Errorf("schedule %q is more frequent than min-interval %s", s.Schedule, lim.MinInterval)
				}
				prev = next
			}
		}
	case s.Every != "":
		d, err := ParseEvery(s.Every)
		if err != nil {
			return err
		}
		if d < lim.MinInterval {
			return fmt.Errorf("every %s is shorter than min-interval %s", s.Every, lim.MinInterval)
		}
	case s.At != "":
		if _, err := parseAt(s.At, loc); err != nil {
			return err
		}
	case s.CheckBack != "":
		return oneOf("check-back", s.CheckBack, "self-paced")
	case s.Event != "":
		return oneOf("event", s.Event, "webhook")
	}
	return nil
}
