package e2elive

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// isRosterLine reports whether a post-redaction line belongs to a sortable
// roster block. The "  <NODE-…" form is the expected post-redaction placeholder
// for both signers and devices in lock status output. The "  sigpub:…" branch
// is unreachable in practice: redaction runs before sorting, and any raw
// sigpub:<hex> that survives trips the volatility check before we get here.
func isRosterLine(line string) bool {
	return strings.HasPrefix(line, "  sigpub:") || strings.HasPrefix(line, "  <NODE-")
}

// sortSignerBlocks sorts consecutive roster lines within the string so that
// their order is stable after redaction regardless of key-generation order.
func sortSignerBlocks(s string) string {
	lines := strings.Split(s, "\n")
	i := 0
	for i < len(lines) {
		if !isRosterLine(lines[i]) {
			i++
			continue
		}
		j := i
		for j < len(lines) && isRosterLine(lines[j]) {
			j++
		}
		sort.Strings(lines[i:j])
		i = j
	}
	return strings.Join(lines, "\n")
}

type redaction struct {
	from string
	to   string
}

var (
	reTime = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
	reDur  = regexp.MustCompile(`(^|[^0-9A-Za-z_-])(\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h))\b`)
	reFP = regexp.MustCompile(`\[[a-z]+(?: [a-z]+){1,7}\]`)

	// A step fails if any of these survive normalization: they are per-run values
	// that would bake randomness into a golden file.
	volatile = []*regexp.Regexp{
		regexp.MustCompile(`[0-9a-fA-F]{32,}`),
		regexp.MustCompile(`127\.0\.0\.1:\d+`),
		regexp.MustCompile(`(?:/private)?/(?:var/folders|tmp)/\S+`),
		regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`),
		regexp.MustCompile(`\d{2}:\d{2}:\d{2}`),
	}
)

// Redact registers a known per-run value and the placeholder that replaces it in
// golden output. Registering the same value twice keeps the first placeholder.
func (c *Cluster) Redact(actual, placeholder string) {
	if actual == "" {
		return
	}
	for _, r := range c.redactions {
		if r.from == actual {
			return
		}
	}
	c.redactions = append(c.redactions, redaction{from: actual, to: placeholder})
}

func (c *Cluster) autoRedact() {
	c.Redact(c.Root, "<ROOT>")
	c.Redact(c.GWAddr, "<GW-ADDR>")
	// The run id names the network and every container, so it reaches the goldens
	// through the internal gateway URL on each command line.
	c.Redact(c.runID, "<RUN-ID>")
	c.Redact(c.Token, "<TOKEN>")
	for id, n := range c.nodes {
		c.Redact(n.Dir, "<"+id+"-DIR>")
	}
}

func (c *Cluster) normalize(s string) (string, error) {
	c.autoRedact()

	sorted := append([]redaction{}, c.redactions...)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i].from) > len(sorted[j].from) })
	for _, r := range sorted {
		s = strings.ReplaceAll(s, r.from, r.to)
	}

	s = reTime.ReplaceAllString(s, "<TIME>")
	s = reDur.ReplaceAllString(s, "${1}<DUR>")
	s = reFP.ReplaceAllString(s, "<FP>")
	s = sortSignerBlocks(s)

	for _, re := range volatile {
		if m := re.FindString(s); m != "" {
			return "", fmt.Errorf("unregistered volatile value: %q — call Cluster.Redact for it", m)
		}
	}
	return s, nil
}
