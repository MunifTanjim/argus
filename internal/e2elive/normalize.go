package e2elive

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type redaction struct {
	from string
	to   string
}

var (
	reTime = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
	reDur  = regexp.MustCompile(`(^|[\s/,\(\)\[\]{}])\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h)\b`)
	reFP   = regexp.MustCompile(`(?m)(fingerprint: ).*$`)

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
	c.Redact(c.Token, "<TOKEN>")
	for id, n := range c.nodes {
		c.Redact(n.Socket, "<"+id+"-SOCKET>")
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
	s = reDur.ReplaceAllString(s, "$1<DUR>")
	s = reFP.ReplaceAllString(s, "${1}<FP>")

	for _, re := range volatile {
		if m := re.FindString(s); m != "" {
			return "", fmt.Errorf("unregistered volatile value: %q — call Cluster.Redact for it", m)
		}
	}
	return s, nil
}
