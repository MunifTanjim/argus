package e2elive

import (
	"regexp"
	"sync"
	"testing"
)

// Patterns for reading values back out of real CLI output. Group 1 is the value.
const (
	PatSignerKey = `  signer:   (sigpub:[0-9a-f]+)`
	PatDeviceKey = `  identity: (devpub:[0-9a-f]+)`
	// PatClientDeviceKey matches the ephemeral device identity printed in client
	// mode (no local node socket), distinct from PatDeviceKey which matches the
	// node-process identity.
	PatClientDeviceKey = `this device identity: (devpub:[0-9a-f]+)`
	PatGenesis         = `genesis: (gen:[0-9a-f]+)`
	// PatTip matches both the operation-confirmation format ("current tip (audit):")
	// and the lock-status section format ("  tip:").
	PatTip         = `tip(?:\s*\(audit\))?: +(tip:[0-9a-f]+)`
	PatDisablement = `disablement secret: (dis:[0-9a-f]+)`
	// Both the `blob: <b64>` stdout line and the `--cosign <b64>` stderr hint.
	PatBlob = `(?:blob: |--cosign )([A-Za-z0-9+/=]+)`
)

var (
	reCacheMu sync.Mutex
	reCache   = map[string]*regexp.Regexp{}
)

func compilePattern(pattern string) *regexp.Regexp {
	reCacheMu.Lock()
	defer reCacheMu.Unlock()
	if re, ok := reCache[pattern]; ok {
		return re
	}
	re := regexp.MustCompile(pattern)
	reCache[pattern] = re
	return re
}

// Scrape returns the first capture-group match across stdout then stderr, failing
// the test when the value the CLI is supposed to print is absent.
func Scrape(t *testing.T, r Result, pattern string) string {
	t.Helper()
	if all := ScrapeAll(t, r, pattern); len(all) > 0 {
		return all[0]
	}
	return ""
}

func ScrapeAll(t *testing.T, r Result, pattern string) []string {
	t.Helper()
	re := compilePattern(pattern)
	var out []string
	for _, s := range []string{r.Stdout, r.Stderr} {
		for _, m := range re.FindAllStringSubmatch(s, -1) {
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		t.Errorf("scrape %q found nothing in:\n--- stdout ---\n%s--- stderr ---\n%s", pattern, r.Stdout, r.Stderr)
	}
	return out
}
