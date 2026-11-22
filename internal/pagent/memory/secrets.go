package memory

import (
	"regexp"
	"sort"
	"strings"
)

type secretRule struct {
	name string
	re   *regexp.Regexp
}

var secretRules = []secretRule{
	{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"github-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)},
	{"github-pat", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`)},
	{"slack-token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"api-key", regexp.MustCompile(`sk-(ant-)?[A-Za-z0-9_-]{20,}`)},
	{"google-api-key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)},
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"jwt", regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)},
	{"generic-secret", regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key)\s*[:=]\s*\S{8,}`)},
}

var cardRe = regexp.MustCompile(`\d(?:[ -]?\d){12,18}`)

// LooksSecret reports the first rule that s matches.
func LooksSecret(s string) (string, bool) {
	for _, r := range secretRules {
		if r.re.MatchString(s) {
			return r.name, true
		}
	}
	for _, m := range cardRe.FindAllString(s, -1) {
		digits := strings.NewReplacer(" ", "", "-", "").Replace(m)
		if len(digits) >= 13 && len(digits) <= 19 && luhn(digits) {
			return "credit-card", true
		}
	}
	return "", false
}

func luhn(digits string) bool {
	sum, double := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// Redact replaces each known secret value in s with «NAME».
func Redact(s string, known map[string]string) string {
	names := make([]string, 0, len(known))
	for name, v := range known {
		if v != "" {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		li, lj := len(known[names[i]]), len(known[names[j]])
		if li != lj {
			return li > lj
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		s = strings.ReplaceAll(s, known[name], "«"+name+"»")
	}
	return s
}
